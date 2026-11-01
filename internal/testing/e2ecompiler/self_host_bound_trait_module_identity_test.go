package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostBoundTraitModuleIdentity(t *testing.T) {
	primary := e2eharness.SelfHostCLI(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	driver := e2eharness.CachedDriverBinFor(t, e2eharness.RepoPath("compiler"), "drivers/checker_modload_run.fern", e2eharness.HostSelfHostTarget())
	for _, tc := range []struct {
		name, declaration string
		reject, nested    bool
	}{
		{"chosen-declares-trait", "pub trait Witness {}\n", false, false},
		{"chosen-trait-requires-an-argument", "pub trait Witness[U] {}\n", true, false},
		{"only-other-declares-trait", "pub trait Other {}\n", true, false},
		{"chosen-reexports-trait", "pub use \"./second\".{Witness};\n", false, false},
		{"chosen-only-imports-trait", "import \"./second\";\npub trait Other {}\n", true, false},
		{"chosen-reexports-two-hops", "pub use \"./bridge\".{Witness};\n", false, false},
		{"nested-chosen-declares-trait", "pub trait Witness {}\n", false, true},
		{"nested-only-other-declares-trait", "pub trait Other {}\n", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, source := range map[string]string{
				"first.fern":  tc.declaration,
				"second.fern": "pub trait Witness {}\n",
				"bridge.fern": "pub use \"./second\".{Witness};\n",
				"main.fern":   "import \"./second\";\nimport \"./first\" as chosen;\nfunction pick[T: chosen.Witness](x: T): i32 { return 0; }\nfunction main(): i32 { return 0; }\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(dir, "main.fern")
			if tc.nested {
				if err := os.Rename(path, filepath.Join(dir, "implementation.fern")); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("import \"./implementation\";\nfunction main(): i32 { return 0; }\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for _, compiler := range []struct {
				name, binary string
				args         []string
			}{
				{"go", oracle, []string{"-check", path}},
				{"primary", primary, []string{"-check", path, stdlib}},
				{"module-driver", driver, []string{path}},
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
