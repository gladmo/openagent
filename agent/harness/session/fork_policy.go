package session

// fork_policy.go ports harness/session/fork-policy.ts.

import (
	"fmt"
	"strings"
)

// ForkCurrentStatePlan mirrors the TS union.
type ForkCurrentStatePlan struct {
	Scope          string // "branch" | "tree"
	Branch         string
	DestinationTip *string
}

// SelectBranchFork walks tip -> root, selecting entries on the ancestry.
func SelectBranchFork(options ForkOptions, source struct {
	Tip         *string
	GetParent   func(entryID string) *string
	SelectEntry func(entryID string)
	HasTip      bool
}) (ForkCurrentStatePlan, error) {
	if !source.HasTip {
		return ForkCurrentStatePlan{}, fmt.Errorf("Unknown source branch: %s", options.Branch)
	}
	var requested *string
	if options.EntryID != nil {
		requested = options.EntryID
	} else {
		requested = source.Tip
	}
	found := requested == nil
	var destinationTip *string
	entryID := source.Tip
	for entryID != nil {
		parentID := source.GetParent(*entryID)
		if parentID == nil {
			return ForkCurrentStatePlan{}, fmt.Errorf("Corrupt source branch: missing parent %s", *entryID)
		}
		if requested != nil && *entryID == *requested {
			found = true
			if options.Position == "before" {
				destinationTip = parentID
			} else {
				tip := *entryID
				destinationTip = &tip
				source.SelectEntry(*entryID)
			}
		} else if found {
			source.SelectEntry(*entryID)
		}
		entryID = parentID
	}
	if !found {
		if requested != nil {
			return ForkCurrentStatePlan{}, fmt.Errorf("Fork entry %s is not on source branch %s", *requested, options.Branch)
		}
		return ForkCurrentStatePlan{}, fmt.Errorf("Fork entry is not on source branch %s", options.Branch)
	}
	return ForkCurrentStatePlan{Scope: "branch", Branch: options.Branch, DestinationTip: destinationTip}, nil
}

// ProjectForkCurrentStateWrite projects one scalar row or surviving list
// element into the destination state (nil = drop).
func ProjectForkCurrentStateWrite(write CommittedWrite, plan ForkCurrentStatePlan, isEntryCopied func(entryID string) bool) *CommittedWrite {
	switch write.Namespace {
	case "pi.session.name":
		result := write
		return &result
	case "pi.entry.label":
		if isEntryCopied(write.Key) {
			result := write
			return &result
		}
		return nil
	case "pi.branch.tip":
		if plan.Scope == "tree" {
			result := write
			return &result
		}
		if write.Key == plan.Branch {
			result := write
			result.Value = nil
			if plan.DestinationTip != nil {
				tip := *plan.DestinationTip
				result.Value = tip
			}
			return &result
		}
		return nil
	case "pi.lane.config":
		if plan.Scope == "tree" || write.Key == plan.Branch {
			result := write
			return &result
		}
		return nil
	case "pi.lane.state":
		if plan.Scope == "tree" || write.Key == plan.Branch {
			result := write
			// Fresh idle state: {"currentOperationId":null,"lastOperationId":null,"inbox":[]}
			result.Value = idleLaneStateJSON()
			return &result
		}
		return nil
	case "pi.result":
		return nil
	}
	if strings.HasPrefix(write.Namespace, "pi.op.") || strings.HasPrefix(write.Namespace, "pi.pending.") {
		return nil
	}
	if write.Namespace == "pi" || strings.HasPrefix(write.Namespace, "pi.") {
		panic(fmt.Sprintf("Unknown reserved fork namespace: %s", write.Namespace))
	}
	if plan.Scope == "tree" {
		result := write
		return &result
	}
	return nil
}

func idleLaneStateJSON() JsonValue {
	obj := map[string]any{
		"currentOperationId": nil,
		"lastOperationId":    nil,
		"inbox":              []any{},
	}
	return obj
}
