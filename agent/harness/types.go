// Package harness ports pi/packages/agent/src/harness: the durable agent
// harness with tools, skills, compaction, sessions and the runtime drive
// pipeline.
package harness

import (
	"fmt"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/chord/context"
)

// ---------------------------------------------------------------------------
// Result (harness/types.ts)
// ---------------------------------------------------------------------------

// Result is the fallible-operation return: Ok or Err.
type Result[TValue any, TError any] struct {
	Ok    bool
	Value TValue
	Error TError
}

// Ok builds a successful Result.
func Ok[TValue any, TError any](value TValue) Result[TValue, TError] {
	return Result[TValue, TError]{Ok: true, Value: value}
}

// Err builds a failed Result.
func Err[TValue any, TError any](err TError) Result[TValue, TError] {
	return Result[TValue, TError]{Ok: false, Error: err}
}

// GetOrThrow returns the value or panics with the error (Go stand-in for
// throw at explicit adapter boundaries).
func GetOrThrow[TValue any, TError any](result Result[TValue, TError]) TValue {
	if !result.Ok {
		panic(result.Error)
	}
	return result.Value
}

// GetOrUndefined returns the value or the zero value.
func GetOrUndefined[TValue any, TError any](result Result[TValue, TError]) TValue {
	var zero TValue
	if !result.Ok {
		return zero
	}
	return result.Value
}

// ToError normalizes thrown values into errors.
func ToError(err any) error {
	switch t := err.(type) {
	case error:
		return t
	case string:
		return fmt.Errorf("%s", t)
	default:
		return fmt.Errorf("%v", err)
	}
}

// ---------------------------------------------------------------------------
// Error types
// ---------------------------------------------------------------------------

// FileErrorCode values.
const (
	FileErrAborted          = "aborted"
	FileErrNotFound         = "not_found"
	FileErrPermissionDenied = "permission_denied"
	FileErrNotDirectory     = "not_directory"
	FileErrIsDirectory      = "is_directory"
	FileErrInvalid          = "invalid"
	FileErrNotSupported     = "not_supported"
	FileErrUnknown          = "unknown"
)

// FileError mirrors the TS class.
type FileError struct {
	Code    string
	Message string
	Path    string
}

func (e *FileError) Error() string { return e.Message }

// NewFileError builds a FileError.
func NewFileError(code, message, path string) *FileError {
	return &FileError{Code: code, Message: message, Path: path}
}

// ExecutionErrorCode values.
const (
	ExecErrAborted          = "aborted"
	ExecErrTimeout          = "timeout"
	ExecErrShellUnavailable = "shell_unavailable"
	ExecErrSpawnError       = "spawn_error"
	ExecErrCallbackError    = "callback_error"
	ExecErrUnknown          = "unknown"
)

// ExecutionError mirrors the TS class.
type ExecutionError struct {
	Code    string
	Message string
}

func (e *ExecutionError) Error() string { return e.Message }

// NewExecutionError builds an ExecutionError.
func NewExecutionError(code, message string) *ExecutionError {
	return &ExecutionError{Code: code, Message: message}
}

// CompactionErrorCode values.
const (
	CompactionErrAborted             = "aborted"
	CompactionErrSummarizationFailed = "summarization_failed"
)

// CompactionError mirrors the TS class.
type CompactionError struct {
	Code    string
	Message string
}

func (e *CompactionError) Error() string { return e.Message }

// NewCompactionError builds a CompactionError.
func NewCompactionError(code, message string) *CompactionError {
	return &CompactionError{Code: code, Message: message}
}

// BranchSummaryErrorCode values.
const (
	BranchSummaryErrAborted             = "aborted"
	BranchSummaryErrSummarizationFailed = "summarization_failed"
)

// BranchSummaryError mirrors the TS class.
type BranchSummaryError struct {
	Code    string
	Message string
}

func (e *BranchSummaryError) Error() string { return e.Message }

// NewBranchSummaryError builds a BranchSummaryError.
func NewBranchSummaryError(code, message string) *BranchSummaryError {
	return &BranchSummaryError{Code: code, Message: message}
}

// ---------------------------------------------------------------------------
// Skills, templates, tools
// ---------------------------------------------------------------------------

// Skill mirrors the TS interface.
type Skill struct {
	Name                   string
	Description            string
	Content                string
	FilePath               string
	DisableModelInvocation bool
}

