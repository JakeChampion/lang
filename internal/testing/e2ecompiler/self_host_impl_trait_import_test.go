package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostImplTraitImportCheck(t *testing.T) {
	primary := e2eharness.SelfHostCLI(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	fixture := e2eharness.RepoPath("conformance", "cases", "diag_e021_impl_trait_not_imported")
	source, err := os.ReadFile(filepath.Join(fixture, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	diagnostic, err := os.ReadFile(filepath.Join(fixture, "expected.error"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(source)
	for _, tc := range []struct {
		name, source string
		reject       bool
	}{
		{"missing-import", src, true},
		{"imported", "import \"core/cmp\";\n" + src, false},
		{"aliased-import", "import \"core/cmp\" as compare;\n" + strings.Replace(src, "impl cmp.Eq", "impl compare.Eq", 1), false},
		{"unimported-qualifier", "import \"core/cmp\" as compare;\n" + src, true},
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
					checkTraitImportResult(t, compiler.binary, compiler.args, tc.reject, strings.TrimSpace(string(diagnostic)))
				})
			}
		})
	}
	t.Run("module-driver", func(t *testing.T) {
		driver := e2eharness.CachedDriverBinFor(t, e2eharness.RepoPath("compiler"), "drivers/checker_modload_run.fern", e2eharness.HostSelfHostTarget())
		for _, tc := range []struct {
			name, imports, qualifier string
			reject                   bool
		}{
			{"missing-import", "", "proof", true},
			{"imported", "import \"./proof\";\n", "proof", false},
			{"aliased-import", "import \"./proof\" as chosen;\n", "chosen", false},
			{"unimported-qualifier", "import \"./proof\" as chosen;\n", "proof", true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "proof.fern"), []byte("pub trait Witness { function get(self: Self): i32; }\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, "main.fern")
				src := tc.imports + "struct Box { v: i32 }\nimpl " + tc.qualifier + ".Witness for Box { function get(self: Self): i32 { return self.v; } }\nfunction main(): i32 { return 0; }\n"
				if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
				for _, compiler := range []struct {
					name, binary string
					args         []string
				}{
					{"go", oracle, []string{"-check", path}},
					{"module-driver", driver, []string{path}},
				} {
					t.Run(compiler.name, func(t *testing.T) {
						checkTraitImportResult(t, compiler.binary, compiler.args, tc.reject, "unknown trait \"proof.Witness\" in impl")
					})
				}
			})
		}
	})
}

func checkTraitImportResult(t *testing.T, binary string, args []string, reject bool, diagnostic string) {
	t.Helper()
	out, err := exec.Command(binary, args...).CombinedOutput()
	if (err != nil) != reject {
		t.Fatalf("check returned %v, reject=%t:\n%s", err, reject, out)
	}
	if reject && (!strings.Contains(string(out), "E021") || !strings.Contains(string(out), diagnostic)) {
		t.Fatalf("want E021 and %q:\n%s", diagnostic, out)
	}
}
