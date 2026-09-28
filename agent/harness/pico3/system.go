package pico3

// system.go ports harness/pico3/system.ts sections core: stable section
// keys + pure renderers, canonical state folding from the newest baseline,
// the draft editing surface, freeze with wrappers, and preparation
// snapshot equality.

import (
	"strings"

	"github.com/gladmo/openagent/jsonx"
)

// SystemSection is a stable key plus a pure renderer.
type SystemSection struct {
	Key    string
	Render func(value any) string
}

// DefineSystemSection builds a section.
func DefineSystemSection(key string, render func(value any) string) SystemSection {
	return SystemSection{Key: key, Render: render}
}

// Built-in sections (pico §12.1).
var (
	SectionIdentity = DefineSystemSection("identity", func(v any) string {
		if s, ok := v.(string); ok {
			return s
		}
		return ""
	})
	SectionEnvironment = DefineSystemSection("environment", func(v any) string {
		if obj, ok := v.(*jsonx.Obj); ok {
			if cwd, ok := obj.Get("cwd"); ok {
				if s, ok := cwd.(string); ok {
					return "Working directory: " + s
				}
			}
		}
		return ""
	})
	SectionSkills = DefineSystemSection("skills", func(v any) string {
		arr, ok := v.([]any)
		if !ok {
			return ""
		}
		var lines []string
		for _, item := range arr {
			if obj, ok := item.(*jsonx.Obj); ok {
				name, _ := obj.Get("name")
				desc, _ := obj.Get("description")
				nameStr, _ := name.(string)
				descStr, _ := desc.(string)
				lines = append(lines, "- "+nameStr+": "+descStr)
			}
		}
		return strings.Join(lines, "\n")
	})
)

// SectionSeedAction discriminates the seed union.
type SectionSeedAction string

const (
	SectionSeedSet    SectionSeedAction = "set"
	SectionSeedRemove SectionSeedAction = "remove"
)

// SectionSeed is §8.1: set with a checked pair, or remove an inherited key.
type SectionSeed struct {
	Action SectionSeedAction
	Key    string
	Value  any
}

// SectionSeedFor builds a set seed.
func SectionSeedFor(section SystemSection, value any) SectionSeed {
	return SectionSeed{Action: SectionSeedSet, Key: section.Key, Value: value}
}

// RemoveSection builds a remove seed.
func RemoveSection(key string) SectionSeed {
	return SectionSeed{Action: SectionSeedRemove, Key: key}
}

// SectionRecord mirrors the TS union (stored, never renderers).
type SectionRecord struct {
	Action   SectionSeedAction // set | remove
	Key      string
	Value    any
	Rendered string
}

// SectionState is one canonical entry.
type SectionState struct {
	Value    any
	Rendered string
}

// Canonical is the folded section state in canonical (insertion) order.
type Canonical struct {
	Order  []string
	Values map[string]SectionState
}

// NewCanonical builds an empty canonical state.
func NewCanonical() *Canonical {
	return &Canonical{Values: map[string]SectionState{}}
}

// Set inserts or replaces preserving position for existing keys.
func (c *Canonical) Set(key string, state SectionState) {
	if _, exists := c.Values[key]; !exists {
		c.Order = append(c.Order, key)
	}
	c.Values[key] = state
}

// Delete removes a key.
func (c *Canonical) Delete(key string) {
	delete(c.Values, key)
	for i, k := range c.Order {
		if k == key {
			c.Order = append(c.Order[:i], c.Order[i+1:]...)
			break
		}
	}
}

// Get reads one section.
func (c *Canonical) Get(key string) (SectionState, bool) {
	state, ok := c.Values[key]
	return state, ok
}

// FoldCanonicalResult carries the fold outputs.
type FoldCanonicalResult struct {
	Canonical      *Canonical
	NewestManaged  *Id
	NewestBaseline *Id
}

// SectionEntrySource reads managed entries for the fold.
type SectionEntrySource interface {
	ScanEntries(scan EntryScan) ([]*Entry, error)
}

