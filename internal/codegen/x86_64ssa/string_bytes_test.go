package x86_64ssa

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

func TestStringBytesCopyOwnedResult(t *testing.T) {
	for _, n := range []int{0, 1, 7, 8, 15, 16, 257} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			f := ssa.NewFunc("main")
			b := f.NewBlock()
			src := constStr(f, b, strings.Repeat("x", n))
			out := callPtrOp(f, b, "__fern_string_bytes_copy", src)
			checks := []ssa.Value{
				f.AddOp(b, ssa.OpNe, out, src),
				f.AddOp(b, ssa.OpEq, loadMem(f, b, out, -4, ssa.OpLoad32U), constOp(f, b, int64(n))),
				f.AddOp(b, ssa.OpEq, loadMem(f, b, out, -8, ssa.OpLoad32U), constOp(f, b, 1)),
			}
			if n > 0 {
				checks = append(checks, f.AddOp(b, ssa.OpEq, loadMem(f, b, out, int64(n-1), ssa.OpLoad8U), constOp(f, b, 'x')))
				storeMem(f, b, out, 0, constOp(f, b, 'y'), ssa.OpStore8)
				checks = append(checks, f.AddOp(b, ssa.OpEq, loadMem(f, b, src, 0, ssa.OpLoad8U), constOp(f, b, 'x')))
			}
			sum := constOp(f, b, 0)
			for _, check := range checks {
				sum = f.AddOp(b, ssa.OpAdd, sum, check)
			}
			f.SetRet(b, sum)
			if got := runSliceModule(t, f, 4); got != len(checks) {
				t.Fatalf("passed %d checks, want %d", got, len(checks))
			}
		})
	}
}
