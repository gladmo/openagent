package codingagent

// Port of core/model-resolver.ts: model-pattern parsing, alias/dated
// preference, glob scoping, CLI resolution with provider inference, and
// initial/restore model selection.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/minimatch"
)

// isValidThinkingLevel validates off/minimal/low/medium/high/xhigh/max.
func isValidThinkingLevel(value string) bool {
	switch value {
	case agent.ThinkingOff, agent.ThinkingMinimal, agent.ThinkingLow,
		agent.ThinkingMedium, agent.ThinkingHigh, agent.ThinkingXHigh, agent.ThinkingMax:
		return true
	}
	return false
}

// DefaultModelPerProvider lists default chat models for known providers.
var DefaultModelPerProvider = map[string]string{
	"amazon-bedrock":             "us.anthropic.claude-opus-4-6-v1",
	"ant-ling":                   "Ring-2.6-1T",
	"anthropic":                  "claude-opus-4-8",
	"openai":                     "gpt-5.5",
	"azure-openai-responses":     "gpt-5.4",
	"openai-codex":               "gpt-5.5",
	"radius":                     "balanced",
	"nvidia":                     "nvidia/nemotron-3-super-120b-a12b",
	"deepseek":                   "deepseek-v4-pro",
	"google":                     "gemini-3.1-pro-preview",
	"google-vertex":              "gemini-3.1-pro-preview",
	"github-copilot":             "gpt-5.4",
	"openrouter":                 "moonshotai/kimi-k2.6",
	"vercel-ai-gateway":          "zai/glm-5.1",
	"xai":                        "grok-4.7",
	"groq":                       "openai/gpt-oss-120b",
	"cerebras":                   "gpt-oss-120b",
	"zai":                        "glm-5.3",
	"zai-coding-cn":              "glm-5.3",
	"mistral":                    "devstral-medium-latest",
	"minimax":                    "MiniMax-M2.7",
	"minimax-cn":                 "MiniMax-M2.7",
	"moonshotai":                 "kimi-k2.6",
	"moonshotai-cn":              "kimi-k2.6",
	"huggingface":                "moonshotai/Kimi-K2.6",
	"fireworks":                  "accounts/fireworks/models/kimi-k2p6",
	"together":                   "moonshotai/Kimi-K2.6",
	"baseten":                    "zai-org/GLM-5.2",
	"opencode":                   "kimi-k2.6",
	"opencode-go":                "kimi-k2.6",
	"kimi-coding":                "kimi-for-coding",
	"meta":                       "muse-spark-1.3",
	"cloudflare-workers-ai":      "@cf/moonshotai/kimi-k2.6",
	"cloudflare-ai-gateway":      "workers-ai/@cf/moonshotai/kimi-k2.6",
	"qwen-token-plan":            "qwen3.7-max",
	"qwen-token-plan-cn":         "qwen3.7-max",
	"qwen-token-plan-individual": "qwen3.8-max",
	"xiaomi":                     "mimo-v2.5-pro",
	"xiaomi-token-plan-cn":       "mimo-v2.5-pro",
	"xiaomi-token-plan-ams":      "mimo-v2.5-pro",
	"xiaomi-token-plan-sgp":      "mimo-v2.5-pro",
}

// ScopedModel pairs a model with an explicit thinking level.
type ScopedModel struct {
	Model *ai.Model
	// ThinkingLevel is set when the pattern carried ":level".
	ThinkingLevel    string
	HasThinkingLevel bool
}

// datedSuffixPattern detects -YYYYMMDD suffixes.
var datedSuffixPattern = regexp.MustCompile(`-\d{8}$`)

// isAlias reports whether an id has no date suffix.
func isAlias(id string) bool {
	if strings.HasSuffix(id, "-latest") {
		return true
	}
	return !datedSuffixPattern.MatchString(id)
}

