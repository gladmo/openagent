package runtime

// deferred.go ports harness/runtime/drive/deferred.ts's source-handle
// resolution and poll preparation decisions.

import (
	"fmt"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// DeferredConfigurationError mirrors configurationError for the deferred
// leaf.
func DeferredConfigurationError(identity *jsonx.Obj) *session.OperationError {
	return &session.OperationError{
		Code:    "model_unavailable",
		Message: "The configured model is unavailable in this process",
		Details: identity,
	}
}

// ReadDeferredSourceHandle mirrors readDeferredSourceHandle: the source
// entry must be an assistant message with stopReason deferred and a
// handle whose id is non-empty and provider/model/api match the deferred
// configuration (api against the source message's api).
func ReadDeferredSourceHandle(reader session.SessionReader, sourceEntryID string, identity *jsonx.Obj, ctx sessionCtxAlias) (*jsonx.Obj, error) {
	entries, err := reader.GetEntries([]string{sourceEntryID}, ctx)
	if err != nil {
		return nil, err
	}
	entry, ok := entries[sourceEntryID]
	invalid := func() error {
		return &session.SessionInvariantError{Message: fmt.Sprintf("Deferred source %s is missing its assistant handle", sourceEntryID)}
	}
	if !ok || entry.Type != session.EntryTypeMessage || entry.Message.Role != "assistant" {
		return nil, invalid()
	}
	message := entry.Message.Message
	if message == nil {
		return nil, invalid()
	}
	if stopReason, _ := message.Get("stopReason"); stopReason != "deferred" {
		return nil, invalid()
	}
	handleValue, hasHandle := message.Get("deferred")
	if !hasHandle || handleValue == nil {
		return nil, invalid()
	}
	handle, ok := handleValue.(*jsonx.Obj)
	if !ok {
		return nil, invalid()
	}

	provider, _ := identity.Get("provider")
	modelID, _ := identity.Get("modelId")
	handleID, _ := handle.Get("id")
	idStr, _ := handleID.(string)
	handleProvider, _ := handle.Get("provider")
	handleModel, _ := handle.Get("modelId")
	messageAPI, _ := message.Get("api")
	handleAPI, _ := handle.Get("api")
	if idStr == "" || handleProvider != provider || handleModel != modelID || handleAPI != messageAPI {
		return nil, &session.SessionInvariantError{Message: fmt.Sprintf("Deferred source %s has an invalid handle", sourceEntryID)}
	}
	return handle, nil
}

// DeferredPollKind discriminates the preparation.
type DeferredPollKind string

const (
	DeferredPollReady           DeferredPollKind = "ready"
	DeferredPollCancelRequested DeferredPollKind = "cancel_requested"
	DeferredPollWaiting         DeferredPollKind = "waiting"
	DeferredPollConfigFailure   DeferredPollKind = "configuration_failure"
)

// DeferredPollPreparation mirrors the DeferredPreparation union.
type DeferredPollPreparation struct {
	Kind   DeferredPollKind
	Handle *jsonx.Obj
	Poll   int64
}

// PrepareDeferredPoll mirrors prepareDeferredPoll's decision: cancelled
// short-circuits; a missing model is a configuration failure; a due poll
// is ready; a future pollAt waits.
func PrepareDeferredPoll(
	cancelRequested bool,
	modelAvailable bool,
	handle *jsonx.Obj,
	poll int64,
	pollAt, now float64,
) *DeferredPollPreparation {
	if cancelRequested {
		return &DeferredPollPreparation{Kind: DeferredPollCancelRequested}
	}
	if !modelAvailable {
		return &DeferredPollPreparation{Kind: DeferredPollConfigFailure}
	}
	if now < pollAt {
		return &DeferredPollPreparation{Kind: DeferredPollWaiting, Handle: handle}
	}
	return &DeferredPollPreparation{Kind: DeferredPollReady, Handle: handle, Poll: poll}
}

// DeferredFramesCleanup mirrors the deferred-frame list delete when the
// poll settles.
func DeferredFramesCleanup(operationID, responseEntryID string) session.Write {
	return session.WriteFromList(session.DeleteList(
		session.PendingAssistantFrames(operationID, responseEntryID),
	))
}

// DeferredSuspension mirrors the deferred.suspended state construction
// from a settled suspension.
func DeferredSuspension(scope *session.OperationState, stepID, sourceEntryID string, poll int64, configuration *jsonx.Obj) *session.OperationState {
	next := OperationScopeCopy(scope)
	next.At = session.AtDeferredSuspended
	next.StepID = stepID
	next.SourceEntryID = sourceEntryID
	next.Poll = poll
	next.Configuration = configuration
	return &next
}
