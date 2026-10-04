package codingagent

// Port of core/cache-stats.ts and core/experimental.ts: cumulative cache
// waste detection over session entries with per-message miss collection
// and the just-completed-message probe.

import (
	"math"
	"time"
)

// CacheTTLMS is the prompt-cache idle gap worth mentioning as a miss
// cause (Anthropic's default 5-minute TTL).
const CacheTTLMS = 5 * 60 * 1000

// noiseFloorTokens bounds granularity noise.
const noiseFloorTokens = 1024

// CacheMiss is one counted miss on an assistant message.
type CacheMiss struct {
	// MissedTokens were in the previous prompt but not read from cache.
	MissedTokens int
	// MissedCost is extra dollars vs. a full hit (0 when pricing is
	// unknown).
	MissedCost float64
	// IdleMS is milliseconds since the previous request.
	IdleMS int64
	// ModelChanged reports a model switch vs. the previous request.
	ModelChanged bool
}

// CacheWasteTotals aggregates the counted misses.
type CacheWasteTotals struct {
	MissedTokens int
	MissedCost   float64
	MissCount    int
}

// ModelPriceSource is the minimal pricing lookup ($/million tokens).
type ModelPriceSource interface {
	// CacheReadPrice returns the provider/model cache-read price.
	CacheReadPrice(provider, modelID string) (float64, bool)
}

// assistantUsage carries the fields the scan reads.
type assistantUsage struct {
	Input          float64
	CacheRead      float64
	CacheWrite     float64
	CostInput      float64
	CostCacheRead  float64
	CostCacheWrite float64
}

// assistantCacheMessage is the assistant-message surface the scan reads.
type assistantCacheMessage struct {
	Provider    string
	Model       string
	TimestampMs float64
	Usage       assistantUsage
}

// previousRequest is the last request the scan saw.
type previousRequest struct {
	promptTokens  float64
	modelKey      string
	timestamp     float64
	reportedCache bool
}

// detectMiss computes one message's miss (nil when nothing counts: first
// turn, after a reset, no cache activity ever, or below the noise floor).
func detectMiss(prev *previousRequest, message *assistantCacheMessage, models ModelPriceSource) *CacheMiss {
	usage := message.Usage
	promptTokens := usage.Input + usage.CacheRead + usage.CacheWrite
	if prev == nil || promptTokens <= 0 || (usage.CacheRead+usage.CacheWrite == 0 && !prev.reportedCache) {
		return nil
	}

	missedTokens := math.Min(prev.promptTokens, promptTokens) - usage.CacheRead
	if missedTokens <= noiseFloorTokens {
		return nil
	}

	// Extra cost = missed tokens at the paid rate (input/cacheWrite incl.
	// the write premium) instead of the cache-read rate.
	paidTokens := usage.Input + usage.CacheWrite
	paidPerToken := 0.0
	if paidTokens > 0 {
		paidPerToken = (usage.CostInput + usage.CostCacheWrite) / paidTokens
	}
	readPerToken := 0.0
	if usage.CacheRead > 0 {
		readPerToken = usage.CostCacheRead / usage.CacheRead
	} else if price, ok := models.CacheReadPrice(message.Provider, message.Model); ok {
		readPerToken = price / 1_000_000
	}

	return &CacheMiss{
		MissedTokens: int(missedTokens),
		MissedCost:   missedTokens * math.Max(0, paidPerToken-readPerToken),
		IdleMS:       int64(math.Max(0, message.TimestampMs-prev.timestamp)),
		ModelChanged: message.Provider+"/"+message.Model != prev.modelKey,
	}
}

func asPreviousRequest(message *assistantCacheMessage, reportedCache bool) *previousRequest {
	usage := message.Usage
	promptTokens := usage.Input + usage.CacheRead + usage.CacheWrite
	if promptTokens <= 0 {
		return nil
	}
	return &previousRequest{
		promptTokens:  promptTokens,
		modelKey:      message.Provider + "/" + message.Model,
		timestamp:     message.TimestampMs,
		reportedCache: reportedCache || usage.CacheRead+usage.CacheWrite > 0,
	}
}

// cacheScanResult carries the scan state.
type cacheScanResult struct {
	prev   *previousRequest
	totals CacheWasteTotals
	misses map[*assistantCacheMessage]*CacheMiss
}

