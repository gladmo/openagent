package jsonl

import (
	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/ai"
)

type aiUsageAlias = ai.Usage
type textLineAlias = harness.TextLine

// FileValueResult unwraps a Result with the TS action-prefixed error.
func FileValueString(result harness.Result[string, *harness.FileError], action string) (string, error) {
	if !result.Ok {
		return "", wrapAction(action, result.Error)
	}
	return result.Value, nil
}

func wrapAction(action string, err *harness.FileError) error {
	return &actionError{action: action, cause: err}
}

type actionError struct {
	action string
	cause  *harness.FileError
}

func (e *actionError) Error() string { return e.action + ": " + e.cause.Message }

// PublishFileAtomically mirrors publishFileAtomically.
func PublishFileAtomically(fs harness.FileSystem, destinationPath string, ctx harness.Context, writeContent func(appendFn func(content string) error) error) error {
	tempPath := destinationPath + ".tmp"
	if result := fs.WriteFile(tempPath, nil, ctx); !result.Ok {
		return wrapAction("Failed to stage JSONL storage "+destinationPath, result.Error)
	}
	err := writeContent(func(content string) error {
		if result := fs.AppendFile(tempPath, []byte(content), ctx); !result.Ok {
			return wrapAction("Failed to append JSONL storage "+destinationPath, result.Error)
		}
		return nil
	})
	if err == nil {
		if result := fs.RenameFile(tempPath, destinationPath, ctx); !result.Ok {
			err = wrapAction("Failed to publish JSONL storage "+destinationPath, result.Error)
		}
	}
	if err != nil {
		force := true
		_ = fs.Remove(tempPath, &harness.RemoveOptions{Force: &force}, ctx)
		return err
	}
	return nil
}

// ReadHeaderFromReader mirrors readJsonlHeader over a TextLineReader.
func ReadHeaderFromReader(reader harness.TextLineReader, path string, ctx harness.Context) (ParsedSessionHeader, error) {
	result, err := reader.ReadLine(ctx)
	if err != nil {
		return ParsedSessionHeader{}, &readError{path: path, cause: err}
	}
	if !result.Ok {
		return ParsedSessionHeader{}, wrapAction("Failed to read JSONL storage "+path, result.Error)
	}
	line := result.Value
	if line == nil || !line.Terminated || line.Text == "" {
		return ParsedSessionHeader{}, &headerError{path: path}
	}
	parsed, parseErr := ParseSessionHeader(line.Text)
	if parseErr != nil {
		return ParsedSessionHeader{}, &headerError{path: path}
	}
	return parsed, nil
}

type readError struct {
	path  string
	cause error
}

func (e *readError) Error() string {
	return "Failed to read JSONL storage " + e.path + ": " + e.cause.Error()
}

type headerError struct{ path string }

func (e *headerError) Error() string { return "Invalid JSONL storage " + e.path + ": missing header" }

// SerializeHeader renders the v4 header with the TS key order
// (v,kind,id,storageVersion,createdAt,cwd,parentSessionId?,legacyParentSessionPath?,nextSeq?).
func SerializeHeader(header StorageHeader) string {
	obj := jsonxObj()
	obj.Set("v", float64(header.V))
	obj.Set("kind", header.Kind)
	obj.Set("id", header.ID)
	obj.Set("storageVersion", float64(header.StorageVersion))
	obj.Set("createdAt", header.CreatedAt)
	obj.Set("cwd", header.Cwd)
	if header.ParentSessionID != nil {
		obj.Set("parentSessionId", *header.ParentSessionID)
	}
	if header.LegacyParentSessionPath != nil {
		obj.Set("legacyParentSessionPath", *header.LegacyParentSessionPath)
	}
	if header.NextSeq != nil {
		obj.Set("nextSeq", float64(*header.NextSeq))
	}
	return jsonxStringify(obj)
}

// CommittedWriteFromEntry builds a committed entry write (test helper).
func CommittedWriteFromEntry(entry *session.Entry) session.CommittedWrite {
	return session.CommittedWrite{Kind: "entry", Seq: entry.Seq, Timestamp: entry.Timestamp, Entry: entry}
}
