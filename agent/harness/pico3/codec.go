package pico3

import (
	"encoding/json"

	"github.com/gladmo/openagent/jsonx"
)

type jsonxObjPtr = jsonx.Obj

func jsonxNewObj() *jsonx.Obj           { return jsonx.NewObj() }
func jsonxStringifyHelper(v any) string { return jsonx.Stringify(v) }
func jsonxParse(s string) (any, error)  { return jsonx.Parse(s) }

// valueToJSON converts a Go struct carrier into a jsonx value via its JSON
// round trip (carriers hold only plain fields + jsonx values).
func valueToJSON(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	parsed, err := jsonx.Parse(string(data))
	if err != nil {
		return nil
	}
	return parsed
}

// jsonDecodeInto converts a jsonx value back into a struct carrier.
func jsonDecodeInto(v any, target any) any {
	data := jsonx.Stringify(v)
	if err := json.Unmarshal([]byte(data), target); err != nil {
		return target
	}
	return target
}
