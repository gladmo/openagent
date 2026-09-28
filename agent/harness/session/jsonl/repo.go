package jsonl

// repo.go ports harness/session/jsonl/repo.ts: the file-backed format-4
// session repository.

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gladmo/openagent/agent/harness"
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/ai"
)

// SessionDirectoryName mirrors sessionDirectoryName: -- + cwd with / \ :
// replaced by - + -- (lossy: /a/b and /a-b collide; list re-filters by
// header cwd).
func SessionDirectoryName(cwd string) string {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(cwd, "/"), "\\")
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-")
	return "--" + replacer.Replace(trimmed) + "--"
}

// SessionFileName mirrors sessionFileName: ISO timestamp with : and .
// replaced by - + _ + encodeURIComponent(id) + .jsonl.
func SessionFileName(createdAt float64, id string) string {
	timestamp := formatISOTimestamp(createdAt)
	return timestamp + "_" + JSEncodeURIComponent(id) + ".jsonl"
}

func formatISOTimestamp(ms float64) string {
	t := time.UnixMilli(int64(ms)).UTC()
	return strings.NewReplacer(":", "-", ".", "-").Replace(t.Format("2006-01-02T15-04-05.000Z"))
}

// JsonlSessionRepo mirrors the TS class.
type JsonlSessionRepo struct {
	mu             sync.Mutex
	fileSystem     harness.FileSystem
	sessionsRoot   string
	nowFn          func() float64
	openSessions   map[string]*JsonlStorage
	pendingCreates map[string]bool
	pathBySession  map[string]string
	closed         bool
}

// NewJsonlSessionRepo builds an open repo.
func NewJsonlSessionRepo(fs harness.FileSystem, sessionsRoot string, nowFn func() float64) *JsonlSessionRepo {
	if nowFn == nil {
		nowFn = defaultNow
	}
	return &JsonlSessionRepo{
		fileSystem:     fs,
		sessionsRoot:   sessionsRoot,
		nowFn:          nowFn,
		openSessions:   map[string]*JsonlStorage{},
		pendingCreates: map[string]bool{},
		pathBySession:  map[string]string{},
	}
}

func (r *JsonlSessionRepo) sessionKey(cwd, id string) string { return cwd + " " + id }

func (r *JsonlSessionRepo) assertOpen() error {
	if r.closed {
		return fmt.Errorf("JsonlSessionRepo is closed")
	}
	return nil
}

// Create builds a new session file.
func (r *JsonlSessionRepo) Create(options map[string]any, ctx harness.Context) (session.Session, error) {
	if err := r.assertOpen(); err != nil {
		return nil, err
	}
	createdAt := r.nowFn()
	cwdInput, _ := options["cwd"].(string)
	idInput, _ := options["id"].(string)
	parentSessionID, hasParent := options["parentSessionId"].(string)

	cwdAbs := cwdResult(r.fileSystem, cwdInput, ctx)
	id := idInput
	if id == "" {
		id = ai.UUIDv7(createdAt)
	}
	key := r.sessionKey(cwdAbs, id)
	r.mu.Lock()
	if _, open := r.openSessions[key]; open || r.pendingCreates[key] {
		r.mu.Unlock()
		return nil, fmt.Errorf("Session already exists: %s", id)
	}
	r.pendingCreates[key] = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pendingCreates, key)
		r.mu.Unlock()
	}()

	path := r.newSessionPath(cwdAbs, createdAt, id)
	header := StorageHeader{
		V: JSONLFormatVersion, Kind: "header", ID: id,
		StorageVersion: JSONLStorageVersion, CreatedAt: createdAt, Cwd: cwdAbs,
	}
	if hasParent {
		header.ParentSessionID = &parentSessionID
	}
	storage, err := CreateJsonlStorage(r.fileSystem, path, header, nil, ctx, r.nowFn)
	if err != nil {
		force := true
		_ = r.fileSystem.Remove(path, &harness.RemoveOptions{Force: &force}, ctx)
		return nil, err
	}
	info := r.fileSystem.FileInfo(path, ctx)
	modifiedAt := float64(time.Now().UnixMilli())
	if info.Ok {
		modifiedAt = info.Value.MtimeMs
	}
	metadata := SessionMetadata{
		SessionMetadata: session.SessionMetadata{
			ID: header.ID, CreatedAt: header.CreatedAt, StorageVersion: header.StorageVersion,
			Cwd: &header.Cwd, ParentSessionID: header.ParentSessionID,
		},
		Cwd: header.Cwd, Path: path, ModifiedAt: modifiedAt,
	}
	return r.publishOpenSession(metadata, storage, key, ctx), nil
}

