# Generated model catalog snapshot

Snapshot of `pi/packages/ai` `src/providers/data/` produced by `node scripts/generate-models.ts` (non-strict) at pi commit `ff72faba2`, generatedAt `2026-09-28T03:09:18.703Z` (see `.manifest.json`).

Shape: `<provider>.json` = `{ [api]: { ["chat"|"image"|"classifier":<modelId>]: Model } }`. `.manifest.json` carries schemaVersion/generatedAt/structureHash/files (sha256).

To regenerate:

    cd /Users/mo/OPC/pi/packages/ai && node scripts/generate-models.ts
    cp src/providers/data/*.json src/providers/data/.manifest.json \
       /Users/mo/OPC/workspace/openagent/ai/catalog/data/

Non-strict mode tolerates unreachable upstream sources (their shards come back empty); the 42 shards here were generated with models.dev and openrouter.ai reachable.
