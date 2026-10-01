package arm64ssa_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

func TestArmRunStringBytesCopyOwnedResult(t *testing.T) {
	for _, n := range []int{0, 1, 7, 8, 15, 16, 257} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			f := ssa.NewFunc("main")
			b := f.NewBlock()
			src := constStr(f, b, strings.Repeat("x", n))
			out := addrCallOp(f, b, "__fern_string_bytes_copy", src)
			checks := []ssa.Value{
				f.AddOp(b, ssa.OpNe, out, src),
				f.AddOp(b, ssa.OpEq, load32u(f, b, out, -4), constOp(f, b, int64(n))),
				f.AddOp(b, ssa.OpEq, load32u(f, b, out, -8), constOp(f, b, 1)),
			}
			if n > 0 {
				checks = append(checks, f.AddOp(b, ssa.OpEq, load8u(f, b, out, int64(n-1)), constOp(f, b, 'x')))
				store8(f, b, out, constOp(f, b, 'y'), 0)
				checks = append(checks, f.AddOp(b, ssa.OpEq, load8u(f, b, src, 0), constOp(f, b, 'x')))
			}
			sum := constOp(f, b, 0)
			for _, check := range checks {
				sum = f.AddOp(b, ssa.OpAdd, sum, check)
			}
			f.SetRet(b, sum)
			if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 8); got != len(checks) {
				t.Fatalf("passed %d checks, want %d", got, len(checks))
			}
		})
	}
}