func cwdResult(fs harness.FileSystem, cwd string, ctx harness.Context) string {
	abs := fs.AbsolutePath(cwd, ctx)
	if abs.Ok {
		return abs.Value
	}
	return cwd
}

func (r *JsonlSessionRepo) newSessionPath(cwd string, createdAt float64, id string) string {
	dir := SessionDirectoryName(cwd)
	file := SessionFileName(createdAt, id)
	root := r.rootDir()
	join := r.fileSystem.JoinPath([]string{root, dir, file}, nil)
	if join.Ok {
		return join.Value
	}
	return root + "/" + dir + "/" + file
}

func (r *JsonlSessionRepo) rootDir() string {
	abs := r.fileSystem.AbsolutePath(r.sessionsRoot, nil)
	if abs.Ok {
		return abs.Value
	}
	return r.sessionsRoot
}

func (r *JsonlSessionRepo) publishOpenSession(metadata SessionMetadata, storage *JsonlStorage, key string, _ harness.Context) session.Session {
	r.mu.Lock()
	r.openSessions[key] = storage
	r.pathBySession[sessionPathKey(metadata.SessionMetadata)] = metadata.Path
	r.mu.Unlock()
	onClose := func() {
		r.mu.Lock()
		delete(r.openSessions, key)
		r.mu.Unlock()
	}
	return session.NewStorageBackedSession(metadata.SessionMetadata, storage, session.StorageBackedSessionOptions{OnClose: onClose})
}

// Open loads an existing session file.
func (r *JsonlSessionRepo) Open(metadata session.SessionMetadata, ctx harness.Context) (session.Session, error) {
	if err := r.assertOpen(); err != nil {
		return nil, err
	}
	key := r.sessionKey(*metadata.Cwd, metadata.ID)
	r.mu.Lock()
	if _, open := r.openSessions[key]; open {
		r.mu.Unlock()
		return nil, fmt.Errorf("Session is already open: %s", metadata.ID)
	}
	r.mu.Unlock()
	storage, err := OpenJsonlStorage(r.fileSystem, r.pathOfMetadata(metadata), ctx, r.nowFn)
	if err != nil {
		return nil, err
	}
	jsonlMeta := SessionMetadata{
		SessionMetadata: metadata,
		Cwd:             derefOr(metadata.Cwd, ""),
		Path:            r.pathOfMetadata(metadata),
		ModifiedAt:      0,
	}
	return r.publishOpenSession(jsonlMeta, storage, key, ctx), nil
}

func (r *JsonlSessionRepo) pathOfMetadata(metadata session.SessionMetadata) string {
	// Check the path side-table first (Create/Open register it).
	r.mu.Lock()
	if p, ok := r.pathBySession[sessionPathKey(metadata)]; ok {
		r.mu.Unlock()
		return p
	}
	r.mu.Unlock()
	return r.newSessionPath(derefOr(metadata.Cwd, ""), metadata.CreatedAt, metadata.ID)
}

func sessionPathKey(metadata session.SessionMetadata) string {
	return derefOr(metadata.Cwd, "") + " " + metadata.ID + " " + fmt.Sprintf("%.0f", metadata.CreatedAt)
}

