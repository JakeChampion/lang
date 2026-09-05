package x86_64ssa

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// __fern_arr_push_grow sized its allocation in 32 bits (#8587): the capacity
// doubling went negative past 2^30 elements (the max(.., 4) floor then set
// cap = 4 under a length near 1e9) and cap * stride wrapped past 4 GiB. The
// arithmetic is 64-bit now, and a request past 2^31 - 1 exits 134 — this
// emitter's bounds-trap shape, since it has no __fern_report.
func TestArrPushGrowSizesIn64BitsAndRefusesOverflow(t *testing.T) {
	var b strings.Builder
	runtimeHelperEmitters["__fern_arr_push_grow"](func(format string, args ...any) {
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	})
	body := b.String()
	for _, want := range []string{"shl r9, 1", "imul r11, rdx", "cmp r11, 2147483647", "ja .Lssa_apg_sizebad", "imul rax, rdx", ".Lssa_apg_sizebad:\n\tmov edi, 134"} {
		if !strings.Contains(body, want) {
			t.Errorf("__fern_arr_push_grow lacks %q:\n%s", want, body)
		}
	}
	for _, stale := range []string{"shl r9d, 1", "imul r11d, edx", "imul eax, edx"} {
		if strings.Contains(body, stale) {
			t.Errorf("__fern_arr_push_grow still sizes in 32 bits (%q):\n%s", stale, body)
		}
	}
}

// The guard fires on the REQUEST, before anything is allocated or copied: a
// header claiming 2^30 byte elements at full capacity sends the grow down the
// copy path, where the doubled request is 2^31 + 18 bytes. Nothing behind the
// header exists, so this touches no memory beyond the 16-byte block.
func TestAsmRunArrPushGrowRefusesOverflowedRequest(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	arr := callPtrOp(f, e, "__alloc_u8", constOp(f, e, 16))                                                 // 16-byte header, 16 data bytes
	f.AddOpNoResult(e, ssa.OpStore32, f.AddOp(e, ssa.OpAdd, arr, constOp(f, e, -4)), constOp(f, e, 1<<30))  // len
	f.AddOpNoResult(e, ssa.OpStore32, f.AddOp(e, ssa.OpAdd, arr, constOp(f, e, -12)), constOp(f, e, 1<<30)) // cap
	buf := callPtrOp(f, e, "__fern_arr_push_grow", arr, constOp(f, e, 1<<30), constOp(f, e, 1))
	f.SetRet(e, f.AddOp(e, ssa.OpEq, buf, arr))
	if got := assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil); got != 134 {
		t.Errorf("grow of a 2^30-byte array exited %d, want 134 (allocation size refused)", got)
	}
}