// FindExactModelReferenceMatch finds one exact reference; ambiguous matches
// across providers are rejected.
func FindExactModelReferenceMatch(modelReference string, availableModels []*ai.Model) *ai.Model {
	trimmedReference := strings.TrimSpace(modelReference)
	if trimmedReference == "" {
		return nil
	}
	normalizedReference := strings.ToLower(trimmedReference)

	var canonicalMatches []*ai.Model
	for _, model := range availableModels {
		if strings.ToLower(model.Provider+"/"+model.ID) == normalizedReference {
			canonicalMatches = append(canonicalMatches, model)
		}
	}
	if len(canonicalMatches) == 1 {
		return canonicalMatches[0]
	}
	if len(canonicalMatches) > 1 {
		return nil
	}

	if slashIndex := strings.Index(trimmedReference, "/"); slashIndex != -1 {
		provider := strings.TrimSpace(trimmedReference[:slashIndex])
		modelID := strings.TrimSpace(trimmedReference[slashIndex+1:])
		if provider != "" && modelID != "" {
			var providerMatches []*ai.Model
			for _, model := range availableModels {
				if strings.EqualFold(model.Provider, provider) && strings.EqualFold(model.ID, modelID) {
					providerMatches = append(providerMatches, model)
				}
			}
			if len(providerMatches) == 1 {
				return providerMatches[0]
			}
			if len(providerMatches) > 1 {
				return nil
			}
		}
	}

	var idMatches []*ai.Model
	for _, model := range availableModels {
		if strings.ToLower(model.ID) == normalizedReference {
			idMatches = append(idMatches, model)
		}
	}
	if len(idMatches) == 1 {
		return idMatches[0]
	}
	return nil
}

func tryMatchModel(modelPattern string, availableModels []*ai.Model) *ai.Model {
	if exactMatch := FindExactModelReferenceMatch(modelPattern, availableModels); exactMatch != nil {
		return exactMatch
	}

	lowerPattern := strings.ToLower(modelPattern)
	var matches []*ai.Model
	for _, model := range availableModels {
		if strings.Contains(strings.ToLower(model.ID), lowerPattern) ||
			strings.Contains(strings.ToLower(model.Name), lowerPattern) {
			matches = append(matches, model)
		}
	}
	if len(matches) == 0 {
		return nil
	}

	var aliases, datedVersions []*ai.Model
	for _, model := range matches {
		if isAlias(model.ID) {
			aliases = append(aliases, model)
		} else {
			datedVersions = append(datedVersions, model)
		}
	}

	pool := datedVersions
	if len(aliases) > 0 {
		pool = aliases
	}
	sort.SliceStable(pool, func(a, b int) bool { return pool[a].ID > pool[b].ID })
	return pool[0]
}

// ParsedModelResult carries a parsed pattern outcome.
type ParsedModelResult struct {
	Model            *ai.Model
	ThinkingLevel    string
	HasThinkingLevel bool
	Warning          string
}

// ParseModelOptions control invalid-suffix handling.
type ParseModelOptions struct {
	AllowInvalidThinkingLevelFallback *bool
}

func buildFallbackModel(provider, modelID string, availableModels []*ai.Model) *ai.Model {
	var providerModels []*ai.Model
	for _, model := range availableModels {
		if model.Provider == provider {
			providerModels = append(providerModels, model)
		}
	}
	if len(providerModels) == 0 {
		return nil
	}

	baseModel := providerModels[0]
	if defaultID, ok := DefaultModelPerProvider[provider]; ok {
		for _, model := range providerModels {
			if model.ID == defaultID {
				baseModel = model
				break
			}
		}
	}

	fallback := *baseModel
	fallback.ID = modelID
	fallback.Name = modelID
	return &fallback
}

