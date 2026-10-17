import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import fs from 'node:fs'
import { homedir, tmpdir } from 'node:os'
import path from 'node:path'
import process from 'node:process'
import readline from 'node:readline'
import { fileURLToPath } from 'node:url'
import { hash, validateRun } from './apply.js'

const target = 'guide/handbook/12-database.md'
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms))
const print = value => process.stdout.write(`${JSON.stringify(value)}\n`)
function projectKey(root) {
  return `${path.basename(root).toLowerCase().replace(/[^a-z0-9._-]+/g, '-').slice(0, 48)}-${hash(root).slice(0, 12)}`
}
function fixture(repo) {
  const root = fs.mkdtempSync(path.join(tmpdir(), 'omp-handbook-smoke-'))
  for (const dir of ['.omp/agents', '.omp/workflows/saved', 'guide/handbook', 'work/digest', 'work/text'])
    fs.mkdirSync(path.join(root, dir), { recursive: true })
  fs.cpSync(path.join(repo, '.omp/agents'), path.join(root, '.omp/agents'), { recursive: true })
  fs.copyFileSync(path.join(repo, '.omp/config.yml'), path.join(root, '.omp/config.yml'))
  fs.copyFileSync(path.join(repo, '.omp/workflows/saved/handbook-batch.json'), path.join(root, '.omp/workflows/saved/handbook-batch.json'))
  const runtimeHome = path.join(homedir(), '.omp/workflows/projects')
  const settings = path.join(runtimeHome, projectKey(repo), 'settings.json')
  const fixtureSettings = path.join(runtimeHome, projectKey(fs.realpathSync(root)), 'settings.json')
  fs.mkdirSync(path.dirname(fixtureSettings), { recursive: true })
  fs.copyFileSync(settings, fixtureSettings)
  const facts = '这个测试业务规定三类查询。打开资源页时加载第一页的20条一级评论；同一次资源页请求中，为每条一级评论附带最多3条回复预览，便于先看讨论概况；用户点击某条评论的更多按钮时，仅对这条评论的回复继续按每页10条分页加载。20、3、10均为这个测试业务的参数，不代表真实课程规定。'
  fs.writeFileSync(path.join(root, target), `# 评论查询（harness 测试样本）\n\n${facts}〔W16-1 p5〕\n`)
  fs.writeFileSync(path.join(root, 'work/digest/W16-1.md'), `# 测试样本\n- K2 三类查询场景：${facts}〔W16-1 p5〕[必读]\n`)
  fs.writeFileSync(path.join(root, 'work/text/W16-1.txt'), `[[p5]]\n评论的典型查询场景（harness 测试样本）\n${facts}\n本样本没有缓存、限流或部署要求。\n`)
  return fs.realpathSync(root)
}
function readNewRun(runs, before) {
  for (const name of fs.existsSync(runs) ? fs.readdirSync(runs) : []) {
    if (before.has(name) || !name.endsWith('.json'))
      continue
    try {
      const run = JSON.parse(fs.readFileSync(path.join(runs, name), 'utf8'))
      if (run.workflowName === 'handbook-batch' && String(run.args?._raw || run.args?.task || '').includes('配置冒烟'))
        return run
    }
    catch { /* An atomic update may still be in flight. */ }
  }
}
function diagnostics(child) {
  const state = { message: '', stderr: '' }
  readline.createInterface({ input: child.stdout }).on('line', (line) => {
    try {
      const event = JSON.parse(line)
      const content = event.message?.content
      const text = typeof content === 'string' ? content : ''
      if (text.includes('failed:'))
        state.message = text.slice(0, 2000)
      if (event.type === 'response' && event.success === false)
        state.message = String(event.error || '').slice(0, 2000)
    }
    catch { /* Ignore startup output outside the RPC protocol. */ }
  })
  child.stderr.on('data', chunk => state.stderr = `${state.stderr}${chunk}`.slice(-10000))
  child.stdin.on('error', error => state.message ||= String(error))
  return state
}
async function watch(child, runs, before) {
  const deadline = Date.now() + 600000
  let active
  let last = ''
  while (Date.now() < deadline && child.exitCode === null) {
    active = readNewRun(runs, before) || active
    if (active) {
      const view = { runId: active.runId, status: active.status, phase: active.currentPhase, agents: active.agents.map(a => [a.label, a.status]) }
      if (JSON.stringify(view) !== last)
        print(view)
      last = JSON.stringify(view)
      if (['completed', 'failed', 'aborted'].includes(active.status))
        return { active, finished: true }
    }
    await sleep(1000)
  }
  return { active, finished: false }
}
async function main() {
  const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
  const root = process.argv.includes('--fixture') ? fixture(repo) : repo
  const text = fs.readFileSync(path.join(root, target), 'utf8')
  const revision = hash(text)
  const runs = path.join(homedir(), '.omp/workflows/projects', projectKey(root), 'runs')
  const before = new Set(fs.existsSync(runs) ? fs.readdirSync(runs) : [])
  const child = spawn('omp', [
    '--mode',
    'rpc',
    '--no-ui',
    '--no-session',
    '--no-title',
    '--no-lsp',
    '--no-tools',
    '--system-prompt',
    '这是只读配置冒烟测试。收到后台结果后只报告结果，不自行诊断、重试或启动其他任务。',
  ], { cwd: root, stdio: ['pipe', 'pipe', 'pipe'] })
  const diagnostic = diagnostics(child)
  const send = message => child.stdin.write(`${JSON.stringify({ id: 'smoke', type: 'prompt', message })}\n`)
  send(`/handbook-batch target=${target} items=W16-1#K2 revision=${revision} 配置冒烟验证：只核对三类查询的区别，不扩展到其他条目；若手册已完整可提出before等于after。只读，不落盘。`)
  let outcome
  try {
    outcome = await watch(child, runs, before)
    await sleep(1000)
    assert(outcome.finished, '冒烟运行未在十分钟内结束')
    assert.equal(outcome.active.status, 'completed', diagnostic.message || '运行失败')
    validateRun(outcome.active, text, fs.readFileSync(path.join(repo, '.omp/handbook/batch.workflow'), 'utf8'))
    assert.equal(hash(fs.readFileSync(path.join(root, target))), revision, '只读验证修改了手册')
    print({ status: 'PASS', runId: outcome.active.runId, fixture: root !== repo, handbookUnchanged: true })
  }
  finally {
    if (outcome?.active && !outcome.finished) {
      send(`/workflows stop ${outcome.active.runId}`)
      await sleep(1000)
    }
    child.kill('SIGTERM')
    const kill = setTimeout(() => child.kill('SIGKILL'), 5000)
    kill.unref()
  }
}
main().catch((error) => {
  console.error(String(error))
  process.exitCode = 1
})
