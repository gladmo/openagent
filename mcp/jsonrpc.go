package mcp

// Port of packages/mcp/src/protocol/jsonrpc.ts: JSON-RPC message model over
// the jsonx JSON value model (nil | bool | float64 | string | []any |
// *jsonx.Obj), structural validation, and the MCP error types.

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/gladmo/openagent/jsonx"
)

// JsonRpcID is a JSON-RPC message id: a string or a finite number.
type JsonRpcID struct {
	IsString bool
	Str      string
	Num      float64
}

// NewStringID builds a string id.
func NewStringID(value string) JsonRpcID { return JsonRpcID{IsString: true, Str: value} }

// NewNumberID builds a numeric id.
func NewNumberID(value float64) JsonRpcID { return JsonRpcID{Num: value} }

// Equal reports whether both ids are the same JSON-RPC id.
func (id JsonRpcID) Equal(other JsonRpcID) bool {
	if id.IsString != other.IsString {
		return false
	}
	if id.IsString {
		return id.Str == other.Str
	}
	return id.Num == other.Num
}

// Value returns the id as a JSON value.
func (id JsonRpcID) Value() any {
	if id.IsString {
		return id.Str
	}
	return id.Num
}

// String renders the id like `String(message.id)` in the reference.
func (id JsonRpcID) String() string {
	if id.IsString {
		return id.Str
	}
	if id.Num == math.Trunc(id.Num) && math.Abs(id.Num) < 1e15 {
		return strconv.FormatInt(int64(id.Num), 10)
	}
	return strconv.FormatFloat(id.Num, 'g', -1, 64)
}

// JSON_RPC_ERROR_CODES mirrors the JSON-RPC 2.0 reserved error codes.
var JSON_RPC_ERROR_CODES = struct {
	ParseError     int
	InvalidRequest int
	MethodNotFound int
	InvalidParams  int
	InternalError  int
}{
	ParseError:     -32700,
	InvalidRequest: -32600,
	MethodNotFound: -32601,
	InvalidParams:  -32602,
	InternalError:  -32603,
}

// McpError is a JSON-RPC error response payload or a protocol-level
// rejection. Code carries the JSON-RPC error code; Data is optional.
type McpError struct {
	Code    int
	Message string
	Data    any
}

func (e *McpError) Error() string { return e.Message }

// NewMcpError builds an McpError.
func NewMcpError(code int, message string, data any) *McpError {
	return &McpError{Code: code, Message: message, Data: data}
}

// McpConnectionClosedError reports an operation on a closed connection.
type McpConnectionClosedError struct{ Message string }

func (e *McpConnectionClosedError) Error() string {
	if e.Message == "" {
		return "MCP connection closed"
	}
	return e.Message
}

// NewMcpConnectionClosedError builds the error with a context message.
func NewMcpConnectionClosedError(message string) *McpConnectionClosedError {
	return &McpConnectionClosedError{Message: message}
}

// McpTimeoutError reports a request that exceeded its timeout.
type McpTimeoutError struct{ TimeoutMs int }

func (e *McpTimeoutError) Error() string {
	return fmt.Sprintf("MCP request timed out after %dms", e.TimeoutMs)
}

// McpAbortError reports a request aborted by its caller.
type McpAbortError struct{}

func (e *McpAbortError) Error() string { return "MCP request aborted" }

// MessageKind discriminates the JSON-RPC message shapes.
type MessageKind int

const (
	KindRequest MessageKind = iota
	KindNotification
	KindSuccessResponse
	KindErrorResponse
)

// JsonRpcMessage is one parsed JSON-RPC message. Raw keeps the whole object
// so unknown fields survive round-trips through transports.
type JsonRpcMessage struct {
	Kind MessageKind
	Raw  *jsonx.Obj
}

// ID returns the message id. Requests and responses always have one.
func (m *JsonRpcMessage) ID() JsonRpcID {
	value, _ := m.Raw.Get("id")
	return idOf(value)
}

// HasID reports whether an id field is present (notifications do not have
// one even when it would be null).
func (m *JsonRpcMessage) HasID() bool { return m.Raw.Has("id") }

