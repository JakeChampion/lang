package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostImplTraitModuleIdentity(t *testing.T) {
	primary := e2eharness.SelfHostCLI(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	for _, tc := range []struct {
		name, declaration string
		reject            bool
	}{
		{"chosen-module-declares-trait", "pub trait Witness { function get(self: Self): i32; }\n", false},
		{"only-other-module-declares-trait", "pub trait Other { function other(self: Self): i32; }\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, source := range map[string]string{
				"first.fern":  tc.declaration,
				"second.fern": "pub trait Witness { function get(self: Self): i32; }\n",
				"main.fern":   "import \"./first\" as chosen;\nimport \"./second\";\nstruct Box { v: i32 }\nimpl chosen.Witness for Box { function get(self: Self): i32 { return self.v; } }\nfunction main(): i32 { return 0; }\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(dir, "main.fern")
			for _, compiler := range []struct {
				name, binary string
				args         []string
			}{
				{"go", oracle, []string{"-check", path}},
				{"primary", primary, []string{"-check", path, stdlib}},
			} {
				t.Run(compiler.name, func(t *testing.T) {
					out, err := exec.Command(compiler.binary, compiler.args...).CombinedOutput()
					if (err != nil) != tc.reject {
						t.Fatalf("check returned %v, reject=%t:\n%s", err, tc.reject, out)
					}
				})
			}
		})
	}
}
