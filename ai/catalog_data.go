package ai

// catalog_data.go loads the embedded model-catalog snapshot (model-catalog.ts
// + models.generated.ts + providers/data/*.json + .manifest.json). The
// shards are generated in the pi repo by scripts/generate-models.ts and
// snapshotted into catalog/data (see that directory's README).

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/gladmo/openagent/jsonx"
)

//go:embed all:catalog/data
var catalogFS embed.FS

// ModelDataSchemaVersion must match the manifest.
const ModelDataSchemaVersion = 6

// ModelDataManifest mirrors .manifest.json.
type ModelDataManifest struct {
	SchemaVersion int
	GeneratedAt   string
	StructureHash string
	Files         map[string]string // file name -> sha256
}

// CatalogModelEntry is one decoded catalog entry.
type CatalogModelEntry struct {
	Provider string
	API      string
	Model    *Model
}

// CatalogProviderShard is one provider's flattened catalog.
type CatalogProviderShard struct {
	Provider string
	Chat     []*Model
	All      []CatalogModelEntry
}

var (
	catalogOnce     sync.Once
	catalogShards   map[string]*CatalogProviderShard
	catalogOrder    []string
	catalogManifest *ModelDataManifest
	catalogErr      error
)

// LoadModelCatalog parses the embedded snapshot once. Returns the provider
// shards (keyed by provider id, insertion-ordered via CatalogProviderIDs)
// and the manifest.
func LoadModelCatalog() (map[string]*CatalogProviderShard, *ModelDataManifest, error) {
	catalogOnce.Do(func() {
		catalogShards, catalogOrder, catalogManifest, catalogErr = decodeModelCatalog()
	})
	return catalogShards, catalogManifest, catalogErr
}

// CatalogProviderIDs returns the snapshot's provider ids in manifest order.
func CatalogProviderIDs() []string {
	if _, _, err := LoadModelCatalog(); err != nil {
		return nil
	}
	return catalogOrder
}

func decodeModelCatalog() (map[string]*CatalogProviderShard, []string, *ModelDataManifest, error) {
	manifestBytes, err := catalogFS.ReadFile("catalog/data/.manifest.json")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("model catalog manifest missing: %w", err)
	}
	manifestValue, err := jsonx.ParseBytes(manifestBytes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("model catalog manifest invalid: %w", err)
	}
	manifestObj, ok := JxObj(manifestValue)
	if !ok {
		return nil, nil, nil, fmt.Errorf("model catalog manifest must be an object")
	}
	manifest := &ModelDataManifest{Files: map[string]string{}}
	if schemaVersion, has := JxFloat(manifestObj, "schemaVersion"); has {
		manifest.SchemaVersion = int(schemaVersion)
	}
	manifest.GeneratedAt, _ = JxString(manifestObj, "generatedAt")
	manifest.StructureHash, _ = JxString(manifestObj, "structureHash")
	if files, ok := JxObjectField(manifestObj, "files"); ok {
		for _, name := range files.Keys() {
			hash, _ := JxString(files, name)
			manifest.Files[name] = hash
		}
	}
	if manifest.SchemaVersion != ModelDataSchemaVersion {
		return nil, nil, nil, fmt.Errorf("model catalog schema version %d unsupported (want %d)", manifest.SchemaVersion, ModelDataSchemaVersion)
	}

	shards := map[string]*CatalogProviderShard{}
	var order []string
	fileNames := make([]string, 0, len(manifest.Files))
	for name := range manifest.Files {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	for _, name := range fileNames {
		provider := strings.TrimSuffix(name, ".json")
		data, err := catalogFS.ReadFile("catalog/data/" + name)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("model catalog shard %s missing: %w", name, err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != manifest.Files[name] {
			return nil, nil, nil, fmt.Errorf("model catalog shard %s failed its manifest hash", name)
		}
		value, err := jsonx.ParseBytes(data)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("model catalog shard %s invalid JSON: %w", name, err)
		}
		root, ok := JxObj(value)
		if !ok {
			return nil, nil, nil, fmt.Errorf("model catalog shard %s must be an object", name)
		}
		shard := &CatalogProviderShard{Provider: provider}
		for _, api := range root.Keys() {
			groupValue, _ := root.Get(api)
			group, ok := JxObjectField(root, api)
			if !ok {
				_ = groupValue
				continue
			}
			for _, modelKey := range group.Keys() {
				entryValue, _ := group.Get(modelKey)
				entry, ok := JxObj(entryValue)
				if !ok {
					continue
				}
				model, err := decodeCatalogModel(entry, provider, api)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("model catalog shard %s entry %s: %w", name, modelKey, err)
				}
				shard.All = append(shard.All, CatalogModelEntry{Provider: provider, API: api, Model: model})
				if model.Type == "" || model.Type == "chat" {
					shard.Chat = append(shard.Chat, model)
				}
			}
		}
		if len(shard.All) == 0 {
			return nil, nil, nil, fmt.Errorf("model catalog shard %s contains no model data", name)
		}
		shards[provider] = shard
		order = append(order, provider)
	}
	if len(order) == 0 {
		return nil, nil, nil, fmt.Errorf("model catalog manifest lists no shards")
	}
	return shards, order, manifest, nil
}