// FoldCanonical folds fork-visible managed entries from the newest
// baseline forward (§12.4): newest-first pages until a baseline, then
// reverse and apply section records in order.
func FoldCanonical(reads SectionEntrySource, conversationID Id) (*FoldCanonicalResult, error) {
	managed := []*Entry{}
	var before Id
	var hasBefore bool
	var baseline *Id
	for {
		scan := EntryScan{ConversationID: conversationID, Kind: "pi.system", Limit: 64}
		if hasBefore {
			b := before
			scan.Before = &b
		}
		page, err := reads.ScanEntries(scan)
		if err != nil {
			return nil, err
		}
		for _, entry := range page {
			managed = append(managed, entry)
			if entry.Data != nil {
				if isBaselineEntry(entry) {
					id := entry.ID
					baseline = &id
					break
				}
			}
		}
		if baseline != nil || len(page) < 64 {
			break
		}
		before = page[len(page)-1].ID
		hasBefore = true
	}
	// Reverse to oldest-first.
	for i, j := 0, len(managed)-1; i < j; i, j = i+1, j-1 {
		managed[i], managed[j] = managed[j], managed[i]
	}
	canonical := NewCanonical()
	for _, entry := range managed {
		for _, record := range sectionRecordsOf(entry) {
			if record.Action == SectionSeedRemove {
				canonical.Delete(record.Key)
			} else {
				canonical.Set(record.Key, SectionState{Value: record.Value, Rendered: record.Rendered})
			}
		}
	}
	result := &FoldCanonicalResult{Canonical: canonical}
	if len(managed) > 0 {
		newest := managed[len(managed)-1].ID
		result.NewestManaged = &newest
	}
	result.NewestBaseline = baseline
	return result, nil
}

func isBaselineEntry(entry *Entry) bool {
	if entry.Data == nil {
		return false
	}
	if v, ok := entry.Data.Get("baseline"); ok {
		return v == true
	}
	return false
}

func sectionRecordsOf(entry *Entry) []SectionRecord {
	if entry.Data == nil {
		return nil
	}
	recordsValue, ok := entry.Data.Get("sections")
	if !ok {
		return nil
	}
	arr, ok := recordsValue.([]any)
	if !ok {
		return nil
	}
	var out []SectionRecord
	for _, item := range arr {
		obj, ok := item.(*jsonx.Obj)
		if !ok {
			continue
		}
		record := SectionRecord{Action: SectionSeedSet}
		if key, ok := obj.Get("key"); ok {
			if s, ok := key.(string); ok {
				record.Key = s
			}
		}
		if action, ok := obj.Get("action"); ok {
			if s, ok := action.(string); ok {
				if s == "remove" {
					record.Action = SectionSeedRemove
				}
			}
		}
		if value, ok := obj.Get("value"); ok {
			record.Value = value
		}
		if rendered, ok := obj.Get("rendered"); ok {
			if s, ok := rendered.(string); ok {
				record.Rendered = s
			}
		}
		out = append(out, record)
	}
	return out
}

// Draft is what systemInstructions handlers edit (§12.2).
type Draft struct {
	values   map[string]any
	order    []string
	wrappers map[string][]func(string) string
	touched  map[string]bool
}

// NewDraft seeds a draft from the canonical state (owned copy).
func NewDraft(seed *Canonical) *Draft {
	d := &Draft{
		values:   map[string]any{},
		wrappers: map[string][]func(string) string{},
		touched:  map[string]bool{},
	}
	if seed != nil {
		for _, key := range seed.Order {
			state := seed.Values[key]
			d.values[key] = state.Value
			d.order = append(d.order, key)
		}
	}
	return d
}

// Get returns an owned copy of one section value.
func (d *Draft) Get(section SystemSection) any {
	value, ok := d.values[section.Key]
	if !ok {
		return nil
	}
	return cloneJSONValue(value)
}

// Set stores a value: existing key keeps position; new key appends.
func (d *Draft) Set(section SystemSection, value any) {
	if _, exists := d.values[section.Key]; !exists {
		d.order = append(d.order, section.Key)
	}
	d.values[section.Key] = value
	d.touched[section.Key] = true
}

