package arm64ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// strOrd is the answer __str_ord owes: the first differing byte's difference,
// or the length difference when one string is a prefix of the other.
func strOrd(a, b string) int64 {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return int64(a[i]) - int64(b[i])
		}
	}
	return int64(len(a)) - int64(len(b))
}

// __str_ord routes through the NEON mismatch kernel, whose 16-byte blocks and
// overlapping short windows put every length from 0 to 40 in a different
// combination; a difference planted at the first, middle and last index, and
// a prefix in each direction, must each come back with the exact difference.
func TestArmRunStrOrdComparesEveryLengthClass(t *testing.T) {
	for n := 0; n <= 40; n++ {
		mutated := func(at int) string {
			b := []byte(bcopyProbe[:n])
			b[at] ^= 0x20
			return string(b)
		}
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		heap := addrCallOp(f, e, "__str_slice", constStr(f, e, bcopyProbe), constOp(f, e, 0), constOp(f, e, int64(n)))
		var others []string
		if n > 0 {
			others = append(others, mutated(0), mutated(n/2), mutated(n-1))
		}
		others = append(others, bcopyProbe[:n+1])
		if n > 0 {
			others = append(others, bcopyProbe[:n-1])
		}
		// Each comparison is checked against its exact answer, in both
		// argument orders, and the checks are summed.
		acc := f.AddOp(e, ssa.OpEq, callOp(f, e, "__str_ord", heap, constStr(f, e, bcopyProbe[:n])), constOp(f, e, 0))
		want := 1
		for _, o := range others {
			acc = f.AddOp(e, ssa.OpAdd, acc, f.AddOp(e, ssa.OpEq,
				callOp(f, e, "__str_ord", heap, constStr(f, e, o)), constOp(f, e, strOrd(bcopyProbe[:n], o))))
			acc = f.AddOp(e, ssa.OpAdd, acc, f.AddOp(e, ssa.OpEq,
				callOp(f, e, "__str_ord", constStr(f, e, o), heap), constOp(f, e, strOrd(o, bcopyProbe[:n]))))
			want += 2
		}
		f.SetRet(e, acc)
		if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 22); got != want {
			t.Errorf("__str_ord at length %d: %d of %d comparisons gave the exact difference", n, got, want)
		}
	}
}
