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

const here = path.dirname(fileURLToPath(import.meta.url))
const plugin = path.join(homedir(), '.omp/plugins/node_modules/omp-dynamic-workflows')
const target = path.join(plugin, 'src/omp-lazy.ts')
const patch = fs.readFileSync(path.join(here, 'omp-lazy-compat.ts'))
const current = fs.readFileSync(target)
const hash = value => createHash('sha256').update(value).digest('hex')
const upstreamHash = 'e5f2492db0f27453cb13ecdfa71bb55dbb86296589a886ffe1fd82badd2d42a9'
assert.equal(JSON.parse(fs.readFileSync(path.join(plugin, 'package.json'))).version, '0.1.0', '插件版本已变化，请先复核兼容补丁')
if (hash(current) === hash(patch)) {
  print('OMP 18 compatibility bridge already installed')
}
else {
  assert.equal(hash(current), upstreamHash, '插件桥接文件已变化，拒绝覆盖未知版本')
  fs.writeFileSync(`${target}.upstream`, current, { flag: 'wx' })
  fs.writeFileSync(target, patch)
  print('Installed OMP 18 compatibility bridge; original retained as omp-lazy.ts.upstream')
}