// ParseModelPattern matches a pattern, splitting trailing :thinking levels.
func ParseModelPattern(pattern string, availableModels []*ai.Model, options *ParseModelOptions) ParsedModelResult {
	if exactMatch := tryMatchModel(pattern, availableModels); exactMatch != nil {
		return ParsedModelResult{Model: exactMatch}
	}

	lastColonIndex := strings.LastIndex(pattern, ":")
	if lastColonIndex == -1 {
		return ParsedModelResult{}
	}

	prefix := pattern[:lastColonIndex]
	suffix := pattern[lastColonIndex+1:]

	if isValidThinkingLevel(suffix) {
		result := ParseModelPattern(prefix, availableModels, options)
		if result.Model != nil {
			return ParsedModelResult{
				Model:            result.Model,
				ThinkingLevel:    suffix,
				HasThinkingLevel: result.Warning == "",
				Warning:          result.Warning,
			}
		}
		return result
	}

	allowFallback := true
	if options != nil && options.AllowInvalidThinkingLevelFallback != nil {
		allowFallback = *options.AllowInvalidThinkingLevelFallback
	}
	if !allowFallback {
		return ParsedModelResult{}
	}

	result := ParseModelPattern(prefix, availableModels, options)
	if result.Model != nil {
		return ParsedModelResult{
			Model:   result.Model,
			Warning: fmt.Sprintf("Invalid thinking level %q in pattern %q. Using default instead.", suffix, pattern),
		}
	}
	return result
}

// ModelScopeDiagnostic reports a scoping warning.
type ModelScopeDiagnostic struct {
	Type    string // "warning"
	Code    string // "no-match" | "invalid-thinking-level"
	Message string
	Pattern string
}

// ResolveModelScopeResult carries scoped models plus diagnostics.
type ResolveModelScopeResult struct {
	ScopedModels []ScopedModel
	Diagnostics  []ModelScopeDiagnostic
}

