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
// It marshals a length-prefixed i32[] of fds into a struct pollfd[],
// requests POLLIN on each, calls the OS poll (x86-64 poll(2) #7;
// arm64 ppoll(2) #73), and returns the index of the first readable fd
// (or -1). A regular file is always poll-readable, so a poll over file
// fds is deterministic — no socket timing in the test.
//
// (wasm (wasi:io/poll) follows.)
func TestPollBuiltin(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.txt")
	fileB := filepath.Join(dir, "b.txt")
	for _, p := range []string{fileA, fileB} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write probe %s: %v", p, err)
		}
	}

	cases := []struct {
		name, src string
		want      int
	}{
		{
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
			name: "empty_set",
			src: `function main(): i32 {
    var fds: i32[] = [];
    return poll(fds, 0);
}`,
			want: 255, // -1 truncated to the process exit low byte
		},
	}

	backends := []struct {
		target string
		qemu   func(*testing.T) string
		run    func(qemu, bin string, args ...string) *exec.Cmd
	}{
		{"x86-64-linux", x86QemuOrEmpty, runX86Bin},
		{"arm64-linux", arm64QemuOrEmpty, runArm64Bin},
	}

	for _, be := range backends {
		be := be
		t.Run(be.target, func(t *testing.T) {
			qemu := be.qemu(t) // skips if no runner for this backend
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					src := filepath.Join(dir, be.target+"_"+tc.name+".fern")
					if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
						t.Fatalf("write src: %v", err)
					}
					out := filepath.Join(dir, be.target+"_"+tc.name+".bin")
					if o, err := exec.Command(bin, "-target", be.target, "-o", out, src).CombinedOutput(); err != nil {
						t.Fatalf("build failed: %v\n%s", err, o)
					}
					cmd := be.run(qemu, out)
					_ = cmd.Run()
					if code := cmd.ProcessState.ExitCode(); code != tc.want {
						t.Errorf("%s/%s: exit = %d, want %d", be.target, tc.name, code, tc.want)
					}
				})
			}
		})
	}
}
