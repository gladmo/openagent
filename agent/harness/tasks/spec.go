package tasks

// spec.go: the persisted task definition, its validation, and the
// discriminant-tagged JSON codec for schedules (closed union: cron |
// every | at).

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SessionStrategy values.
const (
	StrategyNew   = "new"
	StrategyFixed = "fixed"
)

// Trigger values carried on Fire and TaskRun.
const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
)

// Schedule type discriminants.
const (
	scheduleTypeCron  = "cron"
	scheduleTypeEvery = "every"
	scheduleTypeAt    = "at"
)

// TaskSpec is one automated-task definition. Register validates and
// normalizes it (Name defaults to ID, SessionStrategy defaults to new);
// zero values elsewhere mean "unset".
type TaskSpec struct {
	ID              string
	Name            string
	Schedule        Schedule
	SessionStrategy string
	FixedSessionID  string
	Prompt          string
	Timeout         time.Duration
	Enabled         bool
	MaxRuns         int
	Metadata        map[string]string
}

// Validate enforces the loud-failure contract for task definitions.
func (s TaskSpec) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("tasks: task ID is required")
	}
	if s.Schedule == nil {
		return fmt.Errorf("tasks: task %q: schedule is required", s.ID)
	}
	if s.Prompt == "" {
		return fmt.Errorf("tasks: task %q: prompt is required", s.ID)
	}
	strategy := s.SessionStrategy
	if strategy == "" {
		strategy = StrategyNew
	}
	switch strategy {
	case StrategyNew:
	case StrategyFixed:
		if s.FixedSessionID == "" {
			return fmt.Errorf("tasks: task %q: fixedSessionId is required with sessionStrategy %q", s.ID, StrategyFixed)
		}
	default:
		return fmt.Errorf("tasks: task %q: unknown sessionStrategy %q (want %q or %q)", s.ID, s.SessionStrategy, StrategyNew, StrategyFixed)
	}
	if s.Timeout < 0 {
		return fmt.Errorf("tasks: task %q: negative timeout %s", s.ID, s.Timeout)
	}
	if s.MaxRuns < 0 {
		return fmt.Errorf("tasks: task %q: negative maxRuns %d", s.ID, s.MaxRuns)
	}
	if every, ok := s.Schedule.(Every); ok && every.Interval <= 0 {
		return fmt.Errorf("tasks: task %q: every interval must be positive", s.ID)
	}
	return nil
}

// normalizedSessionStrategy resolves the default in one place.
func (s TaskSpec) normalizedSessionStrategy() string {
	if s.SessionStrategy == "" {
		return StrategyNew
	}
	return s.SessionStrategy
}

type scheduleData struct {
	Type     string  `json:"type"`
	Expr     string  `json:"expr,omitempty"`
	Location string  `json:"location,omitempty"`
	Seconds  float64 `json:"seconds,omitempty"`
	Anchor   string  `json:"anchor,omitempty"`
	At       string  `json:"at,omitempty"`
}

func encodeSchedule(schedule Schedule) (scheduleData, error) {
	switch s := schedule.(type) {
	case *CronSchedule:
		return scheduleData{Type: scheduleTypeCron, Expr: s.expr, Location: scheduleLocationName(s.location)}, nil
	case Every:
		return scheduleData{
			Type:    scheduleTypeEvery,
			Seconds: s.Interval.Seconds(),
			Anchor:  s.Anchor.Format(time.RFC3339Nano),
		}, nil
	case At:
		return scheduleData{Type: scheduleTypeAt, At: s.Time.Format(time.RFC3339Nano)}, nil
	default:
		return scheduleData{}, fmt.Errorf("tasks: schedule type %T is not persistable", schedule)
	}
}

func scheduleLocationName(location *time.Location) string {
	if location == nil {
		return time.Local.String()
	}
	return location.String()
}

