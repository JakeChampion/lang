package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// The integer sibling of TestImplicitFloatReturnMatchesDeclaredWidth (#6192).
//
// An i64/u64-returning function whose body falls off the end gets an implicit
// zero return, and that zero must be an i64 constant. OpConstI32 is wrong for
// the same reason OpConstF32 was wrong for an f64: the natives do not notice —
// the value is unreachable and both widths share a register — but wasm's stack
// is typed, so an i64 result fed an i32 constant fails validation with
// "expected i64, found i32" and takes the whole module with it.
//
// It was not hypothetical. Every i64-returning function ending in a `match` hit
// it, `vint_as_i64` in examples/self_host/interp.fern among them, which is why
// that interpreter had never produced a valid wasm module at all — the defect
// predated the driver work that found it and was invisible to every native
// backend.
//
// This lives here rather than only in internal/e2e for the reason the float
// test gives: the e2e wasm harness turns a validator rejection into a t.Skip,
// so a regression would go quiet rather than red. Here it is a hard failure on
// the op kind.
func TestImplicitWideIntReturnIsI64(t *testing.T) {
	// Each body ends in an if/else where both arms return, so the builder has
	// to synthesise a value for the path after it. i32 and u32 are the
	// controls: their implicit zero is legitimately an i32 const, so a fix
	// that emitted i64 everywhere fails here.
	src := `function fall_i64(flag: boolean): i64 {
		if (flag) { return 1i64; } else { return 2i64; }
	}
	function fall_u64(flag: boolean): u64 {
		if (flag) { return 1u64; } else { return 2u64; }
	}
	function fall_i32(flag: boolean): i32 {
		if (flag) { return 1; } else { return 2; }
	}
	function fall_u32(flag: boolean): u32 {
		if (flag) { return 1u32; } else { return 2u32; }
	}
	function main(): i32 {
		if (fall_i64(true) > 0i64) { return 0; }
		if (fall_u64(true) > 0u64) { return 0; }
		if (fall_i32(true) > 0) { return 0; }
		if (fall_u32(true) > 0u32) { return 0; }
		return 1;
	}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	want := map[string]OpKind{
		"fall_i64": OpConstI64,
		"fall_u64": OpConstI64,
		"fall_i32": OpConstI32,
		"fall_u32": OpConstI32,
	}
	// Both pointer widths: wasm (4) is where this bites, native (8) guards
	// against fixing only the width that complained.
	for _, ptrW := range []int{4, 8} {
		ip, err := LowerWith(prog, info, ptrW)
		if err != nil {
			t.Fatalf("ptrW=%d: lower: %v", ptrW, err)
		}
		for _, fn := range ip.Funcs {
			if fn == nil {
				continue
			}
			wantKind, tracked := want[fn.Name]
			if !tracked {
				continue
			}
			var sawWanted, sawWrongWidth bool
			for _, op := range fn.Ops {
				if !isZeroIntConst(op) {
					continue
				}
				if op.Kind == wantKind {
					sawWanted = true
				} else {
					sawWrongWidth = true
				}
			}
			if sawWrongWidth {
				t.Errorf("ptrW=%d: %s emits a zero int const of the wrong width; the "+
					"implicit return must match the declared type (wasm validates this)",
					ptrW, fn.Name)
			}
			if !sawWanted {
				t.Errorf("ptrW=%d: %s emits no zero %v at all — the implicit return is "+
					"missing or changed shape; re-point this test at it",
					ptrW, fn.Name, wantKind)
			}
		}
	}
}

// isZeroIntConst reports whether op is an integer constant of value zero — the
// shape the implicit return synthesises, as opposed to a literal from source.
func isZeroIntConst(op Op) bool {
	switch op.Kind {
	case OpConstI32:
		return op.I32 == 0
	case OpConstI64:
		return op.I64 == 0
	}
	return false
}
