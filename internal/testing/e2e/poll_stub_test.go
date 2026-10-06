package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The `poll(fds, timeout_ms)` builtin is poll(2) / ppoll(2) on the native
// backends, wasi:io/poll.poll over the tokens as pollable handles on wasm
// (docs/ASYNC-FUTURE-UNIFICATION.md), and a set over the handles' descriptors
// on the interpreter. This pins that interp `poll([], 0)` answers -1 for the
// empty set, and that a poll-using program compiles on wasm.
func TestPollEmptySetInterpWasm(t *testing.T) {
	bin := buildFernCLI(t)
	const src = `function main(): i32 {
    let fds: i32[] = [];
    return poll(fds, 0);
}`

	t.Run("interp", func(t *testing.T) {
		cmd := exec.Command(bin, "-interp", "-")
		cmd.Stdin = strings.NewReader(src)
		_ = cmd.Run()
		// -1 truncated to the process exit low byte.
		if code := cmd.ProcessState.ExitCode(); code != 255 {
			t.Errorf("interp poll([], 0) exit = %d, want 255 (-1)", code)
		}
	})

	t.Run("wasm-compiles", func(t *testing.T) {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "poll.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, "poll.wasm")
		if o, err := exec.Command(bin, "-target", "wasm32-wasi", "-o", out, srcPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm build of a poll-using program failed: %v\n%s", err, o)
		}
	})
}
