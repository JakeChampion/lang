package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// TestSelfHostTaskFrame pins what a parked frame keeps
// (docs/NET-P3-SUSPENSION-PLAN.md §3.5): views and a shared mutated capture
// read after a park answer what the plain run answers, on x86-64 and arm64
// through the self-host CLI.
func TestSelfHostTaskFrame(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(e2eharness.TaskFrameProgram), 0o644); err != nil {
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
			if out.String() != e2eharness.TaskFrameWant {
				t.Fatalf("stdout:\n%s\nwant:\n%s\nstderr:\n%s", out.String(), e2eharness.TaskFrameWant, errb.String())
			}
		})
	}
}
