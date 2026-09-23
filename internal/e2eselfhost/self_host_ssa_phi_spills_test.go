package e2eselfhost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostSSAPhiCyclesAcrossSpills(t *testing.T) {
	h := selfHostCLIForHost(t)
	for _, count := range []int{2, 24, 40} {
		for _, target := range h.targets {
			t.Run(fmt.Sprintf("%s/%d-values", target.target, count), func(t *testing.T) {
				var source strings.Builder
				source.WriteString("@noinline function probe(n: i32, salt: i32): i32 {\n")
				for i := 0; i < count; i++ {
					fmt.Fprintf(&source, "var v%d: i32 = salt + %d;\n", i, i)
				}
				source.WriteString("var j: i32 = 0; while (j < n) {\n")
				for i := 0; i < count; i += 2 {
					fmt.Fprintf(&source, "var tmp%d: i32 = v%d; v%d = v%d; v%d = tmp%d;\n", i, i, i, i+1, i+1, i)
				}
				source.WriteString("j = j + 1; }\n")
				for i := 0; i < count; i++ {
					fmt.Fprintf(&source, "var want%d: i32 = %d; if (n %% 2 == 1) { want%d = %d; } if (v%d != salt + want%d) { return %d; }\n", i, i, i, i^1, i, i, i+1)
				}
				source.WriteString("return 0; } function main(): i32 { var n = 0; while (n < 4) { var result = probe(n, 100); if (result != 0) { return result; } n = n + 1; } return 0; }")
				dir := t.TempDir()
				path, bin := filepath.Join(dir, "cycles.fern"), filepath.Join(dir, "cycles")
				if err := os.WriteFile(path, []byte(source.String()), 0o600); err != nil {
					t.Fatal(err)
				}
				h.compileWithSemantic(t, target, path, bin)
				if out, code := h.runProduced(t, target, bin); code != 0 {
					t.Fatalf("parallel swap corrupted value %d:\n%s", code-1, out)
				}
			})
		}
	}
}