// PromptTemplate mirrors the TS interface.
type PromptTemplate struct {
	Name           string
	Description    string
	HasDescription bool
	Content        string
}

// AgentHarnessResources mirrors the TS interface.
type AgentHarnessResources struct {
	PromptTemplates []PromptTemplate
	Skills          []Skill
}

// AgentHarnessToolUpdateOptions mirrors the TS interface.
type AgentHarnessToolUpdateOptions struct {
	Checkpoint bool
}

// AgentHarnessToolUpdateCallback is the synchronous full-snapshot progress
// callback.
type AgentHarnessToolUpdateCallback func(partialResult *agent.AgentToolResult, options *AgentHarnessToolUpdateOptions)

// AgentHarnessToolInvocation mirrors the TS interface.
type AgentHarnessToolInvocation interface {
	InvocationID() string
	OperationID() string
	TurnID() string
	GetMemo(name string, ctx context.Context) (any, error)
	SetMemo(name string, value any, ctx context.Context) error
}

// AgentHarnessTool mirrors the TS type: an agent.AgentTool whose Execute
// receives harness-specific callbacks and context.
type AgentHarnessTool struct {
	// AgentTool fields (name/label/description/parameters/prepareArguments/
	// replay/executionMode) are embedded.
	agent.AgentTool
	// Execute is the harness-native execution signature.
	Execute func(
		toolCallID string,
		params any,
		onUpdate AgentHarnessToolUpdateCallback,
		toolContext any,
		invocation AgentHarnessToolInvocation,
		ctx context.Context,
	) (*agent.AgentToolResult, error)
}

// AgentHarnessToolContextSource is a static context or a per-turn resolver.
type AgentHarnessToolContextSource func(ctx context.Context) (any, error)

// AgentHarnessStreamOptions mirrors the TS interface.
type AgentHarnessStreamOptions struct {
	Transport       *string
	TimeoutMs       *float64
	MaxRetries      *float64
	MaxRetryDelayMs *float64
	Headers         map[string]string
	Metadata        map[string]any
	CacheRetention  *string
	Deferred        any // bool | {window}
}

// AgentHarnessStreamOptionsPatch mirrors the TS patch: header/metadata
// entries with nil values delete keys; nil maps clear everything.
type AgentHarnessStreamOptionsPatch struct {
	Transport          *string
	HasTransport       bool
	TimeoutMs          *float64
	HasTimeoutMs       bool
	MaxRetries         *float64
	HasMaxRetries      bool
	MaxRetryDelayMs    *float64
	HasMaxRetryDelayMs bool
	Headers            map[string]*string
	HasHeaders         bool
	ClearHeaders       bool
	Metadata           map[string]any
	HasMetadata        bool
	ClearMetadata      bool
	CacheRetention     *string
	HasCacheRetention  bool
	Deferred           any
	HasDeferred        bool
}

// ---------------------------------------------------------------------------
// Filesystem contracts
// ---------------------------------------------------------------------------

// FileKind values.
const (
	FileKindFile      = "file"
	FileKindDirectory = "directory"
	FileKindSymlink   = "symlink"
)

// FileInfo mirrors the TS interface.
type FileInfo struct {
	Name    string
	Path    string
	Kind    string
	Size    int64
	MtimeMs float64
}

// TextLine mirrors the TS interface.
type TextLine struct {
	Text       string
	Terminated bool
}

// TextLineReader mirrors the TS interface.
type TextLineReader interface {
	ReadLine(ctx context.Context) (Result[*TextLine, *FileError], error)
	Close(ctx context.Context) error
}

// ReadTextLinesOptions mirrors the options.
type ReadTextLinesOptions struct {
	MaxLines *int
}

// CreateDirOptions mirrors the options.
type CreateDirOptions struct {
	Recursive *bool
}

// RemoveOptions mirrors the options.
type RemoveOptions struct {
	Recursive *bool
	Force     *bool
}

// CreateTempFileOptions mirrors the options.
type CreateTempFileOptions struct {
	Prefix *string
	Suffix *string
}

