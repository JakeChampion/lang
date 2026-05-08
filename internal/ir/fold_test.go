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
// OpIf wrapper entirely so only the surviving arm remains. Same shape
// surfaces from a ternary `(1 < 2) ? 10 : 20`, which the IR lowers to
// `OpConstI32 1; OpIf i32; OpConstI32 10; OpElse; OpConstI32 20; OpEnd`.
func TestFoldConstIfPicksTrueBranch(t *testing.T) {
	p := loweredAndFolded(t, `function f(): i32 { return (1 < 2) ? 10 : 20; }`)
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
	p := loweredAndFolded(t, `function f(): i32 { return (1 > 2) ? 10 : 20; }`)
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
