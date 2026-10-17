/**
 * OMP 18.11 standalone-binary compatibility for omp-dynamic-workflows 0.1.0.
 * Use Bun's YAML parser and the host-injected schema validator. Loading a
 * second SDK dependency graph from a compiled extension cannot resolve natives.
 * This file replaces only the plugin's lazy helper bridge, not its workflow engine.
 */
import { YAML } from 'bun'
import { hostTypeBox } from './omp-host.js'

export async function parseConfiguredThinkingLevel(value: unknown): Promise<string | undefined> {
  const levels = ['inherit', 'off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'auto']
  return typeof value === 'string' && levels.includes(value) ? value : undefined
}

function normalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(normalize)
  if (value !== null && typeof value === 'object') {
    return Object.fromEntries(Object.entries(value).map(([key, entry]) => [
      key.replace(/-([a-z])/g, (_, c: string) => c.toUpperCase()), normalize(entry),
    ]))
  }
  return value
}

export async function warmFrontmatter(): Promise<void> {}

export function parseFrontmatter(content: string): { frontmatter: Record<string, unknown>, body: string } {
  const text = content.replace(/\r\n?/g, '\n').replace(/<!--[\s\S]*?-->/g, '')
  if (!text.startsWith('---\n')) return { frontmatter: {}, body: text }
  const end = text.indexOf('\n---', 4)
  if (end < 0) throw new Error('Unclosed agent frontmatter')
  const data = YAML.parse(text.slice(4, end))
  if (data !== null && (typeof data !== 'object' || Array.isArray(data)))
    throw new Error('Agent frontmatter must be a YAML mapping')
  return { frontmatter: normalize(data || {}) as Record<string, unknown>, body: text.slice(end + 4).trim() }
}

export async function warmSchemaValidator(): Promise<void> {}

export function isJsonSchemaValueValid(schema: unknown, value: unknown): boolean {
  if (schema === true) return true
  if (schema === false) return false
  if (schema === null || typeof schema !== 'object') return false
  const validator = hostTypeBox().Type.Unsafe(schema as Record<string, unknown>) as unknown as {
    safeParse: (input: unknown) => { success: boolean }
  }
  return validator.safeParse(value).success
}
