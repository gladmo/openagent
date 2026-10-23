# Generated model catalog snapshot

Snapshot of `pi/packages/ai` `src/providers/data/` produced by `node scripts/generate-models.ts` (non-strict) at pi commit `bd3c9b823`, generatedAt `2026-10-23T19:11:20.382Z` (see `.manifest.json`).

Shape: `<provider>.json` = `{ [api]: { ["chat"|"image"|"classifier":<modelId>]: Model } }`. `.manifest.json` carries schemaVersion/generatedAt/structureHash/files (sha256).

To regenerate:

    cd /Users/mo/OPC/pi/packages/ai && node scripts/generate-models.ts
    cp src/providers/data/*.json src/providers/data/.manifest.json \
       /Users/mo/OPC/workspace/openagent/ai/catalog/data/

Non-strict mode tolerates unreachable upstream sources (their shards come back empty); the 42 shards here were generated with models.dev and openrouter.ai reachable.
