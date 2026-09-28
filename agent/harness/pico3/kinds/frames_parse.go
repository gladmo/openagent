package kinds

import "github.com/gladmo/openagent/jsonx"

func jsonxParseHelper(s string) (any, error) { return jsonx.Parse(s) }
