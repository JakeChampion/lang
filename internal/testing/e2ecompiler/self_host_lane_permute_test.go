package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostLanePermute(t *testing.T) {
	for _, target := range []string{e2eharness.HostSelfHostTarget(), e2eharness.TargetWasm32Wasi} {
		if target == "" {
			continue
		}
		t.Run(target, func(t *testing.T) {
			entry := e2eharness.RepoPath("examples", "tests", "lane_permute.fern")
			bin := filepath.Join(t.TempDir(), "permutations")
			cmd := e2eharness.SelfHostCompileCmd(t, target, entry, bin)
			cmd.Env = e2eharness.SelfHostChildEnv()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			run := exec.Command(bin)
			if target == e2eharness.TargetWasm32Wasi {
				run = e2eharness.RunWasmCore(t, bin)
			}
			if out, err := run.CombinedOutput(); err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
		})
	}
}
