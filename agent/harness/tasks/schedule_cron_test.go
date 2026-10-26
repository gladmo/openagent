package tasks

// schedule_cron_test.go pins cron semantics to the robfig/cron v3.0.0
// reference behavior via golden Next() vectors. All times use a fixed
// zone so results do not depend on the host time zone.

import (
	"testing"
	"time"
)

var zone = time.FixedZone("+08", 8*60*60)

func at(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation(time.RFC3339, value, zone)
	if err != nil {
		t.Fatalf("bad test time %q: %v", value, err)
	}
	return parsed
}

func TestCronNextGolden(t *testing.T) {
	cases := []struct {
		name string
		expr string
		from string
		want string
	}{
		{"daily at 2:30", "30 2 * * *", "2026-10-25T09:00:00+08:00", "2026-10-26T02:30:00+08:00"},
		{"strictly after same match", "30 2 * * *", "2026-10-26T02:30:00+08:00", "2026-10-27T02:30:00+08:00"},
		{"step from half-minute", "*/5 * * * *", "2026-10-25T09:00:30+08:00", "2026-10-25T09:05:00+08:00"},
		{"step rounds up in-minute", "*/5 * * * *", "2026-10-25T09:03:00+08:00", "2026-10-25T09:05:00+08:00"},
		{"yearly rolls to next year", "0 0 1 1 *", "2026-10-25T09:00:00+08:00", "2027-01-01T00:00:00+08:00"},
		{"weekday name", "0 12 * * MON", "2026-10-25T09:00:00+08:00", "2026-10-26T12:00:00+08:00"},
		{"dom-dow both restricted ORs to nearer weekday", "0 0 1 * 1", "2026-10-25T09:00:00+08:00", "2026-10-26T00:00:00+08:00"},
		{"dom-dow both restricted ORs to nearer monthday", "0 0 13 * FRI", "2026-10-25T09:00:00+08:00", "2026-10-30T00:00:00+08:00"},
		{"feb 29 skips non-leap years", "0 0 29 2 *", "2026-10-25T09:00:00+08:00", "2028-02-29T00:00:00+08:00"},
		{"range with step", "10-30/5 * * * *", "2026-10-25T09:00:00+08:00", "2026-10-25T09:10:00+08:00"},
		{"range with step mid-range", "10-30/5 * * * *", "2026-10-25T09:12:00+08:00", "2026-10-25T09:15:00+08:00"},
		{"start-anchored step", "5/15 * * * *", "2026-10-25T09:00:00+08:00", "2026-10-25T09:05:00+08:00"},
		{"start-anchored step wraps hour", "5/15 * * * *", "2026-10-25T09:50:00+08:00", "2026-10-25T10:05:00+08:00"},
		{"month name", "0 0 1 APR *", "2026-10-25T09:00:00+08:00", "2027-04-01T00:00:00+08:00"},
		{"last day of month same month", "0 0 31 * *", "2026-10-25T09:00:00+08:00", "2026-10-31T00:00:00+08:00"},
		{"31st skips 30-day month", "0 0 31 * *", "2026-11-01T09:00:00+08:00", "2026-12-31T00:00:00+08:00"},
		{"midnight wildcard dom-dow", "0 0 * * *", "2026-10-25T09:00:00+08:00", "2026-10-26T00:00:00+08:00"},
		{"question mark matches star in dow", "0 0 * * ?", "2026-10-25T09:00:00+08:00", "2026-10-26T00:00:00+08:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schedule, err := ParseScheduleIn(tc.expr, zone)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.expr, err)
			}
			got, ok := schedule.Next(at(t, tc.from))
			if !ok {
				t.Fatalf("Next(%q from %s) = !ok, want %s", tc.expr, tc.from, tc.want)
			}
			if want := at(t, tc.want); !got.Equal(want) {
				t.Fatalf("Next(%q from %s) = %s, want %s", tc.expr, tc.from, got, want)
			}
		})
	}
}