// decodeCatalogModel converts one catalog JSON entry into a Model. Field
// names mirror the TS Model serialization.
func decodeCatalogModel(entry *jsonx.Obj, provider, api string) (*Model, error) {
	model := &Model{Provider: provider}
	model.ID, _ = JxString(entry, "id")
	model.Name, _ = JxString(entry, "name")
	model.API, _ = JxString(entry, "api")
	model.BaseURL, _ = JxString(entry, "baseUrl")
	if model.API == "" {
		model.API = api
	}
	if modelType, has := JxString(entry, "type"); has {
		model.Type = modelType
	}
	if model.ID == "" || model.API == "" {
		return nil, fmt.Errorf("entry lacks id/api")
	}

	if input, ok := JxList(entry, "input"); ok {
		for _, value := range input {
			if s, isString := value.(string); isString {
				model.Input = append(model.Input, s)
			}
		}
	}
	if reasoning, has := JxBool(entry, "reasoning"); has {
		model.Reasoning = reasoning
	}
	if compatValue, has := entry.Get("compat"); has {
		model.Compat = compatValue
	}
	if cacheValue, has := entry.Get("promptCache"); has {
		if cache, ok := JxObj(cacheValue); ok {
			promptCache := &ModelPromptCache{}
			promptCache.Short, _ = JxFloat(cache, "short")
			promptCache.Long, _ = JxFloat(cache, "long")
			model.PromptCache = promptCache
		}
	}
	if limitsValue, has := entry.Get("inputLimits"); has {
		model.InputLimits = limitsValue
	}
	if headers, ok := JxObjectField(entry, "headers"); ok {
		model.Headers = map[string]string{}
		for _, name := range headers.Keys() {
			value, _ := JxString(headers, name)
			model.Headers[name] = value
		}
	}
	if cost, ok := JxObjectField(entry, "cost"); ok {
		model.Cost.Input, _ = JxFloat(cost, "input")
		model.Cost.Output, _ = JxFloat(cost, "output")
		model.Cost.CacheRead, _ = JxFloat(cost, "cacheRead")
		model.Cost.CacheWrite, _ = JxFloat(cost, "cacheWrite")
		if tiers, ok := JxList(cost, "tiers"); ok {
			for _, tierValue := range tiers {
				tier, ok := JxObj(tierValue)
				if !ok {
					continue
				}
				costTier := ModelCostTier{}
				costTier.Input, _ = JxFloat(tier, "input")
				costTier.Output, _ = JxFloat(tier, "output")
				costTier.CacheRead, _ = JxFloat(tier, "cacheRead")
				costTier.CacheWrite, _ = JxFloat(tier, "cacheWrite")
				costTier.InputTokensAbove, _ = JxFloat(tier, "inputTokensAbove")
				model.Cost.Tiers = append(model.Cost.Tiers, costTier)
			}
		}
	}
	if levelMap, ok := JxObjectField(entry, "thinkingLevelMap"); ok {
		model.ThinkingLevelMap = ThinkingLevelMap{}
		for _, level := range levelMap.Keys() {
			value, _ := levelMap.Get(level)
			if value == nil {
				model.ThinkingLevelMap[level] = nil
			} else if mapped, isString := value.(string); isString {
				mappedValue := mapped
				model.ThinkingLevelMap[level] = &mappedValue
			}
		}
	}
	model.ContextWindow, _ = JxFloat(entry, "contextWindow")
	model.MaxTokens, _ = JxFloat(entry, "maxTokens")
	return model, nil
}

// GetBuiltinModelDataGeneratedAt exposes the snapshot generation time
// (getBuiltinModelDataGeneratedAt).
func GetBuiltinModelDataGeneratedAt() string {
	_, manifest, err := LoadModelCatalog()
	if err != nil || manifest == nil {
		return ""
	}
	return manifest.GeneratedAt
}

// GetCatalogChatModels returns one provider's chat models (flattenChatModelCatalog).
func GetCatalogChatModels(provider string) []*Model {
	shards, _, err := LoadModelCatalog()
	if err != nil {
		return nil
	}
	if shard, ok := shards[provider]; ok {
		return shard.Chat
	}
	return nil
}
