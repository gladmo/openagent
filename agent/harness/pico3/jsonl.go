package pico3

// jsonl.go ports harness/pico3/jsonl.ts: the file-backed Storage.
//
// Publication (§10): one commit Seq; sidecar records are appended first,
// then exactly one main record, last, listing the sidecar refs it expects —
// the publication point. Replay applies a sidecar record only when main
// has the marker for that seq naming that file; unconfirmed sidecar tails
// are ignored. Bytes after the last newline of any file are a torn write
// and are truncated before the file is opened for append.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	jsonx "github.com/gladmo/openagent/jsonx"

	chorddelta "github.com/gladmo/openagent/chord/delta"
)

// jsonlRecord mirrors Record_.
type jsonlRecord struct {
	Seq    Seq              `json:"seq"`
	MaxID  Id               `json:"maxId"`
	Writes []jsonlWriteJSON `json:"writes"`
	Refs   []string         `json:"refs,omitempty"`
}

// jsonlWriteJSON is the serialized Write union (kind + payload).
type jsonlWriteJSON struct {
	Kind         string         `json:"type"`
	Conversation *Conversation  `json:"conversation,omitempty"`
	Entry        *Entry         `json:"entry,omitempty"`
	Task         *Task          `json:"task,omitempty"`
	Patch        *TaskPatchJSON `json:"patch,omitempty"`
	Input        *Input         `json:"input,omitempty"`
	DocRef       *DocRefJSON    `json:"ref,omitempty"`
	OpsJSON      []any          `json:"-"`
}

// TaskPatchJSON is the serialized patch.
type TaskPatchJSON struct {
	ID                Id      `json:"id"`
	Status            *string `json:"status,omitempty"`
	Checkpoint        any     `json:"checkpoint,omitempty"`
	Abort             *bool   `json:"abort,omitempty"`
	Outcome           any     `json:"outcome,omitempty"`
	Owns              []Id    `json:"owns,omitempty"`
	hasNullCheckpoint bool
}

// DocRefJSON is the serialized doc ref.
type DocRefJSON struct {
	Doc            string `json:"doc"`
	ConversationID Id     `json:"conversationId,omitempty"`
}

// JsonlStorage extends MemoryStorage with a JSONL directory.
type JsonlStorage struct {
	MemoryStorage
	dir          string
	fsync        bool
	mainFile     *os.File
	sidecars     map[Id]*os.File
	taskSidecars map[Id]*os.File
	closedOnce   bool
}