// List enumerates sessions: read only line 1 of each .jsonl, re-filter by
// header cwd, sort createdAt desc then id then cwd.
func (r *JsonlSessionRepo) List(options any, ctx harness.Context) ([]session.SessionMetadata, error) {
	if err := r.assertOpen(); err != nil {
		return nil, err
	}
	filterCwd := ""
	if opts, ok := options.(struct{ Cwd string }); ok && opts.Cwd != "" {
		filterCwd = cwdResult(r.fileSystem, opts.Cwd, ctx)
	}
	root := r.rootDir()
	if exists := r.fileSystem.Exists(root, ctx); !exists.Ok || !exists.Value {
		return nil, nil
	}
	if filterCwd != "" {
		// Only one directory matches a cwd filter.
		entries := listFiles(r.fileSystem, joinPathFS(r.fileSystem, root, SessionDirectoryName(filterCwd)), ctx)
		return r.collectMetadata(entries, filterCwd, ctx)
	}
	// All dirs.
	allDirs := listDirs(r.fileSystem, root, ctx)
	var out []session.SessionMetadata
	for _, dir := range allDirs {
		entries := listFiles(r.fileSystem, joinPathFS(r.fileSystem, root, dir.Name), ctx)
		metadata, err := r.collectMetadata(entries, "", ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, metadata...)
	}
	sortMetadata(out)
	return out, nil
}

func listFiles(fs harness.FileSystem, dir string, ctx harness.Context) []harness.FileInfo {
	result := fs.ListDir(dir, ctx)
	if !result.Ok {
		return nil
	}
	out := []harness.FileInfo{}
	for _, entry := range result.Value {
		if entry.Kind == harness.FileKindFile && strings.HasSuffix(entry.Name, ".jsonl") {
			out = append(out, entry)
		}
	}
	return out
}

func listDirs(fs harness.FileSystem, dir string, ctx harness.Context) []harness.FileInfo {
	result := fs.ListDir(dir, ctx)
	if !result.Ok {
		return nil
	}
	out := []harness.FileInfo{}
	for _, entry := range result.Value {
		if entry.Kind == harness.FileKindDirectory && strings.HasPrefix(entry.Name, "--") {
			out = append(out, entry)
		}
	}
	return out
}

func joinPathFS(fs harness.FileSystem, base, name string) string {
	join := fs.JoinPath([]string{base, name}, nil)
	if join.Ok {
		return join.Value
	}
	return base + "/" + name
}

func (r *JsonlSessionRepo) collectMetadata(entries []harness.FileInfo, filterCwd string, ctx harness.Context) ([]session.SessionMetadata, error) {
	out := []session.SessionMetadata{}
	for _, entry := range entries {
		header := readFirstLineHeader(r.fileSystem, entry.Path, ctx)
		if header == nil {
			continue
		}
		if filterCwd != "" && header.Cwd != filterCwd {
			continue
		}
		info := r.fileSystem.FileInfo(entry.Path, ctx)
		_ = info // modifiedAt lands on the Jsonl metadata in TS; base metadata omits it
		out = append(out, session.SessionMetadata{
			ID:                      header.ID,
			CreatedAt:               header.CreatedAt,
			StorageVersion:          header.StorageVersion,
			Cwd:                     &header.Cwd,
			ParentSessionID:         header.ParentSessionID,
			LegacyParentSessionPath: header.LegacyParentSessionPath,
		})
	}
	sortMetadata(out)
	return out, nil
}

func readFirstLineHeader(fs harness.FileSystem, path string, ctx harness.Context) *StorageHeader {
	readerResult := fs.OpenTextLineReader(path, ctx)
	if !readerResult.Ok {
		return nil
	}
	reader := readerResult.Value
	defer reader.Close(ctx)
	lineResult, _ := reader.ReadLine(ctx)
	if !lineResult.Ok || lineResult.Value == nil {
		return nil
	}
	parsed, err := ParseSessionHeader(lineResult.Value.Text)
	if err != nil {
		return nil
	}
	if parsed.Format == "v4" {
		return &parsed.Header
	}
	return nil
}

func sortMetadata(metadata []session.SessionMetadata) {
	sort.SliceStable(metadata, func(i, j int) bool {
		if metadata[i].CreatedAt != metadata[j].CreatedAt {
			return metadata[i].CreatedAt > metadata[j].CreatedAt
		}
		if metadata[i].ID != metadata[j].ID {
			return metadata[i].ID < metadata[j].ID
		}
		return derefOr(metadata[i].Cwd, "") < derefOr(metadata[j].Cwd, "")
	})
}