func containsGlobChar(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

// ResolveModelScopeFromModels resolves patterns (exact, partial, glob) to
// scoped models with alias preference and dedup.
func ResolveModelScopeFromModels(patterns []string, models []*ai.Model) ResolveModelScopeResult {
	availableModels := append([]*ai.Model(nil), models...)
	var scopedModels []ScopedModel
	var diagnostics []ModelScopeDiagnostic

	hasModel := func(target *ai.Model) bool {
		for _, scoped := range scopedModels {
			if ai.ModelsAreEqual(scoped.Model, target) {
				return true
			}
		}
		return false
	}

	for _, pattern := range patterns {
		if containsGlobChar(pattern) {
			globPattern := pattern
			thinkingLevel := ""
			hasThinkingLevel := false
			if colonIdx := strings.LastIndex(pattern, ":"); colonIdx != -1 {
				suffix := pattern[colonIdx+1:]
				if isValidThinkingLevel(suffix) {
					thinkingLevel = suffix
					hasThinkingLevel = true
					globPattern = pattern[:colonIdx]
				}
			}

			if exactMatch := FindExactModelReferenceMatch(globPattern, availableModels); exactMatch != nil {
				if !hasModel(exactMatch) {
					scopedModels = append(scopedModels, ScopedModel{
						Model: exactMatch, ThinkingLevel: thinkingLevel, HasThinkingLevel: hasThinkingLevel,
					})
				}
				continue
			}

			matcher := minimatch.New(globPattern, minimatch.Options{NoCase: true})
			var matchingModels []*ai.Model
			for _, model := range availableModels {
				if matcher.Match(model.Provider+"/"+model.ID) || matcher.Match(model.ID) {
					matchingModels = append(matchingModels, model)
				}
			}

			if len(matchingModels) == 0 {
				diagnostics = append(diagnostics, ModelScopeDiagnostic{
					Type: "warning", Code: "no-match",
					Message: fmt.Sprintf("No models match pattern %q", pattern), Pattern: pattern,
				})
				continue
			}

			for _, model := range matchingModels {
				if !hasModel(model) {
					scopedModels = append(scopedModels, ScopedModel{
						Model: model, ThinkingLevel: thinkingLevel, HasThinkingLevel: hasThinkingLevel,
					})
				}
			}
			continue
		}

		result := ParseModelPattern(pattern, availableModels, nil)

		if result.Warning != "" {
			diagnostics = append(diagnostics, ModelScopeDiagnostic{
				Type: "warning", Code: "invalid-thinking-level", Message: result.Warning, Pattern: pattern,
			})
		}
		if result.Model == nil {
			diagnostics = append(diagnostics, ModelScopeDiagnostic{
				Type: "warning", Code: "no-match",
				Message: fmt.Sprintf("No models match pattern %q", pattern), Pattern: pattern,
			})
			continue
		}

		if !hasModel(result.Model) {
			scopedModels = append(scopedModels, ScopedModel{
				Model: result.Model, ThinkingLevel: result.ThinkingLevel, HasThinkingLevel: result.HasThinkingLevel,
			})
		}
	}

	return ResolveModelScopeResult{ScopedModels: scopedModels, Diagnostics: diagnostics}
}

// ============================================================================
// resolveCliModel
// ============================================================================

// ResolveCliModelOptions carry the CLI model flags.
type ResolveCliModelOptions struct {
	CliProvider  string
	CliModel     string
	CliThinking  string
	ModelRuntime *ModelRuntime
}

// ResolveCliModelResult reports one CLI model resolution; Error set means
// Model is nil and the message suits CLI display.
type ResolveCliModelResult struct {
	Model         *ai.Model
	ThinkingLevel string
	Warning       string
	Error         string
}

// ResolveCliModel resolves --provider/--model to one model. It matches
// exact ids first (provider/slash inference included), prefers an
// authenticated provider on bare-id ambiguity, and falls back to a custom
// model ID when the provider is known but the id is not.
func ResolveCliModel(options ResolveCliModelOptions) ResolveCliModelResult {
	cliProvider, cliModel, cliThinking, modelRuntime := options.CliProvider, options.CliModel, options.CliThinking, options.ModelRuntime
	if cliModel == "" {
		return ResolveCliModelResult{}
	}

	// Use *all* models here, not just models with pre-configured auth
	// (--api-key can be used for first-time setup).
	availableModels := modelRuntime.GetModels()
	if len(availableModels) == 0 {
		return ResolveCliModelResult{Error: "No models available. Check your installation or add models to models.json."}
	}

	// Canonical provider lookup (case-insensitive).
	providerMap := map[string]string{}
	for _, model := range availableModels {
		providerMap[strings.ToLower(model.Provider)] = model.Provider
	}

	provider := ""
	if cliProvider != "" {
		canonical, ok := providerMap[strings.ToLower(cliProvider)]
		if !ok {
			return ResolveCliModelResult{Error: fmt.Sprintf("Unknown provider %q. Use --list-models to see available providers/models.", cliProvider)}
		}
		provider = canonical
	}

	// Without an explicit --provider, try "provider/model" inference first;
	// a prefix matching a known provider wins over literal slash-containing
	// model ids.
	pattern := cliModel
	inferredProvider := false
	if provider == "" {
		if slashIndex := strings.Index(cliModel, "/"); slashIndex != -1 {
			if canonical, ok := providerMap[strings.ToLower(cliModel[:slashIndex])]; ok {
				provider = canonical
				pattern = cliModel[slashIndex+1:]
				inferredProvider = true
			}
		}
	}

	// Without provider inference, try exact matches without provider
	// preference; a sole authenticated match resolves bare-id ambiguity.
	if provider == "" {
		lower := strings.ToLower(cliModel)
		var exactMatches []*ai.Model
		for _, model := range availableModels {
			if strings.ToLower(model.ID) == lower ||
				strings.ToLower(model.Provider+"/"+model.ID) == lower {
				exactMatches = append(exactMatches, model)
			}
		}
		if len(exactMatches) == 1 {
			return ResolveCliModelResult{Model: exactMatches[0]}
		}
		if len(exactMatches) > 1 {
			var authenticated []*ai.Model
			for _, model := range exactMatches {
				if modelRuntime.HasConfiguredAuth(model.Provider) {
					authenticated = append(authenticated, model)
				}
			}
			if len(authenticated) == 1 {
				return ResolveCliModelResult{Model: authenticated[0]}
			}
			matches := make([]string, 0, len(exactMatches))
			for _, model := range exactMatches {
				matches = append(matches, model.Provider+"/"+model.ID)
			}
			sort.Strings(matches)
			authHint := "No matching provider is authenticated."
			if len(authenticated) > 1 {
				authHint = "More than one matching provider is authenticated."
			}
			return ResolveCliModelResult{Error: fmt.Sprintf(
				"Model %q is ambiguous across providers: %s. %s Use --provider or provider/model",
				cliModel, strings.Join(matches, ", "), authHint,
			)}
		}
	}

	// Both flags given: tolerate --model <provider>/<pattern> by stripping
	// the provider prefix.
	if cliProvider != "" && provider != "" {
		prefix := provider + "/"
		if len(cliModel) >= len(prefix) && strings.EqualFold(cliModel[:len(prefix)], prefix) {
			pattern = cliModel[len(prefix):]
		}
	}

	candidates := availableModels
	if provider != "" {
		candidates = nil
		for _, model := range availableModels {
			if model.Provider == provider {
				candidates = append(candidates, model)
			}
		}
	}
	noFallback := false
	parsed := ParseModelPattern(pattern, candidates, &ParseModelOptions{AllowInvalidThinkingLevelFallback: &noFallback})

	if parsed.Model != nil {
		// Provider inference that matched an unauthenticated pair prefers
		// a sole exact raw model-id match that IS authenticated, keeping
		// "provider/model" syntax preferred only when usable.
		if inferredProvider {
			var rawExactMatches []*ai.Model
			for _, model := range availableModels {
				if strings.EqualFold(model.ID, cliModel) && !ai.ModelsAreEqual(model, parsed.Model) {
					rawExactMatches = append(rawExactMatches, model)
				}
			}
			if len(rawExactMatches) > 0 && !modelRuntime.HasConfiguredAuth(parsed.Model.Provider) {
				var authenticated []*ai.Model
				for _, model := range rawExactMatches {
					if modelRuntime.HasConfiguredAuth(model.Provider) {
						authenticated = append(authenticated, model)
					}
				}
				if len(authenticated) == 1 {
					return ResolveCliModelResult{Model: authenticated[0]}
				}
			}
		}
		return ResolveCliModelResult{Model: parsed.Model, ThinkingLevel: parsed.ThinkingLevel, Warning: parsed.Warning}
	}

	// A provider inferred from the slash that matched nothing within that
	// provider falls back to the full input as a raw model id.
	if inferredProvider {
		lower := strings.ToLower(cliModel)
		for _, model := range availableModels {
			if strings.ToLower(model.ID) == lower ||
				strings.ToLower(model.Provider+"/"+model.ID) == lower {
				return ResolveCliModelResult{Model: model}
			}
		}
		fallback := ParseModelPattern(cliModel, availableModels, &ParseModelOptions{AllowInvalidThinkingLevelFallback: &noFallback})
		if fallback.Model != nil {
			return ResolveCliModelResult{Model: fallback.Model, ThinkingLevel: fallback.ThinkingLevel, Warning: fallback.Warning}
		}
	}

	if provider != "" {
		// Parse a thinking suffix off the pattern before building the
		// fallback model, but only without an explicit --thinking.
		fallbackPattern := pattern
		fallbackThinking := ""
		if cliThinking == "" {
			if lastColon := strings.LastIndex(pattern, ":"); lastColon != -1 {
				suffix := pattern[lastColon+1:]
				if isValidThinkingLevel(suffix) {
					fallbackPattern = pattern[:lastColon]
					fallbackThinking = suffix
				}
			}
		}
		if fallbackModel := buildFallbackModel(provider, fallbackPattern, availableModels); fallbackModel != nil {
			requestedThinking := cliThinking
			if requestedThinking == "" {
				requestedThinking = fallbackThinking
			}
			if requestedThinking != "" && requestedThinking != agent.ThinkingOff {
				fallbackModel.Reasoning = true
			}
			fallbackWarning := fmt.Sprintf("Model %q not found for provider %q. Using custom model ID.", fallbackPattern, provider)
			if parsed.Warning != "" {
				fallbackWarning = parsed.Warning + " " + fallbackWarning
			}
			return ResolveCliModelResult{Model: fallbackModel, ThinkingLevel: fallbackThinking, Warning: fallbackWarning}
		}
	}

	display := cliModel
	if provider != "" {
		display = provider + "/" + pattern
	}
	return ResolveCliModelResult{Error: fmt.Sprintf("Model %q not found. Use --list-models to see available models.", display)}
}
