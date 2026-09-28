// Gate: the Agent Note tree follows the closed classification. Lifecycle
// folders come from the closed lifecycle set, class folders from the closed
// class set, notes sit exactly one class deep, and note filenames carry the
// first-proposed date. An empty tree is valid for a fresh project.

import { existsSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { repoRoot } from './lib/md.mjs'

const LIFECYCLES = new Set(['proposed', 'implemented', 'rejected', 'archived'])
const CLASSES = new Set(['feature', 'bug-fix', 'simplification', 'architecture', 'process', 'testing'])
const ROOT_FILES = new Set(['README.md', 'README.zh.md', 'AGENTS.md'])
const NAME_RE = /^\d{4}-\d{2}-\d{2}-\S+\.(md|zh\.md)$/

let failures = 0
const fail = (msg) => { console.error(`verify-agent-note-classification: ${msg}`); failures++ }

const notesDir = join(repoRoot, '.agents', 'notes')
const notesPresent = existsSync(notesDir)
if (!notesPresent) fail('missing .agents/notes tree; copy it from the scaffold or drop this gate')
for (const ent of notesPresent ? readdirSync(notesDir, { withFileTypes: true }) : []) {
  if (!ent.isDirectory()) {
    if (!ROOT_FILES.has(ent.name)) fail(`unexpected file at notes root: ${ent.name}`)
    continue
  }
  if (!LIFECYCLES.has(ent.name)) {
    fail(`unknown lifecycle folder: ${ent.name} (expected one of ${[...LIFECYCLES].join(', ')})`)
    continue
  }
  const lifecycleDir = join(notesDir, ent.name)
  for (const sub of readdirSync(lifecycleDir, { withFileTypes: true })) {
    if (!sub.isDirectory()) {
      if (sub.name !== 'AGENTS.md') fail(`${ent.name}/${sub.name}: notes must sit inside a class folder`)
      continue
    }
    if (!CLASSES.has(sub.name)) {
      fail(`unknown class folder: ${ent.name}/${sub.name} (expected one of ${[...CLASSES].join(', ')})`)
      continue
    }
    const classDir = join(lifecycleDir, sub.name)
    for (const file of readdirSync(classDir)) {
      if (file === '.gitkeep') continue
      if (!NAME_RE.test(file)) fail(`${ent.name}/${sub.name}/${file}: name must be yyyy-mm-dd-topic-title.md(.zh.md)`)
    }
  }
}

if (failures > 0) {
  console.error(`verify-agent-note-classification: ${failures} failure(s)`)
  process.exit(1)
}
console.log('verify-agent-note-classification: ok (closed tree)')
