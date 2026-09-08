package arm64ssa_test

import (
	"strconv"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// args caches one array for the lifetime of the process. Dropping a caller's
// reference must not free that array or its strings. A fresh same-class buffer
// would overwrite the cached array's length if it reached the freelist.
func TestArgsCacheSurvivesCallerDrop(t *testing.T) {
	for _, n := range []int{2, 4, arm64ssa.DefaultNumAlloc} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			f := ssa.NewFunc("main")
			b := f.NewBlock()
			args := addrCallOp(f, b, "args")
			callOp(f, b, "__fern_drop_arr_str", args, constOp(f, b, 8))
			addrCallOp(f, b, "__alloc_u8", constOp(f, b, 8))
			again := addrCallOp(f, b, "args")
			f.SetRet(b, load32u(f, b, again, -4))
			if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", n); got != 1 {
				t.Errorf("cached args length after caller drop and allocation = %d, want 1", got)
			}
		})
	}
}
