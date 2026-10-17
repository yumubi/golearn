import { createHash } from 'node:crypto'
import fs from 'node:fs'
import { homedir } from 'node:os'
import path from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'
import './patch-plugin.js'

function print(value) {
  process.stdout.write(`${value}\n`)
}

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const home = path.join(homedir(), '.omp/workflows')
const key = `${path.basename(root).toLowerCase().replace(/[^a-z0-9._-]+/g, '-').slice(0, 48)}-${createHash('sha256').update(root).digest('hex').slice(0, 12)}`
function json(file, value) {
  fs.mkdirSync(path.dirname(file), { recursive: true })
  fs.writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`)
}
const settingsPath = path.join(home, 'projects', key, 'settings.json')
const existing = fs.existsSync(settingsPath) ? JSON.parse(fs.readFileSync(settingsPath, 'utf8')) : {}
if (fs.existsSync(settingsPath))
  fs.copyFileSync(settingsPath, `${settingsPath}.before-handbook-${Date.now()}`)
json(settingsPath, {
  ...existing,
  keywordTriggerEnabled: false,
  defaultConcurrency: 3,
  defaultAgentRetries: 1,
  persistAgentSessions: true,
  deliveredResultMaxChars: 30000,
  progressPanelMode: 'detailed',
  progressPanelMaxAgents: 4,
  excludeSubagentTools: [...new Set([...(existing.excludeSubagentTools || []), 'store_get', 'store_put'])],
  web: { enabled: true, open: false, announce: false },
})
// Loading the plugin in unrelated projects should not auto-arm ordinary prose.
if (!fs.existsSync(path.join(home, 'settings.json')))
  json(path.join(home, 'settings.json'), { keywordTriggerEnabled: false, web: { enabled: false } })
const model = process.argv[2]
if (model && !fs.existsSync(path.join(home, 'model-tiers.json')))
  json(path.join(home, 'model-tiers.json'), { tiers: { small: model, medium: model, big: model } })
json(path.join(root, '.omp/workflows/saved/handbook-batch.workflowon'), {
  name: 'handbook-batch',
  description: '指定 1-8 个原文条目，提出候选稿并独立双审，通过后才允许落盘',
  location: 'project',
  savedAt: new Date().toISOString(),
  script: fs.readFileSync(path.join(root, '.omp/handbook/batch.workflow'), 'utf8'),
  parameters: {
    target: { type: 'string', required: true, description: '手册章节相对路径' },
    items: { type: 'string', required: true, description: '逗号分隔的单元#条目编号，最多 8 个' },
    revision: { type: 'string', required: true, description: '目标文件当前 SHA-256' },
  },
})
print(`Registered /handbook-batch for ${root}`)
print(`Project workflow settings: ${settingsPath}`)
