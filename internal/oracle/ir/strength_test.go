package ir

import "testing"

// `x * 4` rewrites to `x << 2`. The original const-of-the-multiplier
// turns into the shift count; the OpMul becomes OpShl.
func TestReduceStrengthMulPow2(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpLoadLocal, I32: 0}, // x
			{Kind: OpConstI32, I32: 4},
			{Kind: OpMul},
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	ReduceStrength(p)
	hasShl := false
	hasMul := false
	for _, op := range fn.Ops {
		if op.Kind == OpShl {
			hasShl = true
		}
		if op.Kind == OpMul {
			hasMul = true
		}
	}
	if !hasShl {
		t.Errorf("expected OpShl after reduction:\n%s", p)
	}
	if hasMul {
		t.Errorf("OpMul should have been replaced:\n%s", p)
	}
	// Shift count must be log2(4) = 2.
	for _, op := range fn.Ops {
		if op.Kind == OpConstI32 && op.I32 != 2 {
			t.Errorf("expected shift amount 2, got %d:\n%s", op.I32, p)
		}
	}
}

// `x * 1` collapses to just `x` — both the const and the mul go
// away; the loaded value flows into the next op directly.
func TestReduceStrengthMulOne(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpLoadLocal, I32: 0},
			{Kind: OpConstI32, I32: 1},
			{Kind: OpMul},
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	ReduceStrength(p)
	for _, op := range fn.Ops {
		if op.Kind == OpMul || (op.Kind == OpConstI32 && op.I32 == 1) {
			t.Errorf("`x * 1` should drop both ops:\n%s", p)
		}
	}
}

// `x * 0` keeps the operand-stack discipline by inserting a Drop —
// any side effects in the operand position still execute, then the
// constant 0 takes the multiply's place. The OpDrop pairs with the
// const-of-zero so Fold can't strip it without losing the side
// effect.
func TestReduceStrengthMulZeroPreservesSideEffect(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpCallDirect, Str: "side_effect", I32: 0}, // produces an i32 we'd otherwise multiply
			{Kind: OpConstI32, I32: 0},
			{Kind: OpMul},
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	ReduceStrength(p)
	hasCall := false
	hasDrop := false
	hasZero := false
	for _, op := range fn.Ops {
		if op.Kind == OpCallDirect {
			hasCall = true
		}
		if op.Kind == OpDrop {
			hasDrop = true
		}
		if op.Kind == OpConstI32 && op.I32 == 0 {
			hasZero = true
		}
	}
	if !hasCall {
		t.Errorf("call (side effect) must survive `* 0`:\n%s", p)
	}
	if !hasDrop {
		t.Errorf("expected OpDrop to consume the call's value:\n%s", p)
	}
	if !hasZero {
		t.Errorf("expected `const.i32 0` as the product result:\n%s", p)
	}
}

// `x + 0`, `x - 0`, `x | 0`, `x ^ 0`, `x << 0`, `x >> 0` all
// collapse to just `x`.
func TestReduceStrengthIdentityOps(t *testing.T) {
	for _, k := range []OpKind{OpAdd, OpSub, OpOr, OpXor, OpShl, OpShrS} {
		fn := &Func{
			Name: "f",
			Ops: []Op{
				{Kind: OpLoadLocal, I32: 0},
				{Kind: OpConstI32, I32: 0},
				{Kind: k},
				{Kind: OpReturn},
			},
		}
		p := &Program{Funcs: []*Func{fn}}
		ReduceStrength(p)
		for _, op := range fn.Ops {
			if op.Kind == k {
				t.Errorf("`x %s 0` should drop the op pair:\n%s", k, p)
			}
		}
	}
}

