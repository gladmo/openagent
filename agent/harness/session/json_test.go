package session

import "github.com/gladmo/openagent/jsonx"

func parseJSONForTest(s string) *jsonx.Obj {
	v, err := jsonx.Parse(s)
	if err != nil {
		panic(err)
	}
	return v.(*jsonx.Obj)
}

type jsonxObjAlias = jsonx.Obj
