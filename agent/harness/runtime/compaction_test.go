package runtime

// Ports of compaction threshold decision behaviors.

import (
	"strings"
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
)

func messageEntryOf(id string) *session.Entry {
	return &session.Entry{
		EntryBase: session.EntryBase{ID: id, Type: session.EntryTypeMessage},
		Message:   session.AgentMessagePayload{Role: "user"},
	}
}

func compactionEntryOf(id string) *session.Entry {
	return &session.Entry{EntryBase: session.EntryBase{ID: id, Type: session.EntryTypeCompaction}, Summary: "s"}
}

func TestShouldCompact(t *testing.T) {
	settings := CompactionSettings{Enabled: true, ReserveTokens: 1000}
	// Below threshold: window - reserve = 9000.
	if ShouldCompact(9000, 10000, settings) {
		t.Fatal("at threshold compacted")
	}
	// Above.
	if !ShouldCompact(9001, 10000, settings) {
		t.Fatal("above threshold not compacted")
	}
	// Disabled never compacts.
	if ShouldCompact(999999, 10000, CompactionSettings{Enabled: false, ReserveTokens: 1000}) {
		t.Fatal("disabled compacted")
	}
}

func TestTriggerCompactionDisabledOrNoModel(t *testing.T) {
	settings := CompactionSettings{Enabled: false}
	result, err := TriggerCompaction(settings, true, 10000, []*session.Entry{messageEntryOf("e1")}, "e1", 99999)
	if err != nil || result.Compact {
		t.Fatalf("result = %+v err = %v", result, err)
	}
	// Model unavailable.
	result, err = TriggerCompaction(CompactionSettings{Enabled: true, ReserveTokens: 1000}, false, 10000, []*session.Entry{messageEntryOf("e1")}, "e1", 99999)
	if err != nil || result.Compact {
		t.Fatalf("result = %+v err = %v", result, err)
	}
}

func TestTriggerCompactionCompactionAtOrAfterTrigger(t *testing.T) {
	settings := CompactionSettings{Enabled: true, ReserveTokens: 1000}
	entries := []*session.Entry{
		messageEntryOf("e1"),
		compactionEntryOf("c1"), // compaction after the trigger e1
	}
	result, err := TriggerCompaction(settings, true, 10000, entries, "e1", 99999)
	if err != nil || result.Compact {
		t.Fatalf("result = %+v err = %v", result, err)
	}
	// Compaction BEFORE the trigger is eligible again.
	entries = []*session.Entry{
		compactionEntryOf("c0"),
		messageEntryOf("e1"),
	}
	result, err = TriggerCompaction(settings, true, 10000, entries, "e1", 99999)
	if err != nil || !result.Compact {
		t.Fatalf("result = %+v err = %v", result, err)
	}
}

func TestTriggerCompactionMissingTriggerInvariant(t *testing.T) {
	settings := CompactionSettings{Enabled: true, ReserveTokens: 1000}
	_, err := TriggerCompaction(settings, true, 10000, []*session.Entry{messageEntryOf("e1")}, "ghost", 5000)
	if err == nil || !strings.Contains(err.Error(), "missing from its Branch") {
		t.Fatalf("err = %v", err)
	}
}

func TestTriggerCompactionThresholdDecision(t *testing.T) {
	settings := CompactionSettings{Enabled: true, ReserveTokens: 2000}
	entries := []*session.Entry{messageEntryOf("e1")}
	// Below threshold: 10000 - 2000 = 8000.
	result, err := TriggerCompaction(settings, true, 10000, entries, "e1", 8000)
	if err != nil || result.Compact {
		t.Fatalf("result = %+v err = %v", result, err)
	}
	// Above.
	result, err = TriggerCompaction(settings, true, 10000, entries, "e1", 8001)
	if err != nil || !result.Compact || result.TokensBefore != 8001 {
		t.Fatalf("result = %+v err = %v", result, err)
	}
}
