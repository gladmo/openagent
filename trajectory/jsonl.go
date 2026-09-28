// trajectory/jsonl.go: the file store. One trajectory per file: a header
// line first, then one record per line (jsonx formatting). Appends are
// buffered; Flush/Close sync. The reader tolerates a torn final line (crash
// tail) and refuses unknown record types without the ignorable marker.
package trajectory

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/gladmo/openagent/jsonx"
)

// TrajectoryFormatVersion is the JSONL trajectory format generation.
const TrajectoryFormatVersion = int64(1)

// FileHeader is the first line of a stored trajectory.
type FileHeader struct {
	FormatVersion    int64   `json:"v"`
	Kind             string  `json:"kind"`
	ID               string  `json:"id"`
	CreatedAt        float64 `json:"createdAt"`
	Model            string  `json:"model,omitempty"`
	Provider         string  `json:"provider,omitempty"`
	ParentTrajectory string  `json:"parentTrajectoryId,omitempty"`
}

// FileStore appends records to one trajectory file.
type FileStore struct {
	file   *os.File
	buf    *bufio.Writer
	closed bool
}

// NewFileStore creates (or truncates) the trajectory file and writes the
// header line. The caller owns flushing and closing.
func NewFileStore(path string, header FileHeader) (*FileStore, error) {
	if header.FormatVersion == 0 {
		header.FormatVersion = TrajectoryFormatVersion
	}
	if header.Kind == "" {
		header.Kind = "trajectory"
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("trajectory: create %s: %w", path, err)
	}
	store := &FileStore{file: file, buf: bufio.NewWriter(file)}
	if err := store.writeHeader(header); err != nil {
		_ = file.Close()
		return nil, err
	}
	return store, nil
}

func (s *FileStore) writeHeader(header FileHeader) error {
	value := jsonx.NewObj()
	value.Set("v", float64(header.FormatVersion))
	value.Set("kind", header.Kind)
	value.Set("id", header.ID)
	value.Set("createdAt", header.CreatedAt)
	if header.Model != "" {
		value.Set("model", header.Model)
	}
	if header.Provider != "" {
		value.Set("provider", header.Provider)
	}
	if header.ParentTrajectory != "" {
		value.Set("parentTrajectoryId", header.ParentTrajectory)
	}
	if _, err := s.buf.WriteString(jsonx.Stringify(value) + "\n"); err != nil {
		return fmt.Errorf("trajectory: write header: %w", err)
	}
	return s.Flush()
}

// Write appends one record line.
func (s *FileStore) Write(rec *Record) error {
	if s.closed {
		return fmt.Errorf("trajectory: file store is closed")
	}
	if _, err := s.buf.WriteString(rec.String() + "\n"); err != nil {
		return fmt.Errorf("trajectory: write record: %w", err)
	}
	return nil
}

// Flush flushes the buffer and syncs the file.
func (s *FileStore) Flush() error {
	if err := s.buf.Flush(); err != nil {
		return fmt.Errorf("trajectory: flush: %w", err)
	}
	return s.file.Sync()
}

// Close flushes and closes the file; idempotent.
func (s *FileStore) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	err := s.buf.Flush()
	if syncErr := s.file.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := s.file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("trajectory: close: %w", err)
	}
	return nil
}

// File is a read trajectory: its header and records.
type File struct {
	Path    string
	Header  FileHeader
	Records []Record
	// TornTail is true when the final line was incomplete and dropped
	// (a crashed writer's tail — the committed prefix is intact).
	TornTail bool
}

// ReadFile reads and validates a stored trajectory. Unknown record types
// without ignorable=true are refused; a torn final line is dropped and
// flagged.
func ReadFile(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("trajectory: read %s: %w", path, err)
	}
	lines := strings.Split(string(raw), "\n")
	// A trailing newline yields one empty final element; without one, the
	// final line may be a crashed writer's torn tail.
	tornPossible := !strings.HasSuffix(string(raw), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("trajectory: %s is empty", path)
	}
	header, err := parseHeaderLine(lines[0])
	if err != nil {
		return nil, fmt.Errorf("trajectory: %s: %w", path, err)
	}
	out := &File{Path: path, Header: header}
	lastIndex := len(lines) - 1
	for i, line := range lines[1:] {
		if line == "" {
			return nil, fmt.Errorf("trajectory: %s: blank record at line %d", path, i+2)
		}
		value, err := jsonx.Parse(line)
		if err != nil {
			if i+1 == lastIndex && tornPossible {
				out.TornTail = true
				break
			}
			return nil, fmt.Errorf("trajectory: %s: line %d: %w", path, i+2, err)
		}
		rec, err := RecordFromJSON(value)
		if err != nil {
			return nil, fmt.Errorf("trajectory: %s: line %d: %w", path, i+2, err)
		}
		if rec.Seq != int64(i)+1 {
			return nil, fmt.Errorf("trajectory: %s: line %d: seq %d breaks the dense sequence", path, i+2, rec.Seq)
		}
		if !knownToAnyReader(rec) {
			return nil, fmt.Errorf("trajectory: %s: line %d: record type %q is unknown to this reader and not ignorable", path, i+2, rec.Type)
		}
		out.Records = append(out.Records, *rec)
	}
	return out, nil
}

// knownToAnyReader mirrors the read-side admission rule: a record kind this
// package does not know must carry ignorable=true, otherwise the log may
// not be silently reconstructed without it. Extension kinds written through
// RegisterKind(kind, false) carry the marker; required extension kinds
// refuse here for foreign readers.
func knownToAnyReader(rec *Record) bool {
	if _, ok := coreKinds[rec.Type]; ok {
		return true
	}
	return rec.Ignorable
}

func parseHeaderLine(line string) (FileHeader, error) {
	value, err := jsonx.Parse(line)
	if err != nil {
		return FileHeader{}, fmt.Errorf("invalid header line: %w", err)
	}
	obj, ok := value.(*jsonx.Obj)
	if !ok {
		return FileHeader{}, fmt.Errorf("header is not a JSON object")
	}
	kindValue, _ := obj.Get("kind")
	if kindValue != "trajectory" {
		return FileHeader{}, fmt.Errorf("unsupported header kind %q", kindValue)
	}
	header := FileHeader{Kind: "trajectory"}
	if v, ok := obj.Get("v"); ok {
		if f, ok := jsonx.ToFloat(v); ok {
			header.FormatVersion = int64(f)
		}
	}
	if header.FormatVersion != TrajectoryFormatVersion {
		return FileHeader{}, fmt.Errorf("unsupported trajectory format v%d (reader supports v%d)", header.FormatVersion, TrajectoryFormatVersion)
	}
	if v, ok := obj.Get("id"); ok {
		if s, ok := v.(string); ok {
			header.ID = s
		}
	}
	if v, ok := obj.Get("createdAt"); ok {
		if f, ok := jsonx.ToFloat(v); ok {
			header.CreatedAt = f
		}
	}
	if v, ok := obj.Get("model"); ok {
		if s, ok := v.(string); ok {
			header.Model = s
		}
	}
	if v, ok := obj.Get("provider"); ok {
		if s, ok := v.(string); ok {
			header.Provider = s
		}
	}
	if v, ok := obj.Get("parentTrajectoryId"); ok {
		if s, ok := v.(string); ok {
			header.ParentTrajectory = s
		}
	}
	return header, nil
}
