package ssa

import "testing"

// TestShiftFoldShl — `1 << 3` folds to 8.
func TestShiftFoldShl(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 3
	r := f.AddOp(entry, OpShl, a, b)
	f.SetRet(entry, r)

	Fold(f)

	if got := entry.Ops[2]; got.Kind != OpConstInt || got.Imm != 8 {
		t.Errorf("1<<3 = {%v %d}, want {OpConstInt 8}", got.Kind, got.Imm)
	}
}

// TestShiftFoldShr — `0xff >> 4` folds to 0x0f.
func TestShiftFoldShr(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0xff
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 4
	r := f.AddOp(entry, OpShr, a, b)
	f.SetRet(entry, r)

	Fold(f)

	if got := entry.Ops[2]; got.Kind != OpConstInt || got.Imm != 0xf {
		t.Errorf("0xff>>4 = {%v %d}, want {OpConstInt 0xf}", got.Kind, got.Imm)
	}
}

// TestShiftCountMaskedWhenFolded — a shift count >= the operand width
// does NOT trap: wasm (and arm/x86) mask the count to the operand width
// (i32: count mod 32). So `1 << 64` on an i32 op is `1 << (64 mod 32)`
// = `1 << 0` = 1, and folding to that masked result matches the runtime
// (mirroring internal/ir/fold.go). Regression for I1 in
// docs/ADVERSARIAL-REVIEW-2026-06.md (the old folder left it unfolded
// under the mistaken belief that an out-of-range shift trapped).
func TestShiftCountMaskedWhenFolded(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 64
	r := f.AddOp(entry, OpShl, a, b) // i32 (Width 0): 1 << (64 & 31) = 1
	f.SetRet(entry, r)

	Fold(f)

	if got := entry.Ops[2]; got.Kind != OpConstInt || got.Imm != 1 {
		t.Errorf("1<<64 (i32) = {%v %d}, want {OpConstInt 1} (count masked mod 32)", got.Kind, got.Imm)
	}
}

// TestEvalShiftCountMasked — the Eval sibling of the fold rule above. Eval is
// the oracle the codegen tests diff real assembly against, so an unmasked
// count here does not just report a wrong number: it agrees with a backend
// that renders a 32-bit shift on a 64-bit register (both go to 0 for a count
// >= 32) and hides the bug. Go's `a << 64` yields 0 rather than wrapping, so
// this needs the same explicit `& 31` / `& 63` foldIntBinary32/64 apply.
func TestEvalShiftCountMasked(t *testing.T) {
	build := func(k OpKind, a, b int64, width int8) *Func {
		f := NewFunc("f")
		e := f.NewBlock()
		konst := func(v int64) Value {
			c := constIn(f, e, v)
			e.Ops[len(e.Ops)-1].Width = width
			return c
		}
		r := f.AddOp(e, k, konst(a), konst(b))
		e.Ops[len(e.Ops)-1].Width = width
		f.SetRet(e, r)
		return f
	}
	cases := []struct {
		name  string
		kind  OpKind
		a, b  int64
		width int8
		want  int64
	}{
		{"shl_i32_124", OpShl, 460, 124, 0, -1073741824}, // 460 << 28
		{"shl_i32_32", OpShl, 1, 32, 0, 1},               // count & 31 == 0
		{"shl_i32_neg1", OpShl, 1, -1, 0, -2147483648},   // count & 31 == 31
		{"shr_i32_33", OpShr, -8, 33, 0, -4},
		{"shru_i32_33", OpShrU, -8, 33, 0, 2147483644}, // 0xfffffff8 >>u 1
		{"shl_i64_64", OpShl, 1, 64, 64, 1},            // count & 63 == 0
		{"shl_i64_65", OpShl, 1, 65, 64, 2},
		{"shr_i64_33", OpShr, 1 << 40, 33, 64, 128},
		{"shr_i64_65", OpShr, -8, 65, 64, -4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Eval(build(tc.kind, tc.a, tc.b, tc.width))
			if err != nil {
				t.Fatalf("Eval: %v", err)
			}
			if got != tc.want {
				t.Errorf("%v(%d, %d) at width %d = %d, want %d",
					tc.kind, tc.a, tc.b, tc.width, got, tc.want)
			}
		})
	}
}

// TestShiftSimplifyZero — `x << 0`, `x >> 0`, `x >>u 0` all
// alias to `x`. The signed/unsigned distinction matters for
// the shift's runtime semantics; the zero-count identity
// holds for all three.
func TestShiftSimplifyZero(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	zero := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 0
	shl := f.AddOp(entry, OpShl, x, zero)
	shr := f.AddOp(entry, OpShr, shl, zero)
	shrU := f.AddOp(entry, OpShrU, shr, zero)
	f.SetRet(entry, shrU)

	Simplify(f)
	if entry.Term.Value != x {
		t.Errorf("Term.Value = %v, want %v (shifts by 0 → x)", entry.Term.Value, x)
	}
}

