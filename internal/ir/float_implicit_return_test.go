package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// A float-returning function whose body falls off the end gets an implicit
// zero return. That zero must carry the DECLARED float width: an f64 function
// gets OpConstF64, an f32 function gets OpConstF32.
//
// OpConstF32 unconditionally is wrong. The natives don't notice — the
// value is unreachable and both widths live in the same register file — but
// wasm's stack is typed, so an f64 result fed an f32 constant makes the whole
// module fail validation ("expected f64, found f32") and every program with an
// f64-returning `match` was rejected at instantiation, std/test included
// (#6192).
//
// This assertion lives here rather than only in internal/e2e because the e2e
// wasm harness turns a validator rejection into a t.Skip (it is the signal for
// a wasmbin coverage gap, and this failure is indistinguishable from one). A
// regression would therefore go quiet rather than red. Here it is a hard
// failure on the op kind itself.
func TestImplicitFloatReturnMatchesDeclaredWidth(t *testing.T) {
	// `fall64` / `fall32` end in an if/else where both arms return, so the
	// builder has to synthesise a value for the path after it.
	src := `function fall64(flag: boolean): f64 {
		if (flag) { return 1.5; } else { return 2.5; }
	}
	function fall32(flag: boolean): f32 {
		if (flag) { return 1.5 as f32; } else { return 2.5 as f32; }
	}
	function main(): i32 {
		if (fall64(true) > 1.0) { return 0; }
		if (fall32(true) > (1.0 as f32)) { return 0; }
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

	want := map[string]OpKind{"fall64": OpConstF64, "fall32": OpConstF32}
	// Both pointer widths: the wasm one (4) is where this bites, the native
	// one (8) guards against fixing it only on the width that complained.
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
			// The implicit return is the tail: a float const followed by
			// OpReturn, appended after every explicit return in the body.
			var sawWrongWidth bool
			var sawWanted bool
			for _, op := range fn.Ops {
				switch op.Kind {
				case OpConstF32, OpConstF64:
					if op.Kind == wantKind {
						sawWanted = true
					} else if isZeroFloatConst(op) {
						sawWrongWidth = true
					}
				}
			}
			if sawWrongWidth {
				t.Errorf("ptrW=%d: %s emits a zero float const of the wrong width; "+
					"the implicit return must match the declared type (wasm validates this)",
					ptrW, fn.Name)
			}
			if !sawWanted {
				t.Errorf("ptrW=%d: %s emits no %v at all — the implicit return is missing "+
					"or changed shape; re-point this test at it",
					ptrW, fn.Name, wantKind)
			}
		}
	}
}

// isZeroFloatConst reports whether op is a float constant of value zero — the
// shape the implicit return synthesises, as opposed to a literal from the
// source.
func isZeroFloatConst(op Op) bool {
	switch op.Kind {
	case OpConstF32:
		return op.F32 == 0
	case OpConstF64:
		return op.F64 == 0
	}
	return false
}
