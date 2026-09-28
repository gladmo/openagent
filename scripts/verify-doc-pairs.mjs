// Gate: human-facing documents exist as .md + .zh.md pairs with mirrored
// heading structure. AGENTS.md files, skills, and the porting work-papers
// (PARITY.md, PORTING.md) are exempt as single-language by design.

import { analyze, isEnglishOnly, markdownFiles, readRel, repoRoot } from './lib/md.mjs'

let failures = 0
const english = []
const cache = new Set(markdownFiles(repoRoot))
function has(rel) { return cache.has(rel) }
for (const rel of cache) {
  if (isEnglishOnly(rel)) continue
  if (rel.endsWith('.zh.md')) {
    const twin = rel.slice(0, -'.zh.md'.length) + '.md'
    if (!has(twin)) {
      console.error(`verify-doc-pairs: ${rel}: missing English twin ${twin}`)
      failures++
    }
    continue
  }
  const twin = rel.slice(0, -'.md'.length) + '.zh.md'
  if (!has(twin)) {
    console.error(`verify-doc-pairs: ${rel}: missing Chinese twin ${twin}`)
    failures++
    continue
  }
  english.push(rel)
}

for (const rel of english) {
  const en = analyze(readRel(repoRoot, rel)).headings
  const zh = analyze(readRel(repoRoot, rel.slice(0, -'.md'.length) + '.zh.md')).headings
  if (en.length !== zh.length || en.some((level, i) => level !== zh[i])) {
    console.error(`verify-doc-pairs: ${rel}: heading structure differs from its .zh.md twin`)
    console.error(`  en: [${en.join(',')}] zh: [${zh.join(',')}]`)
    failures++
  }
}

if (failures > 0) {
  console.error(`verify-doc-pairs: ${failures} pairing failure(s)`)
  process.exit(1)
}
console.log(`verify-doc-pairs: ok (${english.length} pair(s))`)
