package session

// commit.go ports harness/session/commit.ts.

import (
	"fmt"
)

// CommittedWrite is the committed union; Kind/Op discriminate and Seq is
// assigned for every write.
type CommittedWrite struct {
	Kind      string    `json:"kind"` // entry | usage | value | list
	Op        string    `json:"op,omitempty"`
	Seq       int64     `json:"seq"`
	Timestamp float64   `json:"timestamp,omitempty"` // entries only
	Entry     *Entry    `json:"-"`                   // entry payload (already carries seq/timestamp)
	Row       *UsageRow `json:"-"`                   // usage payload (already carries seq)
	Namespace string    `json:"namespace,omitempty"`
	Key       string    `json:"key,omitempty"`
	Value     JsonValue `json:"value,omitempty"`
}

// ID returns the entry-or-usage id for validation.
func (w *CommittedWrite) ID() string {
	if w.Kind == "entry" && w.Entry != nil {
		return w.Entry.ID
	}
	if w.Kind == "usage" && w.Row != nil {
		return w.Row.ID
	}
	return ""
}

// PreparedCommit mirrors the TS interface.
type PreparedCommit struct {
	Writes    []CommittedWrite
	FirstSeq  int64
	Seqs      []int64
	Timestamp float64
}

// CommitValidationState mirrors the TS interface.
type CommitValidationState interface {
	HasEntryOrUsageID(id string) bool
	HasEntryID(id string) bool
}

// InsertEntry mirrors insertEntry().
func InsertEntry(entry *Entry) Write {
	return Write{Kind: "entry", Entry: entry}
}

// InsertUsage mirrors insertUsage().
func InsertUsage(row UsageRow) Write {
	return Write{Kind: "usage", Row: &row}
}

// CommitWrite mirrors commitWrite(): assigns one seq per write and a shared
// timestamp (entries only carry the timestamp).
func CommitWrite(write Write, seq int64, timestamp float64) CommittedWrite {
	switch write.Kind {
	case "entry":
		entry := *write.Entry
		entry.Seq = seq
		entry.Timestamp = timestamp
		return CommittedWrite{Kind: "entry", Seq: seq, Timestamp: timestamp, Entry: &entry}
	case "usage":
		row := *write.Row
		row.Seq = seq
		return CommittedWrite{Kind: "usage", Seq: seq, Row: &row}
	case "value":
		if write.Op == "set" {
			return CommittedWrite{Kind: "value", Op: "set", Seq: seq, Namespace: write.Namespace, Key: write.Key, Value: write.Value}
		}
		return CommittedWrite{Kind: "value", Op: "delete", Seq: seq, Namespace: write.Namespace, Key: write.Key}
	case "list":
		if write.Op == "append" {
			return CommittedWrite{Kind: "list", Op: "append", Seq: seq, Namespace: write.Namespace, Key: write.Key, Value: write.Value}
		}
		return CommittedWrite{Kind: "list", Op: "delete", Seq: seq, Namespace: write.Namespace, Key: write.Key}
	default:
		return CommittedWrite{Kind: write.Kind, Seq: seq}
	}
}

// PrepareStorageCommit mirrors prepareStorageCommit().
func PrepareStorageCommit(writes []Write, firstSeq int64, timestamp float64) PreparedCommit {
	committed := make([]CommittedWrite, 0, len(writes))
	seqs := make([]int64, 0, len(writes))
	for index, write := range writes {
		c := CommitWrite(write, firstSeq+int64(index), timestamp)
		committed = append(committed, c)
		seqs = append(seqs, c.Seq)
	}
	return PreparedCommit{Writes: committed, FirstSeq: firstSeq, Seqs: seqs, Timestamp: timestamp}
}

// ValidateCommittedWrites mirrors validateCommittedWrites: strictly
// increasing seqs, entries+usage share one id namespace, parents must exist
// in prior state or earlier in the transaction.
func ValidateCommittedWrites(writes []CommittedWrite, firstSeq int64, state CommitValidationState) error {
	previousSeq := firstSeq - 1
	transactionIDs := map[string]bool{}
	transactionEntryIDs := map[string]bool{}
	for i := range writes {
		write := &writes[i]
		if write.Seq <= previousSeq {
			return fmt.Errorf("Non-monotonic storage sequence: %d", write.Seq)
		}
		previousSeq = write.Seq
		if write.Kind != "entry" && write.Kind != "usage" {
			continue
		}
		id := write.ID()
		if state.HasEntryOrUsageID(id) || transactionIDs[id] {
			return fmt.Errorf("Duplicate entry or usage id: %s", id)
		}
		if write.Kind == "entry" && write.Entry.ParentID != nil {
			parentID := *write.Entry.ParentID
			if !state.HasEntryID(parentID) && !transactionEntryIDs[parentID] {
				return fmt.Errorf("Missing parent entry: %s", parentID)
			}
		}
		transactionIDs[id] = true
		if write.Kind == "entry" {
			transactionEntryIDs[id] = true
		}
	}
	return nil
}