// `x & -1` collapses to `x` (bit-mask of all ones is identity).
// `x & 0` collapses to `<expr>; drop; const 0` so any side
// effect runs.
func TestReduceStrengthBitwiseAnd(t *testing.T) {
	// & -1
	{
		fn := &Func{
			Name: "f",
			Ops: []Op{
				{Kind: OpLoadLocal, I32: 0},
				{Kind: OpConstI32, I32: -1},
				{Kind: OpAnd},
				{Kind: OpReturn},
			},
		}
		p := &Program{Funcs: []*Func{fn}}
		ReduceStrength(p)
		for _, op := range fn.Ops {
			if op.Kind == OpAnd {
				t.Errorf("`x & -1` should be eliminated:\n%s", p)
			}
		}
	}
	// & 0 with a side effect in the operand position
	{
		fn := &Func{
			Name: "f",
			Ops: []Op{
				{Kind: OpCallDirect, Str: "side_effect", I32: 0},
				{Kind: OpConstI32, I32: 0},
				{Kind: OpAnd},
				{Kind: OpReturn},
			},
		}
		p := &Program{Funcs: []*Func{fn}}
		ReduceStrength(p)
		// Drop must be present so the call's value gets consumed.
		hasDrop := false
		for _, op := range fn.Ops {
			if op.Kind == OpDrop {
				hasDrop = true
			}
		}
		if !hasDrop {
			t.Errorf("`call & 0` must keep an OpDrop for the call's value:\n%s", p)
		}
	}
}

// Division and remainder are NOT touched — signed semantics differ
// between `x / 2^k` and `x >> k` for negative dividends, and same
// for `% 2^k` vs `& mask`. Replacing them would silently change
// behaviour.
func TestReduceStrengthSkipsSignedDivAndRem(t *testing.T) {
	for _, k := range []OpKind{OpDivS, OpRemS} {
		fn := &Func{
			Name: "f",
			Ops: []Op{
				{Kind: OpLoadLocal, I32: 0},
				{Kind: OpConstI32, I32: 4},
				{Kind: k},
				{Kind: OpReturn},
			},
		}
		p := &Program{Funcs: []*Func{fn}}
		ReduceStrength(p)
		survived := false
		for _, op := range fn.Ops {
			if op.Kind == k {
				survived = true
			}
		}
		if !survived {
			t.Errorf("%s by power of 2 must NOT be reduced:\n%s", k, p)
		}
	}
}

// Non-power-of-two multipliers stay as multiplies — only the
// power-of-two case has a cheap shift equivalent.
func TestReduceStrengthLeavesNonPow2Multipliers(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpLoadLocal, I32: 0},
			{Kind: OpConstI32, I32: 7},
			{Kind: OpMul},
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	ReduceStrength(p)
	hasMul := false
	for _, op := range fn.Ops {
		if op.Kind == OpMul {
			hasMul = true
		}
	}
	if !hasMul {
		t.Errorf("`* 7` must keep its multiply:\n%s", p)
	}
}

// End-to-end through OptimizeCleanup: `x * 4 + 0` becomes
// `x << 2`. The const+drop and identity rules combine in a single
// fixed-point sweep.
func TestReduceStrengthCleanupCascade(t *testing.T) {
	p := lowerSource(t, `function f(x: i32): i32 { return x * 4 + 0; }`)
	OptimizeCleanup(p)
	fn := findFunc(p, "f")
	hasShl := false
	for _, op := range fn.Ops {
		if op.Kind == OpShl {
			hasShl = true
		}
		if op.Kind == OpMul {
			t.Errorf("multiply should have reduced to shift:\n%s", p)
		}
		if op.Kind == OpAdd {
			t.Errorf("`+ 0` should have been eliminated:\n%s", p)
		}
	}
	if !hasShl {
		t.Errorf("expected the reduced shift in output:\n%s", p)
	}
}

// i64-width multiply by a power of two reduces to an i64 shift with
// an i64 shift count (so the wasm validator sees matching operand
// widths). `x * 8` at 64-bit → `x << 3`.
func TestReduceStrengthMulPow2I64(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpLoadLocal, I32: 0},
			{Kind: OpConstI64, I64: 8},
			{Kind: OpMul, Width: 64},
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	ReduceStrength(p)
	var shl, count *Op
	for i := range fn.Ops {
		switch fn.Ops[i].Kind {
		case OpMul:
			t.Errorf("i64 OpMul should have been replaced:\n%s", p)
		case OpShl:
			shl = &fn.Ops[i]
		case OpConstI64:
			count = &fn.Ops[i]
		case OpConstI32:
			t.Errorf("i64 shift count must stay i64, found OpConstI32:\n%s", p)
		}
	}
	if shl == nil || shl.Width != 64 {
		t.Errorf("expected an i64 (Width 64) OpShl:\n%s", p)
	}
	if count == nil || count.I64 != 3 {
		t.Errorf("expected i64 shift amount 3 (log2 8):\n%s", p)
	}
}

