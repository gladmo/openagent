package pico3

import chordjson "github.com/gladmo/openagent/chord"

func chordCopy(v any) (JsonValue, error) { return chordjson.CopyJson(v, nil) }
