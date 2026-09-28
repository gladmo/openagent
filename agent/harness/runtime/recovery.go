package runtime

// recovery.go ports harness/runtime/drive/recovery.ts: settling an
// orphaned assistant request from its bounded committed frame prefix
// without another provider call.

import (
	"time"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// InterruptedAssistantWarning is the recovery warning text.
const InterruptedAssistantWarning = "Assistant request was interrupted. The preceding content is the latest committed partial; newer live output may be missing and the external outcome is unknown."

// InterruptedAssistantMessage mirrors interruptedAssistantMessage: no
// partial -> an empty error assistant carrying the lane's model identity;
// a partial keeps its content but zeroes usage and forces the error stop
// with the warning.
func InterruptedAssistantMessage(provider, modelID string, partial *jsonx.Obj) *jsonx.Obj {
	if partial == nil {
		message := jsonx.NewObj()
		message.Set("role", "assistant")
		message.Set("content", []any{})
		message.Set("api", "unknown")
		message.Set("provider", provider)
		message.Set("model", modelID)
		message.Set("usage", zeroUsageJSON())
		message.Set("stopReason", "error")
		message.Set("errorMessage", InterruptedAssistantWarning)
		message.Set("timestamp", float64(time.Now().UnixMilli()))
		return message
	}
	recovered := jsonx.NewObj()
	for _, key := range partial.Keys() {
		value, _ := partial.Get(key)
		recovered.Set(key, value)
	}
	recovered.Set("usage", zeroUsageJSON())
	recovered.Set("stopReason", "error")
	recovered.Set("errorMessage", InterruptedAssistantWarning)
	return recovered
}

func zeroUsageJSON() *jsonx.Obj {
	usage := jsonx.NewObj()
	usage.Set("input", float64(0))
	usage.Set("output", float64(0))
	usage.Set("cacheRead", float64(0))
	usage.Set("cacheWrite", float64(0))
	usage.Set("totalTokens", float64(0))
	cost := jsonx.NewObj()
	cost.Set("input", float64(0))
	cost.Set("output", float64(0))
	cost.Set("cacheRead", float64(0))
	cost.Set("cacheWrite", float64(0))
	cost.Set("total", float64(0))
	usage.Set("cost", cost)
	return usage
}

// ReadGenerationFrames reads the committed frame prefix through the
// lane's session (the continueOperation read wrapper).
func ReadGenerationFrames(lane *Lane, drive *Drive, responseEntryID string) ([]*jsonx.Obj, error) {
	return ReadAssistantFrames(lane.session, drive.OperationID, responseEntryID, drive.Context)
}

// RecoverAssistantMessage reduces the frame prefix into the interrupted
// assistant message for the lane's model identity.
func RecoverAssistantMessage(provider, modelID string, frames []*jsonx.Obj) *jsonx.Obj {
	if len(frames) == 0 {
		return InterruptedAssistantMessage(provider, modelID, nil)
	}
	reduced := reduceFramesToMessage(frames)
	return InterruptedAssistantMessage(provider, modelID, reduced)
}

// reduceFramesToMessage applies the frames through the ai reducer
// (mirrors reduceAssistantMessageFrames over the JSON frame form).
func reduceFramesToMessage(frames []*jsonx.Obj) *jsonx.Obj {
	aiFrames := make([]ai.AssistantMessageFrame, 0, len(frames))
	for _, frame := range frames {
		decoded, err := ai.FrameFromJSON(frame)
		if err != nil {
			continue
		}
		aiFrames = append(aiFrames, decoded)
	}
	reduced := ai.ReduceAssistantMessageFrames(aiFrames)
	return assistantToJSON(reduced)
}

func assistantToJSON(message *ai.AssistantMessage) *jsonx.Obj {
	obj := jsonx.NewObj()
	obj.Set("role", "assistant")
	contentBlocks := make([]any, 0, len(message.Content))
	for _, block := range message.Content {
		contentBlocks = append(contentBlocks, blockToJSON(block))
	}
	obj.Set("content", contentBlocks)
	obj.Set("api", message.API)
	obj.Set("provider", message.Provider)
	obj.Set("model", message.Model)
	obj.Set("stopReason", message.StopReason)
	return obj
}

// RecoveryOutcome captures the procedure result after settling.
type RecoveryOutcome struct {
	// Settled reports the orphaned request was settled.
	Settled bool
	// Message is the interrupted assistant message.
	Message *jsonx.Obj
}

// SettleRecovered mirrors the settle flow decision: emit recovery events
// then publish the response (the response.ts port wires the durable
// writes); here the outcome captures what publish receives.
func SettleRecovered(provider, modelID string, frames []*jsonx.Obj) *RecoveryOutcome {
	message := RecoverAssistantMessage(provider, modelID, frames)
	return &RecoveryOutcome{Settled: true, Message: message}
}

// keep the session import referenced.
var _ session.JsonValue = nil

// blockToJSON renders one content block through its JSON form.
func blockToJSON(block ai.ContentBlock) any {
	return jsonx.Stringify(block)
}