// Delete removes a session file (refusing open sessions).
func (r *JsonlSessionRepo) Delete(metadata session.SessionMetadata, ctx harness.Context) error {
	if err := r.assertOpen(); err != nil {
		return err
	}
	key := r.sessionKey(derefOr(metadata.Cwd, ""), metadata.ID)
	r.mu.Lock()
	if _, open := r.openSessions[key]; open {
		r.mu.Unlock()
		return fmt.Errorf("Session is open: %s", metadata.ID)
	}
	r.mu.Unlock()
	path := r.pathOfMetadata(metadata)
	force := true
	if result := r.fileSystem.Remove(path, &harness.RemoveOptions{Force: &force}, ctx); !result.Ok {
		return result.Error
	}
	return nil
}

// Fork copies a session into a destination via runJsonlFork (deferred to
// the fork.go port); the tree/branch projection reuses the in-memory fork.
func (r *JsonlSessionRepo) Fork(source session.SessionMetadata, options session.ForkOptions, ctx harness.Context) (session.Session, error) {
	if err := r.assertOpen(); err != nil {
		return nil, err
	}
	sourcePath := r.pathOfMetadata(source)
	sourceKey := r.sessionKey(derefOr(source.Cwd, ""), source.ID)
	r.mu.Lock()
	if _, open := r.openSessions[sourceKey]; open {
		r.mu.Unlock()
		return nil, fmt.Errorf("Source session is open: %s", source.ID)
	}
	r.mu.Unlock()

	sourceStorage, err := OpenJsonlStorage(r.fileSystem, sourcePath, ctx, r.nowFn)
	if err != nil {
		return nil, err
	}
	// Build the fork state from the source storage's materialized state.
	forkState, err := sourceStorage.storageState.CreateFork(options)
	if err != nil {
		_ = sourceStorage.Close(ctx)
		return nil, err
	}
	nextSeq, err := sourceStorage.CaptureForkNextSeq(ctx)
	_ = sourceStorage.Close(ctx)
	if err != nil {
		return nil, err
	}

	destinationID := ai.UUIDv7(r.nowFn())
	if options.ID != nil {
		destinationID = *options.ID
	}
	createdAt := r.nowFn()
	destCwd := derefOr(source.Cwd, "")
	destPath := r.newSessionPath(destCwd, createdAt, destinationID)
	header := StorageHeader{
		V: JSONLFormatVersion, Kind: "header", ID: destinationID,
		StorageVersion: JSONLStorageVersion, CreatedAt: createdAt, Cwd: destCwd,
		ParentSessionID: &source.ID,
	}
	header.NextSeq = &nextSeq
	// Serialize the fork state as v4 transactions.
	var initialWrites []session.Write
	for _, entry := range forkState.SnapshotEntries() {
		initialWrites = append(initialWrites, session.InsertEntry(entry))
	}
	for _, stored := range forkState.SnapshotScalars() {
		initialWrites = append(initialWrites, session.WriteFromValue(session.SetValue(stored.Address, stored.Value)))
	}
	destination, err := CreateJsonlStorage(r.fileSystem, destPath, header, initialWrites, ctx, r.nowFn)
	if err != nil {
		return nil, err
	}
	key := r.sessionKey(destCwd, destinationID)
	info := r.fileSystem.FileInfo(destPath, ctx)
	modifiedAt := createdAt
	if info.Ok {
		modifiedAt = info.Value.MtimeMs
	}
	metadata := SessionMetadata{
		SessionMetadata: session.SessionMetadata{
			ID: destinationID, CreatedAt: createdAt, StorageVersion: JSONLStorageVersion,
			Cwd: &destCwd, ParentSessionID: &source.ID,
		},
		Cwd: destCwd, Path: destPath, ModifiedAt: modifiedAt,
	}
	return r.publishOpenSession(metadata, destination, key, ctx), nil
}

// Close is a no-op TODO in TS.
func (r *JsonlSessionRepo) Close(_ harness.Context) error { return nil }

func derefOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

// JSEncodeURIComponent matches JS encodeURIComponent: unreserved chars
// A-Za-z0-9 - _ . ! ~ * ' ( ) stay raw; space becomes %20 (not +).
func JSEncodeURIComponent(s string) string {
	escaped := url.QueryEscape(s)
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	for _, pair := range [][2]string{
		{"%21", "!"}, {"%27", "'"}, {"%28", "("}, {"%29", ")"}, {"%2A", "*"}, {"%7E", "~"},
	} {
		escaped = strings.ReplaceAll(escaped, pair[0], pair[1])
	}
	return escaped
}