func TestCronNextFeb29AcrossFourYearWindow(t *testing.T) {
	schedule, err := ParseScheduleIn("0 0 29 2 *", zone)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, ok := schedule.Next(at(t, "2028-03-01T00:00:00+08:00"))
	if !ok {
		t.Fatal("want a match within the 4-year window")
	}
	if want := at(t, "2032-02-29T00:00:00+08:00"); !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestCronNextReturnsAfterLocation(t *testing.T) {
	schedule, err := ParseScheduleIn("0 9 * * *", time.UTC)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	after := at(t, "2026-10-25T09:30:00+08:00") // 01:30 UTC
	got, ok := schedule.Next(after)
	if !ok {
		t.Fatal("want a match")
	}
	want := at(t, "2026-10-25T17:00:00+08:00") // 09:00 UTC same day
	if !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
	if got.Location() != after.Location() {
		t.Fatalf("result location %v, want after's location %v", got.Location(), after.Location())
	}
}

func TestParseScheduleRejectsInvalid(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"60 * * * *",
		"*/5 24 * * *",
		"0 0 0 * *",
		"0 0 32 * *",
		"0 0 * 13 *",
		"0 0 * * 7",
		"* * * *",
		"* * * * * *",
		"? * * * *",
		"*/0 * * * *",
		"5-1 * * * *",
		"0 0 1 FOO *",
		"0 0 1 1 MONX",
		"0 0 L 2 *",
		"0 0 1 2 1#3",
		"0 0 1 1 * extra",
		"@weekly extra",
		"@fortnightly",
		"@every",
		"@every 0s",
		"@every -5m",
		"@every notaduration",
		"0 0 1 1 *-",
	}
	for _, expr := range cases {
		if _, err := ParseScheduleIn(expr, zone); err == nil {
			t.Fatalf("ParseScheduleIn(%q) = nil error, want loud failure", expr)
		}
	}
}

func TestParseScheduleAcceptsComposedForms(t *testing.T) {
	valid := []string{
		"*/5 * * * *",
		"0 0-12/3 * * *",
		"1,15,30 * * * *",
		"0 4 1 JAN-MAR *",
		"0 4 * * MON-FRI",
		"0 0 * * ?",
	}
	for _, expr := range valid {
		if _, err := ParseScheduleIn(expr, zone); err != nil {
			t.Fatalf("ParseScheduleIn(%q) = %v, want accepted", expr, err)
		}
	}
}

func TestEveryNextIsAnchoredAndDriftFree(t *testing.T) {
	anchor := at(t, "2026-10-25T09:00:00+08:00")
	every := NewEvery(10, anchor)
	cases := []struct {
		from string
		want string
	}{
		{"2026-10-25T09:00:00+08:00", "2026-10-25T09:00:10+08:00"},
		{"2026-10-25T09:00:09+08:00", "2026-10-25T09:00:10+08:00"},
		{"2026-10-25T09:00:10+08:00", "2026-10-25T09:00:20+08:00"},
		{"2026-10-25T09:00:29+08:00", "2026-10-25T09:00:30+08:00"},
		{"2026-10-25T09:00:31+08:00", "2026-10-25T09:00:40+08:00"},
		{"2026-10-25T08:59:55+08:00", "2026-10-25T09:00:10+08:00"},
	}
	for _, tc := range cases {
		got, ok := every.Next(at(t, tc.from))
		if !ok {
			t.Fatalf("Next(%s) = !ok", tc.from)
		}
		if want := at(t, tc.want); !got.Equal(want) {
			t.Fatalf("Next(%s) = %s, want %s", tc.from, got, want)
		}
	}
}

func TestEveryChainedNextStaysOnGrid(t *testing.T) {
	anchor := at(t, "2026-10-25T09:00:00+08:00")
	every := NewEvery(7, anchor)
	next, _ := every.Next(anchor)
	for i := 1; i <= 50; i++ {
		want := anchor.Add(time.Duration(i+1) * 7 * time.Second)
		next, _ = every.Next(next)
		if !next.Equal(want) {
			t.Fatalf("chained step %d: got %s, want %s", i, next, want)
		}
	}
}

func TestAtNext(t *testing.T) {
	fire := at(t, "2026-10-26T09:00:00+08:00")
	schedule := NewAt(fire)
	if got, ok := schedule.Next(at(t, "2026-10-25T09:00:00+08:00")); !ok || !got.Equal(fire) {
		t.Fatalf("future: got %s ok=%v, want %s", got, ok, fire)
	}
	if got, ok := schedule.Next(fire); ok {
		t.Fatalf("expired: got %s, want !ok", got)
	}
	if got, ok := schedule.Next(at(t, "2026-10-27T09:00:00+08:00")); ok {
		t.Fatalf("past: got %s, want !ok", got)
	}
}

func TestParseEveryDescriptor(t *testing.T) {
	schedule, err := ParseScheduleIn("@every 90s", zone)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	every, ok := schedule.(Every)
	if !ok {
		t.Fatalf("got %T, want Every", schedule)
	}
	if every.Interval != 90*time.Second {
		t.Fatalf("interval %s, want 1m30s", every.Interval)
	}
	first, has := every.Next(every.Anchor)
	if !has || !first.Equal(every.Anchor.Add(90*time.Second)) {
		t.Fatalf("first fire %s, want anchor+90s", first)
	}
}
