// Gate: every relative Markdown link target resolves to an existing file or
// directory. External targets (http, mailto) and pure anchors are skipped.

import { existsSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { analyze, markdownFiles, readRel, repoRoot } from './lib/md.mjs'

let failures = 0
let checked = 0
for (const rel of markdownFiles(repoRoot)) {
  const { links } = analyze(readRel(repoRoot, rel))
  for (const { target } of links) {
    if (/^(https?:|mailto:|irc:|#)/i.test(target)) continue
    const pathOnly = target.split('#')[0]
    if (pathOnly === '') continue
    checked++
    const resolved = join(dirname(join(repoRoot, rel)), pathOnly)
    if (!existsSync(resolved)) {
      console.error(`verify-md-links: ${rel}: broken link -> ${target}`)
      failures++
    }
  }
}
if (failures > 0) {
  console.error(`verify-md-links: ${failures} broken link(s)`)
  process.exit(1)
}
console.log(`verify-md-links: ok (${checked} link(s))`)
