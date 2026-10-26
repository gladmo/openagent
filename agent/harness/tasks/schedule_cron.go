package tasks

// schedule_cron.go: the cron Schedule adapter. The accepted grammar is
// the 5-field subset of robfig/cron v3.0.0 (minute hour dom month dow;
// names, lists, ranges, steps, `?` in dom/dow, @descriptors) with
// robfig's Next() semantics: minute granularity, both-dom-and-dow-
// restricted matches OR, and a 4-year search window. No third-party
// dependency; golden vectors in schedule_cron_test.go pin the behavior.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const starBit = 1 << 63

type bounds struct {
	min, max uint
	names    map[string]uint
}

var (
	minuteBounds = bounds{min: 0, max: 59}
	hourBounds   = bounds{min: 0, max: 23}
	domBounds    = bounds{min: 1, max: 31}
	monthBounds  = bounds{min: 1, max: 12, names: map[string]uint{
		"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
		"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
	}}
	dowBounds = bounds{min: 0, max: 6, names: map[string]uint{
		"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6,
	}}
)

// CronSchedule is a parsed 5-field cron expression. Masks carry one bit
// per allowed value; starBit marks a wildcard (`*` or `?`) field.
type CronSchedule struct {
	minute, hour, dom, month, dow uint64
	location                      *time.Location
	expr                          string
}

func (c *CronSchedule) String() string { return "cron:" + c.expr }

// Next returns the first matching minute strictly after `after`,
// expressed in `after`'s location. ok=false when no match occurs within
// four years (e.g. Feb 29 from a year whose following leap years are all
// past that window).
func (c *CronSchedule) Next(after time.Time) (time.Time, bool) {
	origLocation := after.Location()
	t := after.In(c.location).Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(4, 0, 0)
	for t.Before(limit) {
		if c.month&(1<<uint(t.Month())) == 0 {
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, c.location).AddDate(0, 1, 0)
			continue
		}
		if !c.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, c.location).AddDate(0, 0, 1)
			continue
		}
		if c.hour&(1<<uint(t.Hour())) == 0 {
			next := nextSetBit(c.hour, uint(t.Hour())+1, hourBounds.max)
			if next < 0 {
				t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, c.location).AddDate(0, 0, 1)
			} else {
				t = time.Date(t.Year(), t.Month(), t.Day(), int(next), 0, 0, 0, c.location)
			}
			continue
		}
		if c.minute&(1<<uint(t.Minute())) == 0 {
			next := nextSetBit(c.minute, uint(t.Minute())+1, minuteBounds.max)
			if next < 0 {
				t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, c.location).Add(time.Hour)
			} else {
				t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), int(next), 0, 0, c.location)
			}
			continue
		}
		return t.In(origLocation), true
	}
	return time.Time{}, false
}

// dayMatches implements the cron dom/dow rule (robfig semantics): when
// both fields are restricted the day matches on EITHER; when either is a
// wildcard both must match (the wildcard side always does).
func (c *CronSchedule) dayMatches(t time.Time) bool {
	domMatch := c.dom&(1<<uint(t.Day())) > 0
	dowMatch := c.dow&(1<<uint(t.Weekday())) > 0
	if c.dom&starBit > 0 || c.dow&starBit > 0 {
		return domMatch && dowMatch
	}
	return domMatch || dowMatch
}

func nextSetBit(mask uint64, from, max uint) int {
	for v := from; v <= max; v++ {
		if mask&(1<<v) > 0 {
			return int(v)
		}
	}
	return -1
}

var descriptorExpressions = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

func parseSchedule(expr string, location *time.Location) (Schedule, error) {
	trimmed := strings.TrimSpace(expr)
	if trimmed == "" {
		return nil, fmt.Errorf("tasks: empty schedule expression")
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "@") {
		fields := strings.Fields(lower)
		if len(fields) == 2 && fields[0] == "@every" {
			duration, err := time.ParseDuration(fields[1])
			if err != nil {
				return nil, fmt.Errorf("tasks: @every duration %q: %w", fields[1], err)
			}
			if duration <= 0 {
				return nil, fmt.Errorf("tasks: @every duration must be positive, got %s", duration)
			}
			return Every{Interval: duration, Anchor: time.Now().In(location)}, nil
		}
		if expanded, ok := descriptorExpressions[lower]; ok {
			return parseCronFields(expanded, location, trimmed)
		}
		return nil, fmt.Errorf("tasks: unrecognized schedule descriptor %q", trimmed)
	}
	return parseCronFields(trimmed, location, trimmed)
}

