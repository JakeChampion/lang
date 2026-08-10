// Materialising a call's returned array used to cost one reference per
// iteration (#6036). __fern_arr_push_grow mutates in place only at rc == 1, so
// a single stray retain turns every append in a threaded accumulator into a
// full-buffer copy: the program stays CORRECT while going O(n²) in bytes
// MOVED. Bytes RETAINED stay flat — the freelist recycles each copy — so
// __heap_bump_bytes() cannot see this at all. __arr_push_shared_count() can,
// and is the oracle here.
//
// Two independent emit sites produced the retain, and they are separable:
//
//   - the #4873 grow-containment bracket fired on an argument that does not
//     survive the call, because callArgDeaths recognised only the strict
//     `x = f(.., x, ..)` and `return f(.., x, ..)` shapes. `var t = f(b, v)`
//     is neither, so `b` was inc'd across the call purely to force the callee
//     onto its copy path — probe H below;
//   - the exit half of the consumed-array-param ownership-flag protocol was
//     gated on the static freeEligible taint, which a reassignment from a
//     CALL whose result may alias a param always clears. The flag was set, the
//     reassign's overwrite dec fired, the return-transfer inc fired, and the
//     matching exit dec was silently dropped — probes I and L.
//
// The controls matter as much as the fixes: A/B/C/F were already clean, and a
// change that drops H/I/L to zero while pushing any control off zero has
// relocated the problem rather than solved it.
package e2e

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// materialiseCase is one accumulator body threaded through the same driver.
// `g` is called 50 times as `acc = g(acc, i)`; `wantCliff` is the number of
// appends that copied a buffer with spare capacity.
type materialiseCase struct {
	name string
	g    string
	// appends per call: 1 → acc is [0..49]; 2 → acc is [0,1,1,2,…,49,50].
	appends   int
	wantCliff int
}

// materialiseCases. The residual group (J/K/M) is a DIFFERENT mechanism from
// the two fixed above and is pinned at its measured value rather than left
// unasserted — see the comment on the group below.
var materialiseCases = []materialiseCase{
	// Clean before and after — the controls.
	{"A_inline_append_tail", `function g(a: i32[], v: i32): i32[] { return a.append(v); }`, 1, 0},
	{"B_inline_append_rebind", `function g(a: i32[], v: i32): i32[] { a = a.append(v); return a; }`, 1, 0},
	{"C_call_tail", `function g(b: i32[], v: i32): i32[] { return f(b, v); }`, 1, 0},
	{"F_two_inline_appends", `function g(b: i32[], v: i32): i32[] { b = b.append(v); return b.append(v + 1); }`, 2, 0},

	// Fixed: the call result is materialised into a slot and handed back.
	// H is the grow-bracket half, I and L the exit-sweep half.
	{"H_call_result_into_local", `function g(b: i32[], v: i32): i32[] { var t: i32[] = f(b, v); return t; }`, 1, 0},
	{"I_call_result_into_param", `function g(b: i32[], v: i32): i32[] { b = f(b, v); return b; }`, 1, 0},
	{"L_two_calls_via_param", `function g(b: i32[], v: i32): i32[] { b = f(b, v); return f(b, v + 1); }`, 2, 0},

	// RESIDUAL (#6036 follow-up, work-list item 8): a SECOND append on a
	// temporary that was never rebound into the param slot. The rebind is
	// what releases the incoming borrow's count — via the ownership flag in
	// L, via the overwrite dec in F. Without it the caller's live reference
	// is still on the buffer when the second append runs, so rc == 2 and the
	// copy is conservatively CORRECT: only move-in semantics on the argument
	// (`own`) can retire it, not a change to where retains are emitted.
	//
	// Pinned at 49 deliberately. If these reach 0 the move-in work has
	// landed and they belong in the clean group above; a test that merely
	// tolerated any non-zero value would let that pass unnoticed, and would
	// equally hide a regression that made the residual worse.
	{"J_nested_call_arg", `function g(b: i32[], v: i32): i32[] { return f(f(b, v), v + 1); }`, 2, 49},
	{"K_two_calls_via_local", `function g(b: i32[], v: i32): i32[] { var t: i32[] = f(b, v); return f(t, v + 1); }`, 2, 49},
	{"M_call_then_inline_append", `function g(b: i32[], v: i32): i32[] { var t: i32[] = f(b, v); return t.append(v + 1); }`, 2, 49},
}

// src builds the driver. The length + element checks run before the counter is
// read, so a case that miscompiles reports 254/253 rather than a plausible
// cliff count — the counter is only meaningful on a program that computed the
// right answer.
func (c materialiseCase) src() string {
	wantLen := 50 * c.appends
	// Last element: 49 for one append per call, 50 for `v` then `v + 1`.
	wantLast := 49 + (c.appends - 1)
	return fmt.Sprintf(`function f(b: i32[], v: i32): i32[] { return b.append(v); }
%s
function main(): i32 {
    var acc: i32[] = [];
    var i: i32 = 0;
    while (i < 50) { acc = g(acc, i); i = i + 1; }
    if (acc.len() != %d) { return 254; }
    if (acc[0] != 0 || acc[%d] != %d) { return 253; }
    return __arr_push_shared_count();
}`, c.g, wantLen, wantLen-1, wantLast)
}

func (c materialiseCase) check(t *testing.T, backend string, got int) {
	t.Helper()
	switch got {
	case 254, 253:
		t.Errorf("%s %s: driver returned %d — the accumulator computed the WRONG contents; "+
			"the cliff count below it is meaningless until that is fixed", backend, c.name, got)
	case c.wantCliff:
	default:
		t.Errorf("%s %s: __arr_push_shared_count() = %d, want %d — %s",
			backend, c.name, got, c.wantCliff,
			map[bool]string{
				true:  "an extra reference is making every append copy the whole buffer (#6036)",
				false: "the residual copy count moved; if it is now 0 the move-in work landed and this case belongs in the clean group",
			}[c.wantCliff == 0])
	}
}

func TestX86_64CallResultMaterialiseCliff(t *testing.T) {
	for _, c := range materialiseCases {
		t.Run(c.name, func(t *testing.T) {
			_, got := compileAndRunX86_64FreeOn(t, c.src())
			c.check(t, "x86-64-linux", got)
		})
	}
}

func TestArm64CallResultMaterialiseCliff(t *testing.T) {
	for _, c := range materialiseCases {
		t.Run(c.name, func(t *testing.T) {
			_, got := compileAndRunArm64(t, c.src())
			c.check(t, "arm64-linux", got)
		})
	}
}

func TestWASMCallResultMaterialiseCliff(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, c := range materialiseCases {
		t.Run(c.name, func(t *testing.T) {
			c.check(t, "wasm32-wasi", runWasm(t, c.src()))
		})
	}
}

// TestInterpCallResultMaterialiseSemantics is the oracle for the group: every
// case must compute the SAME accumulator under the interpreter, which has no
// in-place append at all. A backend that returns 254/253 above is diverging
// from this; a backend that agrees with it is only trading copies.
func TestInterpCallResultMaterialiseSemantics(t *testing.T) {
	for _, c := range materialiseCases {
		t.Run(c.name, func(t *testing.T) {
			if got := runInterpExit(t, c.src()); got != 0 {
				t.Errorf("interp %s: exit %d, want 0 (the interpreter's cliff counter is a "+
					"constant 0, so anything else is a length / element mismatch)", c.name, got)
			}
		})
	}
}
