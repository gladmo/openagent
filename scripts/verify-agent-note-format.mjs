// Gate: Agent Notes follow the uniform format — the three-line header, the
// lifecycle-agreeing Status line, the required body sections, and no
// proposal-era spec-speak in implemented notes. Checks both languages; the
// machine tokens (# Agent Note: and Status:) stay English verbatim.

import { markdownFiles, readRel, repoRoot } from './lib/md.mjs'

const NOTE_RE = /^\.agents\/notes\/(proposed|implemented|rejected|archived)\/[^/]+\/(.+\.md)$/

function sections(text) {
  const found = new Set()
  for (const m of text.matchAll(/^##\s+(.+?)\s*$/gm)) found.add(m[1])
  return found
}

let failures = 0
let checked = 0
for (const rel of markdownFiles(repoRoot)) {
  const match = NOTE_RE.exec(rel)
  if (!match) continue
  const lifecycle = match[1]
  const text = readRel(repoRoot, rel)
  const lines = text.split(/\r?\n/)
  const fail = (msg) => { console.error(`verify-agent-note-format: ${rel}: ${msg}`); failures++ }

  checked++
  if (!/^# Agent Note: \S/.test(lines[0] ?? '')) fail('line 1 must be "# Agent Note: <title>"')
  if ((lines[1] ?? '') !== '') fail('line 2 must be blank')
  const statusLine = lines[2] ?? ''
  if (!/^Status: /.test(statusLine)) fail('line 3 must start with "Status: "')

  if (lifecycle === 'proposed' && statusLine !== 'Status: proposed') fail('proposed note must carry "Status: proposed"')
  if (lifecycle === 'implemented' && statusLine !== 'Status: implemented') fail('implemented note must carry "Status: implemented"')
  if (lifecycle === 'rejected' && !/^Status: rejected — \S/.test(statusLine)) fail('rejected note must carry "Status: rejected — <why>"')
  if (lifecycle === 'archived') {
    if (statusLine !== 'Status: implemented') fail('archived note keeps "Status: implemented"')
    if (!/^Archived: \d{4}-\d{2}-\d{2}$/.test(lines[3] ?? '')) fail('line 4 must be "Archived: YYYY-MM-DD"')
  }

  const have = sections(text)
  const require = (name) => { if (!have.has(name)) fail(`missing "## ${name}"`) }
  require('Problem')
  require('Alternatives considered')
  if (lifecycle === 'proposed' || lifecycle === 'rejected') require('Proposal')
  if (lifecycle === 'implemented' || lifecycle === 'archived') {
    require('Decision')
    require('Consequences')
    for (const banned of ['Proposal', 'Plan', 'Migration plan', 'Acceptance criteria']) {
      if (have.has(banned)) fail(`implemented note must not carry "## ${banned}"`)
    }
  }
}

if (failures > 0) {
  console.error(`verify-agent-note-format: ${failures} failure(s)`)
  process.exit(1)
}
console.log(`verify-agent-note-format: ok (${checked} note(s))`)
