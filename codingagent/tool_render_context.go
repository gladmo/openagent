package codingagent

import "github.com/gladmo/openagent/tui"

// toolRenderersAdapter exposes a definition's hooks as ToolRenderers.
type toolRenderersAdapter struct {
	definition *ToolDefinition
}

func (a *toolRenderersAdapter) RenderCall(args any) tui.Component {
	if a.definition.RenderCall == nil {
		return nil
	}
	return a.definition.RenderCall(args, &ToolRenderContext{
		Args:             args,
		ExecutionStarted: true,
		ArgsComplete:     true,
	})
}

func (a *toolRenderersAdapter) RenderResult(result any, options ToolRenderResultOptions) tui.Component {
	if a.definition.RenderResult == nil {
		return nil
	}
	typed, _ := result.(*ToolExecutionResult)
	return a.definition.RenderResult(typed, options, &ToolRenderContext{
		ExecutionStarted: true,
		ArgsComplete:     true,
		IsPartial:        options.IsPartial,
		Expanded:         options.Expanded,
	})
}

func (a *toolRenderersAdapter) RenderShell() string { return a.definition.RenderShell }
