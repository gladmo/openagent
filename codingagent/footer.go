package codingagent

// Port of modes/interactive/components/footer.ts (data surface): status
// text sanitization, token/cost formatting, home-relative cwd display,
// usage aggregation across session entries, and the footer render as a
// line builder the TUI stacks.

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strings"
)

var statusControlPattern = regexp.MustCompile(`[\r\n\t]`)
var multiSpacePattern = regexp.MustCompile(` +`)

// SanitizeStatusText strips control characters and collapses spaces.
func SanitizeStatusText(text string) string {
	replaced := statusControlPattern.ReplaceAllString(text, " ")
	replaced = multiSpacePattern.ReplaceAllString(replaced, " ")
	return strings.TrimSpace(replaced)
}

// FormatTokens renders compact token counts (<10k at one decimal).
func FormatTokens(count float64) string {
	if count < 1000 {
		return fmt.Sprintf("%d", int(count))
	}
	if count < 10000 {
		return fmt.Sprintf("%.1fk", count/1000)
	}
	if count < 1000000 {
		return fmt.Sprintf("%dk", int(math.Round(count/1000)))
	}
	if count < 10000000 {
		return fmt.Sprintf("%.1fM", count/1000000)
	}
	return fmt.Sprintf("%dM", int(math.Round(count/1000000)))
}

// FormatCwdForFooter renders home-relative paths with ~.
func FormatCwdForFooter(cwd, home string) string {
	if home == "" {
		return cwd
	}
	resolvedCwd := filepath.Clean(cwd)
	resolvedHome := filepath.Clean(home)
	relative, err := filepath.Rel(resolvedHome, resolvedCwd)
	if err != nil {
		return cwd
	}
	// Outside home: keep the absolute form.
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return cwd
	}
	if relative == "." {
		return "~"
	}
	return "~" + string(filepath.Separator) + relative
}

// UsageTotals aggregates session usage.
type UsageTotals struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	Cost       float64
}

// usageEntryFields extracts the usage record's totals.
func usageEntryFields(raw []byte) (input, output, cacheRead, cacheWrite, cost float64, ok bool) {
	var record struct {
		Input      float64 `json:"Input"`
		Output     float64 `json:"Output"`
		CacheRead  float64 `json:"CacheRead"`
		CacheWrite float64 `json:"CacheWrite"`
		Cost       struct {
			Input      float64 `json:"Input"`
			Output     float64 `json:"Output"`
			CacheRead  float64 `json:"CacheRead"`
			CacheWrite float64 `json:"CacheWrite"`
		} `json:"Cost"`
	}
	if err := jsonUnmarshal(raw, &record); err != nil {
		return 0, 0, 0, 0, 0, false
	}
	cost = record.Cost.Input + record.Cost.Output + record.Cost.CacheRead + record.Cost.CacheWrite
	return record.Input, record.Output, record.CacheRead, record.CacheWrite, cost, true
}

func addUsageToTotals(totals *UsageTotals, raw []byte) {
	input, output, cacheRead, cacheWrite, cost, ok := usageEntryFields(raw)
	if !ok {
		return
	}
	totals.Input += input
	totals.Output += output
	totals.CacheRead += cacheRead
	totals.CacheWrite += cacheWrite
	totals.Cost += cost
}

// FooterData feeds the footer (git branch, subscription state).
type FooterData interface {
	GetGitBranch() string
	IsUsingSubscription(providerID string) bool
}

// Footer renders pwd + usage + context stats.
type Footer struct {
	session         *AgentSession
	footerData      FooterData
	autoCompact     bool
	latestCacheHit  float64
	hasCacheHitRate bool
}

// NewFooter builds the component.
func NewFooter(session *AgentSession, data FooterData) *Footer {
	return &Footer{session: session, footerData: data, autoCompact: true}
}

// SetSession swaps the driven session.
func (f *Footer) SetSession(session *AgentSession) { f.session = session }

// SetAutoCompactEnabled toggles the auto-compact marker.
func (f *Footer) SetAutoCompactEnabled(enabled bool) { f.autoCompact = enabled }

