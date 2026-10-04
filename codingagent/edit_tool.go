package codingagent

// Port of core/tools/edit.ts: exact-text replacement editing with fuzzy
// fallback, argument normalization for quirky models, and diff details.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/typebox"
)

// EditToolSystemPromptContribution is the edit tool's prompt section.
var EditToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet: "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
	Guidelines: []string{
		"Use edit for precise changes (edits[].oldText must match exactly)",
		"When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls",
		"Each edits[].oldText is matched against the original file, not after earlier edits are applied. Do not emit overlapping or nested edits. Merge nearby changes into one edit.",
		"Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions.",
	},
}

// EditToolInput is the tool's validated input.
type EditToolInput struct {
	Path  string `json:"path"`
	Edits []Edit `json:"edits"`
}

// EditToolDetails carries the display diff and patch for renderers.
type EditToolDetails struct {
	Diff             string
	Patch            string
	FirstChangedLine int
	HasFirstChanged  bool
}

// EditOperations are the pluggable edit operations.
type EditOperations interface {
	ReadFile(absolutePath string) ([]byte, error)
	WriteFile(absolutePath, content string) error
	Access(absolutePath string) error
}

type localEditOperations struct{}

func (localEditOperations) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (localEditOperations) WriteFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
func (localEditOperations) Access(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return file.Close()
}

// DefaultEditOperations edits the local filesystem.
var DefaultEditOperations EditOperations = localEditOperations{}

// EditToolOptions configure the edit tool.
type EditToolOptions struct {
	Operations EditOperations
}

// PrepareEditArguments normalizes quirky model inputs: edits as a JSON
// string, a single edit object, or legacy top-level oldText/newText.
func PrepareEditArguments(input any) any {
	args, ok := input.(map[string]any)
	if !ok {
		return input
	}

	switch edits := args["edits"].(type) {
	case string:
		var parsed any
		if err := json.Unmarshal([]byte(edits), &parsed); err == nil {
			switch typed := parsed.(type) {
			case []any:
				args["edits"] = typed
			case map[string]any:
				if isSingleEditInput(typed) {
					args["edits"] = []any{typed}
				}
			}
		}
	case map[string]any:
		if isSingleEditInput(edits) {
			args["edits"] = []any{edits}
		}
	}

	oldText, oldOK := args["oldText"].(string)
	newText, newOK := args["newText"].(string)
	if !oldOK || !newOK {
		return args
	}

	var edits []any
	if existing, ok := args["edits"].([]any); ok {
		edits = append(edits, existing...)
	}
	edits = append(edits, map[string]any{"oldText": oldText, "newText": newText})
	delete(args, "oldText")
	delete(args, "newText")
	args["edits"] = edits
	return args
}

func isSingleEditInput(value map[string]any) bool {
	_, oldOK := value["oldText"].(string)
	_, newOK := value["newText"].(string)
	return oldOK && newOK
}

func validateEditInput(input EditToolInput) (string, []Edit, error) {
	if len(input.Edits) == 0 {
		return "", nil, fmt.Errorf("Edit tool input is invalid. edits must contain at least one replacement.")
	}
	return input.Path, input.Edits, nil
}

func mapEditToolInput(params any) (EditToolInput, bool) {
	switch typed := params.(type) {
	case EditToolInput:
		return typed, true
	case map[string]any:
		input := EditToolInput{}
		if path, ok := typed["path"].(string); ok {
			input.Path = path
		}
		if edits, ok := typed["edits"].([]any); ok {
			for _, entry := range edits {
				editMap, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				edit := Edit{}
				if oldText, ok := editMap["oldText"].(string); ok {
					edit.OldText = oldText
				}
				if newText, ok := editMap["newText"].(string); ok {
					edit.NewText = newText
				}
				input.Edits = append(input.Edits, edit)
			}
		}
		return input, true
	}
	return EditToolInput{}, false
}

