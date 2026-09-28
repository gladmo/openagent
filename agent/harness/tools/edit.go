package tools

// edit.go ports harness/tools/edit.ts. The edit-diff engine lives in
// edit_diff.go.

import (
	"fmt"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/typebox"
)

// Edit mirrors the TS Edit.
type Edit struct {
	OldText string
	NewText string
}

// EditToolDetails mirrors the TS interface.
type EditToolDetails struct {
	Diff             string
	Patch            string
	FirstChangedLine *int
}

// PrepareEditArguments normalizes legacy shapes: JSON-string edits arrays,
// a single-edit object under edits, and top-level oldText/newText.
func PrepareEditArguments(input any) any {
	obj, ok := input.(*jsonx.Obj)
	if !ok {
		return input
	}
	if editsValue, ok := obj.Get("edits"); ok {
		if s, isStr := editsValue.(string); isStr {
			if parsed, err := jsonx.Parse(s); err == nil {
				if arr, isArr := parsed.([]any); isArr {
					obj.Set("edits", arr)
				} else if p, isObj := parsed.(*jsonx.Obj); isObj {
					if isSingleEditInput(p) {
						obj.Set("edits", []any{p})
					}
				}
			}
		} else if p, isObj := editsValue.(*jsonx.Obj); isObj {
			if isSingleEditInput(p) {
				obj.Set("edits", []any{p})
			}
		}
	}

	oldText, hasOld := obj.Get("oldText")
	newText, hasNew := obj.Get("newText")
	oldStr, oldIsStr := oldText.(string)
	newStr, newIsStr := newText.(string)
	if !hasOld || !hasNew || !oldIsStr || !newIsStr {
		return obj
	}
	edits := []any{}
	if editsValue, ok := obj.Get("edits"); ok {
		if arr, isArr := editsValue.([]any); isArr {
			edits = append(edits, arr...)
		}
	}
	edits = append(edits, jsonx.ObjFrom("oldText", oldStr, "newText", newStr))
	out := jsonx.NewObj()
	for _, key := range obj.Keys() {
		if key == "oldText" || key == "newText" {
			continue
		}
		out.Set(key, obj.MustGet(key))
	}
	out.Set("edits", edits)
	return out
}

func isSingleEditInput(value *jsonx.Obj) bool {
	oldText, hasOld := value.Get("oldText")
	newText, hasNew := value.Get("newText")
	_, oldIsStr := oldText.(string)
	_, newIsStr := newText.(string)
	return hasOld && hasNew && oldIsStr && newIsStr
}

func validateEditInput(input any) (string, []Edit, error) {
	obj, ok := input.(*jsonx.Obj)
	if !ok {
		return "", nil, fmt.Errorf("Edit tool input is invalid. edits must contain at least one replacement.")
	}
	editsValue, hasEdits := obj.Get("edits")
	if !hasEdits {
		return "", nil, fmt.Errorf("Edit tool input is invalid. edits must contain at least one replacement.")
	}
	arr, isArr := editsValue.([]any)
	if !isArr || len(arr) == 0 {
		return "", nil, fmt.Errorf("Edit tool input is invalid. edits must contain at least one replacement.")
	}
	path := ""
	if v, ok := obj.Get("path"); ok {
		if s, ok := v.(string); ok {
			path = s
		}
	}
	edits := make([]Edit, 0, len(arr))
	for _, e := range arr {
		if editObj, ok := e.(*jsonx.Obj); ok {
			oldText, _ := editObj.Get("oldText")
			newText, _ := editObj.Get("newText")
			edits = append(edits, Edit{OldText: oldText.(string), NewText: newText.(string)})
		}
	}
	return path, edits, nil
}

func editAccessError(path string, err *harness.FileError) error {
	return fmt.Errorf("Could not edit file: %s. Error code: %s.", path, err.Code)
}

// CreateEditTool builds the edit tool.
func CreateEditTool() *harness.AgentHarnessTool {
	replaceEditSchema := typebox.Object([]*typebox.Property{
		typebox.Prop("oldText", typebox.String(typebox.Description(
			"Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."))),
		typebox.Prop("newText", typebox.String(typebox.Description("Replacement text for this targeted edit."))),
	})
	schema := typebox.Object([]*typebox.Property{
		typebox.Prop("path", typebox.String(typebox.Description("Path to the file to edit (relative or absolute)"))),
		typebox.Prop("edits", typebox.Array(replaceEditSchema, typebox.Description(
			"One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead."))),
	})
	tool := &harnessTool{
		name:        "edit",
		label:       "edit",
		description: "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes.",
		parameters:  schema,
	}
	tool.execute = func(_ string, input any, _ harness.AgentHarnessToolUpdateCallback, toolContext any, _ harness.AgentHarnessToolInvocation, ctx harness.Context) (*agent.AgentToolResult, error) {
		path, edits, err := validateEditInput(input)
		if err != nil {
			return nil, err
		}
		env := toolContext.(ExecutionToolContext).Env
		absolutePath, err := ResolveToolPath(env, path, ctx)
		if err != nil {
			return nil, err
		}
		return WithFileMutationQueue(env, absolutePath, func() (*agent.AgentToolResult, error) {
			if ctx.AbortSignal().Aborted() {
				return nil, fmt.Errorf("Operation aborted")
			}
			info := env.FileInfo(absolutePath, ctx)
			if !info.Ok {
				return nil, editAccessError(path, info.Error)
			}
			if info.Value.Kind != harness.FileKindFile && info.Value.Kind != harness.FileKindSymlink {
				return nil, fmt.Errorf("Could not edit file: %s. Path is not a file.", path)
			}

			readResult := env.ReadTextFile(absolutePath, ctx)
			if !readResult.Ok {
				return nil, editAccessError(path, readResult.Error)
			}
			if ctx.AbortSignal().Aborted() {
				return nil, fmt.Errorf("Operation aborted")
			}

			bom, content := StripBom(readResult.Value)
			originalEnding := DetectLineEnding(content)
			normalizedContent := NormalizeToLF(content)
			baseContent, newContent, err := ApplyEditsToNormalizedContent(normalizedContent, edits, path)
			if err != nil {
				return nil, err
			}
			if ctx.AbortSignal().Aborted() {
				return nil, fmt.Errorf("Operation aborted")
			}

			finalContent := bom + RestoreLineEndings(newContent, originalEnding)
			writeResult := env.WriteFile(absolutePath, []byte(finalContent), ctx)
			if !writeResult.Ok {
				return nil, editAccessError(path, writeResult.Error)
			}
			if ctx.AbortSignal().Aborted() {
				return nil, fmt.Errorf("Operation aborted")
			}

			diffText, firstChangedLine := GenerateDiffString(baseContent, newContent, 4)
			details := EditToolDetails{
				Diff:  diffText,
				Patch: GenerateUnifiedPatch(path, baseContent, newContent, 4),
			}
			if firstChangedLine > 0 {
				line := firstChangedLine
				details.FirstChangedLine = &line
			}
			return &agent.AgentToolResult{
				Content: []ai.ContentBlock{ai.TextContent{Text: fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(edits), path)}},
				Details: details,
			}, nil
		}, ctx)
	}
	result := tool.tool()
	result.PrepareArguments = PrepareEditArguments
	return result
}
