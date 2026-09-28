// Shared Markdown utilities for the zero-dependency verification gates.
// Gates resolve the repository root from this file's location (scripts/lib/),
// so they behave identically from any working directory.

import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

/** The repository root that owns this scripts directory. */
export const repoRoot = fileURLToPath(new URL('../..', import.meta.url))

const SKIP_DIRS = new Set(['.git', 'node_modules', '.claude', '.generated', '.dist', '.cache'])

/** Every Markdown file under root as a sorted list of '/'-separated relative paths. */
export function markdownFiles(root) {
  const out = []
  const walk = (dir, prefix) => {
    for (const ent of readdirSync(dir, { withFileTypes: true })) {
      if (SKIP_DIRS.has(ent.name)) continue
      const rel = prefix === '' ? ent.name : `${prefix}/${ent.name}`
      let isDir = ent.isDirectory()
      if (ent.isSymbolicLink()) {
        try { isDir = statSync(join(dir, ent.name)).isDirectory() } catch { isDir = false }
      }
      if (isDir) walk(join(dir, ent.name), rel)
      else if (ent.name.endsWith('.md') && ent.name !== 'CLAUDE.md') out.push(rel)
    }
  }
  walk(root, '')
  out.sort()
  return out
}

/** Read a repo-relative file as UTF-8 text. */
export function readRel(root, rel) {
  return readFileSync(join(root, rel), 'utf8')
}

/**
 * One pass over a Markdown document, skipping fenced code blocks and YAML
 * frontmatter. Returns heading levels, Markdown links, and wrap violations
 * (a physical line that continues a paragraph started on the previous line,
 * violating the one-physical-line-per-paragraph rule).
 */
export function analyze(text) {
  const lines = text.split(/\r?\n/)
  const headings = []
  const links = []
  const wrapViolations = []
  let inFence = false
  let inFrontmatter = lines[0] === '---'
  let prevPlain = false
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    if (i === 0 && inFrontmatter) { prevPlain = false; continue }
    if (inFrontmatter) {
      if (line === '---' || line === '...') inFrontmatter = false
      prevPlain = false
      continue
    }
    if (/^\s*(```|~~~)/.test(line)) { inFence = !inFence; prevPlain = false; continue }
    if (inFence) continue
    for (const m of line.matchAll(/\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)/g)) {
      links.push({ text: m[1], target: m[2] })
    }
    const heading = /^(#{1,6})\s/.exec(line)
    if (heading) { headings.push(heading[1].length); prevPlain = false; continue }
    const trimmed = line.trim()
    const structural = trimmed === '' ||
      /^\s/.test(line) ||
      /^(\||>|-->|<!--|-|\*|\+|\d+[.)])/.test(trimmed)
    const plain = !structural
    if (plain && prevPlain) wrapViolations.push(i + 1)
    prevPlain = plain
  }
  return { headings, links, wrapViolations }
}

/** Names that never take a `.zh.md` counterpart: agent instructions, skills,
 *  and the single-language port work-papers PARITY.md and PORTING.md, which
 *  track the pi rewrite and retire when the port completes. */
const UNPAIRED_NAMES = new Set(['AGENTS.md', 'SKILL.md', 'PARITY.md', 'PORTING.md'])

export function isEnglishOnly(rel) {
  const name = rel.split('/').pop()
  return UNPAIRED_NAMES.has(name)
}
