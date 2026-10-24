package codingagent

// Port of core/model-resolver.ts resolveModelReference (d7f7e0dfe):
// candidates are collected before a provider is chosen, so catalog order
// never decides the provider and an ambiguous match is reported instead of
// falling through to a weaker one.

import (
	"sort"
	"strings"

	"github.com/gladmo/openagent/ai"
)

// ThinkingSuffixMode controls how a trailing `:<suffix>` is treated.
type ThinkingSuffixMode string

const (
	// ThinkingSuffixNone keeps the suffix part of the id.
	ThinkingSuffixNone ThinkingSuffixMode = "none"
	// ThinkingSuffixStrict parses a valid thinking level; any other suffix
	// stays part of the id.
	ThinkingSuffixStrict ThinkingSuffixMode = "strict"
	// ThinkingSuffixLenient is like strict, but an invalid suffix is
	// dropped with a warning.
	ThinkingSuffixLenient ThinkingSuffixMode = "lenient"
)

// ModelReferenceRequest is one resolution ask.
type ModelReferenceRequest struct {
	// Text is a bare model id or `provider/id`, optionally followed by
	// `:<thinking>`.
	Text string
	// Provider, when set, restricts to models of that provider.
	Provider string
	// ThinkingSuffix policy for a trailing `:<suffix>`; ids such as
	// OpenRouter's `qwen/qwen3-coder:exacto` contain colons, so the suffix
	// is only considered when the full text matches no model.
	ThinkingSuffix ThinkingSuffixMode
	// Fuzzy matches id and name substrings when no exact match exists.
	Fuzzy bool
}

// ModelReferenceTarget describes an explicit or named provider for
// building a custom model id.
type ModelReferenceTarget struct {
	Provider       string
	ProviderModels []*ai.Model
	// ModelText is the text after the provider prefix as typed, including
	// any `:<suffix>`.
	ModelText string
	// ModelID is ModelText without a valid thinking suffix (unless
	// ThinkingSuffix is none).
	ModelID       string
	ThinkingLevel string
	HasThinking   bool
}

// ModelReferenceResult is the resolution outcome: resolved, ambiguous, or
// not-found (optionally with a custom-id target).
type ModelReferenceResult struct {
	Status string // "resolved" | "ambiguous" | "not-found"

	// Resolved fields.
	Model         *ai.Model
	ThinkingLevel string
	HasThinking   bool
	Warning       string

	// Ambiguous fields.
	Candidates  []*ai.Model
	UsableCount int

	// Not-found fields.
	Target    *ModelReferenceTarget
	HasTarget bool
}

// ResolveModelReference resolves a model reference with one provider
// selection policy. Usable only breaks ties between providers, so a unique
// match resolves even without credentials (--api-key first-time setup).
func ResolveModelReference(request ModelReferenceRequest, models []*ai.Model, usable func(provider string) bool) ModelReferenceResult {
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return ModelReferenceResult{Status: "not-found"}
	}
	constraint := strings.ToLower(request.Provider)
	pool := models
	if constraint != "" {
		pool = nil
		for _, model := range models {
			if strings.ToLower(model.Provider) == constraint {
				pool = append(pool, model)
			}
		}
	}
	result := resolveModelText(text, request, pool, usable)
	if result.Status != "not-found" {
		return result
	}

	split := splitProviderPrefix(text, pool)
	anchorIndex := -1
	if constraint != "" {
		anchorIndex = 0
	} else if split != nil {
		for index, model := range pool {
			if model.Provider == split.provider {
				anchorIndex = index
				break
			}
		}
	}
	if anchorIndex == -1 || len(pool) == 0 {
		return result
	}
	anchor := pool[anchorIndex]
	modelText := text
	if split != nil {
		modelText = split.modelText
	}
	target := &ModelReferenceTarget{Provider: anchor.Provider, ModelText: modelText, ModelID: modelText}
	if request.ThinkingSuffix != ThinkingSuffixNone {
		if base, level, ok := splitThinkingSuffix(modelText); ok {
			target.ModelID = base
			target.ThinkingLevel, target.HasThinking = level, true
		}
	}
	target.ProviderModels = []*ai.Model{anchor}
	for index, model := range pool {
		if index != anchorIndex && model.Provider == anchor.Provider {
			target.ProviderModels = append(target.ProviderModels, model)
		}
	}
	return ModelReferenceResult{Status: "not-found", Target: target, HasTarget: true}
}

