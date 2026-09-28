package session

import (
	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/ai"
)

type aiUsage = ai.Usage

func harnessAddUsage(left, right ai.Usage) ai.Usage { return harness.AddUsage(left, right) }
