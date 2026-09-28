# Postmortems

English | [中文](README.zh.md)

Numbered incident records for openagent. A postmortem is the only documentation tier where narrative belongs: it tells the story of a defect that reached users or CI, establishes the root cause, and converts into durable fixes.

## Format

Files are `NNNN-slug.md` with the next free number, paired with `NNNN-slug.zh.md`. Each postmortem carries these sections:

- **Impact:** who observed what, and how badly.
- **Timeline:** ordered evidence with timestamps; record facts, not a teaching sequence.
- **Root cause:** the mechanism, not the proximate mistake.
- **Action items:** each item converts into an issue, a test, a gate, or an [Agent Note](../../.agents/notes/README.md); a postmortem without executed follow-up is incomplete.

## Rules

- Write the postmortem when the incident closes, not when the fix lands; the timeline is evidence.
- Link every follow-up from its action item; the postmortem itself stays an incident record, not a task tracker.
- Blameless on people, strict on mechanisms: name the condition that allowed the defect, not the author.
