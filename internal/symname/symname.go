// Package symname is the asm symbol-naming convention for Fern functions on
// the native backends.
//
// A Fern identifier and an assembler's own vocabulary share one namespace.
// Emitted bare, a function named `cs` becomes a segment-register operand
// (`call cs`), `r16` becomes an APX register that encodes a clean indirect
// call with no diagnostic at all, and `__fern_alloc` becomes a second
// definition of the runtime helper. Which of those a program hits is not
// discoverable from the Fern source, and the two assembler paths do not even
// agree on it: this project's in-process assembler reads those tokens as the
// symbols they are and builds a (wrong) binary, while GNU `as` rejects or
// silently mis-encodes them (#6022).
//
// Prefixing every Fern function symbol removes the overlap rather than
// enumerating it: no `__fn_`-prefixed token is a register, a keyword, an
// operator, or a runtime helper, so no Fern identifier can reach any of them.
// The self-host emitters have always mangled this way, so native symbol names
// now match theirs.
package symname

import "strings"

// Prefix marks a symbol as a Fern function's. Runtime helpers, the process
// entry point, and every compiler-internal label sit outside it, so the two
// namespaces cannot collide.
const Prefix = "__fn_"

// Fn returns the asm symbol for the Fern function `name`. It is injective,
// so distinct functions keep distinct symbols.
func Fn(name string) string { return Prefix + name }

// Source returns the Fern function name behind an asm symbol, and reports
// whether `sym` named one at all. Debug info describes the source, so it
// carries this rather than the emitted symbol.
func Source(sym string) (string, bool) {
	return strings.CutPrefix(sym, Prefix)
}

// Fns mangles a list of Fern function names, preserving order. Export lists
// arrive as Fern names and have to be looked up as asm symbols.
func Fns(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = Fn(n)
	}
	return out
}
