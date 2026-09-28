package kinds

// Ports of kinds/entries.ts + kinds/task-api.ts behaviors.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent/harness/pico3"
)

func TestDefineEntryReserved(t *testing.T) {
	if _, err := DefineEntry("pi.custom"); err == nil {
		t.Fatal("reserved prefix accepted")
	} else if !strings.Contains(err.Error(), "are reserved") {
		t.Fatalf("err = %v", err)
	}
	if _, err := DefineEntry("app.note"); err != nil {
		t.Fatal(err)
	}
}

func TestCoreEntryWitnesses(t *testing.T) {
	user := &pico3.Entry{Kind: KindUser}
	if !Entries.User.Is(user) {
		t.Fatal("user witness failed")
	}
	if Entries.Assistant.Is(user) {
		t.Fatal("assistant witness matched user")
	}
	if Entries.User.Is(nil) {
		t.Fatal("nil matched")
	}
	// Each core witness carries its pi.* name.
	pairs := map[string]pico3.EntryKindWitness{
		"pi.user": Entries.User, "pi.assistant": Entries.Assistant,
		"pi.tool_result": Entries.ToolResult, "pi.system": Entries.System,
		"pi.notice": Entries.Notice, "pi.usage": Entries.Usage,
		"pi.summary": Entries.Summary, "pi.handoff": Entries.Handoff,
		"pi.reset": Entries.Reset,
	}
	for name, witness := range pairs {
		if witness.Name() != name {
			t.Fatalf("witness %q named %q", name, witness.Name())
		}
	}
}

// fakeRuntime records task-API calls.
type fakeRuntime struct {
	created       []pico3.KindToken
	conversations []pico3.ConversationSpec
	sent          []pico3.InputPayload
	aborted       []int64
	tasks         map[int64]*pico3.Task
	inputs        map[int64]*pico3.Input
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{
		tasks:  map[int64]*pico3.Task{},
		inputs: map[int64]*pico3.Input{},
	}
}

func (f *fakeRuntime) CreateOwnedConversation(spec pico3.ConversationSpec, _ pico3.APIContext) (int64, error) {
	f.conversations = append(f.conversations, spec)
	return int64(len(f.conversations)), nil
}
func (f *fakeRuntime) SendOwned(_ int64, input pico3.InputPayload, _ pico3.APIContext) (int64, error) {
	f.sent = append(f.sent, input)
	return int64(100 + len(f.sent)), nil
}
func (f *fakeRuntime) AbortConversation(id int64, _ pico3.APIContext) error {
	f.aborted = append(f.aborted, id)
	return nil
}
func (f *fakeRuntime) CreateTask(kind pico3.KindToken, input any, opts pico3.CreateTaskOptions, _ pico3.APIContext) (*pico3.TaskRef, error) {
	f.created = append(f.created, kind)
	id := int64(200 + len(f.created))
	f.tasks[id] = &pico3.Task{ID: id, Kind: kind.Name, Status: "pending"}
	return &pico3.TaskRef{ID: id}, nil
}
func (f *fakeRuntime) GetTask(id int64, _ pico3.APIContext) (*pico3.Task, error) {
	return f.tasks[id], nil
}
func (f *fakeRuntime) WaitForTask(id int64, _ pico3.APIContext) (*pico3.Task, error) {
	task := f.tasks[id]
	if task != nil {
		terminal := "terminal"
		task.Status = terminal
	}
	return task, nil
}
func (f *fakeRuntime) WaitForInput(id int64, _ pico3.APIContext) (*pico3.Input, error) {
	return f.inputs[id], nil
}
func (f *fakeRuntime) GetInput(id int64, _ pico3.APIContext) (*pico3.Input, error) {
	return f.inputs[id], nil
}

func newTaskAPI(rt pico3.RuntimeAPI) *TaskApiSurface {
	return &TaskApiSurface{TaskID: 7, ConversationID: 1, Runtime: rt}
}

func TestTaskApiToolOnlyMembers(t *testing.T) {
	api := newTaskAPI(newFakeRuntime())
	if err := api.Stream(); err == nil || !strings.Contains(err.Error(), "only available to tools") {
		t.Fatalf("stream err = %v", err)
	}
	if err := api.Progress(); err == nil || !strings.Contains(err.Error(), "only available to tools") {
		t.Fatalf("progress err = %v", err)
	}
	if err := api.Memo(); err == nil || !strings.Contains(err.Error(), "only available to tools") {
		t.Fatalf("memo err = %v", err)
	}
}

func TestTaskApiConversationLifecycle(t *testing.T) {
	rt := newFakeRuntime()
	api := newTaskAPI(rt)
	ctx := pico3.APIContext(nil)

	conversationID, err := api.Conversation(pico3.ConversationSpec{}, ctx)
	if err != nil || conversationID != 1 {
		t.Fatalf("conversation = %d err = %v", conversationID, err)
	}
	handle, err := api.SendInput(conversationID, pico3.InputPayload{RequestID: "r1"}, ctx)
	if err != nil || handle.ID != 101 {
		t.Fatalf("handle = %+v err = %v", handle, err)
	}
	// The runtime tracks the sent input for wait/result.
	rt.inputs[101] = &pico3.Input{ID: 101, Status: "done"}
	if input, err := handle.Wait(ctx); err != nil || input.Status != "done" {
		t.Fatalf("wait = %+v err = %v", input, err)
	}
	if input, err := handle.Result(ctx); err != nil || input.ID != 101 {
		t.Fatalf("result = %+v err = %v", input, err)
	}
	if err := api.AbortConversation(conversationID, ctx); err != nil {
		t.Fatal(err)
	}
	if len(rt.aborted) != 1 || rt.aborted[0] != conversationID {
		t.Fatalf("aborted = %v", rt.aborted)
	}
}

func TestTaskApiTaskCreationRequiresToken(t *testing.T) {
	rt := newFakeRuntime()
	api := newTaskAPI(rt)
	ctx := pico3.APIContext(nil)
	// Empty kind token rejects.
	if _, err := api.CreateTask(pico3.KindToken{}, "in", pico3.CreateTaskOptions{}, ctx); err == nil || !strings.Contains(err.Error(), "kind token") {
		t.Fatalf("err = %v", err)
	}
	ref, err := api.CreateTask(pico3.KindToken{Name: "app.job"}, "in", pico3.CreateTaskOptions{Background: true}, ctx)
	if err != nil || ref.ID != 201 {
		t.Fatalf("ref = %+v err = %v", ref, err)
	}
	if rt.created[0].Name != "app.job" {
		t.Fatalf("created = %v", rt.created)
	}
	// Wait moves the task to terminal.
	task, err := api.WaitForTask(*ref, ctx)
	if err != nil || task.Status != "terminal" {
		t.Fatalf("task = %+v err = %v", task, err)
	}
}

func TestTaskApiWithoutRuntime(t *testing.T) {
	api := newTaskAPI(nil)
	ctx := pico3.APIContext(nil)
	if _, err := api.Conversation(pico3.ConversationSpec{}, ctx); err == nil {
		t.Fatal("conversation without runtime")
	}
	if _, err := api.CreateTask(pico3.KindToken{Name: "k"}, nil, pico3.CreateTaskOptions{}, ctx); err == nil {
		t.Fatal("task without runtime")
	}
}
