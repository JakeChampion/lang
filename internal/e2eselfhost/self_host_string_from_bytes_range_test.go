package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// string_from_bytes_range_unchecked through the self-host compiler on every
// target and its interpreter: the copies match string_from_bytes_unchecked's,
// the source is not shared, x86-64 frees what it allocates, and a backwards
// range traps. The Go compiler's twin is TestStringFromBytesRange.
func TestSelfHostStringFromBytesRange(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src, trap := filepath.Join(dir, "range.fern"), filepath.Join(dir, "trap.fern")
	if err := os.WriteFile(src, []byte(e2eharness.StringFromBytesRangeProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trap, []byte(e2eharness.StringFromBytesRangeTrapProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("interp", func(t *testing.T) {
		if out, err := runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("run: %v\n%s", err, out)
		}
		cmd := runX86_64Bin(cli.runner, cli.bin, "-interp", trap, cli.stdlib)
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("backwards range ran to completion:\n%s", out)
		}
	})
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			env := []string{"FERN_STRICT_IR=1"}
			if target == "x86-64-linux" {
				env = append(env, "FERN_LEAKCHECK=1")
			}
			out, code := cli.exitOfFile(t, src, target, nil, env...)
			if code != 0 {
				t.Fatalf("exit %d, want 0:\n%s", code, out)
			}
			if target == "x86-64-linux" {
				e2eharness.CheckLeakcheckBalanced(t, out)
			}
			if out, code := cli.exitOfFile(t, trap, target, nil, "FERN_STRICT_IR=1"); code != 134 {
				t.Fatalf("backwards range exited %d, want 134:\n%s", code, out)
			}
		})
	}
}
