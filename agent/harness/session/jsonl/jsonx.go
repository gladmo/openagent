package jsonl

import "github.com/gladmo/openagent/jsonx"

func jsonxObj() *jsonx.Obj        { return jsonx.NewObj() }
func jsonxStringify(v any) string { return jsonx.Stringify(v) }
