package codingagent

// Tests for cache waste detection (adapted from cache-stats.test.ts).

import (
	"encoding/json"
	"testing"
)

type stubPriceSource struct {
	prices map[string]float64
}

func (s *stubPriceSource) CacheReadPrice(provider, modelID string) (float64, bool) {
	price, ok := s.prices[provider+"/"+modelID]
	return price, ok
}

func assistantEntry(t *testing.T, manager *SessionManager, provider, model string, input, cacheRead, cacheWrite, timestamp float64) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"role":        "assistant",
		"Provider":    provider,
		"Model":       model,
		"TimestampMs": timestamp,
		"Usage": map[string]any{
			"Input":      input,
			"CacheRead":  cacheRead,
			"CacheWrite": cacheWrite,
			"Cost": map[string]any{
				"Input":      input * 3 / 1e6,
				"CacheRead":  cacheRead * 0.3 / 1e6,
				"CacheWrite": cacheWrite * 3.75 / 1e6,
			},
		},
	})
	manager.AppendMessage(json.RawMessage(raw))
}

func TestComputeCacheWasteNoMiss(t *testing.T) {
	manager := newTestSessionManager(t, false)
	models := &stubPriceSource{}

	// Full cache hit: no miss.
	assistantEntry(t, manager, "p", "m", 100, 50000, 0, 1000)
	totals := ComputeCacheWaste(manager.GetEntries(), models)
	if totals.MissCount != 0 || totals.MissedTokens != 0 {
		t.Fatalf("totals = %+v", totals)
	}
}

func TestComputeCacheWasteMiss(t *testing.T) {
	manager := newTestSessionManager(t, false)
	models := &stubPriceSource{}

	// First turn establishes cache activity.
	assistantEntry(t, manager, "p", "m", 100, 50000, 2000, 1000)
	// Second turn: 40000 of the previous 52100 re-billed (cacheRead only
	// 12100).
	assistantEntry(t, manager, "p", "m", 40000, 12100, 0, 3000)

	totals := ComputeCacheWaste(manager.GetEntries(), models)
	if totals.MissCount != 1 {
		t.Fatalf("count = %d (%+v)", totals.MissCount, totals)
	}
	if totals.MissedTokens <= noiseFloorTokens {
		t.Fatalf("tokens = %d", totals.MissedTokens)
	}
	if totals.MissedCost <= 0 {
		t.Fatalf("cost = %f", totals.MissedCost)
	}
}

func TestComputeCacheWasteNoiseFloor(t *testing.T) {
	manager := newTestSessionManager(t, false)
	models := &stubPriceSource{}

	// Small miss below the noise floor: not counted.
	assistantEntry(t, manager, "p", "m", 100, 50000, 100, 1000)
	assistantEntry(t, manager, "p", "m", 500, 49500, 0, 2000)

	totals := ComputeCacheWaste(manager.GetEntries(), models)
	if totals.MissCount != 0 {
		t.Fatalf("noise counted = %+v", totals)
	}
}

func TestComputeCacheWasteProviderWithoutCache(t *testing.T) {
	manager := newTestSessionManager(t, false)
	models := &stubPriceSource{}

	// No cache activity ever reported: zero-cache turns mean nothing.
	assistantEntry(t, manager, "p", "m", 50000, 0, 0, 1000)
	assistantEntry(t, manager, "p", "m", 50000, 0, 0, 2000)

	totals := ComputeCacheWaste(manager.GetEntries(), models)
	if totals.MissCount != 0 {
		t.Fatalf("no-cache provider counted = %+v", totals)
	}
}

func TestComputeCacheWasteResetOnCompaction(t *testing.T) {
	manager := newTestSessionManager(t, false)
	models := &stubPriceSource{}

	assistantEntry(t, manager, "p", "m", 100, 50000, 2000, 1000)
	firstKept := "k"
	manager.AppendCompaction("summary", &firstKept, 100, nil, false, nil)
	// After compaction: the prompt is new content, not re-billed.
	assistantEntry(t, manager, "p", "m", 50000, 0, 0, 3000)

	totals := ComputeCacheWaste(manager.GetEntries(), models)
	if totals.MissCount != 0 {
		t.Fatalf("compaction miss counted = %+v", totals)
	}
}

func TestComputeCacheWasteModelChangeCounts(t *testing.T) {
	manager := newTestSessionManager(t, false)
	models := &stubPriceSource{}

	assistantEntry(t, manager, "p", "m1", 100, 50000, 2000, 1000)
	// Model switch re-bills the full prompt and should count.
	assistantEntry(t, manager, "p", "m2", 50000, 0, 0, 2000)

	totals := ComputeCacheWaste(manager.GetEntries(), models)
	if totals.MissCount != 1 {
		t.Fatalf("model change = %+v", totals)
	}
}

func TestCollectCacheMisses(t *testing.T) {
	manager := newTestSessionManager(t, false)
	models := &stubPriceSource{}

	assistantEntry(t, manager, "p", "m", 100, 50000, 2000, 1000)
	assistantEntry(t, manager, "p", "m", 40000, 12100, 0, 3000)

	misses := CollectCacheMisses(manager.GetEntries(), models)
	if len(misses) != 1 {
		t.Fatalf("misses = %d", len(misses))
	}
	for _, miss := range misses {
		if miss.MissedTokens <= noiseFloorTokens {
			t.Fatalf("tokens = %d", miss.MissedTokens)
		}
		if miss.ModelChanged {
			t.Fatal("same model reported changed")
		}
	}
}

func TestDetectCacheMiss(t *testing.T) {
	manager := newTestSessionManager(t, false)
	models := &stubPriceSource{}

	// Entries contain the first turn only.
	assistantEntry(t, manager, "p", "m", 100, 50000, 2000, 1000)

	// The just-completed message (not yet in entries).
	message := &assistantCacheMessage{
		Provider: "p", Model: "m", TimestampMs: 3000,
		Usage: assistantUsage{
			Input: 40000, CacheRead: 12100, CacheWrite: 0,
			CostInput: 0.12, CostCacheRead: 0.00363, CostCacheWrite: 0,
		},
	}
	miss := DetectCacheMiss(manager.GetEntries(), message, models)
	if miss == nil {
		t.Fatal("no miss")
	}
	if miss.MissedTokens <= noiseFloorTokens {
		t.Fatalf("tokens = %d", miss.MissedTokens)
	}
	if miss.ModelChanged {
		t.Fatal("same model flagged")
	}
	if miss.IdleMS != 2000 {
		t.Fatalf("idle = %d", miss.IdleMS)
	}
}

func TestAreExperimentalFeaturesEnabled(t *testing.T) {
	t.Setenv("PI_EXPERIMENTAL", "")
	if AreExperimentalFeaturesEnabled() {
		t.Fatal("empty enabled")
	}
	t.Setenv("PI_EXPERIMENTAL", "0")
	if AreExperimentalFeaturesEnabled() {
		t.Fatal("zero enabled")
	}
	t.Setenv("PI_EXPERIMENTAL", "1")
	if !AreExperimentalFeaturesEnabled() {
		t.Fatal("one disabled")
	}
}
