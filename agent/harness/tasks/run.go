package tasks

// run.go: run records and the scheduler-facing task status view.

import "time"

// RunStatus values. running is persisted transiently so a restart can
// mark orphaned runs interrupted; skipped and interrupted are terminal.
type RunStatus string

const (
	RunStatusRunning     RunStatus = "running"
	RunStatusSuccess     RunStatus = "success"
	RunStatusFailed      RunStatus = "failed"
	RunStatusSkipped     RunStatus = "skipped"
	RunStatusInterrupted RunStatus = "interrupted"
)

// Terminal reports whether no further transition can occur.
func (r RunStatus) Terminal() bool {
	switch r {
	case RunStatusSuccess, RunStatusFailed, RunStatusSkipped, RunStatusInterrupted:
		return true
	default:
		return false
	}
}

// TaskRun is one fire's execution record. FireTime is the scheduled
// (or manual request) time; StartedAt/FinishedAt bracket the actual
// agent run.
type TaskRun struct {
	RunID      string    `json:"runId"`
	TaskID     string    `json:"taskId"`
	Trigger    string    `json:"trigger"`
	Status     RunStatus `json:"status"`
	FireTime   time.Time `json:"fireTime"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
	Err        string    `json:"err,omitempty"`
	SkipReason string    `json:"skipReason,omitempty"`
	Tokens     int64     `json:"tokens,omitempty"`
	SessionID  string    `json:"sessionId,omitempty"`
}

// TaskStatus is the List/Get projection of one task.
type TaskStatus struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Schedule        string     `json:"schedule"`
	SessionStrategy string     `json:"sessionStrategy"`
	FixedSessionID  string     `json:"fixedSessionId,omitempty"`
	Enabled         bool       `json:"enabled"`
	Completed       bool       `json:"completed"`
	Running         bool       `json:"running"`
	NextFire        *time.Time `json:"nextFire,omitempty"`
	RunCount        int        `json:"runCount"`
	LastRun         *TaskRun   `json:"lastRun,omitempty"`
}
