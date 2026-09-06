package arm64ssa

import (
	"fmt"
	"strings"
	"testing"
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
	for _, want := range []string{"lsl x5, x4, #1", "csel x5, x5, x6, ge", "madd x7, x5, x2, x6", "lsr x8, x7, #31", "cbnz x8, .Lssa_apg_sizebad", "umull x14, w1, w2", ".Lssa_apg_sizebad:\n\tmov x0, #134"} {
		if !strings.Contains(body, want) {
			t.Errorf("__fern_arr_push_grow lacks %q:\n%s", want, body)
		}
	}
	for _, stale := range []string{"lsl w5, w4, #1", "madd w7, w5, w2, w6", "mul w14, w1, w2"} {
		if strings.Contains(body, stale) {
			t.Errorf("__fern_arr_push_grow still sizes in 32 bits (%q):\n%s", stale, body)
		}
	}
}
