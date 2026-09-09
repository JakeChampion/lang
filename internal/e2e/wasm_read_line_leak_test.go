package e2e

import (
	"fmt"
	"strings"
	"testing"
)

// read_line on wasm used to strand its working memory: the preview-2
// byte reader allocated a result buffer and received a host list per BYTE
// and freed neither, the accumulator's previous generation was dropped on
// every grow, and the reader-handle spelling copied the line out and
// dropped the accumulator too. A 126-byte line read allocs=255 frees=2
// (#8868). What a program keeps live after reading a line must not depend
// on how long the line was, and the natives read these programs clean.
//
// Both spellings, since they are different helpers: the bare `read_line()`
// goes through __fern_read_byte, `stdin().read_line()` through the
// reader-handle body.
var wasmReadLineSrcs = []struct{ name, src string }{
	{"read_line", `function main(): i32 {
    match (read_line()) {
        Some(line) => { return line.len() % 7; },
        None => { return 100; }
    }
}`},
	{"stdin_read_line", `function main(): i32 {
    match (stdin().read_line()) {
        Some(line) => { return line.len() % 7; },
        None => { return 100; }
    }
}`},
}

func TestWASMReadLineLeavesNothingPerByte(t *testing.T) {
	for _, tc := range wasmReadLineSrcs {
		t.Run(tc.name, func(t *testing.T) {
			component := buildLeakCheckComponent(t, tc.src, false)
			var counts [2][3]int64
			lines := []string{"  21 \n", "   21" + strings.Repeat(" ", 120) + "\n"}
			for i, line := range lines {
				stdout, stderr, exit := runComponent(t, component, runOpts{stdin: line})
				if want := fmt.Sprint(len(line) % 7); exit != 0 || trimOut(stdout) != want {
					t.Fatalf("%d-byte line: exit %d stdout %q, want 0 and %q; stderr: %s", len(line), exit, stdout, want, stderr)
				}
				a, f, live := parseLeakCheckLine(t, stderr)
				counts[i] = [3]int64{a, f, live}
				t.Logf("%d-byte line: allocs=%d frees=%d live_bytes=%d", len(line), a, f, live)
			}
			if counts[0][2] != counts[1][2] {
				t.Errorf("live bytes move with the line length (%d for %d bytes, %d for %d): the helper strands its working memory",
					counts[0][2], len(lines[0]), counts[1][2], len(lines[1]))
			}
			if counts[0][0]-counts[0][1] != counts[1][0]-counts[1][1] {
				t.Errorf("unfreed blocks move with the line length (%d for %d bytes, %d for %d)",
					counts[0][0]-counts[0][1], len(lines[0]), counts[1][0]-counts[1][1], len(lines[1]))
			}
		})
	}
}