// Delete removes a value and its wrappers.
func (d *Draft) Delete(key string) {
	delete(d.values, key)
	delete(d.wrappers, key)
	d.touched[key] = true
	for i, k := range d.order {
		if k == key {
			d.order = append(d.order[:i], d.order[i+1:]...)
			break
		}
	}
}

// Wrap registers a preparation-local transform.
func (d *Draft) Wrap(section SystemSection, transform func(rendered string) string) {
	d.wrappers[section.Key] = append(d.wrappers[section.Key], transform)
	d.touched[section.Key] = true
}

// DraftSnapshot captures the draft for restore.
type DraftSnapshot struct {
	values   map[string]any
	order    []string
	wrappers map[string][]func(string) string
	touched  map[string]bool
}

// Snapshot captures the draft state.
func (d *Draft) Snapshot() *DraftSnapshot {
	snap := &DraftSnapshot{
		values:   map[string]any{},
		order:    append([]string{}, d.order...),
		wrappers: map[string][]func(string) string{},
		touched:  map[string]bool{},
	}
	for k, v := range d.values {
		snap.values[k] = v
	}
	for k, v := range d.wrappers {
		snap.wrappers[k] = append([]func(string) string{}, v...)
	}
	for k := range d.touched {
		snap.touched[k] = true
	}
	return snap
}

// Restore rolls the draft back to a snapshot.
func (d *Draft) Restore(snap *DraftSnapshot) {
	d.values = snap.values
	d.order = snap.order
	d.wrappers = snap.wrappers
	d.touched = snap.touched
}

// Freeze renders touched sections (wrappers apply after rendering, in
// registration order); untouched keep their stored text. Unregistered
// touched keys cannot re-render and are treated as removed.
func Freeze(draft *Draft, canonical *Canonical, registry map[string]SystemSection) *Canonical {
	desired := NewCanonical()
	for _, key := range draft.order {
		value, exists := draft.values[key]
		if !exists {
			continue
		}
		if !draft.touched[key] {
			if prev, ok := canonical.Get(key); ok {
				desired.Set(key, prev)
			}
			continue
		}
		def, registered := registry[key]
		if !registered {
			continue
		}
		rendered := def.Render(value)
		for _, wrapper := range draft.wrappers[key] {
			rendered = wrapper(rendered)
		}
		desired.Set(key, SectionState{Value: value, Rendered: rendered})
	}
	return desired
}

func cloneJSONValue(v any) any {
	if obj, ok := v.(*jsonx.Obj); ok {
		return cloneJSONObj(obj)
	}
	return v
}

// PreparationSnapshot mirrors the TS interface (compared field by field
// before the prepared commit).
type PreparationSnapshot struct {
	NewestManaged  *Id
	NewestBaseline *Id
	NewestHead     *Id
	Model          any
	HasModel       bool
	ThinkingLevel  any
	SelectedTools  []string
	Profile        any
	SectionsRev    int64
	ToolsRev       int64
}

// SameSnapshot compares two snapshots by value.
func SameSnapshot(a, b *PreparationSnapshot) bool {
	return jsonx.Stringify(snapshotToJSON(a)) == jsonx.Stringify(snapshotToJSON(b))
}

func snapshotToJSON(s *PreparationSnapshot) *jsonx.Obj {
	obj := jsonx.NewObj()
	obj.Set("newestManaged", idToJSON(s.NewestManaged))
	obj.Set("newestBaseline", idToJSON(s.NewestBaseline))
	obj.Set("newestHead", idToJSON(s.NewestHead))
	if s.HasModel {
		obj.Set("model", s.Model)
	}
	obj.Set("thinkingLevel", s.ThinkingLevel)
	tools := make([]any, 0, len(s.SelectedTools))
	for _, tool := range s.SelectedTools {
		tools = append(tools, tool)
	}
	obj.Set("selectedTools", tools)
	obj.Set("profile", s.Profile)
	obj.Set("sectionsRev", float64(s.SectionsRev))
	obj.Set("toolsRev", float64(s.ToolsRev))
	return obj
}

func idToJSON(id *Id) any {
	if id == nil {
		return nil
	}
	return float64(*id)
}
