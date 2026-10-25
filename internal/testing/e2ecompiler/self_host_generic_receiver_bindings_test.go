package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostGenericReceiverBindings(t *testing.T) {
	for _, name := range []string{"generic_receiver_array_drop", "generic_option_static_trait_helper"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(e2eharness.RepoPath(), "conformance", "cases", name, "main.fern")
			for _, compiler := range []string{buildLangBinForInterp(t), e2eharness.SelfHostCLI(t)} {
				for _, mode := range []string{"-check", "-interp"} {
					cmd := exec.Command(compiler, mode, path, e2eharness.SelfHostStdlibRoot(t))
					want := ""
					if mode == "-interp" {
						want = "ok\n"
					}
					if output, err := cmd.CombinedOutput(); err != nil || string(output) != want {
						t.Fatalf("%s %s: %v\n%s", compiler, mode, err, output)
					}
				}
			}
			for _, target := range selfHostFusionTargets {
				t.Run(target, func(t *testing.T) {
					bin := e2eharness.CompileSelfHostFile(t, target, path,
						[]string{"FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"})
					output, err := runScaleTarget(t, target, bin).CombinedOutput()
					if err != nil || !strings.HasPrefix(string(output), "ok\n") {
						t.Fatalf("program: %v\n%s", err, output)
					}
					var allocs, frees, live int64
					if _, err := fmtSscan(leakSummaryLine(string(output)), &allocs, &frees, &live); err != nil || allocs != frees || live != 0 {
						t.Fatalf("unbalanced allocations: %v\n%s", err, output)
					}
				})
			}
		})
	}
}
