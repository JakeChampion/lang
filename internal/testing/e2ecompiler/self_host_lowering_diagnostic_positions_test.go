package e2ecompiler

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostLoweringDiagnosticPositions(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, location := range []string{"entry", "imported", "literate"} {
			t.Run(target+"/"+location, func(t *testing.T) {
				dir := t.TempDir()
				entry, name, source, line, col := "main.fern", "main.fern", fipUnpairedFbipSrc, 2, 6
				files := map[string]string{}
				if location == "imported" {
					name, col = "dep.fern", 10
					source = strings.Replace(source, "struct State", "pub struct State", 1)
					source = strings.Replace(source, "fbip function", "pub fbip function", 1)
					source = source[:strings.Index(source, "function main")]
					files[entry] = "import \"./dep\";\nfunction main(): i32 { return dep.make(3).count; }\n"
				} else if location == "literate" {
					entry, name, line = "main.fern.md", "main.fern.md", 6
					source = "# Program\n\n```fern\n<<*>>=\n" + source + "```\n"
				}
				files[name] = source
				writeTree(t, dir, files)
				cmd := exec.Command(cli.bin, "-q", "-target", target, "-o", filepath.Join(dir, "out"), filepath.Join(dir, entry), cli.stdlib)
				out, err := cmd.CombinedOutput()
				want := fmt.Sprintf("%s:%d:%d: error[E068]:", filepath.Join(dir, name), line, col)
				if err == nil || !strings.Contains(string(out), want) {
					t.Fatalf("want %q: %v\n%s", want, err, out)
				}
			})
		}
	}
}
