package harness

import "encoding/json"

func jsonMarshalForTest(v any) ([]byte, error) { return json.Marshal(v) }
