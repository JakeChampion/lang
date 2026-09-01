package e2e

import (
	"fmt"
	"strings"
	"testing"
)

// Static data past the first 64 KiB page. Data segments are written at
// INSTANTIATION, before any code — and so before __fern_alloc's
// memory.grow — can run, so a module whose literals reach past its
// initial memory traps while starting up ("out of bounds memory access")
// and never reaches `main`. The emitter sized every module's memory at
// one page, which capped every wasm program at 64 KiB of static data.
//
// The literals have to survive to runtime: `"...".len()` on a literal
// constant-folds away, taking the data with it, so the strings are held
// in an array and indexed through a loop instead. Each is distinct
// because identical literals intern to one data-segment entry.
func TestWASMStaticDataPastOnePage(t *testing.T) {
	const count, width = 40, 2048 // 80 KiB of literals, comfortably past one page
	var b strings.Builder
	b.WriteString("function main(): i32 {\n    var xs: string[] = [\n")
	for i := 0; i < count; i++ {
		if i > 0 {
			b.WriteString(",\n")
		}
		fmt.Fprintf(&b, "        %q", fmt.Sprintf("%04d", i)+strings.Repeat("x", width-4))
	}
	b.WriteString(`
    ];
    var i: i32 = 0;
    var n: i32 = 0;
    while (i < xs.len()) { n = n + xs[i].len(); i = i + 1; }
    return n;
}`)
	want := count * width
	if got := runWasm(t, b.String()); got != want {
		t.Errorf("summed literal length = %d, want %d", got, want)
	}
}
