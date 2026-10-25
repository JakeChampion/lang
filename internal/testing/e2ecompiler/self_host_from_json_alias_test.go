package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostFromJsonImportAliases(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	data, err := os.ReadFile(filepath.Join(e2eharness.RepoPath(), "conformance", "cases", "derive_from_json_import_alias", "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"json", "js", "v", "s", "__fj_r", "__fj_tag", "__fj_Pair_0", "__fj_v_value", "__fja_r", "__from_json_value", "__from_json_text"} {
		t.Run(alias, func(t *testing.T) {
			source := strings.ReplaceAll(string(data), "js.", alias+".")
			if alias == "json" {
				source = strings.Replace(source, ` as js;`, `;`, 1)
			} else {
				source = strings.Replace(source, ` as js;`, ` as `+alias+`;`, 1)
			}
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"-check", "-interp"} {
				for _, frontend := range []string{"Go", "self"} {
					var command *exec.Cmd
					if frontend == "Go" {
						command = exec.Command(oracle, mode, path)
					} else {
						command = runX86_64Bin(cli.runner, cli.bin, mode, path, cli.stdlib)
					}
					if output, err := command.CombinedOutput(); err != nil || len(output) != 0 {
						t.Fatalf("%s %s: %v\n%s", frontend, mode, err, output)
					}
				}
			}
		})
	}
}
