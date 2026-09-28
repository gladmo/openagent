package kinds

// Ports of kinds/plugin.ts behaviors.

import (
	"errors"
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func TestParsePluginInput(t *testing.T) {
	input := jsonx.ObjFrom("handler", "ui.theme", "input", jsonx.ObjFrom("dark", true))
	fields := ParsePluginInput(input)
	if fields.Handler != "ui.theme" {
		t.Fatalf("handler = %s", fields.Handler)
	}
	if obj, ok := fields.Input.(*jsonx.Obj); !ok || obj.MustGet("dark") != true {
		t.Fatalf("input = %v", fields.Input)
	}
}

func TestRunPluginMissingHandler(t *testing.T) {
	completion, err := RunPlugin(
		PluginInputFields{Handler: "ghost"},
		map[string]PluginHandlerFn{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	failure := completion.MustGet("failure").(*jsonx.Obj)
	if completion.MustGet("status") != "failed" || failure.MustGet("reason") != "missing_handler" {
		t.Fatalf("completion = %v", completion)
	}
	if failure.MustGet("detail") != "ghost" {
		t.Fatalf("detail = %v", failure.MustGet("detail"))
	}
}

func TestRunPluginSuccess(t *testing.T) {
	handlers := map[string]PluginHandlerFn{
		"double": func(input any) (any, error) {
			return jsonx.ObjFrom("value", float64(2)), nil
		},
	}
	completion, err := RunPlugin(PluginInputFields{Handler: "double"}, handlers, nil)
	if err != nil {
		t.Fatal(err)
	}
	if completion.MustGet("status") != "completed" {
		t.Fatalf("completion = %v", completion)
	}
	result := completion.MustGet("result").(*jsonx.Obj)
	if result.MustGet("value") != float64(2) {
		t.Fatalf("result = %v", result)
	}
}

func TestRunPluginThrowFails(t *testing.T) {
	handlers := map[string]PluginHandlerFn{
		"boom": func(any) (any, error) { return nil, errors.New("exploded") },
	}
	completion, err := RunPlugin(PluginInputFields{Handler: "boom"}, handlers, nil)
	if err != nil {
		t.Fatal(err)
	}
	failure := completion.MustGet("failure").(*jsonx.Obj)
	if failure.MustGet("reason") != "threw" || failure.MustGet("detail") != "exploded" {
		t.Fatalf("failure = %v", failure)
	}
}

func TestRunPluginThrowReraisesWhenAborted(t *testing.T) {
	handlers := map[string]PluginHandlerFn{
		"boom": func(any) (any, error) { return nil, errors.New("cancelled mid-flight") },
	}
	aborted := func() bool { return true }
	completion, err := RunPlugin(PluginInputFields{Handler: "boom"}, handlers, aborted)
	if completion != nil {
		t.Fatalf("completion = %v", completion)
	}
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err = %v", err)
	}
}

func TestPluginCompletionStrictJSON(t *testing.T) {
	// A handler result that stringifies cleanly completes.
	completion, err := PluginCompletedCompletion(jsonx.ObjFrom("k", "v"))
	if err != nil {
		t.Fatal(err)
	}
	if completion.MustGet("result").(*jsonx.Obj).MustGet("k") != "v" {
		t.Fatal("result lost")
	}
}
