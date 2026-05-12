package ir

import (
	"strings"
	"testing"
)

// loweredAndFolded parses, type-checks, lowers, and runs Fold on src.
// Most folds are visible as fewer ops; assertions are over op kinds /
// counts.
func loweredAndFolded(t *testing.T, src string) *Program {
	t.Helper()
	p := lowerSource(t, src)
	Fold(p)
	return p
}

// `1 + 2 * 3` lowers to push 1, push 2, push 3, mul, add. After
// folding both binops, the body collapses to a single OpConstI32 7.
// The trailing OpReturn keeps it from being completely empty.
func TestFoldChainedArithmetic(t *testing.T) {
	p := loweredAndFolded(t, `function f(): i32 { return 1 + 2 * 3; }`)
	fn := findFunc(p, "f")
	if fn == nil {
		t.Fatal("f not found")
	}
	// Expect: OpConstI32 7, OpReturn.
	if len(fn.Ops) != 2 {
		t.Fatalf("expected 2 ops after fold, got %d:\n%s", len(fn.Ops), p)
	}
	if fn.Ops[0].Kind != OpConstI32 || fn.Ops[0].I32 != 7 {
		t.Errorf("op[0] = %s %d, want OpConstI32 7", fn.Ops[0].Kind, fn.Ops[0].I32)
	}
	if fn.Ops[1].Kind != OpReturn {
		t.Errorf("op[1] = %s, want OpReturn", fn.Ops[1].Kind)
	}
}

// Comparison ops fold the same way as arithmetic. `5 < 3` collapses
// to a single OpConstI32 0.
func TestFoldComparison(t *testing.T) {
	p := loweredAndFolded(t, `function f(): boolean { return 5 < 3; }`)
	fn := findFunc(p, "f")
	if fn.Ops[0].Kind != OpConstI32 || fn.Ops[0].I32 != 0 {
		t.Errorf("op[0] = %s %d, want OpConstI32 0", fn.Ops[0].Kind, fn.Ops[0].I32)
	}
}

// `!true` lowers as `OpConstI32 1; OpNot`. Folding turns it into a
// single `OpConstI32 0`.
func TestFoldNot(t *testing.T) {
	p := loweredAndFolded(t, `function f(): boolean { return !true; }`)
	fn := findFunc(p, "f")
	if fn.Ops[0].Kind != OpConstI32 || fn.Ops[0].I32 != 0 {
		t.Errorf("op[0] = %s %d, want OpConstI32 0", fn.Ops[0].Kind, fn.Ops[0].I32)
	}
}

// Shifts mask the count to 0..31 just like the runtime ops; folding
// `1 << 35` therefore matches the runtime's `1 << (35 & 31)` = 8.
func TestFoldShiftMasksCount(t *testing.T) {
	p := loweredAndFolded(t, `function f(): i32 { return 1 << 35; }`)
	fn := findFunc(p, "f")
	if fn.Ops[0].Kind != OpConstI32 || fn.Ops[0].I32 != 8 {
		t.Errorf("op[0] = %s %d, want OpConstI32 8 (1 << (35 & 31))", fn.Ops[0].Kind, fn.Ops[0].I32)
	}
}

// Division and remainder must NOT be folded — `1 / 0` would mask a
// runtime trap. The ops survive intact even when both operands are
// constants.
func TestFoldSkipsDivisionAndRemainder(t *testing.T) {
	for _, op := range []string{"/", "%"} {
		src := `function f(): i32 { return 6 ` + op + ` 2; }`
		p := loweredAndFolded(t, src)
		fn := findFunc(p, "f")
		// Expect at least three ops: the two constants and the divs/rems.
		if len(fn.Ops) < 3 {
			t.Errorf("operator %q: expected fold to leave divs/rems alone, got:\n%s", op, p)
		}
	}
}

// Constant-if pruning: `if (true) { return 1; }` should drop the
// OpIf wrapper entirely so only the surviving arm remains. Same
// shape surfaces from `if (1 < 2) { 10 } else { 20 }`, which the
// IR lowers to `OpConstI32 1; OpIf i32; OpConstI32 10; OpElse;
// OpConstI32 20; OpEnd`.
func TestFoldConstIfPicksTrueBranch(t *testing.T) {
	p := loweredAndFolded(t, `function f(): i32 { return if (1 < 2) { 10 } else { 20 }; }`)
	fn := findFunc(p, "f")
	for _, op := range fn.Ops {
		if op.Kind == OpIf || op.Kind == OpElse || op.Kind == OpEnd {
			t.Fatalf("constant-if should have been pruned, found %s in:\n%s", op.Kind, p)
		}
	}
	mustContainOp(t, p, "f", OpConstI32)
	// Resulting value must be 10 (the true branch).
	found := false
	for _, op := range fn.Ops {
		if op.Kind == OpConstI32 && op.I32 == 10 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected const 10 (true branch) in:\n%s", p)
	}
}