func parseCronFields(expr string, location *time.Location, display string) (Schedule, error) {
	fields := strings.Fields(expr)
	if len(fields) == 6 {
		return nil, fmt.Errorf("tasks: cron expression %q: 6-field (seconds) form is not supported; use the 5-field minute form", display)
	}
	if len(fields) != 5 {
		return nil, fmt.Errorf("tasks: cron expression %q: want 5 fields (minute hour day-of-month month day-of-week), got %d", display, len(fields))
	}
	minute, err := getField(fields[0], minuteBounds, false)
	if err != nil {
		return nil, fieldError("minute", fields[0], err)
	}
	hour, err := getField(fields[1], hourBounds, false)
	if err != nil {
		return nil, fieldError("hour", fields[1], err)
	}
	dom, err := getField(fields[2], domBounds, true)
	if err != nil {
		return nil, fieldError("day-of-month", fields[2], err)
	}
	month, err := getField(fields[3], monthBounds, false)
	if err != nil {
		return nil, fieldError("month", fields[3], err)
	}
	dow, err := getField(fields[4], dowBounds, true)
	if err != nil {
		return nil, fieldError("day-of-week", fields[4], err)
	}
	return &CronSchedule{
		minute:   minute,
		hour:     hour,
		dom:      dom,
		month:    month,
		dow:      dow,
		location: location,
		expr:     display,
	}, nil
}

func fieldError(name, expr string, err error) error {
	return fmt.Errorf("tasks: cron %s field %q: %w", name, expr, err)
}

func getField(expr string, b bounds, allowQuestion bool) (uint64, error) {
	if expr == "" {
		return 0, fmt.Errorf("empty field")
	}
	var mask uint64
	for i, part := range strings.Split(expr, ",") {
		bits, err := getRange(part, b, allowQuestion)
		if err != nil {
			return 0, err
		}
		if i == 0 {
			mask = bits
		} else {
			mask |= bits
		}
	}
	return mask, nil
}

func getRange(expr string, b bounds, allowQuestion bool) (uint64, error) {
	rangeAndStep := strings.Split(expr, "/")
	if len(rangeAndStep) > 2 {
		return 0, fmt.Errorf("more than one '/' in %q", expr)
	}
	step := uint(1)
	if len(rangeAndStep) == 2 {
		parsed, err := strconv.ParseUint(rangeAndStep[1], 10, 32)
		if err != nil {
			return 0, fmt.Errorf("step %q: %w", rangeAndStep[1], err)
		}
		step = uint(parsed)
		if step == 0 {
			return 0, fmt.Errorf("step of zero in %q", expr)
		}
	}
	lowAndHigh := strings.Split(rangeAndStep[0], "-")
	if len(lowAndHigh) > 2 {
		return 0, fmt.Errorf("more than one '-' in %q", expr)
	}
	var low, high uint
	extra := uint64(0)
	if lowAndHigh[0] == "*" || lowAndHigh[0] == "?" {
		if lowAndHigh[0] == "?" && !allowQuestion {
			return 0, fmt.Errorf("'?' is only allowed in day-of-month and day-of-week")
		}
		if len(lowAndHigh) != 1 {
			return 0, fmt.Errorf("'-' with '*' or '?' in %q", expr)
		}
		low, high, extra = b.min, b.max, starBit
	} else {
		var err error
		low, err = parseValue(lowAndHigh[0], b)
		if err != nil {
			return 0, err
		}
		switch len(lowAndHigh) {
		case 1:
			high = low
			if len(rangeAndStep) == 2 {
				high = b.max // "N/M" means N through max by M
			}
		case 2:
			high, err = parseValue(lowAndHigh[1], b)
			if err != nil {
				return 0, err
			}
		}
	}
	if low < b.min {
		return 0, fmt.Errorf("value %d below minimum %d in %q", low, b.min, expr)
	}
	if high > b.max {
		return 0, fmt.Errorf("value %d above maximum %d in %q", high, b.max, expr)
	}
	if low > high {
		return 0, fmt.Errorf("descending range %d-%d in %q", low, high, expr)
	}
	return getBits(low, high, step) | extra, nil
}

func parseValue(token string, b bounds) (uint, error) {
	if token == "" {
		return 0, fmt.Errorf("missing value")
	}
	if parsed, err := strconv.ParseUint(token, 10, 32); err == nil {
		return uint(parsed), nil
	}
	if b.names != nil {
		if value, ok := b.names[strings.ToUpper(token)]; ok {
			return value, nil
		}
	}
	if token == "L" || token == "W" || strings.Contains(token, "#") {
		return 0, fmt.Errorf("Quartz-only syntax %q is not supported", token)
	}
	return 0, fmt.Errorf("unrecognized value %q", token)
}

func getBits(low, high, step uint) uint64 {
	var mask uint64
	for v := low; v <= high; v += step {
		mask |= 1 << v
	}
	return mask
}
