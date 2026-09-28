package ai

import (
	"fmt"

	"github.com/gladmo/openagent/jsonx"
)

// MessageToJSON converts a message to its jsonx object form with the exact TS
// field order (role first, timestamp last, optional fields omitted when
// absent).
func MessageToJSON(m Message) *jsonx.Obj {
	switch t := m.(type) {
	case *SystemMessage:
		o := jsonx.NewObj()
		o.Set("role", "system")
		setContent(o, t.Content)
		if t.HasSections {
			sections := jsonx.NewObj()
			for _, name := range t.SectionOrder {
				v := t.Sections[name]
				if v == nil {
					sections.Set(name, nil)
				} else {
					sections.Set(name, *v)
				}
			}
			o.Set("sections", sections)
		}
		if len(t.ToolsAdded) > 0 {
			tools := make([]any, 0, len(t.ToolsAdded))
			for _, tool := range t.ToolsAdded {
				tools = append(tools, ToolToJSON(tool))
			}
			o.Set("toolsAdded", tools)
		}
		if len(t.ToolsRemoved) > 0 {
			refs := make([]any, 0, len(t.ToolsRemoved))
			for _, ref := range t.ToolsRemoved {
				r := jsonx.NewObj()
				r.Set("name", ref.Name)
				refs = append(refs, r)
			}
			o.Set("toolsRemoved", refs)
		}
		o.Set("timestamp", t.TimestampMs)
		return o
	case *UserMessage:
		o := jsonx.NewObj()
		o.Set("role", "user")
		setContent(o, t.Content)
		o.Set("timestamp", t.TimestampMs)
		return o
	case *AssistantMessage:
		o := jsonx.NewObj()
		o.Set("role", "assistant")
		o.Set("content", blocksToJSON(t.Content))
		o.Set("api", t.API)
		o.Set("provider", t.Provider)
		o.Set("model", t.Model)
		if t.ResponseModel != nil {
			o.Set("responseModel", *t.ResponseModel)
		}
		if t.ResponseID != nil {
			o.Set("responseId", *t.ResponseID)
		}
		if t.ProviderThinkingLevel != nil {
			o.Set("providerThinkingLevel", *t.ProviderThinkingLevel)
		}
		if len(t.Diagnostics) > 0 {
			diags := make([]any, 0, len(t.Diagnostics))
			for _, d := range t.Diagnostics {
				diags = append(diags, diagnosticToJSON(d))
			}
			o.Set("diagnostics", diags)
		}
		o.Set("usage", usageToJSON(t.Usage))
		o.Set("stopReason", t.StopReason)
		if t.Deferred != nil {
			o.Set("deferred", deferredToJSON(t.Deferred))
		}
		if t.ErrorMessage != nil {
			o.Set("errorMessage", *t.ErrorMessage)
		}
		if t.RawStopReason != nil {
			o.Set("rawStopReason", *t.RawStopReason)
		}
		if t.EndTurn != nil {
			o.Set("endTurn", *t.EndTurn)
		}
		o.Set("timestamp", t.TimestampMs)
		return o
	case *ToolResultMessage:
		o := jsonx.NewObj()
		o.Set("role", "toolResult")
		o.Set("toolCallId", t.ToolCallID)
		o.Set("toolName", t.ToolName)
		o.Set("content", blocksToJSON(t.Content))
		if t.Details != nil {
			o.Set("details", t.Details)
		}
		if t.Usage != nil {
			o.Set("usage", usageToJSON(*t.Usage))
		}
		o.Set("isError", t.IsError)
		o.Set("timestamp", t.TimestampMs)
		return o
	default:
		return jsonx.NewObj()
	}
}

func setContent(o *jsonx.Obj, c Content) {
	if c.IsText {
		o.Set("content", c.Text)
	} else {
		o.Set("content", blocksToJSON(c.Blocks))
	}
}

func blocksToJSON(blocks []ContentBlock) []any {
	out := make([]any, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, blockToJSON(b))
	}
	return out
}

func blockToJSON(b ContentBlock) *jsonx.Obj {
	o := jsonx.NewObj()
	switch t := b.(type) {
	case TextContent:
		o.Set("type", "text")
		o.Set("text", t.Text)
		if t.TextSignature != nil {
			o.Set("textSignature", *t.TextSignature)
		}
	case ThinkingContent:
		o.Set("type", "thinking")
		o.Set("thinking", t.Thinking)
		if t.ThinkingSignature != nil {
			o.Set("thinkingSignature", *t.ThinkingSignature)
		}
		if t.Redacted != nil {
			o.Set("redacted", *t.Redacted)
		}
	case ImageContent:
		o.Set("type", "image")
		o.Set("data", t.Data)
		o.Set("mimeType", t.MimeType)
	case *ToolCall:
		o.Set("type", "toolCall")
		o.Set("id", t.ID)
		o.Set("name", t.Name)
		if t.Arguments == nil {
			o.Set("arguments", jsonx.NewObj())
		} else {
			o.Set("arguments", t.Arguments)
		}
		if t.ThoughtSignature != nil {
			o.Set("thoughtSignature", *t.ThoughtSignature)
		}
		if t.Namespace != nil {
			o.Set("namespace", *t.Namespace)
		}
	}
	return o
}

