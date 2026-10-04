package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// string_from_bytes_range_unchecked through the Go compiler on every target
// and the interpreter: the copies match string_from_bytes_unchecked's, the
// source is not shared, nothing leaks, and a backwards range exits 134. The
// self-host twin is TestSelfHostStringFromBytesRange.
func TestStringFromBytesRange(t *testing.T) {
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "range.fern")
	if err := os.WriteFile(src, []byte(e2eharness.StringFromBytesRangeProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	traps := make([]string, len(e2eharness.StringFromBytesRangeTraps))
	for i, tc := range e2eharness.StringFromBytesRangeTraps {
		traps[i] = filepath.Join(dir, fmt.Sprintf("trap%d.fern", i))
		if err := os.WriteFile(traps[i], []byte(e2eharness.StringFromBytesRangeTrapProgram(tc.From, tc.End)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("interp", func(t *testing.T) {
		if out, err := exec.Command(fern, "-interp", src).CombinedOutput(); err != nil {
			t.Fatalf("run: %v\n%s", err, out)
		}
	})
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			run := func(prog string, env ...string) (string, int) {
				bin := filepath.Join(t.TempDir(), "prog")
				cmd := exec.Command(fern, "-target", target, "-o", bin, prog)
				cmd.Env = append(os.Environ(), env...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("compile %s: %v\n%s", prog, err, out)
				}
				var c *exec.Cmd
				if target == "wasm32-wasi" {
					c = exec.Command(e2eharness.Wasmtime(t), "run", bin)
				} else {
					c = nativeServerRunner(t, target)(bin)
				}
				out, _ := c.CombinedOutput()
				return string(out), c.ProcessState.ExitCode()
			}
			out, code := run(src, "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d, want 0:\n%s", code, out)
			}
			e2eharness.CheckLeakcheckBalanced(t, out)
			for i, tc := range e2eharness.StringFromBytesRangeTraps {
				if out, code := run(traps[i]); code != 134 {
					t.Errorf("%s exited %d, want 134:\n%s", tc.Name, code, out)
				}
			}
		})
	}
}