// scanCacheEntries walks entries maintaining the previous-request state.
func scanCacheEntries(entries []SessionEntry, models ModelPriceSource) *cacheScanResult {
	result := &cacheScanResult{misses: map[*assistantCacheMessage]*CacheMiss{}}
	prev := (*previousRequest)(nil)

	for _, entry := range entries {
		switch typed := entry.(type) {
		case *CompactionEntry, *BranchSummaryEntry:
			// The context legitimately changed; the next prompt is new
			// content, not re-billed content. Model switches are NOT
			// exempt: they re-bill the full prompt and should count.
			prev = nil
			continue

		case *UsageEntry:
			if typed.Kind == "cache_warm" {
				var usage struct {
					Input      float64 `json:"input"`
					CacheRead  float64 `json:"cacheRead"`
					CacheWrite float64 `json:"cacheWrite"`
				}
				if err := jsonUnmarshal(typed.Usage, &usage); err == nil {
					promptTokens := usage.Input + usage.CacheRead + usage.CacheWrite
					if promptTokens > 0 {
						parsed, parseErr := time.Parse(time.RFC3339, typed.Timestamp)
						timestamp := float64(0)
						if parseErr == nil {
							timestamp = float64(parsed.UnixMilli())
						}
						prev = &previousRequest{
							promptTokens:  promptTokens,
							modelKey:      typed.Provider + "/" + typed.Model,
							timestamp:     timestamp,
							reportedCache: true,
						}
					}
				}
			}

		case *SessionMessageEntry:
			var probe struct {
				Role string `json:"role"`
			}
			if err := jsonUnmarshal(typed.Message, &probe); err != nil || probe.Role != "assistant" {
				continue
			}
			var message struct {
				Provider    string  `json:"Provider"`
				Model       string  `json:"Model"`
				TimestampMs float64 `json:"TimestampMs"`
				Usage       struct {
					Input      float64 `json:"Input"`
					CacheRead  float64 `json:"CacheRead"`
					CacheWrite float64 `json:"CacheWrite"`
					Cost       struct {
						Input      float64 `json:"Input"`
						CacheRead  float64 `json:"CacheRead"`
						CacheWrite float64 `json:"CacheWrite"`
					} `json:"Cost"`
				} `json:"Usage"`
			}
			if err := jsonUnmarshal(typed.Message, &message); err != nil {
				continue
			}
			assistant := &assistantCacheMessage{
				Provider:    message.Provider,
				Model:       message.Model,
				TimestampMs: message.TimestampMs,
				Usage: assistantUsage{
					Input:          message.Usage.Input,
					CacheRead:      message.Usage.CacheRead,
					CacheWrite:     message.Usage.CacheWrite,
					CostInput:      message.Usage.Cost.Input,
					CostCacheRead:  message.Usage.Cost.CacheRead,
					CostCacheWrite: message.Usage.Cost.CacheWrite,
				},
			}

			if miss := detectMiss(prev, assistant, models); miss != nil {
				result.totals.MissedTokens += miss.MissedTokens
				result.totals.MissedCost += miss.MissedCost
				result.totals.MissCount++
				result.misses[assistant] = miss
			}
			if next := asPreviousRequest(assistant, prev != nil && prev.reportedCache); next != nil {
				prev = next
			}
		}
	}
	result.prev = prev
	return result
}

// ComputeCacheWaste totals the re-billed prompt tokens across a session.
func ComputeCacheWaste(entries []SessionEntry, models ModelPriceSource) CacheWasteTotals {
	return scanCacheEntries(entries, models).totals
}

// CollectCacheMisses returns every counted miss keyed by its assistant
// message (for transcript notices when rebuilding the chat).
func CollectCacheMisses(entries []SessionEntry, models ModelPriceSource) map[*assistantCacheMessage]*CacheMiss {
	return scanCacheEntries(entries, models).misses
}

// DetectCacheMiss probes a just-completed message; entries must not yet
// contain it.
func DetectCacheMiss(entries []SessionEntry, message *assistantCacheMessage, models ModelPriceSource) *CacheMiss {
	return detectMiss(scanCacheEntries(entries, models).prev, message, models)
}

// AreExperimentalFeaturesEnabled gates experimental/ features.
func AreExperimentalFeaturesEnabled() bool {
	return osGetenv("PI_EXPERIMENTAL") == "1"
}
