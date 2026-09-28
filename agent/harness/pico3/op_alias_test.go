package pico3

import (
	chorddelta "github.com/gladmo/openagent/chord/delta"
	"github.com/gladmo/openagent/jsonx"
)

type replaceOpAlias = chorddelta.Replace
type setOpAlias = chorddelta.Set

func pathOf(segments ...string) chorddelta.Path {
	path := make(chorddelta.Path, 0, len(segments))
	for _, s := range segments {
		path = append(path, s)
	}
	return path
}

var _ = jsonx.NewObj