func usageToJSON(u Usage) *jsonx.Obj {
	o := jsonx.NewObj()
	o.Set("input", u.Input)
	o.Set("output", u.Output)
	o.Set("cacheRead", u.CacheRead)
	o.Set("cacheWrite", u.CacheWrite)
	if u.CacheWrite1h != nil {
		o.Set("cacheWrite1h", *u.CacheWrite1h)
	}
	if u.Reasoning != nil {
		o.Set("reasoning", *u.Reasoning)
	}
	o.Set("totalTokens", u.TotalTokens)
	cost := jsonx.NewObj()
	cost.Set("input", u.Cost.Input)
	cost.Set("output", u.Cost.Output)
	cost.Set("cacheRead", u.Cost.CacheRead)
	cost.Set("cacheWrite", u.Cost.CacheWrite)
	cost.Set("total", u.Cost.Total)
	o.Set("cost", cost)
	return o
}

func deferredToJSON(d *DeferredHandle) *jsonx.Obj {
	o := jsonx.NewObj()
	o.Set("provider", d.Provider)
	o.Set("modelId", d.ModelID)
	o.Set("api", d.API)
	o.Set("id", d.ID)
	if d.ExpiresAt != nil {
		o.Set("expiresAt", *d.ExpiresAt)
	}
	if d.PollAfterMs != nil {
		o.Set("pollAfterMs", *d.PollAfterMs)
	}
	if d.Data != nil {
		o.Set("data", d.Data)
	}
	return o
}

func diagnosticToJSON(d AssistantMessageDiagnostic) *jsonx.Obj {
	o := jsonx.NewObj()
	o.Set("type", d.Type)
	o.Set("timestamp", d.Timestamp)
	if d.Error != nil {
		e := jsonx.NewObj()
		if d.Error.Name != nil {
			e.Set("name", *d.Error.Name)
		}
		e.Set("message", d.Error.Message)
		if d.Error.Stack != nil {
			e.Set("stack", *d.Error.Stack)
		}
		if d.Error.Code != nil {
			e.Set("code", d.Error.Code)
		}
		o.Set("error", e)
	}
	if d.Details != nil {
		o.Set("details", d.Details)
	}
	return o
}

// ToolToJSON serializes a tool the way toToolDeclaration does: a JSON
// round-trip of {name, description, parameters}.
func ToolToJSON(t Tool) *jsonx.Obj {
	o := jsonx.NewObj()
	o.Set("name", t.Name)
	o.Set("description", t.Description)
	if t.Parameters != nil {
		o.Set("parameters", t.Parameters.JSON())
	} else {
		o.Set("parameters", jsonx.NewObj())
	}
	return o
}

// ---------------------------------------------------------------------------
// Decoding
// ---------------------------------------------------------------------------

// MessageFromJSON converts a decoded jsonx object into a Message, keyed on
// the role discriminator.
func MessageFromJSON(v any) (Message, error) {
	obj, ok := v.(*jsonx.Obj)
	if !ok {
		return nil, fmt.Errorf("message is not an object")
	}
	role, _ := obj.Get("role")
	switch role {
	case "system":
		return systemMessageFromJSON(obj)
	case "user":
		return userMessageFromJSON(obj)
	case "assistant":
		return assistantMessageFromJSON(obj)
	case "toolResult":
		return toolResultMessageFromJSON(obj)
	default:
		return nil, fmt.Errorf("unknown message role: %v", role)
	}
}

func contentFromJSON(v any) Content {
	if s, ok := v.(string); ok {
		return StringContent(s)
	}
	arr, ok := v.([]any)
	if !ok {
		return StringContent("")
	}
	blocks := make([]ContentBlock, 0, len(arr))
	for _, e := range arr {
		if b, ok := blockFromJSON(e); ok {
			blocks = append(blocks, b)
		}
	}
	return BlocksContent(blocks...)
}

