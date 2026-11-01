package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostBoundModuleOrigin(t *testing.T) {
	primary := e2eharness.SelfHostCLI(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	driver := e2eharness.CachedDriverBinFor(t, e2eharness.RepoPath("compiler"), "drivers/checker_modload_run.fern", e2eharness.HostSelfHostTarget())
	for _, tc := range []struct {
		name, methodQualifier, implQualifier string
		reject                               bool
	}{
		{"separate-source-aliases", "inside", "outside", false},
		{"impl-cannot-use-trait-module-alias", "inside", "inside", true},
		{"impl-cannot-use-import-path-name", "inside", "witness", true},
		{"method-cannot-use-impl-module-alias", "outside", "outside", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, source := range map[string]string{
				"witness.fern":  "pub trait Witness { function get(self: Self): i32; }\n",
				"defaults.fern": "import \"./witness\" as inside;\npub trait Default { function read[U: " + tc.methodQualifier + ".Witness](self: Self, other: U): i32 { return other.get(); } }\n",
				"main.fern":     "import \"./defaults\";\nimport \"./witness\" as outside;\nstruct Box[T] { value: T }\nimpl[T: " + tc.implQualifier + ".Witness] defaults.Default for Box[T] {}\nfunction main(): i32 { return 0; }\n",
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
				{"module-driver", driver, []string{path}},
			} {
				t.Run(compiler.name, func(t *testing.T) {
					checkTraitImportResult(t, compiler.binary, compiler.args, "", tc.reject, "unknown trait")
				})
			}
		})
	}
}
