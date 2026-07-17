package e2e

import "testing"

// tupleFnElemCases pin tuples with FUNCTION-typed elements on the native
// backends. Two layout bugs made every one of these segfault (or emit
// unassemblable code) before the fix:
//
//  1. tupleEnumMangler didn't escape the `=>` in a fn element's canonical
//     type name, so the generated `__drop_tuple_<mangled>` symbol —
//     `__drop_tuple__LP__LP_i32_RP_=>i32_C_i32_RP_` — broke the native
//     assembler ("cannot parse operand").
//  2. exprType had no *ast.MakeClosure case (a closureconv-rewritten lambda)
//     and no Ident→FuncSigs fallback (a bare named fn in value position), so
//     an enclosing TupleLit sized the fn element's slot at the
//     payloadSlotSize(nil) 4-byte default while the read/drop side used the
//     DECLARED (fn, …) layout's 8-byte slot: the construction packed the
//     neighbouring element 4 bytes below where the load expects it, and the
//     tuple drop rc_dec'd the two misaligned halves as one garbage pointer →
//     SIGSEGV even when the fn element was never called.
//
// Exit codes are cross-checked against the interpreter (the Go reference).
var tupleFnElemCases = []struct {
	name string
	src  string
	exit int
}{
	// Capturing closure in a tuple returned from a factory, element called
	// through the caller's binding.
	{"tuple-closure-returned", "function mk(): ((i32) => i32, i32) { var n = 5; var t = (function (x: i32): i32 { return x + n; }, 1); return t; } function main(): i32 { var t = mk(); return t.0(37); }", 42},
	// The fn element is never CALLED — only the sibling scalar is read. Pins
	// the layout + drop path alone (this crashed in __drop_tuple before the
	// exprType fix).
	{"tuple-closure-uncalled", "function mk(): ((i32) => i32, i32) { var n = 5; var t = (function (x: i32): i32 { return x + n; }, 41); return t; } function main(): i32 { var t = mk(); return t.1 + 1; }", 42},
	// Non-capturing lambda element (still a closure box at the binding).
	{"tuple-closure-nocapture", "function mk(): ((i32) => i32, i32) { var t = (function (x: i32): i32 { return x + 1; }, 1); return t; } function main(): i32 { var t = mk(); return t.0(41); }", 42},
	// Local tuple, never crosses a function boundary.
	{"tuple-closure-local", "function main(): i32 { var n = 5; var t = (function (x: i32): i32 { return x + n; }, 1); return t.0(37); }", 42},
	// A bare NAMED function as the element (an Ident in value position —
	// resolves through FuncSigs, not MakeClosure).
	{"tuple-named-fn", "function dbl(x: i32): i32 { return x * 2; } function main(): i32 { var t = (dbl, 1); return t.0(21); }", 42},
	// TWO closures in one tuple — both slots must be 8-byte for the second
	// element's offset to line up.
	{"tuple-two-closures", "function mk(): ((i32) => i32, (i32) => i32) { var n = 1; var m = 2; var t = (function (x: i32): i32 { return x + n; }, function (x: i32): i32 { return x + m; }); return t; } function main(): i32 { var t = mk(); return t.0(19) + t.1(20); }", 42},
	// A tuple-with-closure as an ARRAY element (`a[0].0(x)`).
	{"array-of-tuple-closure", "function main(): i32 { var n = 5; var a = [(function (x: i32): i32 { return x + n; }, 1)]; return a[0].0(37); }", 42},
	// A tuple-with-closure NESTED in another tuple (`t.0.0(x)`).
	{"nested-tuple-closure", "function main(): i32 { var n = 5; var t = ((function (x: i32): i32 { return x + n; }, 1), 2); return t.0.0(37); }", 42},
}

// TestX86_64TupleFnElem — tuples with fn-typed elements through the native
// x86-64 backend.
func TestX86_64TupleFnElem(t *testing.T) {
	for _, c := range tupleFnElemCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, c.src); code != c.exit {
				t.Errorf("%s: got exit %d, want %d", c.name, code, c.exit)
			}
		})
	}
}

// TestArm64TupleFnElem — the arm64 sibling (qemu-gated like every arm64 e2e).
func TestArm64TupleFnElem(t *testing.T) {
	for _, c := range tupleFnElemCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.exit {
				t.Errorf("%s: got exit %d, want %d", c.name, code, c.exit)
			}
		})
	}
}