// Constant false condition picks the else branch.
func TestFoldConstIfPicksFalseBranch(t *testing.T) {
	p := loweredAndFolded(t, `function f(): i32 { return if (1 > 2) { 10 } else { 20 }; }`)
	fn := findFunc(p, "f")
	for _, op := range fn.Ops {
		if op.Kind == OpConstI32 && op.I32 == 10 {
			t.Errorf("expected const 10 to be pruned, but it survived:\n%s", p)
		}
	}
	found := false
	for _, op := range fn.Ops {
		if op.Kind == OpConstI32 && op.I32 == 20 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected const 20 (false branch) in:\n%s", p)
	}
}

// `if (false) { ... }` with no `else` collapses to nothing: the else
// arm is empty, so we drop the entire if-block.
func TestFoldConstIfWithNoElseDropsBody(t *testing.T) {
	p := loweredAndFolded(t, `function f(): i32 {
		if (false) { return 99; }
		return 1;
	}`)
	fn := findFunc(p, "f")
	for _, op := range fn.Ops {
		if op.Kind == OpConstI32 && op.I32 == 99 {
			t.Errorf("dead branch should have been removed:\n%s", p)
		}
	}
	mustContainOp(t, p, "f", OpReturn)
}

// Folding must respect nested control flow: an inner constant-if
// inside a `while` should fold without disturbing the outer loop's
// scope structure.
func TestFoldHandlesNestedControlFlow(t *testing.T) {
	p := loweredAndFolded(t, `function f(): i32 {
		var i: i32 = 0;
		while (i < 3) {
			if (true) { i = i + 1; }
		}
		return i;
	}`)
	for _, fn := range p.Funcs {
		depth := 0
		for i, op := range fn.Ops {
			switch op.Kind {
			case OpBlock, OpLoop, OpIf:
				depth++
			case OpEnd:
				depth--
				if depth < 0 {
					t.Fatalf("%s: op %d (%s): depth went negative after fold", fn.Name, i, op.Kind)
				}
			}
		}
		if depth != 0 {
			t.Errorf("%s: ended at depth %d, want 0", fn.Name, depth)
		}
	}
}

