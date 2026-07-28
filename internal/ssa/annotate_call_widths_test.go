package ssa

import "testing"

// AnnotateCallWidths marks a call whose result occupies the full 64-bit
// register, so the backend skips the i32 sign-extension that would truncate it.
// An i64 return is the obvious case; a FLOAT return is the subtle one, and it
// is not covered by the width check alone. Floats live in a general register as
// their f64 bit pattern, so an f32 return is 32 bits of TYPE but 64 bits of
// REGISTER — masking it to 32 keeps the low mantissa half and discards the sign
// and exponent, which reads back as a denormal ≈ 0. Before ReturnFloat was
// consulted here, every f32 crossing a call arrived as 0.0 on arm64-ssa.
func TestAnnotateCallWidths(t *testing.T) {
	cases := []struct {
		name        string
		retWidth    int8
		returnFloat bool
		want        int8
	}{
		{"i32 return is left narrow", 32, false, 0},
		{"i64 return is widened", 64, false, 64},
		{"f64 return is widened", 64, true, 64},
		{"f32 return is widened despite width 32", 32, true, 64},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			callee := NewFunc("callee")
			cb := callee.NewBlock()
			callee.SetRet(cb, zeroConst(callee, cb))
			callee.ReturnWidth = c.retWidth
			callee.ReturnFloat = c.returnFloat

			caller := NewFunc("caller")
			eb := caller.NewBlock()
			r := caller.AddOp(eb, OpCall)
			eb.Ops[len(eb.Ops)-1].Str = "callee"
			caller.SetRet(eb, r)

			AnnotateCallWidths(map[string]*Func{"caller": caller, "callee": callee})

			if got := eb.Ops[0].Width; got != c.want {
				t.Errorf("call Width = %d, want %d", got, c.want)
			}
		})
	}
}

// A callee absent from the map is a backend-emitted runtime helper; its i32 /
// pointer return needs no annotation and must be left alone.
func TestAnnotateCallWidthsLeavesUnknownCallee(t *testing.T) {
	caller := NewFunc("caller")
	eb := caller.NewBlock()
	r := caller.AddOp(eb, OpCall)
	eb.Ops[len(eb.Ops)-1].Str = "__fern_alloc"
	caller.SetRet(eb, r)

	AnnotateCallWidths(map[string]*Func{"caller": caller})

	if got := eb.Ops[0].Width; got != 0 {
		t.Errorf("unknown callee: call Width = %d, want 0 (unchanged)", got)
	}
}

func zeroConst(f *Func, b *Block) Value {
	x := f.AddOp(b, OpConstInt)
	b.Ops[len(b.Ops)-1].Imm = 0
	return x
}