// Unsigned division by a power of two is exact — `x /u 4` reduces to
// `x >>u 2` (a logical shift). Signed div by 4 (the other case) is
// left alone by TestReduceStrengthSkipsSignedDivAndRem.
func TestReduceStrengthUnsignedDivPow2(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpLoadLocal, I32: 0},
			{Kind: OpConstI32, I32: 4},
			{Kind: OpDivS, Unsigned: true},
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	ReduceStrength(p)
	var shr, count *Op
	for i := range fn.Ops {
		switch fn.Ops[i].Kind {
		case OpDivS:
			t.Errorf("unsigned div by 2^k should reduce to a shift:\n%s", p)
		case OpShrS:
			shr = &fn.Ops[i]
		case OpConstI32:
			count = &fn.Ops[i]
		}
	}
	if shr == nil || !shr.Unsigned {
		t.Errorf("expected an unsigned OpShrS (shr_u):\n%s", p)
	}
	if count == nil || count.I32 != 2 {
		t.Errorf("expected shift amount 2 (log2 4):\n%s", p)
	}
}

// Unsigned remainder by a power of two is a low-bit mask — `x %u 8`
// reduces to `x & 7`.
func TestReduceStrengthUnsignedRemPow2(t *testing.T) {
	fn := &Func{
		Name: "f",
		Ops: []Op{
			{Kind: OpLoadLocal, I32: 0},
			{Kind: OpConstI32, I32: 8},
			{Kind: OpRemS, Unsigned: true},
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	ReduceStrength(p)
	var and, mask *Op
	for i := range fn.Ops {
		switch fn.Ops[i].Kind {
		case OpRemS:
			t.Errorf("unsigned rem by 2^k should reduce to a mask:\n%s", p)
		case OpAnd:
			and = &fn.Ops[i]
		case OpConstI32:
			mask = &fn.Ops[i]
		}
	}
	if and == nil {
		t.Errorf("expected an OpAnd:\n%s", p)
	}
	if mask == nil || mask.I32 != 7 {
		t.Errorf("expected mask 7 (8-1):\n%s", p)
	}
}

// Division by 1 is exact for either sign — `x / 1` collapses to `x`.
// Remainder by 1 is always 0 — `x % 1` becomes `<expr>; drop; const 0`
// so any side effect in the operand position still runs. Both cases
// apply to signed OpDivS / OpRemS (divisor 1 has no rounding hazard).
func TestReduceStrengthDivRemByOne(t *testing.T) {
	// x / 1 = x
	{
		fn := &Func{
			Name: "f",
			Ops: []Op{
				{Kind: OpLoadLocal, I32: 0},
				{Kind: OpConstI32, I32: 1},
				{Kind: OpDivS},
				{Kind: OpReturn},
			},
		}
		p := &Program{Funcs: []*Func{fn}}
		ReduceStrength(p)
		for _, op := range fn.Ops {
			if op.Kind == OpDivS || (op.Kind == OpConstI32 && op.I32 == 1) {
				t.Errorf("`x / 1` should drop both ops:\n%s", p)
			}
		}
	}
	// call() % 1 = 0, side effect preserved via OpDrop
	{
		fn := &Func{
			Name: "f",
			Ops: []Op{
				{Kind: OpCallDirect, Str: "side_effect", I32: 0},
				{Kind: OpConstI32, I32: 1},
				{Kind: OpRemS},
				{Kind: OpReturn},
			},
		}
		p := &Program{Funcs: []*Func{fn}}
		ReduceStrength(p)
		hasCall, hasDrop, hasRem := false, false, false
		for _, op := range fn.Ops {
			switch op.Kind {
			case OpCallDirect:
				hasCall = true
			case OpDrop:
				hasDrop = true
			case OpRemS:
				hasRem = true
			}
		}
		if hasRem {
			t.Errorf("`x %% 1` should be eliminated:\n%s", p)
		}
		if !hasCall || !hasDrop {
			t.Errorf("`call() %% 1` must keep the call and a Drop for its value:\n%s", p)
		}
	}
}

// Idempotent: a second ReduceStrength on already-reduced ops
// produces identical output.
func TestReduceStrengthIsIdempotent(t *testing.T) {
	p := lowerSource(t, `function f(x: i32): i32 { return x * 8 + x * 1; }`)
	ReduceStrength(p)
	before := p.String()
	ReduceStrength(p)
	after := p.String()
	if before != after {
		t.Errorf("ReduceStrength not idempotent:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
