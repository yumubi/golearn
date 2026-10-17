/* global args, agent, parallel, phase */
export const meta = {
  name: 'handbook-batch',
  description: '原文精读、只读候选稿、独立双审；通过后才允许主控落盘',
  phases: [{ title: '原文精读' }, { title: '候选稿' }, { title: '独立双审' }],
}

const target = args.target
const revision = args.revision
const requiredIds = Array.isArray(args.items) ? args.items : String(args.items || '').split(',').filter(Boolean)
const task = args.task || args._ || ''
if (!/^guide\/handbook\/(?:0[1-9]|1\d|2[0-7])-[a-z-]+\.md$/.test(target || ''))
  throw new Error('target 必须是 guide/handbook/01-27 的一个章节')
if (!/^[a-f0-9]{64}$/.test(revision || ''))
  throw new Error('revision 必须是该文件当前内容的 SHA-256')
if (!requiredIds.length || requiredIds.length > 8 || new Set(requiredIds).size !== requiredIds.length
  || requiredIds.some(id => !/^[A-Za-z0-9_-]+#[A-Za-z0-9_-]+$/.test(id)))
  throw new Error('items 必须是 1-8 个不重复的 单元#条目 编号')
if (typeof task !== 'string' || !task.trim())
  throw new Error('请说明本批业务场景与要补清的问题')

const string = { type: 'string', minLength: 1 }
const strings = { type: 'array', items: string }
const object = (properties, required = Object.keys(properties)) => ({ type: 'object', properties, required, additionalProperties: false })
const ledgerSchema = object({
  complete: { type: 'boolean' }, unreadIds: strings,
  items: { type: 'array', items: object({ id: string, references: strings, facts: strings, question: string }) },
})
const proposalSchema = object({ target: string, before: string, after: string, coveredIds: strings, blockers: strings })
const reviewSchema = object({
  pass: { type: 'boolean' }, issues: strings,
  items: { type: 'array', items: object({ id: string, pass: { type: 'boolean' }, evidence: string }) },
})
function exactIds(ids) {
  return Array.isArray(ids) && ids.length === requiredIds.length && new Set(ids).size === ids.length
    && requiredIds.every(id => ids.includes(id))
}
function validReview(review) {
  return review && review.pass === true && Array.isArray(review.issues) && review.issues.length === 0
    && Array.isArray(review.items) && exactIds(review.items.map(i => i.id))
    && review.items.every(i => i.pass === true && typeof i.evidence === 'string' && i.evidence.trim())
}
const base = { target, revision, requiredIds, task }
phase('原文精读')
const sources = await agent(`精读本批每个条目。先读对应 work/digest/<单元>.md，再读 work/text/<单元>.txt 的实际正文页面；源码类条目沿摘要定位真实源码。不要读目标手册。任务：${JSON.stringify({ requiredIds, task })}`, {
  label: 'source-inventory', agentType: 'handbook-source', schema: ledgerSchema,
})
if (!sources || sources.complete !== true || !Array.isArray(sources.unreadIds) || sources.unreadIds.length
  || !Array.isArray(sources.items) || !exactIds(sources.items.map(i => i.id))
  || sources.items.some(i => !i.references?.length || !i.facts?.length || !i.question?.trim()))
  return { ...base, status: 'blocked', reason: '原文读取或完整条目证据不足', sources }

const questions = sources.items.map(i => ({ id: i.id, question: i.question }))
const references = sources.items.map(i => ({ id: i.id, references: i.references }))
const attempts = []
let feedback = []
for (let round = 1; round <= 3; round++) {
  phase('候选稿')
  const proposal = await agent(`阅读目标手册，按原文事实提出一个连续区块的替换稿。完整覆盖 requiredIds，保留正确既有内容。仅提出修改，不写文件。${JSON.stringify({ ...base, sources, feedback })}`, {
    label: `writer-${round}`, agentType: 'handbook-writer', schema: proposalSchema,
  })
  if (!proposal || proposal.target !== target || !exactIds(proposal.coveredIds)
    || !proposal.before?.trim() || !proposal.after?.trim() || !Array.isArray(proposal.blockers) || proposal.blockers.length)
    return { ...base, status: 'blocked', reason: '候选稿范围、覆盖或来源存在阻塞', sources, proposal, attempts }

  phase('独立双审')
  const reviews = await parallel([
    () => agent(`独立读取原文核对候选稿及其替换前后内容。每个 requiredId 必须有具体证据。${JSON.stringify({ requiredIds, references, before: proposal.before, draft: proposal.after, task })}`, {
      label: `fidelity-${round}`, agentType: 'handbook-fidelity', schema: reviewSchema,
    }),
    () => agent(`仅依靠下面的候选正文回答验收问题。原文和作者说明不可用；不能用背景知识补足文中没有写的步骤。${JSON.stringify({ questions, draft: proposal.after })}`, {
      label: `learner-${round}`, agentType: 'handbook-learner', schema: reviewSchema,
    }),
  ])
  attempts.push({ round, proposal, reviews })
  if (reviews.some(r => r === null))
    return { ...base, status: 'blocked', reason: '独立审稿任务未返回结果', sources, attempts }
  if (reviews.every(validReview))
    return { ...base, status: 'reviewed', sources, proposal, reviews, attempts }
  feedback = reviews.map((review, i) => ({ reviewer: i === 0 ? 'fidelity' : 'learner', review }))
}
return { ...base, status: 'needs_rework', reason: '三轮候选稿后仍未双审通过，保留未完成状态', sources, attempts }
