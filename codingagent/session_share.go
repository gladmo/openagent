package codingagent

// Port of modes/interactive/session-share.ts (trailing-entry + gist
// export surface): the pi.share trailing entry carrying the system prompt
// and tool schemas, JSONL export for sharing, and the GitHub-CLI gist
// upload. Radius upload and HTML export ride the runtime seams.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// shareTrailingEntry is the trailing pi.share entry.
type shareTrailingEntry struct {
	Type       string            `json:"type"`
	CustomType string            `json:"customType"`
	ID         string            `json:"id"`
	ParentID   *string           `json:"parentId"`
	Timestamp  string            `json:"timestamp"`
	Data       shareTrailingData `json:"data"`
}

type shareTrailingData struct {
	SystemPrompt string          `json:"systemPrompt"`
	Tools        []shareToolInfo `json:"tools"`
}

type shareToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// CreateShareTrailingEntries builds the trailing pi.share entry for the
// session viewer.
func CreateShareTrailingEntries(session *AgentSession, parentID *string, timestamp string) []any {
	tools := make([]shareToolInfo, 0)
	for _, name := range session.GetAllTools() {
		if definition := session.GetToolDefinitionRef(name); definition != nil {
			tools = append(tools, shareToolInfo{Name: definition.Name, Description: definition.Description})
		}
	}
	return []any{shareTrailingEntry{
		Type:       EntryTypeCustom,
		CustomType: "pi.share",
		ID:         randomHex(4)[:8],
		ParentID:   parentID,
		Timestamp:  timestamp,
		Data: shareTrailingData{
			SystemPrompt: session.SystemPrompt(),
			Tools:        tools,
		},
	}}
}

// ExportSessionForShare writes the current branch with the trailing
// pi.share entry.
func ExportSessionForShare(filePath string, session *AgentSession) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return err
	}
	entries := session.SessionManager.GetEntries()
	branch := session.SessionManager.GetBranch("")

	var buffer bytes.Buffer
	for _, entry := range entries {
		encoded, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		buffer.Write(encoded)
		buffer.WriteByte('\n')
	}

	// Trailing entry parented at the branch leaf.
	parentID := (*string)(nil)
	if len(branch) > 0 {
		id := branch[len(branch)-1].base().ID
		parentID = &id
	}
	trailing := CreateShareTrailingEntries(session, parentID, nowISOForShare())
	for _, entry := range trailing {
		encoded, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		buffer.Write(encoded)
		buffer.WriteByte('\n')
	}

	return os.WriteFile(filePath, buffer.Bytes(), 0o644)
}

func nowISOForShare() string { return nowISOUtility() }

// GhCLIStatus checks the GitHub CLI auth state.
func GhCLIStatus() error {
	cmd := exec.Command("gh", "auth", "status")
	cmd.Stdin = nil
	var stderr strings.Builder
	cmd.Stdout = nil
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("GitHub CLI is not logged in. Run 'gh auth login' first.")
	}
	return nil
}

// GhCLIAvailable reports whether gh is installed.
func GhCLIAvailable() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// ShareViaGist uploads a file as a secret gist, returning its URL.
func ShareViaGist(filePath string) (string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	cmd := exec.Command("gh", "gist", "create", "--filename", filepath.Base(filePath))
	cmd.Stdin = bytes.NewReader(content)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("Failed to create gist: %s", err.Error())
	}
	return strings.TrimSpace(stdout.String()), nil
}
