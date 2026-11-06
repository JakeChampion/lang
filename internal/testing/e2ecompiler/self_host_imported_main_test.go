package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Both checkers give E084 for a main of an imported module, at that module
// (#14286). The codes harness does not bundle, so both run through their
// command lines.
func TestImportedMainSignatureDiagnostics(t *testing.T) {
	h := selfHostCLIForHost(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	const entry = "import \"./m\";\nfunction main(): i32 { return m.f(); }\n"
	const f = "pub function f(): i32 { return 1; }\n"
	cases := []struct{ name, module, want string }{
		{"params", "function main(n: i32): void {}\n" + f, "m.fern:1:1: error[E084]: main takes no parameters"},
		{"result", "function main(): string { return \"x\"; }\n" + f, "m.fern:1:1: error[E084]: main returns an integer, a boolean or void, not string"},
		{"valid", "function main(): i32 { return 0; }\n" + f, ""},
		{"other-name", "function mainly(n: i32): i32 { return n; }\n" + f, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(path, []byte(entry), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "m.fern"), []byte(c.module), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, run := range [][]string{{h.cli, "-check", path, h.stdlib}, {oracle, "-check", path}} {
				out, err := exec.Command(run[0], run[1:]...).CombinedOutput()
				if c.want == "" && err != nil || c.want != "" && !strings.Contains(string(out), c.want) {
					t.Fatalf("%s -check: err=%v, want %q\n%s", filepath.Base(run[0]), err, c.want, out)
				}
			}
		})
	}
}