// blockFromJSON decodes persisted content blocks. Missing or wrong-typed
// fields decode as zero values (TS reads them as undefined); decoding
// crashes on nothing — this runs inside crash recovery.
func blockFromJSON(v any) (ContentBlock, bool) {
	obj, ok := v.(*jsonx.Obj)
	if !ok {
		return nil, false
	}
	typ, _ := obj.Get("type")
	switch typ {
	case "text":
		tc := TextContent{Text: stringField(obj, "text")}
		if sig, ok := obj.Get("textSignature"); ok {
			tc.TextSignature = strPtr(sig)
		}
		return tc, true
	case "thinking":
		tc := ThinkingContent{Thinking: stringField(obj, "thinking")}
		if sig, ok := obj.Get("thinkingSignature"); ok {
			tc.ThinkingSignature = strPtr(sig)
		}
		if red, ok := obj.Get("redacted"); ok {
			if b, ok := red.(bool); ok {
				tc.Redacted = &b
			}
		}
		return tc, true
	case "image":
		return ImageContent{Data: stringField(obj, "data"), MimeType: stringField(obj, "mimeType")}, true
	case "toolCall":
		tc := &ToolCall{ID: stringField(obj, "id"), Name: stringField(obj, "name")}
		if args, ok := obj.Get("arguments"); ok {
			if argsObj, ok := args.(*jsonx.Obj); ok {
				tc.Arguments = argsObj
			}
		}
		if tc.Arguments == nil {
			tc.Arguments = jsonx.NewObj()
		}
		if sig, ok := obj.Get("thoughtSignature"); ok {
			tc.ThoughtSignature = strPtr(sig)
		}
		if ns, ok := obj.Get("namespace"); ok {
			tc.Namespace = strPtr(ns)
		}
		return tc, true
	default:
		return nil, false
	}
}

func floatField(obj *jsonx.Obj, name string) float64 {
	if v, ok := obj.Get(name); ok {
		if f, ok := jsonx.ToFloat(v); ok {
			return f
		}
	}
	return 0
}

func systemMessageFromJSON(obj *jsonx.Obj) (*SystemMessage, error) {
	m := &SystemMessage{}
	if c, ok := obj.Get("content"); ok {
		m.Content = contentFromJSON(c)
	}
	if sections, ok := obj.Get("sections"); ok {
		if so, ok := sections.(*jsonx.Obj); ok {
			m.HasSections = true
			m.Sections = map[string]*string{}
			m.SectionOrder = so.Keys()
			for _, name := range m.SectionOrder {
				value := so.MustGet(name)
				if value == nil {
					m.Sections[name] = nil
				} else if s, ok := value.(string); ok {
					m.Sections[name] = &s
				}
			}
		}
	}
	if tools, ok := obj.Get("toolsAdded"); ok {
		if arr, ok := tools.([]any); ok {
			for _, e := range arr {
				if toolObj, ok := e.(*jsonx.Obj); ok {
					m.ToolsAdded = append(m.ToolsAdded, ToolFromJSON(toolObj))
				}
			}
		}
	}
	if refs, ok := obj.Get("toolsRemoved"); ok {
		if arr, ok := refs.([]any); ok {
			for _, e := range arr {
				if refObj, ok := e.(*jsonx.Obj); ok {
					if name, ok := refObj.Get("name"); ok {
						m.ToolsRemoved = append(m.ToolsRemoved, ToolReference{Name: name.(string)})
					}
				}
			}
		}
	}
	m.TimestampMs = floatField(obj, "timestamp")
	return m, nil
}

func userMessageFromJSON(obj *jsonx.Obj) (*UserMessage, error) {
	m := &UserMessage{}
	if c, ok := obj.Get("content"); ok {
		m.Content = contentFromJSON(c)
	}
	m.TimestampMs = floatField(obj, "timestamp")
	return m, nil
}