func splitThinkingSuffix(text string) (base, level string, ok bool) {
	colonIndex := strings.LastIndex(text, ":")
	if colonIndex == -1 {
		return "", "", false
	}
	suffix := text[colonIndex+1:]
	if !isValidThinkingLevel(suffix) {
		return "", "", false
	}
	return text[:colonIndex], suffix, true
}

func splitProviderPrefix(text string, models []*ai.Model) *struct{ provider, modelText string } {
	slashIndex := strings.Index(text, "/")
	if slashIndex == -1 {
		return nil
	}
	prefix := strings.ToLower(strings.TrimSpace(text[:slashIndex]))
	modelText := strings.TrimSpace(text[slashIndex+1:])
	if prefix == "" || modelText == "" {
		return nil
	}
	for _, model := range models {
		if strings.ToLower(model.Provider) == prefix {
			return &struct{ provider, modelText string }{provider: model.Provider, modelText: modelText}
		}
	}
	return nil
}

func resolveModelText(text string, request ModelReferenceRequest, pool []*ai.Model, usable func(string) bool) ModelReferenceResult {
	if reference, ok := selectReference(text, pool, request.Fuzzy, usable); ok {
		return reference
	}
	if request.Fuzzy {
		if fuzzy, ok := selectFuzzy(fuzzyMatches(text, pool), usable); ok {
			return fuzzy
		}
	}
	if request.ThinkingSuffix == ThinkingSuffixNone {
		return ModelReferenceResult{Status: "not-found"}
	}
	if base, level, ok := splitThinkingSuffix(text); ok {
		inner := resolveModelText(base, request, pool, usable)
		if inner.Status == "not-found" {
			return inner
		}
		// Only use this thinking level when the inner recursion warned.
		if inner.Warning == "" {
			inner.ThinkingLevel, inner.HasThinking = level, true
		}
		return inner
	}
	// Keep an invalid suffix as part of the id and fail, rather than
	// resolve a different model.
	if request.ThinkingSuffix == ThinkingSuffixStrict {
		return ModelReferenceResult{Status: "not-found"}
	}
	colonIndex := strings.LastIndex(text, ":")
	if colonIndex == -1 {
		return ModelReferenceResult{Status: "not-found"}
	}
	suffix := text[colonIndex+1:]
	inner := resolveModelText(text[:colonIndex], request, pool, usable)
	if inner.Status == "not-found" {
		return inner
	}
	return ModelReferenceResult{
		Status:      inner.Status,
		Model:       inner.Model,
		Candidates:  inner.Candidates,
		UsableCount: inner.UsableCount,
		Warning:     "Invalid thinking level \"" + suffix + "\" in pattern \"" + text + "\". Using default instead.",
	}
}

