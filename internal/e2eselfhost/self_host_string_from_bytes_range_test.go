package e2eselfhost

import (
	"fmt"
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
		if out, err := runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("run: %v\n%s", err, out)
		}
		for i, tc := range e2eharness.StringFromBytesRangeTraps {
			cmd := runX86_64Bin(cli.runner, cli.bin, "-interp", traps[i], cli.stdlib)
			if out, err := cmd.CombinedOutput(); err == nil {
				t.Errorf("%s ran to completion:\n%s", tc.Name, out)
			}
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
			for i, tc := range e2eharness.StringFromBytesRangeTraps {
				if out, code := cli.exitOfFile(t, traps[i], target, nil, "FERN_STRICT_IR=1"); code != 134 {
					t.Errorf("%s exited %d, want 134:\n%s", tc.Name, code, out)
				}
			}
		})
	}
}
