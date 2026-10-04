package codingagent

// Port of core/bug-report.ts (redaction + bundle surface): URL/JSON
// credential stripping, environment metadata collection, and the report
// bundle files. Archive writing and the LLM summary land with the
// runtime phase.

import (
	"encoding/json"
	"net/url"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// BugReportCustomEntryType marks bug-report entries in sessions.
const BugReportCustomEntryType = "pi.bug-report"

// BugReportSchemaVersion is the report document version.
const BugReportSchemaVersion = 1

const redacted = "<redacted>"

var sensitiveKeyPattern = regexp.MustCompile(`(?i)(?:^|[-_])(api[-_]?key|secret|token|password|passwd|credential|authorization|cookie)(?:$|[-_])`)
var camelCaseSplit = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func isSensitiveKey(key string) bool {
	normalized := camelCaseSplit.ReplaceAllString(key, "${1}_${2}")
	return sensitiveKeyPattern.MatchString(normalized)
}

// RedactUrl strips credentials and secret-looking query parameters.
func RedactUrl(value string) string {
	if nested := regexp.MustCompile(`(?i)^([a-z][a-z0-9+.-]*:)([a-z][a-z0-9+.-]*://.*)$`).FindStringSubmatch(value); nested != nil {
		return nested[1] + RedactUrl(nested[2])
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	changed := false
	if parsed.User != nil {
		parsed.User = nil
		changed = true
	}
	query := parsed.Query()
	for key := range query {
		if isSensitiveKey(key) {
			query.Set(key, redacted)
			changed = true
		}
	}
	if changed {
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return value
}

// RedactJsonValue deep-copies a JSON value removing credential-bearing
// values and stripping URLs inside strings.
func RedactJsonValue(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil
	}
	return redactValue("", decoded)
}

func redactValue(key string, value any) any {
	if isSensitiveKey(key) {
		return redacted
	}
	switch typed := value.(type) {
	case string:
		return RedactUrl(typed)
	case map[string]any:
		out := map[string]any{}
		for childKey, child := range typed {
			out[childKey] = redactValue(childKey, child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = redactValue("", child)
		}
		return out
	}
	return value
}

// RedactSettings removes the tracking id and redacts the rest.
func RedactSettings(settings Settings) Settings {
	clone := Settings{}
	for key, value := range settings {
		if key == "trackingId" {
			continue
		}
		clone[key] = value
	}
	redacted := RedactJsonValue(clone)
	if asMap, ok := redacted.(map[string]any); ok {
		out := Settings{}
		for key, value := range asMap {
			out[key] = value
		}
		return out
	}
	return Settings{}
}

// BugReportEnvironment captures the machine context.
type BugReportEnvironment struct {
	Version     string `json:"version"`
	UserAgent   string `json:"userAgent"`
	Runtime     string `json:"runtime"`
	Platform    string `json:"platform"`
	Arch        string `json:"arch"`
	OsRelease   string `json:"osRelease"`
	Shell       string `json:"shell"`
	Term        string `json:"term"`
	TermProgram string `json:"termProgram"`
	Colorterm   string `json:"colorterm"`
	Tmux        bool   `json:"tmux"`
	Ssh         bool   `json:"ssh"`
	Ci          bool   `json:"ci"`
	// PiEnvironmentVariables lists PI_* names (values never leave).
	PiEnvironmentVariables []string `json:"piEnvironmentVariables"`
}

// CollectBugReportEnvironment gathers the machine context.
func CollectBugReportEnvironment() *BugReportEnvironment {
	shell := ""
	if parts := strings.FieldsFunc(osGetenv("SHELL"), func(r rune) bool { return r == '/' || r == '\\' }); len(parts) > 0 {
		shell = parts[len(parts)-1]
	}
	var piVars []string
	for _, entry := range osEnviron() {
		if strings.HasPrefix(entry, "PI_") {
			key, _, _ := strings.Cut(entry, "=")
			piVars = append(piVars, key)
		}
	}
	sort.Strings(piVars)

	return &BugReportEnvironment{
		Version:                Version(),
		UserAgent:              GetPiUserAgent(Version()),
		Runtime:                "go/" + runtime.Version(),
		Platform:               runtime.GOOS,
		Arch:                   runtime.GOARCH,
		Shell:                  shell,
		Term:                   osGetenv("TERM"),
		TermProgram:            osGetenv("TERM_PROGRAM"),
		Colorterm:              osGetenv("COLORTERM"),
		Tmux:                   osGetenv("TMUX") != "",
		Ssh:                    osGetenv("SSH_CONNECTION") != "" || osGetenv("SSH_CLIENT") != "" || osGetenv("SSH_TTY") != "",
		Ci:                     osGetenv("CI") != "",
		PiEnvironmentVariables: piVars,
	}
}

// BugReportFile is one bundled file.
type BugReportFile struct {
	Name        string
	ContentType string
	Data        string
}

// BugReportBundle carries the collected report.
type BugReportBundle struct {
	Metadata     map[string]any
	Diagnostics  map[string]any
	SessionJsonl string
	Summary      string
}

// BugReportFiles lists the bundle's files (report + diagnostics + session
// + summary when present).
func BugReportFiles(bundle *BugReportBundle) []BugReportFile {
	files := []BugReportFile{
		mustJsonFile("report.json", "application/json", bundle.Metadata),
		mustJsonFile("diagnostics.json", "application/json", bundle.Diagnostics),
	}
	if bundle.SessionJsonl != "" {
		files = append(files, BugReportFile{Name: "session.jsonl", ContentType: "application/x-ndjson", Data: bundle.SessionJsonl})
	}
	if bundle.Summary != "" {
		summary := bundle.Summary
		if !strings.HasSuffix(summary, "\n") {
			summary += "\n"
		}
		files = append(files, BugReportFile{Name: "summary.md", ContentType: "text/markdown", Data: summary})
	}
	return files
}

func mustJsonFile(name, contentType string, value any) BugReportFile {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		encoded = []byte("{}")
	}
	return BugReportFile{Name: name, ContentType: contentType, Data: string(encoded) + "\n"}
}

// BugReportArchiveFileName renders the zip name.
func BugReportArchiveFileName(id string) string {
	return "pi-bug-report-" + id + ".zip"
}
