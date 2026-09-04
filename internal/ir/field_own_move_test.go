package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// The superseded-field move (#8186): `a = Asm { ...a, cfi: record(a.cfi, v) }`
// with `record(own s: Cfi, …)` hands `a.cfi` to the callee as a MOVE out of
// a's box — an is_unique(a) test that empties the slot on the unique branch
// and retains the value on the shared one — instead of E051 refusing the
// shape. computeFieldOwnMoves keys on the checker's recognition.

const fieldMoveSrc = `struct Cfi { rules: i32[], n: i32 }
struct Asm { code: i32[], cfi: Cfi }
function record(own s: Cfi, v: i32): Cfi { return Cfi { rules: s.rules.append(v), n: s.n + 1 }; }
function stepOwn(own a: Asm, v: i32): Asm { a = Asm { ...a, cfi: record(a.cfi, v) }; return a; }
function stepRet(own a: Asm, v: i32): Asm { return Asm { ...a, cfi: record(a.cfi, v) }; }
function stepLocal(v: i32): i32 { var a: Asm = Asm { code: [], cfi: Cfi { rules: [], n: 0 } }; a = Asm { ...a, cfi: record(a.cfi, v) }; return a.cfi.n; }
function main(): i32 { var a: Asm = Asm { code: [], cfi: Cfi { rules: [], n: 0 } }; a = stepOwn(a, 1); a = stepRet(a, 2); return a.cfi.n + stepLocal(3); }`

func TestFieldOwnMoveTestsUniquenessBeforeCall(t *testing.T) {
	ip := lowerForTest(t, fieldMoveSrc)
	for _, fn := range []string{"stepOwn", "stepRet", "stepLocal"} {
		f := fnNamed(t, ip, fn)
		if !uniqueTestBeforeCall(f, "record") {
			t.Errorf("%s passes `a.cfi` to record's own parameter without the is_unique-gated move:\n%s", fn, ip)
		}
	}
}

// Anti-vacuity, and the fallback: a two-word STRING field is outside the
// flat helpers' reach, so the analysis declines and the call site retains
// the value for the callee instead of testing uniqueness. Without the
// retain the callee's exit drop would over-release the caller's field.
func TestFieldOwnMoveStringFieldRetainsInstead(t *testing.T) {
	ip := lowerForTest(t, `struct S { tag: string, n: i32 }
function take(own s: string, v: i32): string { return s + "x"; }
function step(own a: S, v: i32): S { a = S { ...a, tag: take(a.tag, v) }; return a; }
function main(): i32 { var a: S = S { tag: "a", n: 0 }; a = step(a, 1); return a.tag.len(); }`)
	f := fnNamed(t, ip, "step")
	if uniqueTestBeforeCall(f, "take") {
		t.Errorf("step moves a STRING field, which the single-word null/retain cannot represent:\n%s", ip)
	}
	if n := retainsBeforeCall(f, "take"); n == 0 {
		t.Errorf("step passes `a.tag` to take's own parameter with no retain; the callee's drop would over-release it:\n%s", ip)
	}
}

// uniqueTestBeforeCall reports whether the run of ops immediately preceding
// the first direct call to `callee` contains an OpRcIsUnique — the move's
// runtime gate.
func uniqueTestBeforeCall(fn *ir.Func, callee string) bool {
	for i, op := range fn.Ops {
		if op.Kind != ir.OpCallDirect || op.Str != callee {
			continue
		}
		for j := i - 1; j >= 0 && j >= i-24; j-- {
			if fn.Ops[j].Kind == ir.OpRcIsUnique {
				return true
			}
			if fn.Ops[j].Kind == ir.OpCallDirect && !fn.Ops[j].Runtime {
				break
			}
		}
		return false
	}
	return false
}

// retainsBeforeCall counts the retains (OpRcInc or a __fern_str_inc call) in
// the run immediately preceding the first direct call to `callee`.
func retainsBeforeCall(fn *ir.Func, callee string) int {
	for i, op := range fn.Ops {
		if op.Kind != ir.OpCallDirect || op.Str != callee {
			continue
		}
		n := 0
		for j := i - 1; j >= 0 && j >= i-24; j-- {
			o := fn.Ops[j]
			if o.Kind == ir.OpRcInc || (o.Kind == ir.OpCallDirect && o.Str == "__fern_str_inc") {
				n++
				continue
			}
			if o.Kind == ir.OpCallDirect && !o.Runtime {
				break
			}
		}
		return n
	}
	return 0
}
