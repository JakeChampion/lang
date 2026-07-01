package ssa

import "testing"

// binConst builds a function returning (a OP b) for two integer constants.
func binConst(kind OpKind, a, b int64) *Func {
	f := NewFunc("d")
	e := f.NewBlock()
	f.SetRet(e, f.AddOp(e, kind, constIn(f, e, a), constIn(f, e, b)))
	return f
}

func TestEvalDivRem(t *testing.T) {
	cases := []struct {
		name string
		kind OpKind
		a, b int64
		want int64
	}{
		{"div", OpDiv, 17, 5, 3},
		{"div-neg", OpDiv, -17, 5, -3}, // truncates toward zero
		{"rem", OpRem, 17, 5, 2},
		{"rem-neg", OpRem, -17, 5, -2},
		{"divU", OpDivU, 17, 5, 3},
		{"remU", OpRemU, 17, 5, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Eval(binConst(tc.kind, tc.a, tc.b))
			if err != nil {
				t.Fatalf("Eval: %v", err)
			}
			if got != tc.want {
				t.Errorf("%v(%d,%d) = %d, want %d", tc.kind, tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// Division by zero is a clear error, not a wrong answer.
func TestEvalDivByZero(t *testing.T) {
	if _, err := Eval(binConst(OpDiv, 1, 0)); err == nil {
		t.Error("expected a division-by-zero error")
	}
	if _, err := Eval(binConst(OpRem, 1, 0)); err == nil {
		t.Error("expected a remainder-by-zero error")
	}
}
