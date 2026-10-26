package tasks

// tool.go: task_manage, the optional agent-facing tool over a
// Scheduler (FR-4). It is a thin adapter: actions map one-to-one to
// Scheduler methods, and misformed requests fail loud as tool errors.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/typebox"
)

// TaskManageActions is the closed action set.
var TaskManageActions = []any{"create", "list", "get", "update", "pause", "resume", "run", "delete"}

// TaskManageTool builds the task_manage AgentTool bound to scheduler.
// The task argument carries a task definition whose schedule is one
// schedule expression string (5-field cron or descriptor).
func TaskManageTool(scheduler *Scheduler) *agent.AgentTool {
	taskSchema := typebox.Object([]*typebox.Property{
		typebox.Prop("id", typebox.String(typebox.Description("Unique task id"))),
		typebox.Prop("name", typebox.Optional(typebox.String(typebox.Description("Human-readable name (defaults to id)")))),
		typebox.Prop("schedule", typebox.String(typebox.Description(`Schedule expression: 5-field cron ("0 9 * * 1") or descriptor ("@daily", "@every 10m")`))),
		typebox.Prop("prompt", typebox.String(typebox.Description("Prompt injected into the agent session on each fire"))),
		typebox.Prop("sessionStrategy", typebox.Optional(typebox.String(typebox.Description(`"new" (fresh session each fire, default) or "fixed" (reuse one session)`)))),
		typebox.Prop("fixedSessionId", typebox.Optional(typebox.String(typebox.Description("Session id to bind when sessionStrategy is fixed")))),
		typebox.Prop("timeoutSeconds", typebox.Optional(typebox.Number(typebox.Description("Per-run timeout in seconds")))),
		typebox.Prop("maxRuns", typebox.Optional(typebox.Number(typebox.Description("Stop after this many runs (0 = unlimited)")))),
		typebox.Prop("enabled", typebox.Optional(typebox.Boolean(typebox.Description("Start active (default true)")))),
	})
	schema := typebox.Object([]*typebox.Property{
		typebox.Prop("action", typebox.Enum(TaskManageActions, typebox.Description("Management action"))),
		typebox.Prop("id", typebox.Optional(typebox.String(typebox.Description("Task id for single-task actions")))),
		typebox.Prop("task", typebox.Optional(taskSchema)),
	})
	return &agent.AgentTool{
		Name:        "task_manage",
		Label:       "task manage",
		Description: "Create and manage automated tasks that fire the agent on a schedule (cron, fixed interval, or one-shot time). Actions: create/list/get/update/pause/resume/run/delete.",
		Parameters:  schema,
		Execute: func(_ string, params any, _ *abort.Signal, _ agent.AgentToolUpdateCallback) (*agent.AgentToolResult, error) {
			result, err := executeTaskManage(scheduler, params.(*jsonx.Obj))
			if err != nil {
				return nil, err
			}
			return &agent.AgentToolResult{
				Content:           []ai.ContentBlock{ai.TextContent{Text: result}},
				StructuredContent: structuredSummary(scheduler),
			}, nil
		},
	}
}

func executeTaskManage(scheduler *Scheduler, params *jsonx.Obj) (string, error) {
	action, err := requireString(params, "action")
	if err != nil {
		return "", err
	}
	switch action {
	case "create", "update":
		spec, err := taskSpecFromParams(params)
		if err != nil {
			return "", err
		}
		if action == "create" {
			if err := scheduler.Register(spec); err != nil {
				return "", err
			}
		} else {
			if err := scheduler.Update(spec); err != nil {
				return "", err
			}
		}
		return describeStatus(scheduler, spec.ID)
	case "list":
		statuses := scheduler.List()
		if len(statuses) == 0 {
			return "no automated tasks registered", nil
		}
		lines := make([]string, 0, len(statuses))
		for _, status := range statuses {
			lines = append(lines, fmt.Sprintf("- %s [%s] schedule=%s enabled=%v completed=%v next=%s last=%s",
				status.ID, status.Name, status.Schedule, status.Enabled, status.Completed, formatTime(status.NextFire), formatRun(status.LastRun)))
		}
		return strings.Join(lines, "\n"), nil
	case "get":
		id, err := requireString(params, "id")
		if err != nil {
			return "", err
		}
		return describeStatus(scheduler, id)
	case "pause", "resume", "delete":
		id, err := requireString(params, "id")
		if err != nil {
			return "", err
		}
		switch action {
		case "pause":
			err = scheduler.Pause(id)
		case "resume":
			err = scheduler.Resume(id)
		case "delete":
			err = scheduler.Unregister(id)
		}
		if err != nil {
			return "", err
		}
		if action == "delete" {
			return fmt.Sprintf("task %s deleted", id), nil
		}
		return describeStatus(scheduler, id)
	case "run":
		id, err := requireString(params, "id")
		if err != nil {
			return "", err
		}
		run, err := scheduler.RunNow(id)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("run %s: status=%s trigger=%s tokens=%d err=%q skipReason=%q",
			run.RunID, run.Status, run.Trigger, run.Tokens, run.Err, run.SkipReason), nil
	default:
		return "", fmt.Errorf("task_manage: unknown action %q", action)
	}
}

