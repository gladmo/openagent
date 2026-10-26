package tasks

// schedule.go: the Schedule seam (when a task fires next) and the every/at
// adapters. The cron adapter lives in schedule_cron.go.

import (
	"fmt"
	"time"
)

// Schedule reports the next fire time strictly after `after`. ok=false
// means the schedule can never fire again.
type Schedule interface {
	Next(after time.Time) (time.Time, bool)
	String() string
}

// Every fires at anchor+k*interval for k=1,2,... The anchor pins the
// series so successive fires do not drift; the anchor time itself never
// fires.
type Every struct {
	Interval time.Duration
	Anchor   time.Time
}

// NewEvery builds an Every schedule. interval must be positive; the
// MinEvery floor is enforced by the Scheduler at registration, not here.
func NewEvery(seconds int, anchor time.Time) Every {
	return Every{Interval: time.Duration(seconds) * time.Second, Anchor: anchor}
}

// Next returns the first anchor+k*Interval strictly after `after`.
func (e Every) Next(after time.Time) (time.Time, bool) {
	k := after.Sub(e.Anchor)/e.Interval + 1
	if k < 1 {
		k = 1
	}
	return e.Anchor.Add(time.Duration(k) * e.Interval), true
}

func (e Every) String() string {
	return "every:" + e.Interval.String()
}

// At fires exactly once at its time, then never again.
type At struct {
	Time time.Time
}

// NewAt builds a one-shot schedule.
func NewAt(t time.Time) At {
	return At{Time: t}
}

// Next returns the fire time while it is still in the future.
func (a At) Next(after time.Time) (time.Time, bool) {
	if after.Before(a.Time) {
		return a.Time, true
	}
	return time.Time{}, false
}

func (a At) String() string {
	return "at:" + a.Time.Format(time.RFC3339)
}

// ParseSchedule parses one schedule expression in the local time zone:
// a 5-field cron expression or a descriptor (@yearly, @annually,
// @monthly, @weekly, @daily, @midnight, @hourly, @every <duration>).
// Misformed input fails loud.
func ParseSchedule(expr string) (Schedule, error) {
	return ParseScheduleIn(expr, time.Local)
}

// ParseScheduleIn is ParseSchedule with an explicit cron time zone.
// Descriptors other than @every carry no zone of their own and use the
// same location.
func ParseScheduleIn(expr string, location *time.Location) (Schedule, error) {
	if location == nil {
		return nil, fmt.Errorf("tasks: schedule location is nil")
	}
	return parseSchedule(expr, location)
}
