import { createHash } from 'node:crypto'
import fs from 'node:fs'
import path from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'

function print(value) {
  process.stdout.write(`${value}\n`)
}

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const [target, items, ...description] = process.argv.slice(2)
if (!/^guide\/handbook\/(?:0[1-9]|1\d|2[0-7])-[a-z0-9-]+\.md$/.test(target || '') || !items || !description.length)
  throw new Error('用法: node .omp/handbook/prepare.js guide/handbook/NN-name.md 单元#K1,单元#K2 业务场景说明')
const text = fs.readFileSync(path.join(root, target))
print(JSON.stringify({
  name: 'handbook-batch',
  args: { target, items, revision: createHash('sha256').update(text).digest('hex'), task: description.join(' ') },
  background: true,
  concurrency: 3,
  maxAgents: 20,
  agentRetries: 1,
}, null, 2))
