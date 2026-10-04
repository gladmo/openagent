package mcp

import (
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func mustParse(t *testing.T, text string) any {
	t.Helper()
	value, err := jsonx.Parse(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return value
}

func TestParseJsonRpcMessageShapes(t *testing.T) {
	request, err := ParseJsonRpcMessage(mustParse(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil || request.Kind != KindRequest || request.Method() != "tools/list" {
		t.Fatalf("request parse: %v %v", request, err)
	}
	if got := request.ID().String(); got != "1" {
		t.Fatalf("id = %q", got)
	}
	notification, err := ParseJsonRpcMessage(mustParse(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if err != nil || notification.Kind != KindNotification {
		t.Fatalf("notification parse: %v %v", notification, err)
	}
	if notification.HasID() {
		t.Fatal("notification must not carry an id")
	}
	// A null id is not a valid id, so the message is not a request.
	if _, err := ParseJsonRpcMessage(mustParse(t, `{"jsonrpc":"2.0","id":null,"method":"x"}`)); err == nil {
		t.Fatal("null id must be rejected")
	}
	// An id on a response-less shape makes it a request, not a notification.
	if message, err := ParseJsonRpcMessage(mustParse(t, `{"jsonrpc":"2.0","id":"a","method":"x"}`)); err != nil || message.Kind != KindRequest {
		t.Fatalf("string id request: %v %v", message, err)
	}
	success, err := ParseJsonRpcMessage(mustParse(t, `{"jsonrpc":"2.0","id":7,"result":{"ok":true}}`))
	if err != nil || success.Kind != KindSuccessResponse {
		t.Fatalf("success parse: %v %v", success, err)
	}
	if result, _ := success.Result().(*jsonx.Obj); result.MustGet("ok") != true {
		t.Fatal("result not preserved")
	}
	failure, err := ParseJsonRpcMessage(mustParse(t, `{"jsonrpc":"2.0","id":"x","error":{"code":-32601,"message":"nope"}}`))
	if err != nil || failure.Kind != KindErrorResponse {
		t.Fatalf("error parse: %v %v", failure, err)
	}
	if errorObject := failure.ErrorObject(); errorObject.Code != -32601 || errorObject.Message != "nope" {
		t.Fatalf("error object: %+v", errorObject)
	}
	// result and error together is invalid.
	if _, err := ParseJsonRpcMessage(mustParse(t, `{"jsonrpc":"2.0","id":1,"result":{},"error":{"code":1,"message":"x"}}`)); err == nil {
		t.Fatal("result+error must be rejected")
	}
	if _, err := ParseJsonRpcMessage(mustParse(t, `{"jsonrpc":"1.0","id":1,"method":"x"}`)); err == nil {
		t.Fatal("wrong version must be rejected")
	}
	if _, err := ParseJsonRpcText(`{"jsonrpc":"2.0","id":2,`); err == nil {
		t.Fatal("broken JSON must be rejected")
	}
}

func TestMessageConstructionRoundTrip(t *testing.T) {
	params := jsonx.ObjFrom("cursor", "page-2")
	request := NewRequest(NewNumberID(3), "tools/list", params)
	encoded := jsonx.Stringify(request.Raw)
	if encoded != `{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"cursor":"page-2"}}` {
		t.Fatalf("request encoded as %s", encoded)
	}
	notification := NewNotification("notifications/cancelled", jsonx.ObjFrom("requestId", float64(3)))
	if _, has := notification.Raw.Get("id"); has {
		t.Fatal("notification must not carry an id")
	}
	response := NewSuccessResponse(NewStringID("a"), nil)
	if _, err := ParseJsonRpcMessage(mustParse(t, jsonx.Stringify(response.Raw))); err != nil {
		t.Fatalf("response round trip: %v", err)
	}
	errorResponse := NewErrorResponse(NewNumberID(9), -32603, "boom", nil)
	if errorResponse.ErrorObject().Message != "boom" {
		t.Fatal("error response object")
	}
}
