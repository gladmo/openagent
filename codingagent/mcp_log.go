package codingagent

// Port of extensions/mcp/log.ts at reference commit a276dabe: log messages
// MCP servers send with notifications/message, appended to mcp.log in
// the agent directory. Several processes may write to the same file, so
// every message is one synchronous append. The file is rotated to
// mcp.log.1 once it grows past maxMcpLogBytes.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gladmo/openagent/jsonx"
)

const maxMcpLogBytes = 5 * 1024 * 1024

func formatMcpLogData(data any) string {
	if text, isString := data.(string); isString {
		return text
	}
	return jsonx.Stringify(data)
}

// FormatMcpLogMessage formats one notifications/message from server as a
// log line; continuation lines are indented.
func FormatMcpLogMessage(server string, params any, now time.Time) string {
	message := params
	if obj, isRecord := isJSONRecord(params); isRecord {
		message = obj
	} else {
		wrapped := jsonx.ObjFrom("data", params)
		message = wrapped
	}
	obj, _ := message.(*jsonx.Obj)
	level := "info"
	if text, isString := obj.MustGet("level").(string); isString {
		level = text
	}
	logger := ""
	if text, isString := obj.MustGet("logger").(string); isString && text != "" {
		logger = " " + text + ":"
	}
	text := strings.ReplaceAll(strings.ReplaceAll(formatMcpLogData(mustGetJSON(obj, "data")), "\r\n", "\n"), "\n", "\n    ")
	return fmt.Sprintf("%s [%s] %s%s %s\n", now.UTC().Format("2006-01-02T15:04:05.000Z"), server, level, logger, text)
}

// McpServerLog appends server log messages to one file. Write errors are
// ignored: logging must not break tools.
type McpServerLog struct {
	// Path is the log file the messages append to.
	Path string

	mu      sync.Mutex
	size    int64
	hasSize bool
}

// NewMcpServerLog builds the appender for one file.
func NewMcpServerLog(path string) *McpServerLog {
	return &McpServerLog{Path: path}
}

// Write appends one message. Errors are ignored: the log is best effort.
func (l *McpServerLog) Write(server string, params any) {
	line := FormatMcpLogMessage(server, params, time.Now())
	l.mu.Lock()
	defer l.mu.Unlock()
	err := func() error {
		if !l.hasSize {
			if err := os.MkdirAll(filepath.Dir(l.Path), 0o755); err != nil {
				return err
			}
			l.size = l.currentSize()
			l.hasSize = true
		}
		if l.size > maxMcpLogBytes {
			// Another process may have rotated it already; check before
			// renaming.
			if l.currentSize() > maxMcpLogBytes {
				if err := os.Rename(l.Path, l.Path+".1"); err != nil {
					return err
				}
			}
			l.size = l.currentSize()
		}
		file, err := os.OpenFile(l.Path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		if _, err := file.WriteString(line); err != nil {
			file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		l.size += int64(len(line))
		return nil
	}()
	_ = err
}

func (l *McpServerLog) currentSize() int64 {
	if info, err := os.Stat(l.Path); err == nil {
		return info.Size()
	}
	return 0
}