func decodeSchedule(data scheduleData) (Schedule, error) {
	switch data.Type {
	case scheduleTypeCron:
		if data.Expr == "" {
			return nil, fmt.Errorf("tasks: cron schedule missing expr")
		}
		location, err := resolveScheduleLocation(data.Location)
		if err != nil {
			return nil, err
		}
		return ParseScheduleIn(data.Expr, location)
	case scheduleTypeEvery:
		if data.Seconds <= 0 {
			return nil, fmt.Errorf("tasks: every schedule seconds must be positive, got %v", data.Seconds)
		}
		if data.Anchor == "" {
			return nil, fmt.Errorf("tasks: every schedule missing anchor")
		}
		anchor, err := time.Parse(time.RFC3339Nano, data.Anchor)
		if err != nil {
			return nil, fmt.Errorf("tasks: every schedule anchor %q: %w", data.Anchor, err)
		}
		return Every{Interval: time.Duration(data.Seconds * float64(time.Second)), Anchor: anchor}, nil
	case scheduleTypeAt:
		if data.At == "" {
			return nil, fmt.Errorf("tasks: at schedule missing at")
		}
		at, err := time.Parse(time.RFC3339Nano, data.At)
		if err != nil {
			return nil, fmt.Errorf("tasks: at schedule time %q: %w", data.At, err)
		}
		return At{Time: at}, nil
	default:
		return nil, fmt.Errorf("tasks: unknown schedule type %q (want cron, every, or at)", data.Type)
	}
}

func resolveScheduleLocation(name string) (*time.Location, error) {
	switch name {
	case "", "Local":
		return time.Local, nil
	case "UTC":
		return time.UTC, nil
	default:
		location, err := time.LoadLocation(name)
		if err != nil {
			// Fixed zones (time.FixedZone) keep their offset-shaped
			// name, e.g. "+08" or "-05:30"; reconstruct them.
			if offset, ok := parseFixedZoneOffset(name); ok {
				return time.FixedZone(name, offset), nil
			}
			return nil, fmt.Errorf("tasks: schedule location %q: %w", name, err)
		}
		return location, nil
	}
}

// parseFixedZoneOffset accepts ±HH, ±HHMM, and ±HH:MM zone names.
func parseFixedZoneOffset(name string) (int, bool) {
	if len(name) < 3 {
		return 0, false
	}
	sign := 0
	switch name[0] {
	case '+':
		sign = 1
	case '-':
		sign = -1
	default:
		return 0, false
	}
	digits := name[1:]
	digits = strings.ReplaceAll(digits, ":", "")
	if len(digits) != 2 && len(digits) != 4 {
		return 0, false
	}
	value := 0
	for _, ch := range digits {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		value = value*10 + int(ch-'0')
	}
	offset := 0
	if len(digits) == 2 {
		offset = value * 3600
	} else {
		offset = value/100*3600 + value%100*60
	}
	return sign * offset, true
}

type taskSpecData struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Schedule        scheduleData      `json:"schedule"`
	SessionStrategy string            `json:"sessionStrategy,omitempty"`
	FixedSessionID  string            `json:"fixedSessionId,omitempty"`
	Prompt          string            `json:"prompt"`
	TimeoutSeconds  float64           `json:"timeoutSeconds,omitempty"`
	Enabled         bool              `json:"enabled"`
	MaxRuns         int               `json:"maxRuns,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// MarshalJSON encodes the spec with its schedule discriminant.
func (s TaskSpec) MarshalJSON() ([]byte, error) {
	schedule, err := encodeSchedule(s.Schedule)
	if err != nil {
		return nil, err
	}
	data := taskSpecData{
		ID:              s.ID,
		Name:            s.Name,
		Schedule:        schedule,
		SessionStrategy: s.normalizedSessionStrategy(),
		FixedSessionID:  s.FixedSessionID,
		Prompt:          s.Prompt,
		TimeoutSeconds:  s.Timeout.Seconds(),
		Enabled:         s.Enabled,
		MaxRuns:         s.MaxRuns,
		Metadata:        s.Metadata,
	}
	return json.Marshal(data)
}

// UnmarshalJSON decodes a persisted spec; unknown schedule types fail.
func (s *TaskSpec) UnmarshalJSON(bytes []byte) error {
	var data taskSpecData
	if err := json.Unmarshal(bytes, &data); err != nil {
		return err
	}
	schedule, err := decodeSchedule(data.Schedule)
	if err != nil {
		return err
	}
	s.ID = data.ID
	s.Name = data.Name
	s.Schedule = schedule
	s.SessionStrategy = data.SessionStrategy
	s.FixedSessionID = data.FixedSessionID
	s.Prompt = data.Prompt
	s.Timeout = time.Duration(data.TimeoutSeconds * float64(time.Second))
	s.Enabled = data.Enabled
	s.MaxRuns = data.MaxRuns
	s.Metadata = data.Metadata
	return nil
}
