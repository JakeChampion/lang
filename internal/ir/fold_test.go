package ir

import (
	"math"
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

// Constant reassociation: `x + 1 + 2` lowers to `load ; const 1 ; add ;
// const 2 ; add` — the two constants are separated by the first add, so
// the plain two-adjacent-constant fold can't reach them. Reassociation
// combines them into `load ; const 3 ; add`. Verified for each
// associative op (add / mul / and / or / xor) and a 3-constant chain.
func TestFoldReassociatesConstantChain(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int32
	}{
		{"add", `function f(x: i32): i32 { return x + 1 + 2; }`, 3},
		{"mul", `function f(x: i32): i32 { return x * 3 * 5; }`, 15},
		{"and", `function f(x: i32): i32 { return (x & 12) & 10; }`, 8},
		{"or", `function f(x: i32): i32 { return x | 1 | 4; }`, 5},
		{"xor", `function f(x: i32): i32 { return x ^ 6 ^ 3; }`, 5},
		{"chain3", `function f(x: i32): i32 { return x + 1 + 2 + 3; }`, 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := loweredAndFolded(t, c.src)
			fn := findFunc(p, "f")
			// Expect exactly: OpLoadLocal, OpConstI32 <want>, <op>, OpReturn.
			if len(fn.Ops) != 4 {
				t.Fatalf("expected 4 ops after reassociation, got %d:\n%s", len(fn.Ops), p)
			}
			if fn.Ops[1].Kind != OpConstI32 || fn.Ops[1].I32 != c.want {
				t.Errorf("op[1] = %s %d, want OpConstI32 %d:\n%s",
					fn.Ops[1].Kind, fn.Ops[1].I32, c.want, p)
			}
		})
	}
}

// i64 constant chains reassociate the same way, keeping the wide width.
func TestFoldReassociatesI64ConstantChain(t *testing.T) {
	p := loweredAndFolded(t, `function f(x: i64): i64 { return x + 10i64 + 20i64; }`)
	fn := findFunc(p, "f")
	if len(fn.Ops) != 4 {
		t.Fatalf("expected 4 ops, got %d:\n%s", len(fn.Ops), p)
	}
	if fn.Ops[1].Kind != OpConstI64 || fn.Ops[1].I64 != 30 {
		t.Errorf("op[1] = %s %d, want OpConstI64 30:\n%s", fn.Ops[1].Kind, fn.Ops[1].I64, p)
	}
}

// Additive chains combine even when + and - mix: `(x ±a c1) ±b c2` folds
// to `x + net` with a single add. Each collapses to `load ; const net ;
// add ; return`, where net carries the signed sum.
func TestFoldCombinesAdditiveChain(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int32
	}{
		{"sub-sub", `function f(x: i32): i32 { return x - 1 - 2; }`, -3},
		{"add-sub", `function f(x: i32): i32 { return x + 8 - 4; }`, 4},
		{"sub-add", `function f(x: i32): i32 { return x - 8 + 4; }`, -4},
		{"chain4", `function f(x: i32): i32 { return x + 1 - 2 + 3; }`, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := loweredAndFolded(t, c.src)
			fn := findFunc(p, "f")
			if len(fn.Ops) != 4 {
				t.Fatalf("expected 4 ops after additive fold, got %d:\n%s", len(fn.Ops), p)
			}
			if fn.Ops[1].Kind != OpConstI32 || fn.Ops[1].I32 != c.want {
				t.Errorf("op[1] = %s %d, want OpConstI32 %d:\n%s",
					fn.Ops[1].Kind, fn.Ops[1].I32, c.want, p)
			}
			if fn.Ops[2].Kind != OpAdd {
				t.Errorf("op[2] = %s, want OpAdd (a mixed chain always surfaces as add):\n%s", fn.Ops[2].Kind, p)
			}
		})
	}
}

// A net-zero additive chain (`x + 5 - 5`) folds to `const 0 ; add`, which
// the strength-reduction pass in OptimizeCleanup then drops entirely — the
// function reduces to just returning its argument.
func TestFoldNetZeroAdditiveChainReducesToArg(t *testing.T) {
	p := lowerSource(t, `function f(x: i32): i32 { return x + 5 - 5; }`)
	OptimizeCleanup(p)
	fn := findFunc(p, "f")
	for _, op := range fn.Ops {
		if op.Kind == OpConstI32 || op.Kind == OpAdd || op.Kind == OpSub {
			t.Errorf("net-zero chain should reduce to just the arg, found leftover %s:\n%s", op.Kind, p)
		}
	}
	mustContainOp(t, p, "f", OpLoadLocal)
}

