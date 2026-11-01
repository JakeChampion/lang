package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostBoundTraitImportCheck(t *testing.T) {
	primary := e2eharness.SelfHostCLI(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	fixture := e2eharness.RepoPath("conformance", "cases", "diag_e021_bound_trait_not_imported", "main.fern")
	source, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	src := string(source)
	for _, tc := range []struct {
		name, source string
		reject       bool
	}{
		{"missing-import", src, true},
		{"imported", "import \"core/ops\";\n" + src, false},
		{"aliased-import", "import \"core/ops\" as chosen;\n" + strings.ReplaceAll(src, "ops.Add", "chosen.Add"), false},
		{"unimported-qualifier", "import \"core/ops\" as chosen;\n" + src, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, compiler := range []struct {
				name, binary string
				args         []string
			}{
				{"go", oracle, []string{"-check", path}},
				{"primary", primary, []string{"-check", path, stdlib}},
			} {
				t.Run(compiler.name, func(t *testing.T) {
					checkTraitImportResult(t, compiler.binary, compiler.args, "", tc.reject, "unknown trait \"ops.Add\" in bound on type parameter T")
				})
			}
		})
	}
	t.Run("module-driver", func(t *testing.T) {
		driver := e2eharness.CachedDriverBinFor(t, e2eharness.RepoPath("compiler"), "drivers/checker_modload_run.fern", e2eharness.HostSelfHostTarget())
		for _, tc := range []struct {
			name, imports, qualifier string
			reject, nested           bool
		}{
			{"missing-import", "", "proof", true, false},
			{"imported", "import \"./proof\";\n", "proof", false, false},
			{"aliased-import", "import \"./proof\" as chosen;\n", "chosen", false, false},
			{"unimported-qualifier", "import \"./proof\" as chosen;\n", "proof", true, false},
			{"nested-aliased-import", "import \"./proof\" as chosen;\n", "chosen", false, true},
			{"nested-unimported-qualifier", "import \"./proof\" as chosen;\n", "proof", true, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "proof.fern"), []byte("pub trait Witness { function get(self: Self): i32; }\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, "main.fern")
				src := tc.imports + "function pick[T: " + tc.qualifier + ".Witness](x: T): i32 { return x.get(); }\nfunction main(): i32 { return 0; }\n"
				if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
				if tc.nested {
					if err := os.Rename(path, filepath.Join(dir, "implementation.fern")); err != nil {
						t.Fatal(err)
					}
					entry := "import \"./proof\";\nimport \"./implementation\";\nfunction main(): i32 { return 0; }\n"
					if err := os.WriteFile(path, []byte(entry), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				for _, compiler := range []struct {
					name, binary string
					args         []string
				}{
					{"go", oracle, []string{"-check", path}},
					{"module-driver", driver, []string{path}},
				} {
					t.Run(compiler.name, func(t *testing.T) {
						checkTraitImportResult(t, compiler.binary, compiler.args, "", tc.reject, "unknown trait \"proof.Witness\" in bound on type parameter T")
					})
				}
			})
		}
	})
}
