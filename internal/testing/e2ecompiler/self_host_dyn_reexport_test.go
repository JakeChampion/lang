package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostDynTraitReexportCheck(t *testing.T) {
	primary := e2eharness.SelfHostCLI(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	driver := e2eharness.CachedDriverBinFor(t, e2eharness.RepoPath("compiler"), "drivers/checker_modload_run.fern", e2eharness.HostSelfHostTarget())
	for _, tc := range []struct {
		name, facade, imports, trait string
		reject                       bool
	}{
		{"direct", "pub use \"./orig\".{Area};\n", "import \"./facade\";\n", "facade.Area", false},
		{"aliased", "pub use \"./orig\".{Area};\n", "import \"./facade\" as chosen;\n", "chosen.Area", false},
		{"two-hops", "pub use \"./middle\".{Area};\n", "import \"./facade\";\n", "facade.Area", false},
		{"unknown-trait", "pub use \"./orig\".{Area};\n", "import \"./facade\";\n", "facade.Missing", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, source := range map[string]string{
				"orig.fern":   "pub trait Area { function area(self: Self): i32; }\n",
				"middle.fern": "pub use \"./orig\".{Area};\n",
				"facade.fern": tc.facade,
				"main.fern":   tc.imports + "function size(d: dyn " + tc.trait + "): i32 { return d.area(); }\nfunction main(): i32 { return 0; }\n",
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
					out, err := exec.Command(compiler.binary, compiler.args...).CombinedOutput()
					if (err != nil) != tc.reject {
						t.Fatalf("check returned %v, reject=%t:\n%s", err, tc.reject, out)
					}
				})
			}
		})
	}
}
