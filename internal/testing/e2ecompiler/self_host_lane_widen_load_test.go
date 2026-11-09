package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostLaneWidenLoad(t *testing.T) {
	for _, target := range []string{e2eharness.HostSelfHostTarget(), e2eharness.TargetWasm32Wasi} {
		if target == "" {
			continue
		}
		t.Run(target, func(t *testing.T) {
			entry := e2eharness.RepoPath("examples", "tests", "lane_widen_load.fern")
			bin := filepath.Join(t.TempDir(), "widen")
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

func TestSelfHostLaneWidenLoadRequiresUnsafe(t *testing.T) {
	target := e2eharness.HostSelfHostTarget()
	if target == "" {
		t.Skip("no native target")
	}
	dir := t.TempDir()
	entry := filepath.Join(dir, "unsafe.fern")
	source := `function main(): i32 { let v = __f64x2_load_f32_ptr(0 as usize); return __f64x2_lane(v, 0) as i32; }`
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := e2eharness.SelfHostCompileCmd(t, target, entry, filepath.Join(dir, "unsafe"))
	cmd.Env = e2eharness.SelfHostChildEnv()
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "error[E095]") {
		t.Fatalf("expected unsafe refusal, got %v\n%s", err, out)
	}
}
