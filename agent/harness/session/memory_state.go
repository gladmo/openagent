package session

// memory_state.go ports harness/session/in-memory-storage-state.ts.

import (
	"fmt"
	"sort"
	"sync"
)

// memoryForkPlan mirrors MemoryForkPlan.
type memoryForkPlan struct {
	scope          string // "tree" | "branch"
	branch         string
	destinationTip *string
	entryIDs       map[string]bool
}

// storedListSnapshot mirrors StoredListSnapshot.
type storedListSnapshot struct {
	address  ValueList
	elements []ListElement
}

func physicalKey(namespace, key string) string {
	return namespace + "" + key
}

// compareKeys compares strings code-point-wise (not byte-wise).
func compareKeys(left, right string) int {
	leftRunes, rightRunes := []rune(left), []rune(right)
	length := len(leftRunes)
	if len(rightRunes) < length {
		length = len(rightRunes)
	}
	for index := 0; index < length; index++ {
		if leftRunes[index] != rightRunes[index] {
			if leftRunes[index] < rightRunes[index] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(leftRunes) < len(rightRunes):
		return -1
	case len(leftRunes) > len(rightRunes):
		return 1
	default:
		return 0
	}
}

// InMemoryStorageState is the complete materialized session state for
// MemoryStorage and JsonlStorage.
type InMemoryStorageState struct {
	mu           sync.RWMutex
	entries      map[string]*Entry
	entriesBySeq []*Entry
	scalarValues map[string]*StoredValue
	listValues   map[string]*storedListSnapshot
	usage        map[string]*UsageRow
	stats        SessionStats
	nextSeq      int64
}

// NewInMemoryStorageState builds an empty state.
func NewInMemoryStorageState() *InMemoryStorageState {
	return &InMemoryStorageState{
		entries:      map[string]*Entry{},
		scalarValues: map[string]*StoredValue{},
		listValues:   map[string]*storedListSnapshot{},
		usage:        map[string]*UsageRow{},
		nextSeq:      1,
	}
}

// PrepareCommit validates and prepares a commit at the current high-water
// mark.
func (s *InMemoryStorageState) PrepareCommit(writes []Write, timestamp float64) (PreparedCommit, error) {
	prepared := PrepareStorageCommit(writes, s.nextSeq, timestamp)
	if err := s.validateCommitted(prepared.Writes); err != nil {
		return PreparedCommit{}, err
	}
	return prepared, nil
}

func (s *InMemoryStorageState) validateCommitted(writes []CommittedWrite) error {
	state := &memoryValidationState{s}
	return ValidateCommittedWrites(writes, s.nextSeq, state)
}

type memoryValidationState struct{ s *InMemoryStorageState }

func (m *memoryValidationState) HasEntryOrUsageID(id string) bool {
	_, hasEntry := m.s.entries[id]
	_, hasUsage := m.s.usage[id]
	return hasEntry || hasUsage
}
func (m *memoryValidationState) HasEntryID(id string) bool {
	_, has := m.s.entries[id]
	return has
}

// ApplyValidated applies writes accepted by validateCommitted and returns
// the post-apply totals.
func (s *InMemoryStorageState) ApplyValidated(writes []CommittedWrite) SessionStats {
	for i := range writes {
		write := &writes[i]
		switch write.Kind {
		case "entry":
			entry := write.Entry
			s.entries[entry.ID] = entry
			s.entriesBySeq = append(s.entriesBySeq, entry)
			if entry.Type == EntryTypeMessage {
				s.stats.MessageCount++
			}
		case "usage":
			row := write.Row
			s.usage[row.ID] = row
			s.stats.Usage = addUsageUtil(s.stats.Usage, row.Usage)
		case "value":
			if write.Op == "delete" {
				delete(s.scalarValues, physicalKey(write.Namespace, write.Key))
			} else {
				s.applyValueSetOrListAppend(write)
			}
		case "list":
			if write.Op == "delete" {
				delete(s.listValues, physicalKey(write.Namespace, write.Key))
			} else {
				s.applyValueSetOrListAppend(write)
			}
		}
		if len(writes) > 0 {
			s.nextSeq = write.Seq + 1
		}
	}
	return s.stats
}

// CreateFork builds the destination state per the fork plan.
func (s *InMemoryStorageState) CreateFork(options ForkOptions) (*InMemoryStorageState, error) {
	plan, err := s.selectForkPlan(options)
	if err != nil {
		return nil, err
	}
	isEntryCopied := func(entryID string) bool {
		if plan.scope == "tree" {
			return true
		}
		return plan.entryIDs[entryID]
	}
	destination := NewInMemoryStorageState()
	messageCount := int64(0)
	for _, entry := range s.entriesBySeq {
		if !isEntryCopied(entry.ID) {
			continue
		}
		destination.entries[entry.ID] = entry
		destination.entriesBySeq = append(destination.entriesBySeq, entry)
		if entry.Type == EntryTypeMessage {
			messageCount++
		}
	}
	destination.stats.MessageCount = messageCount

	project := func(projected *CommittedWrite) {
		if projected != nil {
			destination.applyValueSetOrListAppend(projected)
		}
	}
	for _, stored := range s.scalarValues {
		project(ProjectForkCurrentStateWrite(CommittedWrite{
			Kind: "value", Op: "set", Seq: stored.Seq,
			Namespace: stored.Address.Namespace, Key: stored.Address.Key, Value: stored.Value,
		}, plan.view(), isEntryCopied))
	}
	for _, stored := range s.listValues {
		for _, element := range stored.elements {
			project(ProjectForkCurrentStateWrite(CommittedWrite{
				Kind: "list", Op: "append", Seq: element.Seq,
				Namespace: stored.address.Namespace, Key: stored.address.Key, Value: element.Value,
			}, plan.view(), isEntryCopied))
		}
	}
	destination.nextSeq = s.nextSeq
	return destination, nil
}

func (p memoryForkPlan) view() ForkCurrentStatePlan {
	return ForkCurrentStatePlan{Scope: p.scope, Branch: p.branch, DestinationTip: p.destinationTip}
}

func (s *InMemoryStorageState) selectForkPlan(options ForkOptions) (memoryForkPlan, error) {
	if options.Scope == "tree" {
		return memoryForkPlan{scope: "tree"}, nil
	}
	entryIDs := map[string]bool{}
	tipValue, err := s.GetValue(BranchTip(options.Branch))
	if err != nil {
		return memoryForkPlan{}, err
	}
	if tipValue == nil {
		return memoryForkPlan{}, fmt.Errorf("Unknown source branch: %s", options.Branch)
	}
	var tipID *string
	if tipValue.Value != nil {
		if tip, ok := tipValue.Value.(string); ok {
			tipID = &tip
		}
	}
	plan, err := SelectBranchFork(options, struct {
		Tip         *string
		GetParent   func(entryID string) *string
		SelectEntry func(entryID string)
		HasTip      bool
	}{
		Tip: tipID,
		GetParent: func(entryID string) *string {
			entry, ok := s.entries[entryID]
			if !ok {
				return nil
			}
			return entry.ParentID
		},
		SelectEntry: func(entryID string) { entryIDs[entryID] = true },
		HasTip:      true,
	})
	if err != nil {
		return memoryForkPlan{}, err
	}
	if _, hasConfig := s.scalarValues[physicalKey("pi.lane.config", options.Branch)]; !hasConfig {
		return memoryForkPlan{}, fmt.Errorf("Source branch %q is not a configured AgentLane", options.Branch)
	}
	if _, hasState := s.scalarValues[physicalKey("pi.lane.state", options.Branch)]; !hasState {
		return memoryForkPlan{}, fmt.Errorf("Source branch %q is not a configured AgentLane", options.Branch)
	}
	return memoryForkPlan{scope: "branch", branch: plan.Branch, destinationTip: plan.DestinationTip, entryIDs: entryIDs}, nil
}

func (s *InMemoryStorageState) applyValueSetOrListAppend(write *CommittedWrite) {
	key := physicalKey(write.Namespace, write.Key)
	if write.Kind == "value" {
		s.scalarValues[key] = &StoredValue{
			Address: NewValue(write.Namespace, write.Key),
			Value:   write.Value,
			Seq:     write.Seq,
		}
		return
	}
	element := ListElement{Seq: write.Seq, Value: write.Value}
	if stored, ok := s.listValues[key]; ok {
		stored.elements = append(stored.elements, element)
	} else {
		s.listValues[key] = &storedListSnapshot{
			address:  NewList(write.Namespace, write.Key),
			elements: []ListElement{element},
		}
	}
}

// AdvanceNextSeq raises the high-water mark.
func (s *InMemoryStorageState) AdvanceNextSeq(nextSeq int64) error {
	if nextSeq < 1 {
		return fmt.Errorf("Invalid storage sequence high-water mark: %d", nextSeq)
	}
	if nextSeq > s.nextSeq {
		s.nextSeq = nextSeq
	}
	return nil
}

// GetEntries returns found entries by id.
func (s *InMemoryStorageState) GetEntries(ids []string) map[string]*Entry {
	found := map[string]*Entry{}
	for _, id := range ids {
		if entry, ok := s.entries[id]; ok {
			found[id] = entry
		}
	}
	return found
}

// GetValue reads one scalar.
func (s *InMemoryStorageState) GetValue(address Value) (*StoredValue, error) {
	if stored, ok := s.scalarValues[physicalKey(address.Namespace, address.Key)]; ok {
		return stored, nil
	}
	return nil, nil
}

// ScanValues scans by namespace + key prefix, sorted code-point-wise.
func (s *InMemoryStorageState) ScanValues(prefix Value) []StoredValue {
	out := []StoredValue{}
	for _, stored := range s.scalarValues {
		if stored.Address.Namespace == prefix.Namespace && hasKeyPrefix(stored.Address.Key, prefix.Key) {
			out = append(out, *stored)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return compareKeys(out[i].Address.Key, out[j].Address.Key) < 0
	})
	return out
}

func hasKeyPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// ReadList reads one list with cursor/order/limit semantics.
func (s *InMemoryStorageState) ReadList(address ValueList, options *ListReadOptions) []ListElement {
	var opts ListReadOptions
	if options != nil {
		opts = *options
	}
	resolved := ResolveListReadOptions(opts)
	elements := []ListElement{}
	if stored, ok := s.listValues[physicalKey(address.Namespace, address.Key)]; ok {
		elements = stored.elements
	}
	filtered := []ListElement{}
	for _, element := range elements {
		if resolved.Cursor == nil {
			filtered = append(filtered, element)
			continue
		}
		if resolved.Order == "asc" {
			if element.Seq > resolved.Cursor.Seq {
				filtered = append(filtered, element)
			}
		} else if element.Seq < resolved.Cursor.Seq {
			filtered = append(filtered, element)
		}
	}
	if resolved.Order != "asc" {
		for i, j := 0, len(filtered)-1; i < j; i, j = i+1, j-1 {
			filtered[i], filtered[j] = filtered[j], filtered[i]
		}
	}
	if int64(len(filtered)) > resolved.Limit {
		filtered = filtered[:resolved.Limit]
	}
	return filtered
}

// ScanBranch walks the ancestry from a start entry.
func (s *InMemoryStorageState) ScanBranch(query StorageBranchScan) ([]*Entry, error) {
	start, ok := s.entries[query.StartID]
	if !ok {
		return nil, fmt.Errorf("Unknown branch start: %s", query.StartID)
	}
	path := []*Entry{}
	entry := start
	for entry != nil {
		path = append(path, entry)
		if entry.ParentID == nil {
			break
		}
		parent, ok := s.entries[*entry.ParentID]
		if !ok {
			return nil, fmt.Errorf("Corrupt branch: missing parent")
		}
		entry = parent
	}
	if query.Order == "oldestFirst" {
		for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
			path[i], path[j] = path[j], path[i]
		}
	}
	stopped := []*Entry{}
	for _, candidate := range path {
		stopped = append(stopped, candidate)
		if (query.StopAtID != "" && candidate.ID == query.StopAtID) || (query.StopAtType != "" && candidate.Type == query.StopAtType) {
			break
		}
	}
	filtered := []*Entry{}
	for _, candidate := range stopped {
		if query.Type != "" && candidate.Type != query.Type {
			continue
		}
		if query.CustomType != "" && (candidate.CustomType == nil || *candidate.CustomType != query.CustomType) {
			continue
		}
		if query.Cursor != nil {
			if query.Order == "oldestFirst" {
				if candidate.Seq <= query.Cursor.Seq {
					continue
				}
			} else if candidate.Seq >= query.Cursor.Seq {
				continue
			}
		}
		filtered = append(filtered, candidate)
	}
	if query.Limit != nil && int64(len(filtered)) > *query.Limit {
		if *query.Limit < 0 {
			filtered = nil
		} else {
			filtered = filtered[:*query.Limit]
		}
	}
	return filtered, nil
}