// taskSpecFromParams decodes the task argument into a TaskSpec,
// resolving the schedule expression and defaults in this one place.
func taskSpecFromParams(params *jsonx.Obj) (TaskSpec, error) {
	raw, ok := params.Get("task")
	if !ok {
		return TaskSpec{}, fmt.Errorf("task_manage: task object is required for this action")
	}
	obj, ok := raw.(*jsonx.Obj)
	if !ok {
		return TaskSpec{}, fmt.Errorf("task_manage: task must be an object")
	}
	spec := TaskSpec{Enabled: true}
	if value, ok := obj.Get("id"); ok {
		if id, ok := value.(string); ok {
			spec.ID = id
		}
	}
	if value, ok := obj.Get("name"); ok {
		if name, ok := value.(string); ok {
			spec.Name = name
		}
	}
	if value, ok := obj.Get("sessionStrategy"); ok {
		if strategy, ok := value.(string); ok {
			spec.SessionStrategy = strategy
		}
	}
	if value, ok := obj.Get("fixedSessionId"); ok {
		if sessionID, ok := value.(string); ok {
			spec.FixedSessionID = sessionID
		}
	}
	if value, ok := obj.Get("prompt"); ok {
		if prompt, ok := value.(string); ok {
			spec.Prompt = prompt
		}
	}
	if value, ok := obj.Get("enabled"); ok {
		if enabled, ok := value.(bool); ok {
			spec.Enabled = enabled
		}
	}
	if value, ok := obj.Get("timeoutSeconds"); ok {
		if seconds, ok := value.(float64); ok {
			spec.Timeout = time.Duration(seconds * float64(time.Second))
		}
	}
	if value, ok := obj.Get("maxRuns"); ok {
		if runs, ok := value.(float64); ok {
			spec.MaxRuns = int(runs)
		}
	}
	expression := ""
	if value, ok := obj.Get("schedule"); ok {
		if expr, ok := value.(string); ok {
			expression = expr
		}
	}
	if expression == "" {
		return TaskSpec{}, fmt.Errorf("task_manage: task.schedule is required")
	}
	schedule, err := ParseSchedule(expression)
	if err != nil {
		return TaskSpec{}, err
	}
	spec.Schedule = schedule
	return spec, nil
}

func requireString(params *jsonx.Obj, key string) (string, error) {
	value, ok := params.Get(key)
	if !ok {
		return "", fmt.Errorf("task_manage: %s is required", key)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("task_manage: %s must be a string", key)
	}
	return text, nil
}

func describeStatus(scheduler *Scheduler, id string) (string, error) {
	status, err := scheduler.Get(id)
	if err != nil {
		return "", err
	}
	encoded, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func formatTime(value *time.Time) string {
	if value == nil {
		return "-"
	}
	return value.Format(time.RFC3339)
}

func formatRun(run *TaskRun) string {
	if run == nil {
		return "-"
	}
	return fmt.Sprintf("%s@%s", run.Status, formatTime(&run.FireTime))
}

func structuredSummary(scheduler *Scheduler) *jsonx.Obj {
	summary := jsonx.NewObj()
	taskCount := len(scheduler.List())
	summary.Set("taskCount", taskCount)
	return summary
}
