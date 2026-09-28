// Gate: standing documents stay within the word ceilings declared in
// scripts/doc-budgets.manifest.json. Every manifest entry must exist; English
// sources are budgeted, their .zh.md counterparts are not.

import { existsSync } from 'node:fs'
import { join } from 'node:path'
import { readRel, repoRoot } from './lib/md.mjs'

const manifest = JSON.parse(readRel(repoRoot, 'scripts/doc-budgets.manifest.json'))

let failures = 0
for (const rel of Object.keys(manifest).sort()) {
  const ceiling = manifest[rel]
  if (!existsSync(join(repoRoot, rel))) {
    console.error(`verify-doc-budgets: ${rel}: manifest entry has no file`)
    failures++
    continue
  }
  const words = readRel(repoRoot, rel).split(/\s+/).filter(Boolean).length
  if (words > ceiling) {
    console.error(`verify-doc-budgets: ${rel}: ${words} words exceed ceiling ${ceiling}`)
    failures++
  }
}

if (failures > 0) {
  console.error('verify-doc-budgets: relocate, then condense, then raise the ceiling with a justified manifest diff')
  process.exit(1)
}
console.log(`verify-doc-budgets: ok (${Object.keys(manifest).length} standing document(s))`)
