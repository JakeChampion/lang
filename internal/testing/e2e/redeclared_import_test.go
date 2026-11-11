package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A declaration that an imported module makes two times is E006 at the
// second declaration, with the file of that module. Each checker gives the
// same line. For a function, the call is not E087: the name has an
// exported declaration.
func TestRedeclarationInAnImportNamesItsModule(t *testing.T) {
	bins := map[string]string{"compiler": buildFernCLI(t), "oracle": buildLangBinForInterp(t)}
	for _, tc := range []struct {
		name, lib, main, want string
	}{
		{
			"function",
			"function total(a: i32): i32 { return a; }\npub function total(a: i32, b: i32): i32 { return a + b; }\n",
			"import \"./lib\";\nfunction main(): i32 { return lib.total(1, 2) - 3; }\n",
			"lib.fern:2:5: error[E006]: function \"lib.total\" redeclared",
		},
		{
			"struct",
			"pub struct P { x: i32 }\npub struct P { y: i32 }\n",
			"import \"./lib\";\nfunction main(): i32 { return 0; }\n",
			"lib.fern:2:5: error[E006]: struct \"lib.P\" redeclared",
		},
		{
			"enum",
			"pub enum E { A }\npub enum E { B }\n",
			"import \"./lib\";\nfunction main(): i32 { return 0; }\n",
			"lib.fern:2:5: error[E006]: enum \"lib.E\" redeclared",
		},
		{
			"method",
			"pub struct Q { x: i32 }\npub function (q: Q) get(): i32 { return q.x; }\npub function (q: Q) get(): i32 { return 2; }\n",
			"import \"./lib\";\nfunction main(): i32 { return 0; }\n",
			"lib.fern:3:5: error[E006]: method \"get\" on lib.Q redeclared",
		},
		{
			"const",
			"pub const K: i32 = 1;\npub const K: i32 = 2;\n",
			"import \"./lib\";\nfunction main(): i32 { return 0; }\n",
			"lib.fern:2:5: error[E006]: const \"lib.K\" redeclared",
		},
	} {
		dir := t.TempDir()
		for name, src := range map[string]string{"lib.fern": tc.lib, "main.fern": tc.main} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for who, bin := range bins {
			cmd := exec.Command(bin, "-check", "main.fern")
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			got := string(out)
			if err == nil {
				t.Errorf("%s, %s: the check accepted a declaration that the module makes two times\n%s", tc.name, who, got)
				continue
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("%s, %s: no line %q\n%s", tc.name, who, tc.want, got)
			}
			if strings.Contains(got, "E087") || strings.Contains(got, "main.fern:2:5") || strings.Contains(got, "main.fern:3:5") {
				t.Errorf("%s, %s: a diagnostic names the wrong cause or the wrong file\n%s", tc.name, who, got)
			}
		}
	}
}
