package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The `poll(fds, timeout_ms)` builtin is real on the native backends (poll(2) /
// ppoll(2)) and on wasm (forwards to wasi:io/poll.poll over the tokens as
// pollable handles — docs/ASYNC-FUTURE-UNIFICATION.md). On interp it stays a
// `-1` ("no fd ready") stub (no real fds). This pins that interp `poll([], 0)`
// returns -1, and a poll-using program still compiles on wasm (now pulling the
// io/poll composition rather than the old stub).
func TestPollStubInterpWasm(t *testing.T) {
	bin := buildFernCLI(t)
	const src = `function main(): i32 {
    var fds: i32[] = [];
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
