package codingagent

// Port of core/pi-manifest.ts, core/session-cwd.ts, core/defaults.ts, and
// core/project-trust.ts: package.json pi-manifest reading, missing
// session-cwd detection, default thinking level, and the project-trust
// resolution order (override -> no trust-requiring resources -> extension
// handler -> stored decision -> default policy -> prompt).

import (
	"fmt"
	"os"
)

// ============================================================================
// session-cwd.ts
// ============================================================================

// SessionCwdIssue reports a stored session directory that vanished.
type SessionCwdIssue struct {
	SessionFile string
	SessionCwd  string
	FallbackCwd string
}

// SessionCwdSource is the minimal session-manager surface.
type SessionCwdSource interface {
	GetCwd() string
	GetSessionFile() string
}

// GetMissingSessionCwdIssue detects a missing stored cwd (nil when fine).
func GetMissingSessionCwdIssue(source SessionCwdSource, fallbackCwd string) *SessionCwdIssue {
	sessionFile := source.GetSessionFile()
	if sessionFile == "" {
		return nil
	}
	sessionCwd := source.GetCwd()
	if sessionCwd == "" {
		return nil
	}
	if _, err := os.Stat(sessionCwd); err == nil {
		return nil
	}
	return &SessionCwdIssue{
		SessionFile: sessionFile,
		SessionCwd:  sessionCwd,
		FallbackCwd: fallbackCwd,
	}
}

// FormatMissingSessionCwdError renders the fatal message.
func FormatMissingSessionCwdError(issue *SessionCwdIssue) string {
	sessionFile := ""
	if issue.SessionFile != "" {
		sessionFile = "\nSession file: " + issue.SessionFile
	}
	return fmt.Sprintf("Stored session working directory does not exist: %s%s\nCurrent working directory: %s", issue.SessionCwd, sessionFile, issue.FallbackCwd)
}

// FormatMissingSessionCwdPrompt renders the interactive prompt text.
func FormatMissingSessionCwdPrompt(issue *SessionCwdIssue) string {
	return fmt.Sprintf("cwd from session file does not exist\n%s\n\ncontinue in current cwd\n%s", issue.SessionCwd, issue.FallbackCwd)
}

// MissingSessionCwdError is the fatal error form.
type MissingSessionCwdError struct {
	Issue *SessionCwdIssue
}

func (e *MissingSessionCwdError) Error() string {
	return FormatMissingSessionCwdError(e.Issue)
}

// AssertSessionCwdExists panics with the error when the stored cwd is
// missing (the Go fatal path).
func AssertSessionCwdExists(source SessionCwdSource, fallbackCwd string) error {
	if issue := GetMissingSessionCwdIssue(source, fallbackCwd); issue != nil {
		return &MissingSessionCwdError{Issue: issue}
	}
	return nil
}

// ============================================================================
// project-trust.ts
// ============================================================================

// FormatProjectTrustPrompt renders the trust dialog text.
func FormatProjectTrustPrompt(cwd string) string {
	return fmt.Sprintf("Trust project folder?\n%s\n\nThis allows %s to load %s settings and resources, install missing project packages, and execute project extensions.", cwd, AppName(), ConfigDirName())
}

// ResolveProjectTrustedOptions configure the trust resolution.
type ResolveProjectTrustedOptions struct {
	Cwd                        string
	TrustStore                 *ProjectTrustStore
	TrustOverride              *bool
	DefaultProjectTrust        string
	HasTrustRequiringResources bool
	// ExtensionDecision: when the extension handler answered, its
	// remembered decision lands here.
	ExtensionDecision *struct {
		Trusted  bool
		Remember bool
	}
	HasUI bool
	// SelectPrompt renders the interactive choice (label -> trusted);
	// nil when no UI.
	SelectPrompt func(cwd string) (bool, bool, bool)
}

// ResolveProjectTrusted computes the trust decision.
func ResolveProjectTrusted(options ResolveProjectTrustedOptions) bool {
	if options.TrustOverride != nil {
		return *options.TrustOverride
	}
	if !options.HasTrustRequiringResources {
		return true
	}

	if options.ExtensionDecision != nil {
		decision := options.ExtensionDecision
		if decision.Remember && options.TrustStore != nil {
			trusted := decision.Trusted
			options.TrustStore.Set(options.Cwd, &trusted)
		}
		return decision.Trusted
	}

	if options.TrustStore != nil {
		if decision, exists := options.TrustStore.Get(options.Cwd); exists {
			return decision
		}
	}

	switch options.DefaultProjectTrust {
	case "always":
		return true
	case "never":
		return false
	}

	if !options.HasUI || options.SelectPrompt == nil {
		return false
	}

	trusted, remember, chose := options.SelectPrompt(options.Cwd)
	if chose && remember && options.TrustStore != nil {
		options.TrustStore.Set(options.Cwd, &trusted)
	}
	return chose && trusted
}
