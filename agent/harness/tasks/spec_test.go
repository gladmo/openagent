package tasks

// spec_test.go: TaskSpec validation, defaults, and the schedule JSON
// codec round-trips.

import (
	"encoding/json"
	"testing"
	"time"
)

func specForTest() TaskSpec {
	at, _ := ParseScheduleIn("0 9 * * 1", zone)
	return TaskSpec{
		ID:              "weekly",
		Name:            "Weekly report",
		Schedule:        at,
		SessionStrategy: StrategyFixed,
		FixedSessionID:  "sess-1",
		Prompt:          "Generate the weekly report",
		Timeout:         90 * time.Second,
		Enabled:         true,
		MaxRuns:         3,
		Metadata:        map[string]string{"owner": "ops"},
	}
}

func TestTaskSpecValidateRejects(t *testing.T) {
	cron, _ := ParseScheduleIn("0 9 * * 1", zone)
	cases := []TaskSpec{
		{ID: "", Schedule: cron, Prompt: "p"},
		{ID: "x"},                 // no schedule
		{ID: "x", Schedule: cron}, // no prompt
		{ID: "x", Schedule: cron, Prompt: "p", SessionStrategy: "sometimes"},
		{ID: "x", Schedule: cron, Prompt: "p", SessionStrategy: StrategyFixed},
		{ID: "x", Schedule: cron, Prompt: "p", Timeout: -time.Second},
		{ID: "x", Schedule: cron, Prompt: "p", MaxRuns: -1},
	}
	for i, spec := range cases {
		if err := spec.Validate(); err == nil {
			t.Fatalf("case %d: Validate = nil, want error", i)
		}
	}
}

func TestTaskSpecValidateAcceptsMinimal(t *testing.T) {
	cron, _ := ParseScheduleIn("0 9 * * 1", zone)
	spec := TaskSpec{ID: "x", Schedule: cron, Prompt: "p"}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestTaskSpecJSONRoundTrip(t *testing.T) {
	spec := specForTest()
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded TaskSpec
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ID != spec.ID || decoded.Name != spec.Name || decoded.Prompt != spec.Prompt {
		t.Fatalf("identity fields lost: %+v", decoded)
	}
	if decoded.SessionStrategy != StrategyFixed || decoded.FixedSessionID != "sess-1" {
		t.Fatalf("session strategy lost: %+v", decoded)
	}
	if decoded.Timeout != spec.Timeout {
		t.Fatalf("timeout %s, want %s", decoded.Timeout, spec.Timeout)
	}
	if decoded.MaxRuns != spec.MaxRuns || !decoded.Enabled {
		t.Fatalf("flags lost: %+v", decoded)
	}
	if decoded.Metadata["owner"] != "ops" {
		t.Fatalf("metadata lost")
	}
	cron, ok := decoded.Schedule.(*CronSchedule)
	if !ok {
		t.Fatalf("schedule type %T, want *CronSchedule", decoded.Schedule)
	}
	after := time.Date(2026, 10, 25, 10, 0, 0, 0, zone)
	wantNext, _ := spec.Schedule.Next(after)
	gotNext, _ := cron.Next(after)
	if !wantNext.Equal(gotNext) {
		t.Fatalf("decoded schedule diverged: %s vs %s", gotNext, wantNext)
	}
}

func TestTaskSpecJSONRoundTripEveryAndAt(t *testing.T) {
	anchor := time.Date(2026, 10, 25, 9, 0, 0, 0, zone)
	spec := TaskSpec{ID: "e", Schedule: NewEvery(30, anchor), Prompt: "p"}
	decoded := roundTripSpec(t, spec)
	every, ok := decoded.Schedule.(Every)
	if !ok || every.Interval != 30*time.Second || !every.Anchor.Equal(anchor) {
		t.Fatalf("every round trip: %#v", decoded.Schedule)
	}

	at := time.Date(2026, 10, 26, 9, 0, 0, 0, zone)
	spec = TaskSpec{ID: "a", Schedule: NewAt(at), Prompt: "p"}
	decoded = roundTripSpec(t, spec)
	gotAt, ok := decoded.Schedule.(At)
	if !ok || !gotAt.Time.Equal(at) {
		t.Fatalf("at round trip: %#v", decoded.Schedule)
	}
}

func roundTripSpec(t *testing.T, spec TaskSpec) TaskSpec {
	t.Helper()
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded TaskSpec
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return decoded
}

func TestTaskSpecJSONRejectsUnknownScheduleType(t *testing.T) {
	payload := `{"id":"x","prompt":"p","schedule":{"type":"quarterly"}}`
	var spec TaskSpec
	if err := json.Unmarshal([]byte(payload), &spec); err == nil {
		t.Fatal("unknown schedule type decoded without error")
	}
}

func TestTaskSpecJSONDefaultsStrategyToNew(t *testing.T) {
	payload := `{"id":"x","prompt":"p","schedule":{"type":"cron","expr":"0 9 * * 1"}}`
	var spec TaskSpec
	if err := json.Unmarshal([]byte(payload), &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if spec.normalizedSessionStrategy() != StrategyNew {
		t.Fatalf("strategy %q, want new", spec.SessionStrategy)
	}
}

func TestScheduleLocationRoundTripAndFailure(t *testing.T) {
	utcCron, err := ParseScheduleIn("0 9 * * *", time.UTC)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	decoded := roundTripSpec(t, TaskSpec{ID: "tz", Schedule: utcCron, Prompt: "p"})
	cron := decoded.Schedule.(*CronSchedule)
	if cron.location != time.UTC {
		t.Fatalf("location %v, want UTC", cron.location)
	}

	bad := `{"id":"x","prompt":"p","schedule":{"type":"cron","expr":"0 9 * * *","location":"Nowhere/Nope"}}`
	var spec TaskSpec
	if err := json.Unmarshal([]byte(bad), &spec); err == nil {
		t.Fatal("unknown location decoded without error")
	}
}