// selectReference picks the exact-match candidates. provider/rest is only a
// hint: the whole text may also be a model id on another provider, such as
// gateway id `zai/glm-5`. The named provider wins when usable; otherwise a
// sole usable raw match wins; an unusable named match is still preferred
// over unusable raw matches, so --api-key can set up the named provider.
func selectReference(text string, pool []*ai.Model, fuzzy bool, usable func(string) bool) (ModelReferenceResult, bool) {
	lowerText := strings.ToLower(text)
	var raw []*ai.Model
	for _, model := range pool {
		if strings.ToLower(model.ID) == lowerText {
			raw = append(raw, model)
		}
	}
	split := splitProviderPrefix(text, pool)
	var named *ai.Model
	if split != nil {
		var providerModels []*ai.Model
		for _, model := range pool {
			if model.Provider == split.provider {
				providerModels = append(providerModels, model)
			}
		}
		lowerModelText := strings.ToLower(split.modelText)
		// The provider's own literal id (OpenRouter's `openrouter/free`)
		// beats its fuzzy matches.
		for _, model := range providerModels {
			if strings.ToLower(model.ID) == lowerModelText {
				named = model
				break
			}
		}
		if named == nil {
			for _, model := range raw {
				if model.Provider == split.provider {
					named = model
					break
				}
			}
		}
		if named == nil && fuzzy {
			named = bestFuzzyMatch(fuzzyMatches(split.modelText, providerModels))
		}
	}
	var others []*ai.Model
	for _, model := range raw {
		if model != named {
			others = append(others, model)
		}
	}
	candidates := others
	if named != nil {
		candidates = append([]*ai.Model{named}, others...)
	}
	if len(candidates) == 0 {
		return ModelReferenceResult{}, false
	}
	providers := map[string]bool{}
	for _, model := range candidates {
		providers[model.Provider] = true
	}
	if len(providers) == 1 {
		return ModelReferenceResult{Status: "resolved", Model: candidates[0]}, true
	}
	if named != nil && usable(named.Provider) {
		return ModelReferenceResult{Status: "resolved", Model: named}, true
	}
	var usableOthers []*ai.Model
	for _, model := range others {
		if usable(model.Provider) {
			usableOthers = append(usableOthers, model)
		}
	}
	if len(usableOthers) == 1 {
		return ModelReferenceResult{Status: "resolved", Model: usableOthers[0]}, true
	}
	if len(usableOthers) == 0 && named != nil {
		return ModelReferenceResult{Status: "resolved", Model: named}, true
	}
	return ModelReferenceResult{Status: "ambiguous", Candidates: candidates, UsableCount: len(usableOthers)}, true
}

func fuzzyMatches(text string, models []*ai.Model) []*ai.Model {
	lowerText := strings.ToLower(text)
	var matches []*ai.Model
	for _, model := range models {
		if strings.Contains(strings.ToLower(model.ID), lowerText) ||
			strings.Contains(strings.ToLower(model.Name), lowerText) {
			matches = append(matches, model)
		}
	}
	return matches
}

func selectFuzzy(matches []*ai.Model, usable func(string) bool) (ModelReferenceResult, bool) {
	if len(matches) == 0 {
		return ModelReferenceResult{}, false
	}
	providers := map[string]bool{}
	for _, model := range matches {
		providers[model.Provider] = true
	}
	var usableMatches []*ai.Model
	if len(providers) > 1 {
		for _, model := range matches {
			if usable(model.Provider) {
				usableMatches = append(usableMatches, model)
			}
		}
	}
	pool := matches
	if len(usableMatches) > 0 {
		pool = usableMatches
	}
	best := bestFuzzyMatch(pool)
	if best == nil {
		return ModelReferenceResult{}, false
	}
	// The same id on several providers would otherwise be decided by
	// catalog order.
	var tied []*ai.Model
	for _, model := range pool {
		if model.ID == best.ID {
			tied = append(tied, model)
		}
	}
	tiedProviders := map[string]bool{}
	for _, model := range tied {
		tiedProviders[model.Provider] = true
	}
	if len(tiedProviders) == 1 {
		return ModelReferenceResult{Status: "resolved", Model: best}, true
	}
	usableCount := 0
	if len(usableMatches) > 0 {
		usableCount = len(tied)
	}
	return ModelReferenceResult{Status: "ambiguous", Candidates: tied, UsableCount: usableCount}, true
}

// bestFuzzyMatch prefers aliases (`claude-sonnet-4-5`) over dated versions
// (`claude-sonnet-4-5-20250929`), then the latest.
func bestFuzzyMatch(matches []*ai.Model) *ai.Model {
	if len(matches) == 0 {
		return nil
	}
	var aliases []*ai.Model
	for _, model := range matches {
		if isAlias(model.ID) {
			aliases = append(aliases, model)
		}
	}
	pool := matches
	if len(aliases) > 0 {
		pool = aliases
	}
	sorted := append([]*ai.Model(nil), pool...)
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].ID > sorted[b].ID })
	return sorted[0]
}