func assistantMessageFromJSON(obj *jsonx.Obj) (*AssistantMessage, error) {
	m := &AssistantMessage{}
	if c, ok := obj.Get("content"); ok {
		if arr, ok := c.([]any); ok {
			for _, e := range arr {
				if b, ok := blockFromJSON(e); ok {
					m.Content = append(m.Content, b)
				}
			}
		}
	}
	m.API = stringField(obj, "api")
	m.Provider = stringField(obj, "provider")
	m.Model = stringField(obj, "model")
	if v, ok := obj.Get("responseModel"); ok {
		m.ResponseModel = strPtr(v)
	}
	if v, ok := obj.Get("responseId"); ok {
		m.ResponseID = strPtr(v)
	}
	if v, ok := obj.Get("providerThinkingLevel"); ok {
		m.ProviderThinkingLevel = strPtr(v)
	}
	if diags, ok := obj.Get("diagnostics"); ok {
		if arr, ok := diags.([]any); ok {
			for _, e := range arr {
				if diagObj, ok := e.(*jsonx.Obj); ok {
					d := AssistantMessageDiagnostic{Type: stringField(diagObj, "type"), Timestamp: floatField(diagObj, "timestamp")}
					if errObj, ok := diagObj.Get("error"); ok {
						if eo, ok := errObj.(*jsonx.Obj); ok {
							info := &DiagnosticErrorInfo{Message: stringField(eo, "message")}
							if v, ok := eo.Get("name"); ok {
								info.Name = strPtr(v)
							}
							if v, ok := eo.Get("stack"); ok {
								info.Stack = strPtr(v)
							}
							if v, ok := eo.Get("code"); ok {
								info.Code = v
							}
							d.Error = info
						}
					}
					if det, ok := diagObj.Get("details"); ok {
						if do, ok := det.(*jsonx.Obj); ok {
							d.Details = do
						}
					}
					m.Diagnostics = append(m.Diagnostics, d)
				}
			}
		}
	}
	if u, ok := obj.Get("usage"); ok {
		if uo, ok := u.(*jsonx.Obj); ok {
			m.Usage = usageFromJSON(uo)
		}
	}
	m.StopReason = stringField(obj, "stopReason")
	if d, ok := obj.Get("deferred"); ok {
		if do, ok := d.(*jsonx.Obj); ok {
			handle := &DeferredHandle{
				Provider: stringField(do, "provider"),
				ModelID:  stringField(do, "modelId"),
				API:      stringField(do, "api"),
				ID:       stringField(do, "id"),
			}
			if v, ok := do.Get("expiresAt"); ok {
				if f, ok := jsonx.ToFloat(v); ok {
					handle.ExpiresAt = &f
				}
			}
			if v, ok := do.Get("pollAfterMs"); ok {
				if f, ok := jsonx.ToFloat(v); ok {
					handle.PollAfterMs = &f
				}
			}
			if v, ok := do.Get("data"); ok {
				handle.Data = v
			}
			m.Deferred = handle
		}
	}
	if v, ok := obj.Get("errorMessage"); ok {
		m.ErrorMessage = strPtr(v)
	}
	if v, ok := obj.Get("rawStopReason"); ok {
		m.RawStopReason = strPtr(v)
	}
	if v, ok := obj.Get("endTurn"); ok {
		if b, ok := v.(bool); ok {
			m.EndTurn = &b
		}
	}
	m.TimestampMs = floatField(obj, "timestamp")
	return m, nil
}

func toolResultMessageFromJSON(obj *jsonx.Obj) (*ToolResultMessage, error) {
	m := &ToolResultMessage{}
	m.ToolCallID = stringField(obj, "toolCallId")
	m.ToolName = stringField(obj, "toolName")
	if c, ok := obj.Get("content"); ok {
		if arr, ok := c.([]any); ok {
			for _, e := range arr {
				if b, ok := blockFromJSON(e); ok {
					m.Content = append(m.Content, b)
				}
			}
		}
	}
	if d, ok := obj.Get("details"); ok {
		m.Details = d
	}
	if u, ok := obj.Get("usage"); ok {
		if uo, ok := u.(*jsonx.Obj); ok {
			usage := usageFromJSON(uo)
			m.Usage = &usage
		}
	}
	if v, ok := obj.Get("isError"); ok {
		m.IsError, _ = v.(bool)
	}
	m.TimestampMs = floatField(obj, "timestamp")
	return m, nil
}

func usageFromJSON(obj *jsonx.Obj) Usage {
	u := Usage{
		Input:       floatField(obj, "input"),
		Output:      floatField(obj, "output"),
		CacheRead:   floatField(obj, "cacheRead"),
		CacheWrite:  floatField(obj, "cacheWrite"),
		TotalTokens: floatField(obj, "totalTokens"),
	}
	if v, ok := obj.Get("cacheWrite1h"); ok {
		if f, ok := jsonx.ToFloat(v); ok {
			u.CacheWrite1h = &f
		}
	}
	if v, ok := obj.Get("reasoning"); ok {
		if f, ok := jsonx.ToFloat(v); ok {
			u.Reasoning = &f
		}
	}
	if c, ok := obj.Get("cost"); ok {
		if co, ok := c.(*jsonx.Obj); ok {
			u.Cost = UsageCost{
				Input:      floatField(co, "input"),
				Output:     floatField(co, "output"),
				CacheRead:  floatField(co, "cacheRead"),
				CacheWrite: floatField(co, "cacheWrite"),
				Total:      floatField(co, "total"),
			}
		}
	}
	return u
}

func stringField(obj *jsonx.Obj, name string) string {
	if v, ok := obj.Get(name); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func strPtr(v any) *string {
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

// ToolFromJSON decodes a tool declaration.
func ToolFromJSON(obj *jsonx.Obj) Tool {
	t := Tool{Name: stringField(obj, "name"), Description: stringField(obj, "description")}
	if params, ok := obj.Get("parameters"); ok {
		if schema := schemaFromJSONValue(params); schema != nil {
			t.Parameters = schema
		}
	}
	return t
}

func schemaFromJSONValue(v any) *typeboxSchema {
	if obj, ok := v.(*jsonx.Obj); ok {
		return typeboxSchemaFromJSON(obj)
	}
	return nil
}
