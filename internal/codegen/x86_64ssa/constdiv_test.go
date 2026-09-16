package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// binOnParams builds f(a, b) = a <kind> b at the given width, with b replaced
// by the constant n when n != 0.
func binOnParams(kind ssa.OpKind, width int8, n int64) func() *ssa.Func {
	return func() *ssa.Func {
		f := ssa.NewFunc("f")
		a, b := f.AddParam(), f.AddParam()
		e := f.NewBlock()
		right := b
		if n != 0 {
			right = constOp(f, e, n)
			e.Ops[len(e.Ops)-1].Width = width
		}
		r := f.AddOp(e, kind, a, right)
		e.Ops[len(e.Ops)-1].Width = width
		f.SetRet(e, r)
		return f
	}
}

// A constant shift count is the instruction's own immediate: no copy into
// cl, no rcx save around it.
func TestConstantShiftCountIsAnImmediate(t *testing.T) {
	f := binOnParams(ssa.OpShr, 32, 3)()
	asm, err := EmitAsm(f, 8)
	if err != nil {
		t.Fatalf("EmitAsm: %v", err)
	}
	if !strings.Contains(asm, "sar eax, 3") && !strings.Contains(asm, ", 3\n") {
		t.Errorf("shift by a constant is not an immediate form:\n%s", asm)
	}
	if strings.Contains(asm, "push rcx") || strings.Contains(asm, ", cl") {
		t.Errorf("shift by a constant still goes through cl:\n%s", asm)
	}
	differential(t, binOnParams(ssa.OpShr, 32, 3), [][]int64{{-9, 0}, {9, 0}, {-2147483648, 0}, {2147483647, 0}})
	differential(t, binOnParams(ssa.OpShl, 64, 40), [][]int64{{-9, 0}, {9, 0}, {1 << 30, 0}})
	differential(t, binOnParams(ssa.OpShrU, 32, 4), [][]int64{{-9, 0}, {9, 0}, {-2147483648, 0}})
}

// Division and remainder by a power of two lower to shifts, and agree with the
// interpreter on every sign, including the most negative dividend and the
// remainders that must keep the dividend's sign.
func TestPowerOfTwoDivisionAvoidsIdiv(t *testing.T) {
	args32 := [][]int64{{-9, 0}, {-8, 0}, {-7, 0}, {-1, 0}, {0, 0}, {1, 0}, {7, 0}, {8, 0}, {9, 0}, {-2147483648, 0}, {2147483647, 0}}
	args64 := [][]int64{{-9, 0}, {-1, 0}, {0, 0}, {9, 0}, {-9223372036854775808, 0}, {9223372036854775807, 0}, {1 << 40, 0}, {-(1 << 40) - 1, 0}}
	for _, kind := range []ssa.OpKind{ssa.OpDiv, ssa.OpRem, ssa.OpDivU, ssa.OpRemU} {
		for _, n := range []int64{2, 4, 8, 1024, 1 << 30} {
			asm, err := EmitAsm(binOnParams(kind, 32, n)(), 8)
			if err != nil {
				t.Fatalf("EmitAsm: %v", err)
			}
			if strings.Contains(asm, "idiv") || strings.Contains(asm, "\tdiv ") {
				t.Errorf("%v by %d still divides:\n%s", kind, n, asm)
			}
			differential(t, binOnParams(kind, 32, n), args32)
		}
		for _, n := range []int64{2, 1 << 33, 1 << 62} {
			asm, err := EmitAsm(binOnParams(kind, 64, n)(), 8)
			if err != nil {
				t.Fatalf("EmitAsm: %v", err)
			}
			if strings.Contains(asm, "idiv") || strings.Contains(asm, "\tdiv ") {
				t.Errorf("%v by %d at 64 bits still divides:\n%s", kind, n, asm)
			}
			differential(t, binOnParams(kind, 64, n), args64)
		}
	}
}

// Division and remainder by any other i32 constant multiply by the
// reciprocal instead, and agree with the interpreter on every sign, including
// the most negative dividend, the unsigned dividends past 2^31, the 33-bit
// unsigned magics (7, 641) and the wrapped signed ones (a divisor past 2^30).
func TestConstantDivisionUsesTheReciprocal(t *testing.T) {
	args := [][]int64{{-9, 0}, {-8, 0}, {-7, 0}, {-1, 0}, {0, 0}, {1, 0}, {7, 0}, {8, 0}, {9, 0},
		{99, 0}, {100, 0}, {101, 0}, {-100, 0}, {123456789, 0}, {-123456789, 0},
		{-2147483648, 0}, {2147483647, 0}, {-2147483647, 0}}
	for _, kind := range []ssa.OpKind{ssa.OpDiv, ssa.OpRem, ssa.OpDivU, ssa.OpRemU} {
		for _, n := range []int64{3, 5, 6, 7, 10, 100, 641, 1000, 1000000007, 2147483647, -3, -100, -5, -2147483647} {
			asm, err := EmitAsm(binOnParams(kind, 32, n)(), 8)
			if err != nil {
				t.Fatalf("EmitAsm: %v", err)
			}
			if strings.Contains(asm, "idiv") || strings.Contains(asm, "\tdiv ") {
				t.Errorf("%v by %d still divides:\n%s", kind, n, asm)
			}
			differential(t, binOnParams(kind, 32, n), args)
		}
	}
}

// The sign bit's own power of two, a negative power of two, a 64-bit
// non-power, and a divisor only a register knows all stay with the real
// division.
func TestNonPowerOfTwoDivisionStillDivides(t *testing.T) {
	for _, c := range []struct {
		name  string
		width int8
		n     int64
	}{{"the sign bit", 32, 1 << 31}, {"minus eight", 32, -8}, {"three at 64 bits", 64, 3}, {"a register", 32, 0}} {
		asm, err := EmitAsm(binOnParams(ssa.OpDiv, c.width, c.n)(), 8)
		if err != nil {
			t.Fatalf("EmitAsm: %v", err)
		}
		if !strings.Contains(asm, "idiv") {
			t.Errorf("%s: division by %d no longer divides:\n%s", c.name, c.n, asm)
		}
	}
	differential(t, binOnParams(ssa.OpDiv, 32, -8), [][]int64{{-9, 0}, {9, 0}, {-2147483648, 0}})
	differential(t, binOnParams(ssa.OpDiv, 64, 3), [][]int64{{-9, 0}, {9, 0}, {1 << 40, 0}})
	differential(t, binOnParams(ssa.OpRem, 32, 0), [][]int64{{-9, 4}, {9, -4}, {7, 3}})
}
