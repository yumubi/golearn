import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import fs from 'node:fs'
import { homedir } from 'node:os'
import path from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'

function print(value) {
  process.stdout.write(`${value}\n`)
}

export function hash(text) {
  return createHash('sha256').update(text).digest('hex')
}
export function validateRun(run, text, expectedScript) {
  assert.equal(run.status, 'completed', '运行尚未正常结束')
  assert.equal(run.workflowName, 'handbook-batch', '不是本项目的审读工作流')
  assert.equal(run.script, expectedScript, '工作流定义已变化，请重新复核')
  const r = run.result
  assert.equal(r?.status, 'reviewed', '本批没有通过独立双审')
  assert.equal(r.target, run.args?.target, '返回的目标与原始任务不一致')
  assert.equal(r.revision, run.args?.revision, '返回的版本与原始任务不一致')
  assert.match(r.target, /^guide\/handbook\/(?:0[1-9]|1\d|2[0-7])-[a-z-]+\.md$/)
  const ids = Array.isArray(run.args.items) ? run.args.items : String(run.args.items).split(',').filter(Boolean)
  assert(ids.length > 0 && ids.length <= 8 && new Set(ids).size === ids.length, '任务清单非法')
  const exact = entries => Array.isArray(entries) && entries.length === ids.length
    && new Set(entries).size === entries.length && ids.every(id => entries.includes(id))
  assert(exact(r.requiredIds), '必审条目被删减')
  assert.equal(r.sources?.complete, true, '原文未读全')
  assert.deepEqual(r.sources.unreadIds, [], '存在未读来源')
  assert(exact(r.sources.items?.map(i => i.id)), '原文清单不完整')
  const proposal = r.proposal
  assert.equal(proposal?.target, r.target)
  assert(exact(proposal.coveredIds), '候选稿覆盖不完整')
  assert.deepEqual(proposal.blockers, [], '候选稿有未解决阻塞')
  assert(Array.isArray(r.reviews) && r.reviews.length === 2, '必须有两个独立审稿结果')
  for (const review of r.reviews) {
    assert.equal(review?.pass, true, '审稿未通过')
    assert.deepEqual(review.issues, [], '仍有审稿问题')
    assert(exact(review.items?.map(i => i.id)), '审稿遗漏条目')
    assert(review.items.every(i => i.pass === true && typeof i.evidence === 'string' && i.evidence.trim()), '缺少逐项通过证据')
  }
  assert.equal(hash(text), r.revision, '手册已变化，旧审稿结论失效，请重新准备任务')
  assert(typeof proposal.before === 'string' && proposal.before.trim(), '替换原文为空')
  assert(typeof proposal.after === 'string' && proposal.after.trim(), '候选稿为空')
  assert.equal(text.split(proposal.before).length, 2, '替换原文必须在当前文件中唯一存在')
  assert(!proposal.after.includes(String.fromCharCode(0x2014)), '候选稿含禁用破折号')
  assert(!proposal.after.includes('downloads/'), '候选稿含原材料下载路径')
  return text.replace(proposal.before, () => proposal.after)
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
  const checkOnly = process.argv.includes('--check')
  const runId = process.argv.slice(2).find(arg => arg !== '--check')
  assert(runId && /^[\w.-]+$/.test(runId), '用法: node .omp/handbook/apply.js [--check] RUN_ID')
  const key = `${path.basename(root).toLowerCase().replace(/[^a-z0-9._-]+/g, '-').slice(0, 48)}-${hash(root).slice(0, 12)}`
  const runPath = path.join(homedir(), '.omp/workflows/projects', key, 'runs', `${runId}.json`)
  const run = JSON.parse(fs.readFileSync(runPath, 'utf8'))
  assert.match(run.args?.target || '', /^guide\/handbook\/(?:0[1-9]|1\d|2[0-7])-[a-z-]+\.md$/)
  const target = path.join(root, run.args.target)
  assert(fs.realpathSync(target).startsWith(`${fs.realpathSync(path.join(root, 'guide/handbook'))}${path.sep}`), '目标解析到了手册目录之外')
  const text = fs.readFileSync(target, 'utf8')
  const updated = validateRun(run, text, fs.readFileSync(path.join(root, '.omp/handbook/batch.workflow'), 'utf8'))
  if (!checkOnly) {
    const receiptDir = path.join(root, 'audit/handbook-applied')
    fs.mkdirSync(receiptDir, { recursive: true })
    fs.writeFileSync(path.join(receiptDir, `${runId}.before.md`), text, { flag: 'wx' })
    if (updated !== text) {
      const temporary = `${target}.handbook-${process.pid}.tmp`
      try {
        fs.writeFileSync(temporary, updated, { mode: fs.statSync(target).mode, flag: 'wx' })
        assert.equal(hash(fs.readFileSync(target)), run.args.revision, '落盘前文件再次变化')
        fs.renameSync(temporary, target)
      }
      finally {
        if (fs.existsSync(temporary))
          fs.unlinkSync(temporary)
      }
    }
    fs.writeFileSync(path.join(receiptDir, `${runId}.json`), `${JSON.stringify({ runId, target: run.args.target, before: hash(text), after: hash(updated), requiredIds: run.result.requiredIds }, null, 2)}\n`)
  }
  print(JSON.stringify({ status: checkOnly ? 'applicable' : 'applied', target: run.args.target, changed: text !== updated, runId }))
}
