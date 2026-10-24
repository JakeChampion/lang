package e2e

import (
	"fmt"
	"strings"
	"testing"
)

func TestWASMStaticDataPastOnePage(t *testing.T) {
	const count, width = 40, 2048
	var b strings.Builder
	b.WriteString("function main(): i32 {\n    let xs: string[] = [\n")
	for i := 0; i < count; i++ {
		if i > 0 {
			b.WriteString(",\n")
		}
		fmt.Fprintf(&b, "        %q", fmt.Sprintf("%04d", i)+strings.Repeat("x", width-4))
	}
	b.WriteString(`
    ];
    let i: i32 = 0;
    let n: i32 = 0;
    while (i < xs.len()) { n = n + xs[i].len(); i = i + 1; }
    return n;
}`)
	want := count * width
	if got := runWasm(t, b.String()); got != want {
		t.Errorf("summed literal length = %d, want %d", got, want)
	}
}