// FileSystem mirrors the TS interface. All methods return Results; they
// must never panic. Context is the chord Context.
type FileSystem interface {
	Cwd() string
	AbsolutePath(path string, ctx context.Context) Result[string, *FileError]
	JoinPath(parts []string, ctx context.Context) Result[string, *FileError]
	ReadTextFile(path string, ctx context.Context) Result[string, *FileError]
	OpenTextLineReader(path string, ctx context.Context) Result[TextLineReader, *FileError]
	ReadTextLines(path string, options *ReadTextLinesOptions, ctx context.Context) Result[[]string, *FileError]
	ReadBinaryFile(path string, ctx context.Context) Result[[]byte, *FileError]
	WriteFile(path string, content []byte, ctx context.Context) Result[struct{}, *FileError]
	AppendFile(path string, content []byte, ctx context.Context) Result[struct{}, *FileError]
	RenameFile(sourcePath, destinationPath string, ctx context.Context) Result[struct{}, *FileError]
	FileInfo(path string, ctx context.Context) Result[FileInfo, *FileError]
	ListDir(path string, ctx context.Context) Result[[]FileInfo, *FileError]
	CanonicalPath(path string, ctx context.Context) Result[string, *FileError]
	Exists(path string, ctx context.Context) Result[bool, *FileError]
	CreateDir(path string, options *CreateDirOptions, ctx context.Context) Result[struct{}, *FileError]
	Remove(path string, options *RemoveOptions, ctx context.Context) Result[struct{}, *FileError]
	CreateTempDir(prefix string, ctx context.Context) Result[string, *FileError]
	CreateTempFile(options *CreateTempFileOptions, ctx context.Context) Result[string, *FileError]
	Cleanup(ctx context.Context) error
}

// ---------------------------------------------------------------------------
// Shell contracts
// ---------------------------------------------------------------------------

// ShellOutputRetention values.
const (
	RetainHead = "head"
	RetainTail = "tail"
)

// ShellOutputLimits mirrors the TS interface.
type ShellOutputLimits struct {
	MaxBytes int64
	MaxLines int64
	Retain   string
}

// ShellOutputCaptureOptions mirrors the TS interface.
type ShellOutputCaptureOptions struct {
	Limits ShellOutputLimits
	Spill  bool
}

// TruncationResultData is the truncation metadata without content.
type TruncationResultData struct {
	OriginalBytes int64
	OriginalLines int64
	Truncated     bool
	RetainedBytes int64
	FromStart     bool
	Note          string
}

// ShellOutputMetadata mirrors the TS interface.
type ShellOutputMetadata struct {
	Truncation    TruncationResultData
	SpillPath     string
	HasSpillPath  bool
	LastLineBytes int64
}

// ShellOutputView mirrors the TS interface.
type ShellOutputView struct {
	ShellOutputMetadata
	Text string
}

// ShellOutputUpdate mirrors the TS union.
type ShellOutputUpdate interface{ updateKind() string }

// ShellOutputReplace is {kind:"replace", output}.
type ShellOutputReplace struct{ Output ShellOutputView }

func (*ShellOutputReplace) updateKind() string { return "replace" }

// ShellOutputAppend is {kind:"append", text, metadata}.
type ShellOutputAppend struct {
	Text     string
	Metadata ShellOutputMetadata
}

func (*ShellOutputAppend) updateKind() string { return "append" }

// ShellOutputSlide is {kind:"slide", drop, text, metadata}.
type ShellOutputSlide struct {
	Drop     int64
	Text     string
	Metadata ShellOutputMetadata
}

func (*ShellOutputSlide) updateKind() string { return "slide" }

// ShellOutputMetadataUpdate is {kind:"metadata", metadata}.
type ShellOutputMetadataUpdate struct{ Metadata ShellOutputMetadata }

func (*ShellOutputMetadataUpdate) updateKind() string { return "metadata" }

// ShellExecResult mirrors the TS interface.
type ShellExecResult struct {
	ShellOutputMetadata
	ExitCode int64
}

// ShellExecOptions mirrors the TS interface.
type ShellExecOptions struct {
	Cwd        *string
	Env        map[string]string
	InheritEnv *bool
	Timeout    *float64
	Capture    *ShellOutputCaptureOptions
	OnUpdate   func(update ShellOutputUpdate, ctx context.Context)
}

// Shell mirrors the TS interface.
type Shell interface {
	Exec(command string, options *ShellExecOptions, ctx context.Context) Result[ShellExecResult, *ExecutionError]
	Cleanup(ctx context.Context) error
}

// ExecutionEnv is FileSystem + Shell.
type ExecutionEnv interface {
	FileSystem
	Shell
}
