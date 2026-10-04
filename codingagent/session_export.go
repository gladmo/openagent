package codingagent

// Port of core/session-export.ts and utils/zip.ts: session-branch JSONL
// serialization with export-only trailing entries, and the minimal
// classic ZIP archives used by bug reports.

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TrailingEntries builds export-only entries parented at the branch leaf.
type TrailingEntries func(parentID *string, timestamp string) []any

// SerializeSessionBranch renders the current branch plus optional
// export-only entries as JSONL (leading reconstructed session header).
func SerializeSessionBranch(manager *SessionManager, createTrailingEntries TrailingEntries) (string, error) {
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	header := map[string]any{
		"type":      "session",
		"version":   CurrentSessionVersion,
		"id":        manager.GetSessionID(),
		"timestamp": timestamp,
		"cwd":       manager.GetCwd(),
	}

	var lines []string
	encoded, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	lines = append(lines, string(encoded))

	parentID := (*string)(nil)
	for _, entry := range manager.GetBranch("") {
		base := entry.base()
		record := entryAsExportMap(entry)
		if parentID == nil {
			record["parentId"] = nil
		} else {
			record["parentId"] = *parentID
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return "", err
		}
		lines = append(lines, string(encoded))
		id := base.ID
		parentID = &id
	}

	if createTrailingEntries != nil {
		for _, trailing := range createTrailingEntries(parentID, timestamp) {
			encoded, err := json.Marshal(trailing)
			if err != nil {
				return "", err
			}
			lines = append(lines, string(encoded))
		}
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// ExportSessionToJsonl writes the branch JSONL, returning the path (the
// default name carries a filesystem-safe timestamp).
func ExportSessionToJsonl(manager *SessionManager, outputPath string, createTrailingEntries TrailingEntries) (string, error) {
	filePath := outputPath
	if filePath == "" {
		filePath = "session-" + time.Now().UTC().Format("2006-01-02T15-04-05-000Z") + ".jsonl"
	}
	filePath = ResolvePath(filePath, cwdOfProcess(), PathInputOptions{})

	serialized, err := SerializeSessionBranch(manager, createTrailingEntries)
	if err != nil {
		return "", err
	}
	if dir := filepath.Dir(filePath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filePath, []byte(serialized), 0o644); err != nil {
		return "", err
	}
	return filePath, nil
}

func cwdOfProcess() string {
	cwd, _ := os.Getwd()
	return cwd
}

// entryAsExportMap converts one entry to a marshaling map.
func entryAsExportMap(entry SessionEntry) map[string]any {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return map[string]any{}
	}
	var record map[string]any
	if err := json.Unmarshal(encoded, &record); err != nil {
		return map[string]any{}
	}
	return record
}

// ============================================================================
// zip.ts
// ============================================================================

// ZipEntry is one archive member.
type ZipEntry struct {
	Name string
	Data []byte
}

func dosDateTime(date time.Time) (uint16, uint16) {
	hour, minute, second := date.Clock()
	year, month, day := date.Date()
	if year < 1980 {
		year = 1980
	}
	dosTime := uint16(hour<<11 | minute<<5 | second>>1)
	dosDay := uint16((year-1980)<<9 | int(month)<<5 | day)
	return dosTime, dosDay
}

// CreateZipArchive builds the small classic ZIP (deflate, no extras).
func CreateZipArchive(entries []ZipEntry) []byte {
	var files bytes.Buffer
	var directory bytes.Buffer
	dosTime, dosDay := dosDateTime(time.Now())
	offset := uint32(0)

	for _, entry := range entries {
		name := []byte(entry.Name)
		data := entry.Data
		compressed := deflateRaw(data)
		checksum := crc32.ChecksumIEEE(data)

		local := make([]byte, 30)
		binary.LittleEndian.PutUint32(local[0:], 0x04034b50)
		binary.LittleEndian.PutUint16(local[4:], 20)
		binary.LittleEndian.PutUint16(local[6:], 0x0800)
		binary.LittleEndian.PutUint16(local[8:], 8)
		binary.LittleEndian.PutUint16(local[10:], dosTime)
		binary.LittleEndian.PutUint16(local[12:], dosDay)
		binary.LittleEndian.PutUint32(local[14:], checksum)
		binary.LittleEndian.PutUint32(local[18:], uint32(len(compressed)))
		binary.LittleEndian.PutUint32(local[22:], uint32(len(data)))
		binary.LittleEndian.PutUint16(local[26:], uint16(len(name)))

		central := make([]byte, 46)
		binary.LittleEndian.PutUint32(central[0:], 0x02014b50)
		binary.LittleEndian.PutUint16(central[4:], 20)
		binary.LittleEndian.PutUint16(central[6:], 20)
		binary.LittleEndian.PutUint16(central[8:], 0x0800)
		binary.LittleEndian.PutUint16(central[10:], 8)
		binary.LittleEndian.PutUint16(central[12:], dosTime)
		binary.LittleEndian.PutUint16(central[14:], dosDay)
		binary.LittleEndian.PutUint32(central[16:], checksum)
		binary.LittleEndian.PutUint32(central[20:], uint32(len(compressed)))
		binary.LittleEndian.PutUint32(central[24:], uint32(len(data)))
		binary.LittleEndian.PutUint16(central[28:], uint16(len(name)))
		binary.LittleEndian.PutUint32(central[42:], offset)

		files.Write(local)
		files.Write(name)
		files.Write(compressed)

		directory.Write(central)
		directory.Write(name)

		offset += uint32(len(local) + len(name) + len(compressed))
	}

	centralDirectory := directory.Bytes()
	end := make([]byte, 22)
	binary.LittleEndian.PutUint32(end[0:], 0x06054b50)
	binary.LittleEndian.PutUint16(end[8:], uint16(len(entries)))
	binary.LittleEndian.PutUint16(end[10:], uint16(len(entries)))
	binary.LittleEndian.PutUint32(end[12:], uint32(len(centralDirectory)))
	binary.LittleEndian.PutUint32(end[16:], offset)

	var out bytes.Buffer
	out.Write(files.Bytes())
	out.Write(centralDirectory)
	out.Write(end)
	return out.Bytes()
}

// WriteZipArchive persists the archive.
func WriteZipArchive(filePath string, entries []ZipEntry) error {
	return os.WriteFile(filePath, CreateZipArchive(entries), 0o644)
}

// deflateRaw compresses with the zip method-8 flavor (raw deflate).
func deflateRaw(data []byte) []byte {
	var buf bytes.Buffer
	writer, _ := flate.NewWriter(&buf, flate.DefaultCompression)
	writer.Write(data)
	writer.Close()
	return buf.Bytes()
}
