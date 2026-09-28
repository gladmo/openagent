package nodejs

import (
	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

type agentToolResultAlias = agent.AgentToolResult
type abortSignalAlias = abort.Signal

func newAbortedSignalForTest() *abort.Signal {
	c := abort.NewController()
	c.Abort()
	return c.Signal()
}

func agentContentText(content []ai.ContentBlock) string {
	return ai.ContentText(ai.BlocksContent(content...), "\n")
}