// Shifts must NOT reassociate: the runtime masks the shift count to the
// operand width, so folding `<< a` then `<< b` into `<< (a+b)` would
// diverge once a+b reaches 32. `(x << 30) << 30` is 0 at runtime but
// `x << 60` masks to `x << 28` — the two must stay two shifts.
func TestFoldDoesNotReassociateShifts(t *testing.T) {
	p := loweredAndFolded(t, `function f(x: i32): i32 { return (x << 30) << 30; }`)
	shifts := 0
	for _, op := range findFunc(p, "f").Ops {
		if op.Kind == OpShl {
			shifts++
		}
	}
	if shifts != 2 {
		t.Errorf("shift chain must not reassociate, expected 2 OpShl, got %d:\n%s", shifts, p)
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

// Division / remainder by a NONZERO constant folds like any other
// binop — `6 / 2` → 3, `7 % 3` → 1. (The AST optimiser doesn't fold
// these, so they reach the IR as a const/const/div chain; inlining a
// small `n / K` helper at a constant call site produces the same
// shape.)
func TestFoldDivRemByNonzero(t *testing.T) {
	for _, tc := range []struct {
		expr string
		want int32
	}{
		{"6 / 2", 3},
		{"7 / 2", 3}, // truncates toward zero
		{"7 % 3", 1},
		{"8 % 4", 0},
	} {
		p := loweredAndFolded(t, `function f(): i32 { return `+tc.expr+`; }`)
		fn := findFunc(p, "f")
		if fn.Ops[0].Kind != OpConstI32 || fn.Ops[0].I32 != tc.want {
			t.Errorf("%q: op[0] = %s %d, want OpConstI32 %d:\n%s", tc.expr, fn.Ops[0].Kind, fn.Ops[0].I32, tc.want, p)
		}
		for _, op := range fn.Ops {
			if op.Kind == OpDivS || op.Kind == OpRemS {
				t.Errorf("%q: div/rem by nonzero should have folded:\n%s", tc.expr, p)
			}
		}
	}
}

// Division / remainder by a ZERO constant must NOT be folded — the op
// survives so the runtime trap still fires.
func TestFoldPreservesDivRemByZero(t *testing.T) {
	for _, op := range []string{"/", "%"} {
		p := loweredAndFolded(t, `function f(): i32 { return 6 `+op+` 0; }`)
		fn := findFunc(p, "f")
		if len(fn.Ops) < 3 {
			t.Errorf("operator %q by 0: expected the trapping op to survive, got:\n%s", op, p)
		}
	}
}

// The signed overflow `INT_MIN / -1` traps at runtime (wasm i32.div_s),
// so it stays unfolded; the sibling `INT_MIN %% -1` is 0 and doesn't
// trap, so it folds. Constructed at the IR level — the pair only
// reaches Fold post-inline.
func TestFoldPreservesSignedDivOverflow(t *testing.T) {
	// INT_MIN / -1 → keep the op (traps)
	{
		fn := &Func{Name: "f", Ops: []Op{
			{Kind: OpConstI32, I32: math.MinInt32},
			{Kind: OpConstI32, I32: -1},
			{Kind: OpDivS},
			{Kind: OpReturn},
		}}
		p := &Program{Funcs: []*Func{fn}}
		Fold(p)
		kept := false
		for _, op := range fn.Ops {
			if op.Kind == OpDivS {
				kept = true
			}
		}
		if !kept {
			t.Errorf("INT_MIN / -1 must stay unfolded (traps):\n%s", p)
		}
	}
	// INT_MIN % -1 → fold to 0 (no trap)
	{
		fn := &Func{Name: "f", Ops: []Op{
			{Kind: OpConstI32, I32: math.MinInt32},
			{Kind: OpConstI32, I32: -1},
			{Kind: OpRemS},
			{Kind: OpReturn},
		}}
		p := &Program{Funcs: []*Func{fn}}
		Fold(p)
		if fn.Ops[0].Kind != OpConstI32 || fn.Ops[0].I32 != 0 || fn.Ops[1].Kind != OpReturn {
			t.Errorf("INT_MIN %% -1 should fold to const 0:\n%s", p)
		}
	}
}

// Unsigned div / rem by a nonzero constant folds with unsigned
// semantics: 0xFFFFFFFF /u 2 == 0x7FFFFFFF (not the signed -1 / 2 == 0).
func TestFoldUnsignedDivRem(t *testing.T) {
	// div_u
	{
		fn := &Func{Name: "f", Ops: []Op{
			{Kind: OpConstI32, I32: -1}, // 0xFFFFFFFF
			{Kind: OpConstI32, I32: 2},
			{Kind: OpDivS, Unsigned: true},
			{Kind: OpReturn},
		}}
		p := &Program{Funcs: []*Func{fn}}
		Fold(p)
		if fn.Ops[0].Kind != OpConstI32 || fn.Ops[0].I32 != 0x7FFFFFFF {
			t.Errorf("0xFFFFFFFF /u 2 should fold to 0x7FFFFFFF, got %d:\n%s", fn.Ops[0].I32, p)
		}
	}
	// rem_u
	{
		fn := &Func{Name: "f", Ops: []Op{
			{Kind: OpConstI32, I32: -1}, // 0xFFFFFFFF
			{Kind: OpConstI32, I32: 16},
			{Kind: OpRemS, Unsigned: true},
			{Kind: OpReturn},
		}}
		p := &Program{Funcs: []*Func{fn}}
		Fold(p)
		if fn.Ops[0].Kind != OpConstI32 || fn.Ops[0].I32 != 15 { // 0xFFFFFFFF % 16 = 15
			t.Errorf("0xFFFFFFFF %%u 16 should fold to 15, got %d:\n%s", fn.Ops[0].I32, p)
		}
	}
}

// i64 division / remainder folds too, with the same zero-divisor
// carve-out.
func TestFoldI64DivRem(t *testing.T) {
	// 100 / 7 = 14 (i64)
	{
		fn := &Func{Name: "f", Ops: []Op{
			{Kind: OpConstI64, I64: 100},
			{Kind: OpConstI64, I64: 7},
			{Kind: OpDivS, Width: 64},
			{Kind: OpReturn},
		}}
		p := &Program{Funcs: []*Func{fn}}
		Fold(p)
		if fn.Ops[0].Kind != OpConstI64 || fn.Ops[0].I64 != 14 {
			t.Errorf("100 / 7 (i64) should fold to 14, got %d:\n%s", fn.Ops[0].I64, p)
		}
	}
	// 100 / 0 (i64) stays put
	{
		fn := &Func{Name: "f", Ops: []Op{
			{Kind: OpConstI64, I64: 100},
			{Kind: OpConstI64, I64: 0},
			{Kind: OpDivS, Width: 64},
			{Kind: OpReturn},
		}}
		p := &Program{Funcs: []*Func{fn}}
		Fold(p)
		kept := false
		for _, op := range fn.Ops {
			if op.Kind == OpDivS {
				kept = true
			}
		}
		if !kept {
			t.Errorf("100 / 0 (i64) must stay unfolded (traps):\n%s", p)
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

// A constant loop condition folds the exit br_if. `while (false)`
// lowers its exit test to `<cond> ; OpNot ; OpBrIf breakD`; with a
// literal `false` that collapses to `OpConstI32 1 ; OpBrIf breakD`
// (branch always taken), which Fold turns into an unconditional
// OpBr — the loop exits at the top and no br_if survives.
func TestFoldConstBrIfAlwaysTaken(t *testing.T) {
	src := `function f(): i32 { var i: i32 = 0; while (false) { i = i + 1; } return i; }`
	// Before Fold the exit test is a real br_if on a constant.
	pre := lowerSource(t, src)
	if !hasOpKind(pre, "f", OpBrIf) {
		t.Fatalf("test premise broken: expected an OpBrIf before fold:\n%s", pre)
	}
	p := loweredAndFolded(t, src)
	fn := findFunc(p, "f")
	for _, op := range fn.Ops {
		if op.Kind == OpBrIf {
			t.Fatalf("constant-true br_if should fold to an unconditional OpBr:\n%s", p)
		}
	}
	mustContainOp(t, p, "f", OpBr)
	assertScopesBalanced(t, p)
}

// `while (true)` lowers its exit test to `OpConstI32 1 ; OpNot ;
// OpBrIf breakD`; folding `const ; not` first yields `OpConstI32 0 ;
// OpBrIf breakD` (branch never taken), which Fold drops entirely. The
// only OpBrIf that could remain here is one from inside the body — this
// loop's body branches via an OpBr (`break`), so none survives.
func TestFoldConstBrIfNeverTaken(t *testing.T) {
	src := `function f(): i32 {
		var i: i32 = 0;
		while (true) { i = i + 1; if (i >= 5) { break; } }
		return i;
	}`
	pre := lowerSource(t, src)
	if !hasOpKind(pre, "f", OpBrIf) {
		t.Fatalf("test premise broken: expected an OpBrIf before fold:\n%s", pre)
	}
	p := loweredAndFolded(t, src)
	fn := findFunc(p, "f")
	for _, op := range fn.Ops {
		if op.Kind == OpBrIf {
			t.Fatalf("constant-false exit br_if should be dropped, not survive:\n%s", p)
		}
	}
	assertScopesBalanced(t, p)
}

// hasOpKind reports whether function fn in p contains an op of kind k.
func hasOpKind(p *Program, fn string, k OpKind) bool {
	f := findFunc(p, fn)
	if f == nil {
		return false
	}
	for _, op := range f.Ops {
		if op.Kind == k {
			return true
		}
	}
	return false
}

// assertScopesBalanced fails the test if any function's structured
// control scopes (block / loop / if vs. end) don't balance — a
// rewrite that dropped or added a scope boundary would break wasm
// validation.
func assertScopesBalanced(t *testing.T, p *Program) {
	t.Helper()
	for _, fn := range p.Funcs {
		depth := 0
		for i, op := range fn.Ops {
			switch op.Kind {
			case OpBlock, OpLoop, OpIf:
				depth++
			case OpEnd:
				depth--
				if depth < 0 {
					t.Fatalf("%s: op %d (%s): scope depth went negative", fn.Name, i, op.Kind)
				}
			}
		}
		if depth != 0 {
			t.Errorf("%s: ended at scope depth %d, want 0", fn.Name, depth)
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
