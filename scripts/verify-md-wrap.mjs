// Gate: one physical line per paragraph. Code blocks, tables, lists, and
// frontmatter keep their formatting; consecutive plain-text lines are a
// wrapped paragraph and fail.

import { analyze, markdownFiles, readRel, repoRoot } from './lib/md.mjs'

let failures = 0
for (const rel of markdownFiles(repoRoot)) {
  const { wrapViolations } = analyze(readRel(repoRoot, rel))
  for (const line of wrapViolations) {
    console.error(`verify-md-wrap: ${rel}:${line}: paragraph continues on a second physical line`)
    failures++
  }
}
if (failures > 0) {
  console.error(`verify-md-wrap: ${failures} violation(s); use editor soft-wrap instead`)
  process.exit(1)
}
console.log(`verify-md-wrap: ok (${markdownFiles(repoRoot).length} file(s))`)
