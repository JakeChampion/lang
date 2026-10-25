package e2ecompiler

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostStructFieldDiagnosticPositions(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for _, tc := range []struct{ name, typ, code string }{
		{"unknown", "Missing", "E064"},
		{"lane", "f64x2", "E085"},
	} {
		for _, location := range []string{"entry", "imported", "literate"} {
			t.Run(tc.name+"/"+location, func(t *testing.T) {
				dir := t.TempDir()
				source := "pub struct Holder {\n  value: " + tc.typ + "\n}\n"
				name := "main.fern"
				entry := "main.fern"
				line := 2
				files := map[string]string{}
				if location == "imported" {
					name = "dep.fern"
					files["main.fern"] = "import \"./dep\";\nfunction main(): i32 { return 0; }\n"
				} else {
					source += "function main(): i32 { return 0; }\n"
					if location == "literate" {
						name, entry, line = "main.fern.md", "main.fern.md", 6
						source = "# Module\n\n```fern\n<<*>>=\n" + source + "```\n"
					}
				}
				files[name] = source
				writeTree(t, dir, files)
				for _, frontend := range []struct{ name, bin string }{{"Go", oracle}, {"self", cli.bin}} {
					t.Run(frontend.name, func(t *testing.T) {
						cmd := exec.Command(frontend.bin, "-q", "-check", filepath.Join(dir, entry), cli.stdlib)
						out, err := cmd.CombinedOutput()
						want := fmt.Sprintf("%s:%d:3: error[%s]:", filepath.Join(dir, name), line, tc.code)
						if err == nil || !strings.Contains(string(out), want) {
							t.Fatalf("want %q: %v\n%s", want, err, out)
						}
					})
				}
			})
		}
	}
}
