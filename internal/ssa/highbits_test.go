package ssa

import "testing"

// narrowFor builds a one-block function whose parameter feeds `build`, and
// reports whether the value `build` returns came out narrow.
func narrowFor(t *testing.T, build func(f *Func, b *Block, x Value) Value) bool {
	t.Helper()
	f := NewFunc("f")
	x := f.AddParam()
	b := f.NewBlock()
	v := build(f, b, x)
	f.SetRet(b, f.AddOp(b, OpConstInt))
	return FindNarrowResults(f, BuildUses(f)).HighBitsDead(v)
}

// The whole of the analysis is the whitelist, so the test is the whitelist:
// a use that reads only the low 32 bits leaves the value narrow, and anything
// else does not. The asymmetry matters — an unnecessary fix costs three bytes,
// a missing one is a wrong answer — so a new op kind must arrive here as
// "reads the high bits" until someone shows otherwise.
func TestNarrowOnlyWhenEveryUseReadsTheLowHalf(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
		use  func(f *Func, b *Block, v Value)
	}{
		{"add", true, func(f *Func, b *Block, v Value) { f.AddOp(b, OpAdd, v, v) }},
		{"sub", true, func(f *Func, b *Block, v Value) { f.AddOp(b, OpSub, v, v) }},
		{"mul", true, func(f *Func, b *Block, v Value) { f.AddOp(b, OpMul, v, v) }},
		{"and", true, func(f *Func, b *Block, v Value) { f.AddOp(b, OpAnd, v, v) }},
		{"xor", true, func(f *Func, b *Block, v Value) { f.AddOp(b, OpXor, v, v) }},
		{"neg", true, func(f *Func, b *Block, v Value) { f.AddOp(b, OpNeg, v) }},
		{"trunc", true, func(f *Func, b *Block, v Value) { f.AddOp(b, OpTrunc, v) }},
		{"extendU", true, func(f *Func, b *Block, v Value) { f.AddOp(b, OpExtendU, v) }},
		{"extend8S", true, func(f *Func, b *Block, v Value) { f.AddOp(b, OpExtend8S, v) }},

		// The value shifted is read at 32 bits; the COUNT is not, because an
		// out-of-range one is left for the runtime to define.
		{"shl value", true, func(f *Func, b *Block, v Value) { f.AddOp(b, OpShl, v, f.AddOp(b, OpConstInt)) }},
		{"shl count", false, func(f *Func, b *Block, v Value) { f.AddOp(b, OpShl, f.AddOp(b, OpConstInt), v) }},

		// Args[0] is the address, Args[1] the value stored.
		{"store32 value", true, func(f *Func, b *Block, v Value) { f.AddOp(b, OpStore32, f.AddOp(b, OpConstInt), v) }},
		{"store32 address", false, func(f *Func, b *Block, v Value) { f.AddOp(b, OpStore32, v, f.AddOp(b, OpConstInt)) }},
		{"store (8 bytes)", false, func(f *Func, b *Block, v Value) { f.AddOp(b, OpStore, f.AddOp(b, OpConstInt), v) }},

		// The U variants read their operands as unsigned int64 outright.
		{"divU", false, func(f *Func, b *Block, v Value) { f.AddOp(b, OpDivU, v, v) }},
		{"remU", false, func(f *Func, b *Block, v Value) { f.AddOp(b, OpRemU, v, v) }},
		{"shrU", false, func(f *Func, b *Block, v Value) { f.AddOp(b, OpShrU, v, v) }},
		{"ltU", false, func(f *Func, b *Block, v Value) { f.AddOp(b, OpLtU, v, v) }},

		// The signed compares are emitted at 64 bits, so a garbage high half
		// changes the answer.
		{"lt", false, func(f *Func, b *Block, v Value) { f.AddOp(b, OpLt, v, v) }},
		{"eq", false, func(f *Func, b *Block, v Value) { f.AddOp(b, OpEq, v, v) }},

		// extendS exists to spread bit 31, which has to already be right.
		{"extendS", false, func(f *Func, b *Block, v Value) { f.AddOp(b, OpExtendS, v) }},
		// A callee assumes its parameters arrived correct.
		{"call argument", false, func(f *Func, b *Block, v Value) { f.AddOp(b, OpCall, v) }},
		// A phi's uses are not local to this block.
		{"phi", false, func(f *Func, b *Block, v Value) { f.AddOp(b, OpPhi, v) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := narrowFor(t, func(f *Func, b *Block, x Value) Value {
				v := f.AddOp(b, OpAdd, x, x)
				tc.use(f, b, v)
				return v
			})
			if got != tc.want {
				t.Errorf("HighBitsDead = %v, want %v", got, tc.want)
			}
		})
	}
}

// One narrow use does not make a value narrow: EVERY use has to read only the
// low half, because the fix is on the definition and there is one of it.
func TestOneWideUseIsEnoughToKeepTheFix(t *testing.T) {
	got := narrowFor(t, func(f *Func, b *Block, x Value) Value {
		v := f.AddOp(b, OpAdd, x, x)
		f.AddOp(b, OpAdd, v, v) // narrow
		f.AddOp(b, OpLtU, v, v) // not
		return v
	})
	if got {
		t.Error("a value read by an unsigned compare came out narrow because its other use was an add")
	}
}

// A return is a use, and it crosses into a caller that reads the register at
// whatever width its own types say.
func TestReturnedValueKeepsTheFix(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	b := f.NewBlock()
	v := f.AddOp(b, OpAdd, x, x)
	f.SetRet(b, v)
	if FindNarrowResults(f, BuildUses(f)).HighBitsDead(v) {
		t.Error("a returned value came out narrow")
	}
}

// A 64-bit result is not a candidate: nothing masks one, so there is nothing
// to skip, and saying otherwise would invite a caller to skip a fix that was
// never there.
func TestWideResultIsNeverNarrow(t *testing.T) {
	f := NewFunc("f")
	x := f.AddParam()
	b := f.NewBlock()
	v := f.AddOp(b, OpAdd, x, x)
	b.Ops[len(b.Ops)-1].Width = 64
	f.AddOp(b, OpAdd, v, v)
	f.SetRet(b, f.AddOp(b, OpConstInt))
	if FindNarrowResults(f, BuildUses(f)).HighBitsDead(v) {
		t.Error("a 64-bit result came out narrow")
	}
}
