package e2e

import (
	"fmt"
	"strings"
	"testing"
)

// Reading a line on wasm must not strand working memory per byte (#8868):
// what a program keeps live after io.read_line must not depend on how long
// the line was, and the natives read these programs clean.
var wasmReadLineSrcs = []struct{ name, src string }{
	{"io_read_line", `import "std/io";

function main(): i32 {
    match (io.read_line(stdin())) {
        Ok(Some(line)) => { return line.len() % 7; },
        _ => { return 100; }
    }
}`},
}

func TestWASMReadLineLeavesNothingPerByte(t *testing.T) {
	for _, tc := range wasmReadLineSrcs {
		t.Run(tc.name, func(t *testing.T) {
			core := buildLeakCheckWasmCore(t, tc.src, false)
			var counts [2][3]int64
			lines := []string{"  21 \n", "   21" + strings.Repeat(" ", 120) + "\n"}
			for i, line := range lines {
				stdout, stderr, exit := runWasmArtifact(t, core, runOpts{stdin: line})
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
