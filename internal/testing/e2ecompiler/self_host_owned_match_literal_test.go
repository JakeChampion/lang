package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostOwnedMatchLiteralParts(t *testing.T) {
	host := e2eharness.HostSelfHostTarget()
	if host == "" {
		t.Skip("requires a native self-host target")
	}
	cli := e2eharness.SelfHostCLI(t)
	project := t.TempDir()
	copySelfHostDriver(t, project, "drivers/checker_codes_run.fern")
	for _, target := range []string{host, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "checker")
			runner := []string{}
			args := []string{"-target", target}
			if target == e2eharness.TargetWasm32Wasi {
				runner = []string{e2eharness.Wasmtime(t), "run"}
				args = append(args, "-emit", "core-module")
			}
			args = append(args, "-o", bin, filepath.Join(project, "drivers/checker_codes_run.fern"), e2eharness.SelfHostStdlibRoot(t))
			compile := exec.Command(cli, args...)
			compile.Env = childEnv("FERN_IR_VERIFY=1", "FERN_STRICT_IR=1")
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("compile checker: %v\n%s", err, out)
			}
			for _, tc := range []struct{ name, code string }{
				{"owned_match_results_rebuild_literal", ""},
				{"diag_e051_borrowed_match_result_in_literal", "E051"},
				{"diag_e051_match_result_with_live_source_in_literal", "E051"},
				{"diag_e051_shadowed_none_in_owned_match", "E051"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					source, err := os.ReadFile(e2eharness.RepoPath("conformance", "cases", tc.name, "main.fern"))
					if err != nil {
						t.Fatal(err)
					}
					check := func(label string, ds []driverDiag) {
						t.Helper()
						if tc.code == "" {
							if len(ds) != 0 {
								t.Fatalf("%s: want no diagnostics, got %v", label, ds)
							}
							return
						}
						if len(ds) == 0 {
							t.Fatalf("%s: want %s, got no diagnostics", label, tc.code)
						}
						for _, d := range ds {
							if d.code != tc.code {
								t.Fatalf("%s: want only %s, got %v", label, tc.code, ds)
							}
						}
					}
					check("Go", goCheckerDiags(t, project, string(source)))
					run := exec.Command(bin)
					if len(runner) > 0 {
						run = exec.Command(runner[0], append(runner[1:], bin)...)
					}
					run.Stdin = strings.NewReader(string(source))
					check("self-host", driverDiags(runCheckerDriver(t, run, tc.name)))
				})
			}
		})
	}
}

func TestSelfHostAndroidWorkerOwnership(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	cmd := exec.Command(cli, "-target", "arm64-android", "-check", e2eharness.RepoPath("tests", "android", "worker", "app.fern"), e2eharness.SelfHostStdlibRoot(t))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("check Android worker: %v\n%s", err, out)
	}
}
