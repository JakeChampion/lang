package ssa

import "testing"

// const64 adds a 64-bit integer constant.
func const64(f *Func, b *Block, imm int64) Value {
	v := f.AddOp(b, OpConstInt)
	op := b.Ops[len(b.Ops)-1]
	op.Imm = imm
	op.Width = 64
	return v
}

func TestEvalNot(t *testing.T) {
	for _, tc := range []struct{ in, want int64 }{{0, 1}, {1, 0}, {5, 0}, {-3, 0}} {
		f := NewFunc("not")
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, OpNot, constIn(f, e, tc.in)))
		got, err := Eval(f)
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
		if got != tc.want {
			t.Errorf("not(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestEvalTruncExtend(t *testing.T) {
	// Trunc: low 32 bits, sign-aware. 0x1_0000_0007 -> 7.
	t.Run("trunc", func(t *testing.T) {
		f := NewFunc("t")
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, OpTrunc, const64(f, e, 0x100000007)))
		if got, _ := Eval(f); got != 7 {
			t.Errorf("trunc(0x100000007) = %d, want 7", got)
		}
	})
	// 0x80000000 as i32 is negative; ExtendS keeps it negative, ExtendU makes
	// it the positive 2^31.
	for _, tc := range []struct {
		name string
		kind OpKind
		want int64
	}{
		{"extendS", OpExtendS, -2147483648},
		{"extendU", OpExtendU, 2147483648},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := NewFunc("e")
			e := f.NewBlock()
			ext := f.AddOp(e, tc.kind, constIn(f, e, 0x80000000))
			e.Ops[len(e.Ops)-1].Width = 64 // i32 -> i64 produces an i64 result
			f.SetRet(e, ext)
			if got, _ := Eval(f); got != tc.want {
				t.Errorf("%s(0x80000000) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
	// Sub-word sign extension.
	t.Run("extend8S", func(t *testing.T) {
		f := NewFunc("e8")
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, OpExtend8S, constIn(f, e, 200))) // int8(200) = -56
		if got, _ := Eval(f); got != -56 {
			t.Errorf("extend8S(200) = %d, want -56", got)
		}
	})
	t.Run("extend16S", func(t *testing.T) {
		f := NewFunc("e16")
		e := f.NewBlock()
		f.SetRet(e, f.AddOp(e, OpExtend16S, constIn(f, e, 0xFFFF))) // int16(0xFFFF) = -1
		if got, _ := Eval(f); got != -1 {
			t.Errorf("extend16S(0xFFFF) = %d, want -1", got)
		}
	})
}
