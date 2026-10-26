package tasks

// tool_test.go: task_manage action coverage — each action's happy path
// and its loud failure modes.

import (
	"strings"
	"testing"
	"time"

	"github.com/gladmo/openagent/jsonx"
)

func manageCall(t *testing.T, scheduler *Scheduler, fields map[string]any) (string, error) {
	t.Helper()
	params := jsonx.NewObj()
	for key, value := range fields {
		params.Set(key, value)
	}
	return executeTaskManage(scheduler, params)
}

func manageTool(t *testing.T) *Scheduler {
	t.Helper()
	scheduler, _ := newTestScheduler(t, NewMemoryStore(), &fakeExecutor{}, nil)
	mustStart(t, scheduler)
	t.Cleanup(func() { stopScheduler(t, scheduler) })
	return scheduler
}

func newTaskObj() *jsonx.Obj {
	task := jsonx.NewObj()
	task.Set("id", "report")
	task.Set("name", "Daily report")
	task.Set("schedule", "@daily")
	task.Set("prompt", "Write the daily report")
	return task
}

func TestTaskManageCreateAndGetAndList(t *testing.T) {
	scheduler := manageTool(t)
	outcome, err := manageCall(t, scheduler, map[string]any{"action": "create", "task": newTaskObj()})
	if err != nil {
		t.Fatalf("create: %v (outcome %q)", err, outcome)
	}
	if !strings.Contains(outcome, "daily report") && !strings.Contains(outcome, "Daily report") {
		// status JSON uses the Name field; accept the schedule string as
		// proof the task was created and projected.
		if !strings.Contains(outcome, "cron:0 0 * * *") {
			t.Fatalf("create output missing task identity: %s", outcome)
		}
	}

	detail, err := manageCall(t, scheduler, map[string]any{"action": "get", "id": "report"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(detail, `"id": "report"`) {
		t.Fatalf("get output: %s", detail)
	}

	listing, err := manageCall(t, scheduler, map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listing, "report") {
		t.Fatalf("list output: %s", listing)
	}
}

func TestTaskManageCreateValidates(t *testing.T) {
	scheduler := manageTool(t)
	badSchedule := newTaskObj()
	badSchedule.Set("schedule", "not a cron")
	if _, err := manageCall(t, scheduler, map[string]any{"action": "create", "task": badSchedule}); err == nil {
		t.Fatal("invalid schedule accepted")
	}
	noPrompt := newTaskObj()
	noPrompt.Set("prompt", "")
	if _, err := manageCall(t, scheduler, map[string]any{"action": "create", "task": noPrompt}); err == nil {
		t.Fatal("missing prompt accepted")
	}
	if _, err := manageCall(t, scheduler, map[string]any{"action": "create"}); err == nil {
		t.Fatal("missing task object accepted")
	}
}

func TestTaskManageUpdatePauseResumeRunDelete(t *testing.T) {
	scheduler := manageTool(t)
	if _, err := manageCall(t, scheduler, map[string]any{"action": "create", "task": newTaskObj()}); err != nil {
		t.Fatalf("create: %v", err)
	}

	updated := newTaskObj()
	updated.Set("schedule", "0 9 * * 1")
	updated.Set("maxRuns", float64(5))
	if _, err := manageCall(t, scheduler, map[string]any{"action": "update", "task": updated}); err != nil {
		t.Fatalf("update: %v", err)
	}
	status, _ := scheduler.Get("report")
	if status.Schedule != "cron:0 9 * * 1" || status.RunCount != 0 {
		t.Fatalf("update not applied: %+v", status)
	}

	if _, err := manageCall(t, scheduler, map[string]any{"action": "pause", "id": "report"}); err != nil {
		t.Fatalf("pause: %v", err)
	}
	status, _ = scheduler.Get("report")
	if status.Enabled {
		t.Fatal("pause did not disable")
	}
	if _, err := manageCall(t, scheduler, map[string]any{"action": "resume", "id": "report"}); err != nil {
		t.Fatalf("resume: %v", err)
	}

	outcome, err := manageCall(t, scheduler, map[string]any{"action": "run", "id": "report"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(outcome, "status=success") {
		t.Fatalf("run outcome: %s", outcome)
	}

	if _, err := manageCall(t, scheduler, map[string]any{"action": "delete", "id": "report"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := scheduler.Get("report"); err == nil {
		t.Fatal("deleted task still present")
	}
}

func TestTaskManageEveryDescriptorAndDefaults(t *testing.T) {
	scheduler := manageTool(t)
	task := newTaskObj()
	task.Set("schedule", "@every 15m")
	task.Set("sessionStrategy", "fixed")
	task.Set("fixedSessionId", "sess-9")
	task.Set("timeoutSeconds", float64(30))
	if _, err := manageCall(t, scheduler, map[string]any{"action": "create", "task": task}); err != nil {
		t.Fatalf("create: %v", err)
	}
	status, err := scheduler.Get("report")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if status.SessionStrategy != StrategyFixed || status.FixedSessionID != "sess-9" {
		t.Fatalf("fixed strategy lost: %+v", status)
	}
	scheduler.mu.Lock()
	spec := scheduler.entries["report"].record.Spec
	scheduler.mu.Unlock()
	if spec.Timeout != 30*time.Second {
		t.Fatalf("timeout %s, want 30s", spec.Timeout)
	}
	if _, ok := spec.Schedule.(Every); !ok {
		t.Fatalf("schedule type %T, want Every", spec.Schedule)
	}
}

func TestTaskManageUnknownTaskAndActionFail(t *testing.T) {
	scheduler := manageTool(t)
	if _, err := manageCall(t, scheduler, map[string]any{"action": "pause", "id": "ghost"}); err == nil {
		t.Fatal("unknown task accepted")
	}
	if _, err := manageCall(t, scheduler, map[string]any{"action": "transmogrify"}); err == nil {
		t.Fatal("unknown action accepted")
	}
}

func TestTaskManageToolWiring(t *testing.T) {
	scheduler := manageTool(t)
	tool := TaskManageTool(scheduler)
	if tool.Name != "task_manage" || tool.Execute == nil || tool.Parameters == nil {
		t.Fatalf("tool wiring incomplete: %+v", tool)
	}
	params := jsonx.NewObj()
	params.Set("action", "create")
	params.Set("task", newTaskObj())
	result, err := tool.Execute("call-1", any(params), nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result == nil || len(result.Content) == 0 {
		t.Fatalf("result: %+v", result)
	}
}