// Method returns the method of a request or notification.
func (m *JsonRpcMessage) Method() string {
	value, _ := m.Raw.Get("method")
	text, _ := value.(string)
	return text
}

// Params returns the params value of a request or notification (nil when
// absent).
func (m *JsonRpcMessage) Params() any {
	value, _ := m.Raw.Get("params")
	return value
}

// ParamsObj returns the params value as an object, or nil.
func (m *JsonRpcMessage) ParamsObj() *jsonx.Obj {
	value, _ := m.Raw.Get("params")
	obj, _ := value.(*jsonx.Obj)
	return obj
}

// Result returns the result of a success response.
func (m *JsonRpcMessage) Result() any {
	value, _ := m.Raw.Get("result")
	return value
}

// ErrorObject returns the error payload of an error response.
func (m *JsonRpcMessage) ErrorObject() *JsonRpcErrorObject {
	value, _ := m.Raw.Get("error")
	obj, _ := value.(*jsonx.Obj)
	if obj == nil {
		return nil
	}
	code, _ := obj.MustGet("code").(float64)
	message, _ := obj.MustGet("message").(string)
	data, hasData := obj.Get("data")
	errorObject := &JsonRpcErrorObject{Code: int(code), Message: message}
	if hasData {
		errorObject.Data = data
	}
	return errorObject
}

// JsonRpcErrorObject is the error member of an error response.
type JsonRpcErrorObject struct {
	Code    int
	Message string
	Data    any
}

// hasJSONValue treats typed-nil objects as absent, since Go callers pass
// (*jsonx.Obj)(nil) where the reference passes undefined.
func hasJSONValue(value any) bool {
	if value == nil {
		return false
	}
	if obj, isObj := value.(*jsonx.Obj); isObj && obj == nil {
		return false
	}
	return true
}

// NewRequest builds a request message.
func NewRequest(id JsonRpcID, method string, params any) *JsonRpcMessage {
	obj := jsonx.ObjFrom("jsonrpc", "2.0", "id", id.Value(), "method", method)
	if hasJSONValue(params) {
		obj.Set("params", params)
	}
	return &JsonRpcMessage{Kind: KindRequest, Raw: obj}
}

// NewNotification builds a notification message.
func NewNotification(method string, params any) *JsonRpcMessage {
	obj := jsonx.ObjFrom("jsonrpc", "2.0", "method", method)
	if hasJSONValue(params) {
		obj.Set("params", params)
	}
	return &JsonRpcMessage{Kind: KindNotification, Raw: obj}
}

// NewSuccessResponse builds a success response message.
func NewSuccessResponse(id JsonRpcID, result any) *JsonRpcMessage {
	if !hasJSONValue(result) {
		result = jsonx.NewObj()
	}
	return &JsonRpcMessage{
		Kind: KindSuccessResponse,
		Raw:  jsonx.ObjFrom("jsonrpc", "2.0", "id", id.Value(), "result", result),
	}
}

// NewErrorResponse builds an error response message.
func NewErrorResponse(id JsonRpcID, code int, message string, data any) *JsonRpcMessage {
	errorObject := jsonx.ObjFrom("code", float64(code), "message", message)
	if data != nil {
		errorObject.Set("data", data)
	}
	return &JsonRpcMessage{
		Kind: KindErrorResponse,
		Raw:  jsonx.ObjFrom("jsonrpc", "2.0", "id", id.Value(), "error", errorObject),
	}
}

// isObject reports whether the value is a non-null, non-array object.
func isObject(value any) (*jsonx.Obj, bool) {
	obj, ok := value.(*jsonx.Obj)
	return obj, ok
}

// toError converts a thrown value to an error.
func toError(value any) error {
	switch typed := value.(type) {
	case error:
		return typed
	case string:
		return errors.New(typed)
	case nil:
		return errors.New("undefined")
	}
	return errors.New(stringifyValue(value))
}

func stringifyValue(value any) string { return jsonx.Stringify(value) }

