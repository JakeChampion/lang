package symname

import "strings"

const Prefix = "__fn_"

func Fn(name string) string { return Prefix + name }

func Source(sym string) (string, bool) {
	return strings.CutPrefix(sym, Prefix)
}
