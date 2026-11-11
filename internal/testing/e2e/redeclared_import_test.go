package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A function that an imported module declares two times is E006 at the
// second declaration, with the file of that module. The call is not E087:
// the name has an exported declaration. Each checker gives the same lines.
func TestRedeclaredFunctionOfAnImportNamesItsModule(t *testing.T) {
	dir := t.TempDir()
	for name, src := range map[string]string{
		"lib.fern":  "function total(a: i32): i32 { return a; }\npub function total(a: i32, b: i32): i32 { return a + b; }\n",
		"main.fern": "import \"./lib\";\nfunction main(): i32 { return lib.total(1, 2) - 3; }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, bin := range map[string]string{"compiler": buildFernCLI(t), "oracle": buildLangBinForInterp(t)} {
		cmd := exec.Command(bin, "-check", "main.fern")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("%s: the check accepted a function that is declared two times\n%s", name, out)
		}
		got := string(out)
		if !strings.Contains(got, "lib.fern:2:5: error[E006]: function \"lib.total\" redeclared") {
			t.Errorf("%s: no E006 at the second declaration in lib.fern\n%s", name, got)
		}
		if strings.Contains(got, "E087") || strings.Contains(got, "main.fern:2:5") {
			t.Errorf("%s: a diagnostic names the wrong cause or the wrong file\n%s", name, got)
		}
	}
}
