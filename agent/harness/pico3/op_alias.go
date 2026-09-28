package pico3

import chorddelta "github.com/gladmo/openagent/chord/delta"

type opAlias = chorddelta.Op

func opIsReplace(op opAlias) bool { return chorddelta.IsReplace(op) }

func opPath(op opAlias) chorddelta.Path {
	switch t := op.(type) {
	case *chorddelta.Set:
		return t.Path
	case *chorddelta.Delete:
		return t.Path
	case *chorddelta.Append:
		return t.Path
	case *chorddelta.Truncate:
		return t.Path
	case *chorddelta.Splice:
		return t.Path
	case *chorddelta.Permute:
		return t.Path
	}
	return nil
}
