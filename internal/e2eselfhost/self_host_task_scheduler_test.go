package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// TestSelfHostTaskScheduler is slice 2's gate (docs/NET-P3-SUSPENSION-PLAN.md
// §4): the self-host compiler lowers the functions that reach a park to
// their resumable form, so a hand-driven task parks twice three calls deep
// and comes back with its locals, and the same functions with no task take
// the blocking fallback. wasm waits on the task runtime's wasm bodies.
func TestSelfHostTaskScheduler(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(e2eharness.TaskSchedulerProgram), 0o644); err != nil {
				t.Fatal(err)
			}
			var cmd *exec.Cmd
			switch target {
			case "x86-64-linux":
				bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
				if len(cli.runner) == 0 {
					cmd = exec.Command(bin)
				} else {
					cmd = exec.Command(cli.runner[0], append(append([]string{}, cli.runner[1:]...), bin)...)
				}
			case "arm64-linux":
				armgcc, qemu := arm64Tooling(t)
				asm, err := os.ReadFile(cli.emit(t, src, target, "FERN_STRICT_IR=1"))
				if err != nil {
					t.Fatal(err)
				}
				cmd = runArm64Bin(qemu, buildBinArm64(t, armgcc, filepath.Dir(src), "prog", string(asm)))
			}
			var out, errb bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &errb
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errb.String())
			}
			if out.String() != e2eharness.TaskSchedulerWant {
				t.Fatalf("stdout:\n%s\nwant:\n%s\nstderr:\n%s", out.String(), e2eharness.TaskSchedulerWant, errb.String())
			}
		})
	}
}
