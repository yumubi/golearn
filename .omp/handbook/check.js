import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'
import { hash, validateRun } from './apply.js'

async function main() {
  function print(value) {
    process.stdout.write(`${value}\n`)
  }

  const here = path.dirname(fileURLToPath(import.meta.url))
  const script = fs.readFileSync(path.join(here, 'batch.workflow'), 'utf8')
  const AsyncFunction = Object.getPrototypeOf(async () => {}).constructor
  const execute = new AsyncFunction('args', 'agent', 'parallel', 'phase', script.replace('export const meta', 'const meta'))
  const input = {
    target: 'guide/handbook/12-database.md',
    revision: hash('before'),
    items: 'W16-1#K2',
    task: '评论分页',
  }
  const source = {
    complete: true,
    unreadIds: [],
    items: [{ id: 'W16-1#K2', references: ['work/text/W16-1.txt:p5'], facts: ['三类查询场景'], question: '打开资源与加载更多分别查什么？' }],
  }
  const proposal = { target: input.target, before: 'before', after: 'after', coveredIds: ['W16-1#K2'], blockers: [] }
  const pass = { pass: true, issues: [], items: [{ id: 'W16-1#K2', pass: true, evidence: '原文 p5 与候选稿逐项吻合' }] }
  async function simulate(failure) {
    const calls = []
    const result = await execute(input, async (_prompt, options) => {
      calls.push(options.label)
      if (options.agentType === 'handbook-source')
        return failure === 'source-null' ? null : failure === 'missing-id' ? { ...source, items: [] } : structuredClone(source)
      if (options.agentType === 'handbook-writer')
        return structuredClone(proposal)
      if (options.agentType === 'handbook-learner') {
        if (failure === 'review-null')
          return null
        if (failure === 'reject')
          return { ...pass, pass: false, issues: ['缺少查询边界'] }
        if (failure === 'empty-evidence')
          return { ...pass, items: [{ id: 'W16-1#K2', pass: true, evidence: ' ' }] }
      }
      return structuredClone(pass)
    }, thunks => Promise.all(thunks.map(f => f())), () => {})
    return { result, calls }
  }
  const good = await simulate()
  assert.equal(good.result.status, 'reviewed')
  assert.equal(good.calls.length, 4)
  for (const failure of ['source-null', 'missing-id', 'review-null']) {
    const test = await simulate(failure)
    assert.equal(test.result.status, 'blocked', failure)
  }
  for (const failure of ['reject', 'empty-evidence']) {
    const test = await simulate(failure)
    assert.equal(test.result.status, 'needs_rework', failure)
    assert.equal(test.calls.length, 10, '返工必须在三轮候选稿后结束')
  }
  const run = { status: 'completed', workflowName: 'handbook-batch', args: input, script, result: good.result }
  assert.equal(validateRun(run, 'before', script), 'after')
  assert.throws(() => validateRun(run, 'changed', script), /旧审稿结论失效/)
  assert.equal(validateRun({ ...run, script: script.trim() }, 'before', script), 'after')
  assert.throws(() => validateRun(run, 'before', `${script}\n// changed definition`), /工作流定义已变化/)
  for (const mutate of [
    r => r.result.reviews.pop(),
    r => r.result.reviews[0].items = [],
    r => r.result.reviews[1].items[0].evidence = '',
    r => r.result.proposal.target = 'work/text/W16-1.txt',
    r => r.result.requiredIds = [],
    r => r.result.status = 'needs_rework',
  ]) {
    const invalid = structuredClone(run)
    mutate(invalid)
    assert.throws(() => validateRun(invalid, 'before', script))
  }
  print('PASS: empty/missing/failed review rejection, bounded rework, exact scope, stale revision, and apply gate')
}

main().catch((error) => {
  console.error(error)
  process.exitCode = 1
})
