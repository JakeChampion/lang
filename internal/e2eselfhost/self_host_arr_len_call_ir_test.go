package e2eselfhost

import "testing"

// arrLenCallIRCases exercise calling a builtin array method (`.len()`) DIRECTLY
// on a function-call result that returns a STRUCT array (`P[]`) — no intermediate
// local. Before this, irlower's `.len()` guard consulted expr_struct_type, which
// reports the ELEMENT type "P" for a `P[]`-returning call (it strips the `[]` for
// `mk()[i].field` recovery, #3035). That made `decl_is_struct` true, so `.len()`
// was mis-routed to a (nonexistent) `P.len` user method and the whole module
// bailed the module. A struct-array LOCAL was already fine (its arr slot
// reports ""), so only the direct-call form was affected — the exact shape
// std/regex's `regex_count` (`regex_find_all(p, t).len()`) uses. The fix treats
// an array-source receiver as the builtin array length regardless of the
// element-type leak. Oracle = the native interpreter.
var arrLenCallIRCases = []struct {
	name string
	main string
}{
	// The bare gap: `.len()` on a struct-array call result.
	{"struct-arr-len", `struct S { x: i32 }
function mkS(): S[] { return [S { x: 1 }, S { x: 2 }, S { x: 3 }]; }
function main(): i32 { return mkS().len(); }`},
	// Two calls combined, to show it is not a one-shot fluke.
	{"struct-arr-len-twice", `struct S { x: i32 }
function mkS(): S[] { return [S { x: 1 }, S { x: 2 }, S { x: 3 }, S { x: 4 }]; }
function main(): i32 { return mkS().len() * 10 + mkS().len(); }`},
	// A multi-field struct element, to confirm the element layout is irrelevant
	// to reading the array header length.
	{"named-struct-arr-len", `struct Pair { a: i32, b: i32 }
function mk(): Pair[] { return [Pair { a: 1, b: 2 }, Pair { a: 3, b: 4 }]; }
function main(): i32 { return mk().len() + 40; }`},
	// Regression guard: a struct VALUE with a user-defined `.len()` method must
	// STILL dispatch to that method (not the array-length builtin) — the case the
	// original guard protected (#3478).
	{"struct-value-user-len", `struct Box { v: i32 }
function (b: Box) len(): i32 { return b.v + 100; }
function mk(): Box { return Box { v: 5 }; }
function main(): i32 { let b: Box = mk(); return b.len(); }`},
}

// TestSelfHostArrLenCallIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostArrLenCallIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range arrLenCallIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