// ScanBranchStructure returns the structure view of ScanBranch.
func (s *InMemoryStorageState) ScanBranchStructure(query StorageBranchScan) ([]EntryStructure, error) {
	entries, err := s.ScanBranch(query)
	if err != nil {
		return nil, err
	}
	out := make([]EntryStructure, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.EntryBase)
	}
	return out, nil
}

// ScanEntries iterates entriesBySeq in both directions.
func (s *InMemoryStorageState) ScanEntries(query EntryScan) []*Entry {
	limit := int64(-1)
	if query.Limit != nil {
		limit = *query.Limit
	}
	entries := []*Entry{}
	descending := query.Order == "desc"
	index := 0
	if descending {
		index = len(s.entriesBySeq) - 1
	}
	for index >= 0 && index < len(s.entriesBySeq) && (limit < 0 || int64(len(entries)) < limit) {
		entry := s.entriesBySeq[index]
		if (query.Type == "" || entry.Type == query.Type) &&
			(query.CustomType == "" || (entry.CustomType != nil && *entry.CustomType == query.CustomType)) &&
			(query.FromSeq == nil || entry.Seq >= *query.FromSeq) &&
			(query.ToSeq == nil || entry.Seq <= *query.ToSeq) {
			entries = append(entries, entry)
		}
		if descending {
			index--
		} else {
			index++
		}
	}
	return entries
}