// TestNegFold — `-(const 42)` folds to const_int -42.
func TestNegFold(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	c := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 42
	r := f.AddOp(entry, OpNeg, c)
	f.SetRet(entry, r)

	Fold(f)

	if got := entry.Ops[1]; got.Kind != OpConstInt || got.Imm != -42 {
		t.Errorf("Neg = {%v %d}, want {OpConstInt -42}", got.Kind, got.Imm)
	}
}

// TestSimplifyDoubleNeg — `neg(neg(x))` aliases directly to x.
// Two's complement preserves round-trip even for INT_MIN
// (`-(-INT_MIN) == INT_MIN`).
func TestSimplifyDoubleNeg(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	inner := f.AddOp(entry, OpNeg, x)
	outer := f.AddOp(entry, OpNeg, inner)
	f.SetRet(entry, outer)

	Simplify(f)

	if entry.Term.Value != x {
		t.Errorf("Term.Value = %v, want %v (neg(neg(x)) → x)",
			entry.Term.Value, x)
	}
}

// TestSimplifyDoubleFNeg — `fneg(fneg(x))` aliases to x.
// IEEE-754: -(-NaN)=NaN, -(-0.0)=0.0 — both round-trip cleanly.
func TestSimplifyDoubleFNeg(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	inner := f.AddOp(entry, OpFNeg, x)
	outer := f.AddOp(entry, OpFNeg, inner)
	f.SetRet(entry, outer)

	Simplify(f)

	if entry.Term.Value != x {
		t.Errorf("Term.Value = %v, want %v (fneg(fneg(x)) → x)",
			entry.Term.Value, x)
	}
}

// TestSimplifyMixedNegFNegNotCollapsed — `fneg(neg(x))` is NOT
// an identity (different op kinds), so it must stay. Guards
// the kind-mismatch case.
func TestSimplifyMixedNegFNegNotCollapsed(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	inner := f.AddOp(entry, OpNeg, x)
	outer := f.AddOp(entry, OpFNeg, inner)
	f.SetRet(entry, outer)

	Simplify(f)

	if entry.Term.Value != outer {
		t.Errorf("Term.Value = %v, want %v (mixed neg/fneg must stay)",
			entry.Term.Value, outer)
	}
}

// TestNegLeavesNonConst — Neg of a Param stays as OpNeg.
func TestNegLeavesNonConst(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	entry := f.NewBlock()
	r := f.AddOp(entry, OpNeg, x)
	f.SetRet(entry, r)

	Fold(f)
	if entry.Ops[0].Kind != OpNeg {
		t.Errorf("Kind = %v, want OpNeg (non-const arg)", entry.Ops[0].Kind)
	}
}

// TestShiftCanonicalizeUnaffected — shifts are NOT
// commutative; Canonicalize must not swap operands.
func TestShiftCanonicalizeUnaffected(t *testing.T) {
	f := NewFunc("f")
	a := f.AddParam()
	b := f.AddParam()
	entry := f.NewBlock()
	op := &Op{Kind: OpShl, Result: f.NewValue(), Args: []Value{b, a}}
	entry.Ops = append(entry.Ops, op)
	f.SetRet(entry, op.Result)

	Canonicalize(f)

	if op.Args[0] != b || op.Args[1] != a {
		t.Errorf("Args = %v, want [b, a] (shift not commutative)", op.Args)
	}
}

// TestShiftNegInOptimizePipeline — end-to-end via Optimize.
// `-((1 << 3) + (8 - 8))` folds to const_int -8.
func TestShiftNegInOptimizePipeline(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	a := f.AddOp(entry, OpConstInt)
	entry.Ops[0].Imm = 1
	b := f.AddOp(entry, OpConstInt)
	entry.Ops[1].Imm = 3
	eight := f.AddOp(entry, OpShl, a, b) // 8
	eight2 := f.AddOp(entry, OpConstInt)
	entry.Ops[3].Imm = 8
	subZero := f.AddOp(entry, OpSub, eight2, eight2) // 0
	sum := f.AddOp(entry, OpAdd, eight, subZero)     // 8
	neg := f.AddOp(entry, OpNeg, sum)                // -8
	f.SetRet(entry, neg)

	Optimize(f)

	if len(entry.Ops) != 1 {
		t.Fatalf("Ops = %d, want 1; kinds %v", len(entry.Ops), opKinds(entry.Ops))
	}
	if got := entry.Ops[0]; got.Kind != OpConstInt || got.Imm != -8 {
		t.Errorf("survivor = {%v %d}, want {OpConstInt -8}", got.Kind, got.Imm)
	}
}