// CreateEditToolDefinition builds the edit ToolDefinition.
func CreateEditToolDefinition(cwd string, options *EditToolOptions) *ToolDefinition {
	ops := EditOperations(DefaultEditOperations)
	if options != nil && options.Operations != nil {
		ops = options.Operations
	}
	return &ToolDefinition{
		Name:             "edit",
		Label:            "edit",
		Description:      "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes.",
		PromptSnippet:    EditToolSystemPromptContribution.Snippet,
		PromptGuidelines: append([]string(nil), EditToolSystemPromptContribution.Guidelines...),
		Parameters: typebox.Object([]*typebox.Property{
			typebox.Prop("path", typebox.String(typebox.Description("Path to the file to edit (relative or absolute)"))),
			typebox.Prop("edits", typebox.Array(typebox.Object([]*typebox.Property{
				typebox.Prop("oldText", typebox.String(typebox.Description("Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."))),
				typebox.Prop("newText", typebox.String(typebox.Description("Replacement text for this targeted edit."))),
			}), typebox.Description("One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead."))),
		}),
		ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"},
		RenderShell:         "self",
		PrepareArguments:    PrepareEditArguments,
		Execute: func(toolCallID string, params any, signal *abort.Signal, onUpdate func(partial any), ctx *ExtensionContext) (any, error) {
			input, ok := params.(EditToolInput)
			if !ok {
				mapped, mappedOk := mapEditToolInput(params)
				if !mappedOk {
					return nil, fmt.Errorf("edit: invalid params %T", params)
				}
				input = mapped
			}

			path, edits, err := validateEditInput(input)
			if err != nil {
				return nil, err
			}

			base := cwd
			if ctx != nil && ctx.Cwd != "" {
				base = ctx.Cwd
			}
			absolutePath := ResolveToCwd(path, base)

			var output *EditToolOutput
			err = WithFileMutationQueue(absolutePath, func() error {
				throwIfAborted := func() error {
					if signal != nil && signal.Aborted() {
						return fmt.Errorf("Operation aborted")
					}
					return nil
				}

				if err := throwIfAborted(); err != nil {
					return err
				}

				if err := ops.Access(absolutePath); err != nil {
					_ = throwIfAborted()
					return fmt.Errorf("Could not edit file: %s. %s.", path, errorCodeOrMessage(err))
				}
				if err := throwIfAborted(); err != nil {
					return err
				}

				buffer, err := ops.ReadFile(absolutePath)
				if err != nil {
					return err
				}
				rawContent := string(buffer)
				if err := throwIfAborted(); err != nil {
					return err
				}

				// Strip BOM before matching; the model never includes an
				// invisible BOM in oldText.
				bom, content := SplitBom(rawContent)
				originalEnding := DetectLineEnding(content)
				normalizedContent := NormalizeToLF(content)
				applied, err := ApplyEditsToNormalizedContent(normalizedContent, edits, path)
				if err != nil {
					return err
				}
				if err := throwIfAborted(); err != nil {
					return err
				}

				finalContent := bom + RestoreLineEndings(applied.NewContent, originalEnding)
				if err := ops.WriteFile(absolutePath, finalContent); err != nil {
					return err
				}
				if err := throwIfAborted(); err != nil {
					return err
				}

				diffResult := GenerateDiffString(applied.BaseContent, applied.NewContent, 4)
				patch := GenerateUnifiedPatch(path, applied.BaseContent, applied.NewContent, 4)
				output = &EditToolOutput{
					Text: fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(edits), path),
					Details: &EditToolDetails{
						Diff:             diffResult.Diff,
						Patch:            patch,
						FirstChangedLine: diffResult.FirstChangedLine,
						HasFirstChanged:  diffResult.HasFirstChanged,
					},
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			return output, nil
		},
	}
}

// CreateEditTool wraps the definition into an AgentTool.
func CreateEditTool(cwd string, options *EditToolOptions) any {
	return WrapToolDefinition(CreateEditToolDefinition(cwd, options), nil)
}

// EditToolOutput is the edit result shape.
type EditToolOutput struct {
	Text    string
	Details *EditToolDetails
}

// errorCodeOrMessage renders an access error like the reference.
func errorCodeOrMessage(err error) string {
	var pathError *os.PathError
	if ok := errorAs(err, &pathError); ok {
		return "Error code: " + pathErrorCode(pathError)
	}
	return err.Error()
}

func pathErrorCode(err *os.PathError) string {
	if os.IsNotExist(err) {
		return "ENOENT"
	}
	if os.IsPermission(err) {
		return "EACCES"
	}
	return err.Err.Error()
}

// errorAs avoids importing errors in every tool file.
func errorAs(err error, target any) bool {
	return errors.As(err, target)
}