// ScanUsage scans usage rows by seq window and order.
func (s *InMemoryStorageState) ScanUsage(query UsageScan) []UsageRow {
	rows := []UsageRow{}
	for _, row := range s.usage {
		if query.FromSeq != nil && row.Seq < *query.FromSeq {
			continue
		}
		if query.ToSeq != nil && row.Seq > *query.ToSeq {
			continue
		}
		rows = append(rows, *row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if query.Order == "desc" {
			return rows[i].Seq > rows[j].Seq
		}
		return rows[i].Seq < rows[j].Seq
	})
	if query.Limit != nil && int64(len(rows)) > *query.Limit {
		if *query.Limit < 0 {
			rows = nil
		} else {
			rows = rows[:*query.Limit]
		}
	}
	return rows
}

// GetStats returns the session totals.
func (s *InMemoryStorageState) GetStats() SessionStats { return s.stats }

// GetNextSeq returns the next sequence.
func (s *InMemoryStorageState) GetNextSeq() int64 { return s.nextSeq }

func addUsageUtil(left, right aiUsage) aiUsage {
	return harnessAddUsage(left, right)
}

// SnapshotEntries returns the entries in sequence order (fork
// serialization helper).
func (s *InMemoryStorageState) SnapshotEntries() []*Entry {
	out := make([]*Entry, 0, len(s.entriesBySeq))
	out = append(out, s.entriesBySeq...)
	return out
}

// SnapshotScalars returns all scalar values (fork serialization helper).
func (s *InMemoryStorageState) SnapshotScalars() []StoredValue {
	out := make([]StoredValue, 0, len(s.scalarValues))
	for _, stored := range s.scalarValues {
		out = append(out, *stored)
	}
	return out
}
