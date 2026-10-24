package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostLoopBodiesRequireBlocks(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, head := range []string{
		"while (false)", "loop", "for (let i = 0; i < 1; i = i + 1)",
		"for x in [1]", "for x in 0..1", "for x in 0..=1", "for (x, y) in [(1, 2)]",
		"outer: while (false)", "outer: loop", "outer: for x in [1]",
	} {
		t.Run(head, func(t *testing.T) {
			for _, braces := range []bool{false, true} {
				body := "return 1;"
				if braces {
					body = "{ break; }"
				}
				src := "function main(): i32 { " + head + " " + body + " return 0; }"
				path := filepath.Join(t.TempDir(), "main.fern")
				if err := os.WriteFile(path, []byte(src), 0644); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(cli.bin, "-check", path, cli.stdlib)
				out, err := cmd.CombinedOutput()
				if braces {
					if err != nil {
						t.Fatalf("block: %v\n%s", err, out)
					}
				} else if err == nil || !strings.Contains(string(out), "error[P001]") {
					t.Fatalf("braceless loop: %v\n%s", err, out)
				}
			}
		})
	}
}