// The fold pass is idempotent: running it a second time on already-
// folded ops produces identical output. This is what lets backends
// rely on a single Fold call.
func TestFoldIsIdempotent(t *testing.T) {
	p := loweredAndFolded(t, `function f(): i32 { return 1 + 2 * 3 + 4; }`)
	before := p.String()
	Fold(p)
	after := p.String()
	if !strings.EqualFold(before, after) {
		t.Errorf("Fold not idempotent:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// Non-constant operands aren't folded — the runtime values can't be
// known statically, so the Add survives and the locals reads stay.
func TestFoldLeavesRuntimeOperandsAlone(t *testing.T) {
	p := loweredAndFolded(t, `function f(a: i32, b: i32): i32 { return a + b; }`)
	mustContainOp(t, p, "f", OpAdd)
	mustContainOp(t, p, "f", OpLoadLocal)
}

// i64 + i64 of two constants folds to a single OpConstI64. Same
// pipeline shape as the i32 case; just verifies the wide-width
// branch in foldBinary64 fires.
func TestFoldI64Arithmetic(t *testing.T) {
	p := loweredAndFolded(t, `function f(): i64 { return 1000000000i64 * 3i64; }`)
	fn := findFunc(p, "f")
	if fn.Ops[0].Kind != OpConstI64 || fn.Ops[0].I64 != 3000000000 {
		t.Errorf("op[0] = %s %d, want OpConstI64 3000000000:\n%s",
			fn.Ops[0].Kind, fn.Ops[0].I64, p)
	}
}

// i64 comparisons fold to a 0/1 OpConstI32 (boolean shape) just
// like the i32 case.
func TestFoldI64Comparison(t *testing.T) {
	p := loweredAndFolded(t, `function f(): boolean { return 5i64 < 3i64; }`)
	fn := findFunc(p, "f")
	if fn.Ops[0].Kind != OpConstI32 || fn.Ops[0].I32 != 0 {
		t.Errorf("op[0] = %s %d, want OpConstI32 0:\n%s",
			fn.Ops[0].Kind, fn.Ops[0].I32, p)
	}
}

// Unsigned-flagged compare on i32 constants must use unsigned
// semantics. -1 (signed) is 0xFFFFFFFF (unsigned 4294967295), so
// `0xFFFFFFFFu32 > 1u32` is true even though as signed it would
// be `-1 > 1` = false.
func TestFoldUnsignedI32Compare(t *testing.T) {
	p := loweredAndFolded(t, `function f(): boolean { return 4294967295u32 > 1u32; }`)
	fn := findFunc(p, "f")
	if fn.Ops[0].Kind != OpConstI32 || fn.Ops[0].I32 != 1 {
		t.Errorf("op[0] = %s %d, want OpConstI32 1 (unsigned > is true):\n%s",
			fn.Ops[0].Kind, fn.Ops[0].I32, p)
	}
}

// Unsigned shift right (>> on u32) is a logical shift. -1 (signed)
// = 0xFFFFFFFF shifted right by 1 should give 0x7FFFFFFF
// (2147483647) under logical-shift semantics, not -1 (signed
// arithmetic shift).
func TestFoldUnsignedShiftIsLogical(t *testing.T) {
	p := loweredAndFolded(t, `function f(): u32 { return 4294967295u32 >> 1u32; }`)
	fn := findFunc(p, "f")
	if fn.Ops[0].Kind != OpConstI32 || fn.Ops[0].I32 != 0x7FFFFFFF {
		t.Errorf("op[0] = %s %x, want OpConstI32 0x7FFFFFFF:\n%s",
			fn.Ops[0].Kind, fn.Ops[0].I32, p)
	}
}

// `5 as i64` lowers to `OpConstI32 5 ; OpExtendI32S`. Fold should
// fuse that to a single `OpConstI64 5`, saving the runtime
// sign-extend instruction and any operand-stack churn around it.
func TestFoldConstExtendI32SToI64(t *testing.T) {
	p := loweredAndFolded(t, `function f(): i64 { return 5 as i64; }`)
	fn := findFunc(p, "f")
	for _, op := range fn.Ops {
		if op.Kind == OpExtendI32S {
			t.Fatalf("OpExtendI32S survived fold:\n%s", p)
		}
	}
	if fn.Ops[0].Kind != OpConstI64 || fn.Ops[0].I64 != 5 {
		t.Errorf("op[0] = %s %d, want OpConstI64 5:\n%s",
			fn.Ops[0].Kind, fn.Ops[0].I64, p)
	}
}

// Same idea but for unsigned extension. `4294967295u32 as u64`
// must zero-extend, not sign-extend — the folded constant must be
// the same large positive number, not -1.
func TestFoldConstExtendI32UToI64(t *testing.T) {
	p := loweredAndFolded(t, `function f(): u64 { return 4294967295u32 as u64; }`)
	fn := findFunc(p, "f")
	for _, op := range fn.Ops {
		if op.Kind == OpExtendI32U {
			t.Fatalf("OpExtendI32U survived fold:\n%s", p)
		}
	}
	if fn.Ops[0].Kind != OpConstI64 || fn.Ops[0].I64 != int64(0xFFFFFFFF) {
		t.Errorf("op[0] = %s %x, want OpConstI64 0xFFFFFFFF:\n%s",
			fn.Ops[0].Kind, fn.Ops[0].I64, p)
	}
}

// `(7i64) as i32` lowers to `OpConstI64 7 ; OpWrapI64`. Folding
// produces `OpConstI32 7`. The wrap takes the low 32 bits so this
// also covers truncation.
func TestFoldConstWrapI64ToI32(t *testing.T) {
	p := loweredAndFolded(t, `function f(): i32 { return 7i64 as i32; }`)
	fn := findFunc(p, "f")
	for _, op := range fn.Ops {
		if op.Kind == OpWrapI64 {
			t.Fatalf("OpWrapI64 survived fold:\n%s", p)
		}
	}
	if fn.Ops[0].Kind != OpConstI32 || fn.Ops[0].I32 != 7 {
		t.Errorf("op[0] = %s %d, want OpConstI32 7:\n%s",
			fn.Ops[0].Kind, fn.Ops[0].I32, p)
	}
}

// `(a as i64) as i32` for a runtime i32 `a` lowers to
// `OpLoadLocal ; OpExtendI32S ; OpWrapI64`. The extend-wrap pair
// is the identity (the i32 value survives intact); Fold should
// strip both so we end up just loading the local.
func TestFoldStripsExtendWrapIdentity(t *testing.T) {
	p := loweredAndFolded(t, `function f(a: i32): i32 { return (a as i64) as i32; }`)
	fn := findFunc(p, "f")
	for _, op := range fn.Ops {
		if op.Kind == OpExtendI32S || op.Kind == OpExtendI32U || op.Kind == OpWrapI64 {
			t.Fatalf("extend/wrap pair survived fold (found %s):\n%s", op.Kind, p)
		}
	}
}
