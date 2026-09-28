package runtime

// response_settle.go ports harness/runtime/drive/response.ts's
// publishResponse decision core: classifying one settled assistant or
// deferred response into its durable disposition (cancelled checkpoint,
// overflow structural, deferred suspension, retry wait, tools batch,
// finish checkpoint, or failure) plus the response-entry write plan.

import (
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// ResponseDisposition is the classification outcome.
type ResponseDisposition struct {
	// Settled is the next operation state (nil when failing).
	Settled *session.OperationState
	// Failure carries the provider error when failing.
	Failure *session.OperationError
	// Committed is the (possibly normalized) response message.
	Committed *jsonx.Obj
	// Invariant carries a classification invariant error.
	Invarianterror error
}

// OverflowCheck reports whether the response indicates a context overflow
// or a recoverable length error (injectable predicates).
func OverflowCheck(message *jsonx.Obj, isContextOverflow func(*jsonx.Obj) bool, isRecoverableLength func(*jsonx.Obj) bool) bool {
	return isContextOverflow(message) || isRecoverableLength(message)
}

// ClassifyResponse mirrors publishResponse's classification switch for a
// running-control (non-cancelled) intent.
func ClassifyResponse(
	at string,
	response *jsonx.Obj,
	scope *session.OperationState,
	responseEntryID string,
	overflow bool,
	overflowRecoveryUsed bool,
	options struct {
		Recovery bool
	},
	retryState struct {
		Attempt         float64
		MaxAttempts     float64
		RetryEnabled    bool
		MaxRetries      int
		BaseDelayMS     float64
		MaxAgentDelayMS *float64
	},
	isRetryable func(*jsonx.Obj) bool,
	retryNotBefore func() float64,
	overflowPreparation *ThresholdPreparation,
) *ResponseDisposition {
	out := &ResponseDisposition{Committed: response}
	stopReason, _ := response.Get("stopReason")

	if stopReason == "aborted" {
		out.Invarianterror = &session.SessionInvariantError{Message: ResponseSource(at) + " response is aborted while durable control is running"}
		return out
	}

	if at == session.AtAssistantEffectPending && overflow {
		out.Committed = NormalizeError(response, stringOrEmpty2(response, "errorMessage"))
		if stringOrEmpty2(response, "errorMessage") == "" {
			out.Committed = NormalizeError(response, "Assistant request exceeded the context window")
		}
		if overflowRecoveryUsed || overflowPreparation == nil {
			out.Failure = ProviderError(ResponseSource(at), out.Committed)
			return out
		}
		// Structural overflow recovery.
		next := OperationScopeCopy(scope)
		next.At = session.AtSummaryDeciding
		resumeAfter := jsonx.ObjFrom(
			"continuation", jsonx.ObjFrom("kind", "need_assistant", "overflowRecoveryUsed", true),
			"triggerEntryID", scope.TriggerEntryID,
		)
		task := jsonx.ObjFrom(
			"taskId", overflowPreparation.TaskID,
			"reason", "overflow",
			"boundary", jsonx.ObjFrom("kind", "resume_checkpoint", "resumeAfter", resumeAfter),
		)
		next.Task = task
		out.Settled = &next
		return out
	}

	if stopReason == "deferred" {
		next := OperationScopeCopy(scope)
		next.At = session.AtDeferredSuspended
		next.SourceEntryID = responseEntryID
		next.Poll = 0
		if at != session.AtAssistantEffectPending {
			// A deferred response on a deferred intent re-suspends at the
			// current poll.
			next.Poll = scope.Poll
		}
		out.Settled = &next
		return out
	}

	if stopReason == "error" {
		if at == session.AtAssistantEffectPending &&
			(options.Recovery || isRetryable(response)) &&
			retryState.Attempt < retryState.MaxAttempts {
			next := OperationScopeCopy(scope)
			next.At = session.AtAssistantRetryWait
			next.NextAttempt = int64(retryState.Attempt + 1)
			next.NotBefore = retryNotBefore()
			errorMessage := stringOrEmpty2(response, "errorMessage")
			if errorMessage == "" {
				errorMessage = "Assistant request failed"
			}
			next.ErrorMessage = errorMessage
			out.Settled = &next
			return out
		}
		out.Failure = ProviderError(ResponseSource(at), response)
		return out
	}

	// Tool calls -> the tools batch.
	calls := toolCallIndexes(response)
	if len(calls) > 0 {
		next := OperationScopeCopy(scope)
		next.At = session.AtTools
		planned := make([]any, 0, len(calls))
		for _, sourceIndex := range calls {
			planned = append(planned, jsonx.ObjFrom(
				"status", "planned",
				"sourceIndex", float64(sourceIndex),
				"resultEntryId", "reserved",
			))
		}
		next.Batch = jsonx.ObjFrom(
			"assistantEntryId", responseEntryID,
			"turnId", scope.StepID,
			"calls", planned,
		)
		out.Settled = &next
		return out
	}
	if stopReason == "toolUse" {
		out.Committed = NormalizeError(response, "Provider reported tool use without any tool calls")
		out.Failure = ProviderError(ResponseSource(at), out.Committed)
		return out
	}
	// Plain answer -> may_finish checkpoint.
	next := OperationScopeCopy(scope)
	next.At = session.AtCheckpoint
	next.Continuation = jsonx.ObjFrom("kind", "may_finish", "includeFinalAssistant", true)
	next.TriggerEntryID = responseEntryID
	out.Settled = &next
	return out
}

// ClassifyCancelledResponse mirrors the cancel_requested branch: the
// response normalizes to aborted and settles at the finish checkpoint.
func ClassifyCancelledResponse(source string, response *jsonx.Obj, scope *session.OperationState, responseEntryID string) *ResponseDisposition {
	out := &ResponseDisposition{Committed: NormalizeAborted(source, response)}
	next := OperationScopeCopy(scope)
	next.At = session.AtCheckpoint
	next.Continuation = jsonx.ObjFrom("kind", "may_finish", "includeFinalAssistant", true)
	next.TriggerEntryID = responseEntryID
	out.Settled = &next
	return out
}

// ResponseWritePlan builds the response entry writes: entry + usage row +
// tip advance + frame-list delete (or cleanup on failure) + the overflow
// preparation write.
type ResponseWritePlan struct {
	Writes []session.Write
	// Failed carries the failure record when failing.
	Failed bool
	Record *session.OperationResultRecord
}

// PlanResponseWrites mirrors the writes assembly.
func PlanResponseWrites(
	lane string,
	responseEntryID string,
	committed *jsonx.Obj,
	usageID string,
	operationID string,
	scope *session.OperationState,
	reader session.SessionReader,
	ctx contextContextAlias,
	failure *session.OperationError,
	meta *session.OperationMeta,
	overflowPreparation *ThresholdPreparation,
	settled *session.OperationState,
) (*ResponseWritePlan, error) {
	entry := &session.Entry{
		EntryBase: session.EntryBase{ID: responseEntryID, ParentID: nil, Type: session.EntryTypeMessage},
		Message:   session.AgentMessagePayload{Role: "assistant", Message: committed},
	}
	usageRow := session.UsageRow{ID: usageID, EntryID: &responseEntryID}
	if usageValue, ok := committed.Get("usage"); ok && usageValue != nil {
		usageRow.Usage = usageFromObj(usageValue)
	} else {
		usageRow.Usage = zeroUsageStruct()
	}
	plan := &ResponseWritePlan{}
	plan.Writes = append(plan.Writes,
		session.InsertEntry(entry),
		session.InsertUsage(usageRow),
		session.WriteFromValue(session.SetValue(session.BranchTip(lane), responseEntryID)),
	)
	if failure != nil {
		record, err := OperationResultRecord(meta, session.StatusFailed, &responseEntryID, failure)
		if err != nil {
			return nil, err
		}
		cleanup, err := OperationCleanupWrites(reader, operationID, scope, ctx)
		if err != nil {
			return nil, err
		}
		plan.Writes = append(plan.Writes, cleanup...)
		plan.Failed = true
		plan.Record = record
		return plan, nil
	}
	plan.Writes = append(plan.Writes, session.WriteFromList(session.DeleteList(
		session.PendingAssistantFrames(operationID, responseEntryID),
	)))
	if settled != nil && settled.At == session.AtSummaryDeciding && overflowPreparation != nil {
		plan.Writes = append(plan.Writes, session.WriteFromValue(session.SetValue(
			session.OperationPreparation(operationID, overflowPreparation.TaskID), overflowPreparation.Preparation,
		)))
	}
	return plan, nil
}

func zeroUsageStruct() (u aiUsageRuntime) { return u }

func toolCallIndexes(response *jsonx.Obj) []int64 {
	contentValue, ok := response.Get("content")
	if !ok {
		return nil
	}
	content, ok := contentValue.([]any)
	if !ok {
		return nil
	}
	var indexes []int64
	for i, item := range content {
		if block, ok := item.(*jsonx.Obj); ok {
			if t, _ := block.Get("type"); t == "toolCall" {
				indexes = append(indexes, int64(i))
			}
		}
	}
	return indexes
}

func stringOrEmpty2(obj *jsonx.Obj, key string) string {
	if v, ok := obj.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