// ComputeUsageTotals aggregates every entry's usage.
func (f *Footer) ComputeUsageTotals() UsageTotals {
	totals := UsageTotals{}
	if f.session == nil || f.session.SessionManager == nil {
		return totals
	}
	for _, entry := range f.session.SessionManager.GetEntries() {
		switch typed := entry.(type) {
		case *UsageEntry:
			addUsageToTotals(&totals, typed.Usage)
		case *SessionMessageEntry:
			var probe struct {
				Role  string `json:"role"`
				Usage struct {
					Input      float64 `json:"Input"`
					CacheRead  float64 `json:"CacheRead"`
					CacheWrite float64 `json:"CacheWrite"`
				} `json:"Usage"`
			}
			if err := jsonUnmarshal(typed.Message, &probe); err != nil {
				continue
			}
			switch probe.Role {
			case "assistant", "toolResult":
				// The usage record nests under the message's Usage key;
				// extract it before aggregating.
				var nested struct {
					Usage json.RawMessage `json:"Usage"`
				}
				if err := jsonUnmarshal(typed.Message, &nested); err == nil && len(nested.Usage) > 0 {
					addUsageToTotals(&totals, nested.Usage)
				}
				if probe.Role == "assistant" {
					prompt := probe.Usage.Input + probe.Usage.CacheRead + probe.Usage.CacheWrite
					if prompt > 0 {
						f.latestCacheHit = probe.Usage.CacheRead / prompt * 100
						f.hasCacheHitRate = true
					}
				}
			}
		case *BranchSummaryEntry:
			addUsageToTotals(&totals, typed.Usage)
		case *CompactionEntry:
			addUsageToTotals(&totals, typed.Usage)
		}
	}
	return totals
}

// RenderLines produces the footer's two lines (pwd line + stats line).
func (f *Footer) RenderLines(width int) []string {
	totals := f.ComputeUsageTotals()

	pwd := FormatCwdForFooter(f.session.SessionManager.GetCwd(), envHome())
	if f.footerData != nil {
		if branch := f.footerData.GetGitBranch(); branch != "" {
			pwd = pwd + " (" + branch + ")"
		}
	}
	if name := f.session.SessionManager.GetSessionName(); name != "" {
		pwd = pwd + " • " + name
	}

	var stats []string
	if totals.Input > 0 {
		stats = append(stats, "↑"+FormatTokens(totals.Input))
	}
	if totals.Output > 0 {
		stats = append(stats, "↓"+FormatTokens(totals.Output))
	}
	if totals.CacheRead > 0 {
		stats = append(stats, "R"+FormatTokens(totals.CacheRead))
	}
	if totals.CacheWrite > 0 {
		stats = append(stats, "W"+FormatTokens(totals.CacheWrite))
	}
	if (totals.CacheRead > 0 || totals.CacheWrite > 0) && f.hasCacheHitRate {
		stats = append(stats, fmt.Sprintf("CH%.1f%%", f.latestCacheHit))
	}
	usingSubscription := false
	if model := f.session.Model(); model != nil {
		if model.Provider == "kimi-coding" {
			usingSubscription = true
		} else if f.footerData != nil && f.footerData.IsUsingSubscription(model.Provider) {
			usingSubscription = true
		}
	}
	if totals.Cost > 0 || usingSubscription {
		suffix := ""
		if usingSubscription {
			suffix = " (sub)"
		}
		stats = append(stats, fmt.Sprintf("$%.3f%s", totals.Cost, suffix))
	}

	// Context usage always renders (percent of the model's window, or ?
	// when unknown post-compaction); the auto-compact marker trails.
	contextWindow := float64(0)
	if model := f.session.Model(); model != nil {
		contextWindow = model.ContextWindow
	}
	autoIndicator := ""
	if f.autoCompact {
		autoIndicator = " (auto)"
	}
	contextDisplay := fmt.Sprintf("?/%s%s", FormatTokens(contextWindow), autoIndicator)
	stats = append(stats, contextDisplay)

	pwdLine := SanitizeStatusText(pwd)
	statsLine := SanitizeStatusText(strings.Join(stats, " "))
	if width > 0 {
		pwdLine = truncateANSIAware(pwdLine, width)
		statsLine = truncateANSIAware(statsLine, width)
	}
	return []string{pwdLine, statsLine}
}

func envHome() string {
	if home := osGetenv("HOME"); home != "" {
		return home
	}
	return osGetenv("USERPROFILE")
}

func truncateANSIAware(text string, width int) string {
	if len(text) <= width {
		return text
	}
	return text[:width]
}

// sessionFooterData adapts an AgentSession to the FooterData seam.
type sessionFooterData struct{ session *AgentSession }

func (s *sessionFooterData) GetGitBranch() string {
	if s.session == nil || s.session.SessionManager == nil {
		return ""
	}
	return ResolveBranchWithGit(s.session.SessionManager.GetCwd())
}

func (s *sessionFooterData) IsUsingSubscription(providerID string) bool {
	return providerID == "kimi-coding"
}

// Invalidate implements tui.Component.
func (f *Footer) Invalidate() {}

// Render implements tui.Component (two lines: pwd + stats).
func (f *Footer) Render(width int) []string { return f.RenderLines(width) }