// OpenJsonlStorage opens (creating if needed) a session directory.
func OpenJsonlStorage(dir string, fsync bool) (*JsonlStorage, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			if err := truncateTornTail(filepath.Join(dir, entry.Name())); err != nil {
				return nil, err
			}
		}
	}
	storage := &JsonlStorage{
		MemoryStorage: *NewMemoryStorage(),
		dir:           dir,
		fsync:         fsync,
		sidecars:      map[Id]*os.File{},
		taskSidecars:  map[Id]*os.File{},
	}
	mainFile, err := os.OpenFile(filepath.Join(dir, "main.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	storage.mainFile = mainFile
	if err := storage.replay(); err != nil {
		_ = storage.Close()
		return nil, err
	}
	return storage, nil
}

// Dir exposes the session directory (tests).
func (s *JsonlStorage) Dir() string { return s.dir }

func truncateTornTail(file string) error {
	info, err := os.Stat(file)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	end := lastIndexOfByte(data, '\n')
	if int64(end+1) != info.Size() {
		return os.Truncate(file, int64(end+1))
	}
	return nil
}

func lastIndexOfByte(data []byte, b byte) int {
	for i := len(data) - 1; i >= 0; i-- {
		if data[i] == b {
			return i
		}
	}
	return -1
}

// fileRecord pairs one parsed record with its end byte offset.
type fileRecord struct {
	record jsonlRecord
	end    int64
}

// readFileRecords parses one file's records (complete lines only).
func readFileRecords(file string) ([]fileRecord, error) {
	if _, err := os.Stat(file); os.IsNotExist(err) {
		return nil, nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	out := []fileRecord{}
	start := 0
	last := Seq(0)
	for {
		idx := indexOfByteFrom(data, '\n', start)
		if idx < 0 {
			break
		}
		line := string(data[start:idx])
		start = idx + 1
		if line == "" {
			continue
		}
		parsedLine, parseErr := jsonxParse(line)
		if parseErr != nil {
			return nil, fmt.Errorf("%s: malformed record: %v", file, parseErr)
		}
		parsedObj, isObj := parsedLine.(*jsonxObjPtr)
		if !isObj {
			return nil, fmt.Errorf("%s: record is not an object", file)
		}
		if _, hasSeq := parsedObj.Get("seq"); !hasSeq {
			return nil, fmt.Errorf("%s: record lacks seq/maxId/writes", file)
		}
		if _, hasMax := parsedObj.Get("maxId"); !hasMax {
			return nil, fmt.Errorf("%s: record lacks seq/maxId/writes", file)
		}
		if _, hasWrites := parsedObj.Get("writes"); !hasWrites {
			return nil, fmt.Errorf("%s: record lacks seq/maxId/writes", file)
		}
		record, err := recordFromJSONString(line)
		if err != nil {
			return nil, fmt.Errorf("%s: malformed record: %v", file, err)
		}
		if record.Seq <= last {
			return nil, fmt.Errorf("%s: sequence not increasing at %d", file, record.Seq)
		}
		last = record.Seq
		out = append(out, fileRecord{record: record, end: int64(start)})
	}
	return out, nil
}

func indexOfByteFrom(data []byte, b byte, from int) int {
	for i := from; i < len(data); i++ {
		if data[i] == b {
			return i
		}
	}
	return -1
}

func (s *JsonlStorage) replay() error {
	maxID := Id(0)
	mainRecords := []jsonlRecord{}
	mainParsed, err := readFileRecords(filepath.Join(s.dir, "main.jsonl"))
	if err != nil {
		return err
	}
	for _, p := range mainParsed {
		mainRecords = append(mainRecords, p.record)
	}
	confirmed := map[Seq]map[string]bool{}
	for _, record := range mainRecords {
		confirmed[record.Seq] = map[string]bool{}
		for _, ref := range record.Refs {
			confirmed[record.Seq][ref] = true
		}
	}
	dirEntries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	sidecarFiles := []string{}
	for _, entry := range dirEntries {
		name := entry.Name()
		if strings.HasPrefix(name, "sticky-") || strings.HasPrefix(name, "task-") {
			sidecarFiles = append(sidecarFiles, name)
		}
	}
	sort.Strings(sidecarFiles)

	bySeq := map[Seq][]Write{}
	found := map[Seq]map[string]bool{}
	retainedStickyBase := map[string]Seq{}
	retiredTasks := map[Id]Seq{}
	for _, record := range mainRecords {
		for _, w := range record.Writes {
			if w.Kind == "task.patch" && w.Patch != nil && w.Patch.Status != nil && *w.Patch.Status == "terminal" {
				retiredTasks[w.Patch.ID] = record.Seq
			}
		}
		writes, err := writesFromJSON(record.Writes)
		if err != nil {
			return err
		}
		bySeq[record.Seq] = append(bySeq[record.Seq], writes...)
		if record.MaxID > maxID {
			maxID = record.MaxID
		}
	}
	for _, file := range sidecarFiles {
		path := filepath.Join(s.dir, file)
		records, err := readFileRecords(path)
		if err != nil {
			return err
		}
		if len(records) > 0 && strings.HasPrefix(file, "sticky-") {
			first := records[0].record
			if isBaseWrites(first.Writes) {
				retainedStickyBase[file] = first.Seq
			}
		}
		confirmedEnd := int64(0)
		sawUnconfirmed := false
		for _, p := range records {
			marker := confirmed[p.record.Seq]
			if marker == nil || !marker[file] {
				sawUnconfirmed = true
				continue
			}
			if sawUnconfirmed {
				return fmt.Errorf("%s: confirmed record follows an unconfirmed tail at %d", path, p.record.Seq)
			}
			confirmedEnd = p.end
			if found[p.record.Seq] == nil {
				found[p.record.Seq] = map[string]bool{}
			}
			found[p.record.Seq][file] = true
			writes, err := writesFromJSON(p.record.Writes)
			if err != nil {
				return err
			}
			bySeq[p.record.Seq] = append(bySeq[p.record.Seq], writes...)
			if p.record.MaxID > maxID {
				maxID = p.record.MaxID
			}
		}
		if sawUnconfirmed {
			if err := os.Truncate(path, confirmedEnd); err != nil {
				return err
			}
		}
	}
	// Every published ref must be backed.
	for _, record := range mainRecords {
		for _, file := range record.Refs {
			if found[record.Seq] != nil && found[record.Seq][file] {
				continue
			}
			if retainedFrom, ok := retainedStickyBase[file]; ok && record.Seq < retainedFrom {
				continue
			}
			if strings.HasPrefix(file, "task-") {
				id := taskIDOf(file)
				if retiredAt, ok := retiredTasks[id]; ok && record.Seq < retiredAt {
					continue
				}
			}
			return fmt.Errorf("%s: missing record for published sequence %d", filepath.Join(s.dir, file), record.Seq)
		}
	}
	seqs := []Seq{}
	for seq := range bySeq {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for _, seq := range seqs {
		s.MemoryStorage.seq = seq - 1
		if _, err := s.MemoryStorage.Commit(bySeq[seq]); err != nil {
			return err
		}
	}
	s.MemoryStorage.setNextIdExport(maxID + 1)
	// Task sidecars belong to live tasks only.
	for _, file := range sidecarFiles {
		if !strings.HasPrefix(file, "task-") {
			continue
		}
		id := taskIDOf(file)
		task, err := s.MemoryStorage.Task(id)
		if err != nil {
			return err
		}
		if task == nil || task.Status == "terminal" {
			_ = os.Remove(filepath.Join(s.dir, file))
		}
	}
	return nil
}

func taskIDOf(file string) Id {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(file, "task-"), ".jsonl")
	id, _ := strconv.ParseInt(trimmed, 10, 64)
	return id
}

func isBaseWrites(writes []jsonlWriteJSON) bool {
	var ops []Op
	for _, w := range writes {
		if w.Kind == "doc" {
			for _, item := range w.OpsJSON {
				if op, err := chorddelta.OpFromJSON(item); err == nil {
					ops = append(ops, op)
				}
			}
		}
	}
	return chorddelta.IsBase(ops)
}

// Commit stages, routes writes to sidecars, publishes the main marker, and
// applies in memory.
func (s *JsonlStorage) Commit(writes []Write) (Seq, error) {
	if s.closedOnce {
		return 0, fmt.Errorf("storage closed")
	}
	seq := s.MemoryStorage.seq + 1
	// Validate the whole batch first against the real state; nothing is
	// applied on failure.
	if _, err := s.MemoryStorage.ValidateBatch(writes, seq); err != nil {
		return 0, err
	}
	maxID := s.nextIDAfter(writes)

	main := []Write{}
	sticky := map[Id][]Write{}
	tasks := map[Id][]Write{}
	var retired []Id
	for i := range writes {
		w := writes[i]
		if w.Type == "doc" && w.Ref != nil && w.Ref.Doc == "sticky" {
			sticky[w.Ref.ConversationID] = append(sticky[w.Ref.ConversationID], w)
		} else if w.Type == "task.patch" && w.Patch.Status != nil && *w.Patch.Status != "terminal" {
			tasks[w.Patch.ID] = append(tasks[w.Patch.ID], w)
		} else {
			main = append(main, w)
			if w.Type == "task.patch" && w.Patch.Status != nil && *w.Patch.Status == "terminal" {
				retired = append(retired, w.Patch.ID)
			}
		}
	}
	var refs []string
	taskIDs := sortedIDs(tasks)
	for _, id := range taskIDs {
		if err := s.appendTaskSidecar(id, jsonlRecord{Seq: seq, MaxID: maxID, Writes: writesToJSON(tasks[id])}); err != nil {
			return 0, err
		}
		refs = append(refs, fmt.Sprintf("task-%d.jsonl", id))
	}
	stickyIDs := sortedIDs(sticky)
	for _, id := range stickyIDs {
		if err := s.appendStickySidecar(id, jsonlRecord{Seq: seq, MaxID: maxID, Writes: writesToJSON(sticky[id])}); err != nil {
			return 0, err
		}
		refs = append(refs, fmt.Sprintf("sticky-%d.jsonl", id))
	}
	record := jsonlRecord{Seq: seq, MaxID: maxID, Writes: writesToJSON(main)}
	if len(refs) > 0 {
		record.Refs = refs
	}
	if err := s.appendMain(record); err != nil {
		return 0, err
	}
	if _, err := s.MemoryStorage.Commit(writes); err != nil {
		return 0, err
	}
	for _, id := range retired {
		s.retireTaskSidecar(id)
	}
	return seq, nil
}

func sortedIDs[V any](m map[Id]V) []Id {
	ids := make([]Id, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (s *JsonlStorage) nextIDAfter(writes []Write) Id {
	max := s.MemoryStorage.peekNextID() - 1
	for _, w := range writes {
		id := Id(0)
		switch w.Type {
		case "conversation":
			id = w.Conversation.ID
		case "entry":
			id = w.Entry.ID
		case "task":
			id = w.Task.ID
		case "input":
			id = w.Input.ID
		}
		if id > max {
			max = id
		}
	}
	return max
}

func (s *JsonlStorage) appendRecord(file *os.File, record jsonlRecord) error {
	line := []byte(RecordToJSONString(record))
	if _, err := file.Write(append(line, '\n')); err != nil {
		return err
	}
	if s.fsync {
		return file.Sync()
	}
	return nil
}

func (s *JsonlStorage) appendMain(record jsonlRecord) error {
	return s.appendRecord(s.mainFile, record)
}

func (s *JsonlStorage) appendStickySidecar(conversationID Id, record jsonlRecord) error {
	file, err := s.stickyFile(conversationID)
	if err != nil {
		return err
	}
	return s.appendRecord(file, record)
}

func (s *JsonlStorage) appendTaskSidecar(id Id, record jsonlRecord) error {
	file, err := s.taskFile(id)
	if err != nil {
		return err
	}
	return s.appendRecord(file, record)
}

func (s *JsonlStorage) stickyFile(conversationID Id) (*os.File, error) {
	if file, ok := s.sidecars[conversationID]; ok {
		return file, nil
	}
	file, err := os.OpenFile(filepath.Join(s.dir, fmt.Sprintf("sticky-%d.jsonl", conversationID)), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	s.sidecars[conversationID] = file
	return file, nil
}

func (s *JsonlStorage) taskFile(id Id) (*os.File, error) {
	if file, ok := s.taskSidecars[id]; ok {
		return file, nil
	}
	file, err := os.OpenFile(filepath.Join(s.dir, fmt.Sprintf("task-%d.jsonl", id)), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	s.taskSidecars[id] = file
	return file, nil
}

func (s *JsonlStorage) retireTaskSidecar(id Id) {
	if file, ok := s.taskSidecars[id]; ok {
		_ = file.Close()
		delete(s.taskSidecars, id)
	}
	_ = os.Remove(filepath.Join(s.dir, fmt.Sprintf("task-%d.jsonl", id)))
}

// Truncate rewrites the sticky sidecar from its last base.
func (s *JsonlStorage) Truncate(ref DocRef) error {
	if err := s.MemoryStorage.Truncate(ref); err != nil {
		return err
	}
	if ref.Doc != "sticky" {
		return nil
	}
	file := filepath.Join(s.dir, fmt.Sprintf("sticky-%d.jsonl", ref.ConversationID))
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	lines := nonEmptyLines(string(data))
	start := 0
	for i := len(lines) - 1; i >= 0; i-- {
		record, err := recordFromJSONString(lines[i])
		if err != nil {
			continue
		}
		if isBaseWrites(record.Writes) {
			start = i
			break
		}
	}
	if start == 0 {
		return nil
	}
	tmp := file + ".tmp"
	content := strings.Join(lines[start:], "\n") + "\n"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	if s.fsync {
		if f, err := os.Open(tmp); err == nil {
			_ = f.Sync()
			_ = f.Close()
		}
	}
	if old, ok := s.sidecars[ref.ConversationID]; ok {
		_ = old.Close()
	}
	if err := os.Rename(tmp, file); err != nil {
		return err
	}
	appendFile, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	s.sidecars[ref.ConversationID] = appendFile
	return nil
}

func nonEmptyLines(content string) []string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// Close closes every file handle once.
func (s *JsonlStorage) Close() error {
	if s.closedOnce {
		return nil
	}
	s.closedOnce = true
	_ = s.MemoryStorage.Close()
	_ = s.mainFile.Close()
	for _, file := range s.sidecars {
		_ = file.Close()
	}
	for _, file := range s.taskSidecars {
		_ = file.Close()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Write <-> JSON codecs
// ---------------------------------------------------------------------------

func writesToJSON(writes []Write) []jsonlWriteJSON {
	out := make([]jsonlWriteJSON, 0, len(writes))
	for _, w := range writes {
		j := jsonlWriteJSON{Kind: w.Type}
		switch w.Type {
		case "conversation":
			j.Conversation = w.Conversation
		case "entry":
			j.Entry = w.Entry
		case "task":
			j.Task = w.Task
		case "task.patch":
			j.Patch = patchToJSON(w.Patch)
		case "input":
			j.Input = w.Input
		case "doc":
			j.DocRef = &DocRefJSON{Doc: w.Ref.Doc, ConversationID: w.Ref.ConversationID}
			ops := make([]any, 0, len(w.Ops))
			for _, op := range w.Ops {
				ops = append(ops, chorddelta.OpToJSON(op))
			}
			j.OpsJSON = ops
		}
		out = append(out, j)
	}
	return out
}

func patchToJSON(p *TaskPatch) *TaskPatchJSON {
	out := &TaskPatchJSON{ID: p.ID, Status: p.Status, Abort: p.Abort, Owns: p.Owns}
	if p.HasCheckpointNull {
		out.Checkpoint = nil
	} else if p.Checkpoint != nil {
		out.Checkpoint = p.Checkpoint
	}
	if p.HasOutcome {
		out.Outcome = p.Outcome
	}
	return out
}

func writesFromJSON(writes []jsonlWriteJSON) ([]Write, error) {
	out := make([]Write, 0, len(writes))
	for _, w := range writes {
		write := Write{Type: w.Kind}
		switch w.Kind {
		case "conversation":
			write.Conversation = w.Conversation
		case "entry":
			write.Entry = w.Entry
		case "task":
			write.Task = w.Task
		case "task.patch":
			write.Patch = patchFromJSON(w.Patch)
		case "input":
			write.Input = w.Input
		case "doc":
			ref := &DocRef{Doc: w.DocRef.Doc, ConversationID: w.DocRef.ConversationID}
			write.Ref = ref
			for _, item := range w.OpsJSON {
				op, err := chorddelta.OpFromJSON(item)
				if err != nil {
					return nil, err
				}
				write.Ops = append(write.Ops, op)
			}
		default:
			return nil, fmt.Errorf("unknown write type %s", w.Kind)
		}
		out = append(out, write)
	}
	return out, nil
}

func patchFromJSON(p *TaskPatchJSON) *TaskPatch {
	if p == nil {
		return nil
	}
	out := &TaskPatch{ID: p.ID, Status: p.Status, Abort: p.Abort, Owns: p.Owns}
	// checkpoint: explicit null clears; object sets; absent untouched.
	if p.Checkpoint == nil {
		out.HasCheckpointNull = true
	} else if obj, ok := p.Checkpoint.(map[string]any); ok {
		checkpoint := NewObject()
		for k, v := range obj {
			checkpoint.Set(k, v)
		}
		out.Checkpoint = checkpoint
	}
	if p.Outcome != nil {
		if obj, ok := p.Outcome.(map[string]any); ok {
			outcome := NewObject()
			for k, v := range obj {
				outcome.Set(k, v)
			}
			out.Outcome = outcome
			out.HasOutcome = true
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// jsonx-based record codec (encoding/json cannot marshal jsonx objects)
// ---------------------------------------------------------------------------

type jsonxObjType = *jsonxObjPtr

// RecordToJSONString renders one record through the jsonx model.
func RecordToJSONString(record jsonlRecord) string {
	obj := jsonxNewObj()
	obj.Set("seq", float64(record.Seq))
	obj.Set("maxId", float64(record.MaxID))
	writes := make([]any, 0, len(record.Writes))
	for i := range record.Writes {
		writes = append(writes, writeCarrierToJSON(&record.Writes[i]))
	}
	obj.Set("writes", writes)
	if record.Refs != nil {
		refs := make([]any, 0, len(record.Refs))
		for _, ref := range record.Refs {
			refs = append(refs, ref)
		}
		obj.Set("refs", refs)
	}
	return jsonxStringifyHelper(obj)
}

func patchCarrierToJSON(p *TaskPatchJSON) *jsonx.Obj {
	obj := jsonxNewObj()
	obj.Set("id", float64(p.ID))
	if p.Status != nil {
		obj.Set("status", *p.Status)
	}
	if p.hasNullCheckpoint {
		obj.Set("checkpoint", nil)
	} else if p.Checkpoint != nil {
		obj.Set("checkpoint", p.Checkpoint)
	}
	if p.Abort != nil {
		obj.Set("abort", *p.Abort)
	}
	if p.Outcome != nil {
		obj.Set("outcome", p.Outcome)
	}
	if p.Owns != nil {
		owns := make([]any, 0, len(p.Owns))
		for _, id := range p.Owns {
			owns = append(owns, float64(id))
		}
		obj.Set("owns", owns)
	}
	return obj
}

func writeCarrierToJSON(w *jsonlWriteJSON) *jsonxObjPtr {
	obj := jsonxNewObj()
	obj.Set("type", w.Kind)
	switch w.Kind {
	case "conversation":
		obj.Set("conversation", valueToJSON(w.Conversation))
	case "entry":
		obj.Set("entry", entryCarrierToJSON(w.Entry))
	case "task":
		obj.Set("task", taskCarrierToJSON(w.Task))
	case "task.patch":
		obj.Set("patch", patchCarrierToJSON(w.Patch))
	case "input":
		obj.Set("input", valueToJSON(w.Input))
	case "doc":
		ref := jsonxNewObj()
		ref.Set("doc", w.DocRef.Doc)
		ref.Set("conversationId", float64(w.DocRef.ConversationID))
		obj.Set("ref", ref)
		ops := make([]any, 0, len(w.OpsJSON))
		ops = append(ops, w.OpsJSON...)
		obj.Set("ops", ops)
	}
	return obj
}

// recordFromJSONString parses one record line through the jsonx model.
func recordFromJSONString(line string) (jsonlRecord, error) {
	parsed, err := jsonxParse(line)
	if err != nil {
		return jsonlRecord{}, err
	}
	obj, ok := parsed.(*jsonxObjPtr)
	if !ok {
		return jsonlRecord{}, fmt.Errorf("record is not an object")
	}
	var record jsonlRecord
	if v, ok := obj.Get("seq"); ok {
		if f, ok := v.(float64); ok {
			record.Seq = Seq(f)
		}
	}
	if v, ok := obj.Get("maxId"); ok {
		if f, ok := v.(float64); ok {
			record.MaxID = Id(f)
		}
	}
	if v, ok := obj.Get("refs"); ok {
		if arr, ok := v.([]any); ok {
			for _, item := range arr {
				if s, ok := item.(string); ok {
					record.Refs = append(record.Refs, s)
				}
			}
		}
	}
	if v, ok := obj.Get("writes"); ok {
		if arr, ok := v.([]any); ok {
			for _, item := range arr {
				writeObj, ok := item.(*jsonxObjPtr)
				if !ok {
					continue
				}
				record.Writes = append(record.Writes, writeCarrierFromJSON(writeObj))
			}
		}
	}
	return record, nil
}

func writeCarrierFromJSON(obj *jsonxObjPtr) jsonlWriteJSON {
	var w jsonlWriteJSON
	if v, ok := obj.Get("type"); ok {
		if s, ok := v.(string); ok {
			w.Kind = s
		}
	}
	switch w.Kind {
	case "conversation":
		if v, ok := obj.Get("conversation"); ok {
			w.Conversation = jsonDecodeInto(v, &Conversation{}).(*Conversation)
		}
	case "entry":
		if v, ok := obj.Get("entry"); ok {
			w.Entry = jsonDecodeInto(v, &Entry{}).(*Entry)
		}
	case "task":
		if v, ok := obj.Get("task"); ok {
			w.Task = jsonDecodeInto(v, &Task{}).(*Task)
		}
	case "task.patch":
		if v, ok := obj.Get("patch"); ok {
			if patchObj, ok := v.(*jsonxObjPtr); ok {
				w.Patch = patchCarrierFromJSON(patchObj)
			}
		}
	case "input":
		if v, ok := obj.Get("input"); ok {
			w.Input = jsonDecodeInto(v, &Input{}).(*Input)
		}
	case "doc":
		if v, ok := obj.Get("ref"); ok {
			if refObj, ok := v.(*jsonxObjPtr); ok {
				ref := &DocRefJSON{}
				if dv, ok := refObj.Get("doc"); ok {
					if s, ok := dv.(string); ok {
						ref.Doc = s
					}
				}
				if cv, ok := refObj.Get("conversationId"); ok {
					if f, ok := cv.(float64); ok {
						ref.ConversationID = Id(f)
					}
				}
				w.DocRef = ref
			}
		}
		if v, ok := obj.Get("ops"); ok {
			if arr, ok := v.([]any); ok {
				w.OpsJSON = append(w.OpsJSON, arr...)
			}
		}
	}
	return w
}

func patchCarrierFromJSON(obj *jsonxObjPtr) *TaskPatchJSON {
	patch := &TaskPatchJSON{}
	if v, ok := obj.Get("id"); ok {
		if f, ok := v.(float64); ok {
			patch.ID = Id(f)
		}
	}
	if v, ok := obj.Get("status"); ok {
		if s, ok := v.(string); ok {
			patch.Status = &s
		}
	}
	checkpoint, hasCheckpoint := obj.Get("checkpoint")
	if hasCheckpoint {
		if checkpoint == nil {
			patch.Checkpoint = nil
			patch.hasNullCheckpoint = true
		} else if checkpointObj, ok := checkpoint.(*jsonxObjPtr); ok {
			patch.Checkpoint = checkpointObj
		}
	}
	if v, ok := obj.Get("abort"); ok {
		if b, ok := v.(bool); ok {
			patch.Abort = &b
		}
	}
	if v, ok := obj.Get("outcome"); ok {
		if outcomeObj, ok := v.(*jsonxObjPtr); ok {
			patch.Outcome = outcomeObj
		}
	}
	if v, ok := obj.Get("owns"); ok {
		if arr, ok := v.([]any); ok {
			for _, item := range arr {
				if f, ok := item.(float64); ok {
					patch.Owns = append(patch.Owns, Id(f))
				}
			}
		}
	}
	return patch
}

func taskCarrierToJSON(t *Task) *jsonx.Obj {
	obj := jsonxNewObj()
	obj.Set("id", float64(t.ID))
	obj.Set("conversationId", float64(t.ConversationID))
	obj.Set("kind", t.Kind)
	if t.Input != nil {
		obj.Set("input", t.Input)
	}
	obj.Set("status", t.Status)
	if t.Checkpoint != nil {
		obj.Set("checkpoint", t.Checkpoint)
	}
	if t.Abort {
		obj.Set("abort", true)
	}
	if t.Outcome != nil {
		obj.Set("outcome", t.Outcome)
	}
	after := make([]any, 0, len(t.After))
	for _, id := range t.After {
		after = append(after, float64(id))
	}
	obj.Set("after", after)
	owns := make([]any, 0, len(t.Owns))
	for _, id := range t.Owns {
		owns = append(owns, float64(id))
	}
	obj.Set("owns", owns)
	if t.Background {
		obj.Set("background", true)
	}
	return obj
}

func entryCarrierToJSON(e *Entry) *jsonx.Obj {
	obj := jsonxNewObj()
	obj.Set("id", float64(e.ID))
	obj.Set("conversationId", float64(e.ConversationID))
	obj.Set("kind", e.Kind)
	if e.Model != nil {
		model := make([]any, 0, len(e.Model))
		model = append(model, e.Model...)
		obj.Set("model", model)
	}
	if e.Data != nil {
		obj.Set("data", e.Data)
	}
	if e.Head != nil {
		obj.Set("head", float64(*e.Head))
	}
	if e.ByTaskID != nil {
		obj.Set("byTaskId", float64(*e.ByTaskID))
	}
	return obj
}