// isJsonRpcID reports whether the value is a string or finite number.
func isJsonRpcID(value any) (JsonRpcID, bool) {
	switch typed := value.(type) {
	case string:
		return NewStringID(typed), true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return JsonRpcID{}, false
		}
		return NewNumberID(typed), true
	}
	return JsonRpcID{}, false
}

func idOf(value any) JsonRpcID {
	id, ok := isJsonRpcID(value)
	if !ok {
		return JsonRpcID{}
	}
	return id
}

// isJsonRpcRequestShape validates the request message shape.
func isJsonRpcRequestShape(obj *jsonx.Obj) bool {
	version, _ := obj.Get("jsonrpc")
	_, hasID := isJsonRpcID(mustGet(obj, "id"))
	_, hasMethod := obj.MustGet("method").(string)
	return version == "2.0" && hasID && hasMethod
}

func mustGet(obj *jsonx.Obj, key string) any {
	value, _ := obj.Get(key)
	return value
}

// isJsonRpcNotificationShape validates the notification message shape.
func isJsonRpcNotificationShape(obj *jsonx.Obj) bool {
	version, _ := obj.Get("jsonrpc")
	_, hasMethod := obj.MustGet("method").(string)
	return version == "2.0" && !obj.Has("id") && hasMethod
}

// isJsonRpcResponseShape validates the response message shapes.
func isJsonRpcResponseShape(obj *jsonx.Obj) bool {
	version, _ := obj.Get("jsonrpc")
	if version != "2.0" {
		return false
	}
	if _, ok := isJsonRpcID(mustGet(obj, "id")); !ok {
		return false
	}
	if obj.Has("result") {
		return !obj.Has("error")
	}
	errorValue, _ := obj.Get("error")
	errorObject, ok := errorValue.(*jsonx.Obj)
	if !ok {
		return false
	}
	_, codeIsNumber := errorObject.MustGet("code").(float64)
	_, messageIsString := errorObject.MustGet("message").(string)
	return codeIsNumber && messageIsString
}

// ClassifyMessage returns the message kind, or false when the value is not
// a JSON-RPC message.
func ClassifyMessage(value any) (MessageKind, bool) {
	obj, ok := isObject(value)
	if !ok {
		return 0, false
	}
	switch {
	case isJsonRpcRequestShape(obj):
		return KindRequest, true
	case isJsonRpcNotificationShape(obj):
		return KindNotification, true
	case isJsonRpcResponseShape(obj):
		if obj.Has("error") {
			return KindErrorResponse, true
		}
		return KindSuccessResponse, true
	}
	return 0, false
}

// IsJsonRpcRequest reports whether the value is a request message.
func IsJsonRpcRequest(value any) bool {
	obj, ok := isObject(value)
	return ok && isJsonRpcRequestShape(obj)
}

// IsJsonRpcNotification reports whether the value is a notification.
func IsJsonRpcNotification(value any) bool {
	obj, ok := isObject(value)
	return ok && isJsonRpcNotificationShape(obj)
}

// IsJsonRpcResponse reports whether the value is a success or error
// response.
func IsJsonRpcResponse(value any) bool {
	obj, ok := isObject(value)
	return ok && isJsonRpcResponseShape(obj)
}

// ParseJsonRpcMessage validates a parsed JSON value as a JSON-RPC message.
func ParseJsonRpcMessage(value any) (*JsonRpcMessage, error) {
	if kind, ok := ClassifyMessage(value); ok {
		obj, _ := isObject(value)
		return &JsonRpcMessage{Kind: kind, Raw: obj}, nil
	}
	return nil, NewMcpError(JSON_RPC_ERROR_CODES.InvalidRequest, "Invalid JSON-RPC message", nil)
}

// ParseJsonRpcText parses JSON text and validates it as one JSON-RPC
// message.
func ParseJsonRpcText(text string) (*JsonRpcMessage, error) {
	value, err := jsonx.Parse(strings.TrimRight(text, "\r\n"))
	if err != nil {
		return nil, toError(err)
	}
	return ParseJsonRpcMessage(value)
}
