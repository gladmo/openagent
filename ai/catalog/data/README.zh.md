# 生成的模型目录快照

`pi/packages/ai` `src/providers/data/` 的快照，由 `node scripts/generate-models.ts`（非 strict 模式）在 pi 提交 `ff72faba2` 生成，generatedAt `2026-09-28T03:09:18.703Z`（见 `.manifest.json`）。

结构：`<provider>.json` = `{ [api]: { ["chat"|"image"|"classifier":<modelId>]: Model } }`。`.manifest.json` 携带 schemaVersion/generatedAt/structureHash/files（sha256）。

重新生成：

    cd /Users/mo/OPC/pi/packages/ai && node scripts/generate-models.ts
    cp src/providers/data/*.json src/providers/data/.manifest.json \
       /Users/mo/OPC/workspace/openagent/ai/catalog/data/

非 strict 模式容忍不可达的上游源（其分片为空）；这里的 42 个分片在 models.dev 与 openrouter.ai 可达时生成。
