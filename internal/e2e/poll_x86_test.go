package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The `poll(fds, timeout_ms)` builtin is the std/task reactor's
// readiness multiplexer (docs/ASYNC-IMPLEMENTATION-PLAN.md Phase 1).
// On x86-64 it lowers to `__fern_poll`, which marshals a length-
// prefixed i32[] of fds into a struct pollfd[], requests POLLIN on
// each, calls poll(2) (#7), and returns the index of the first ready
// fd (or -1). A regular file is always poll-readable, so a poll over
// file fds is deterministic — no socket timing in the test.
//
// (x86-64 first; arm64 (ppoll) + wasm (wasi:io/poll) follow.)
func TestPollBuiltinX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.txt")
	fileB := filepath.Join(dir, "b.txt")

	cases := []struct {
		name, src string
		want      int
	}{
		{
			// One file fd, always readable → first ready index 0.
			name: "single_ready",
			src: fmt.Sprintf(`function main(): i32 {
    match (open_reader("%s")) {
        Ok(r) => { var fds: i32[] = [r.fd]; return poll(fds, 0); },
        Err(e) => { return 99; }
    }
}`, fileA),
			want: 0,
		},
		{
			// Two file fds, both readable → the first (index 0).
			name: "two_ready_first_wins",
			src: fmt.Sprintf(`function main(): i32 {
    match (open_reader("%s")) {
        Ok(ra) => {
            match (open_reader("%s")) {
                Ok(rb) => { var fds: i32[] = [ra.fd, rb.fd]; return poll(fds, 0); },
                Err(e) => { return 98; }
            }
        },
        Err(e) => { return 99; }
    }
}`, fileA, fileB),
			want: 0,
		},
		{
			// Empty fd set → nothing to wait on → -1 (low byte 255).
			name: "empty_set",
			src: `function main(): i32 {
    var fds: i32[] = [];
    return poll(fds, 0);
}`,
			want: 255,
		},
	}

	// The probe files must exist for open_reader to succeed.
	for _, p := range []string{fileA, fileB} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write probe %s: %v", p, err)
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			out := filepath.Join(dir, tc.name+".bin")
			if o, err := exec.Command(bin, "-target", "x86-64", "-o", out, src).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := runX86Bin(qemu, out)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s: exit = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
