package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// std/reactor's run_io drives fd-tagged stackless tasks (IoStep) to
// completion using the real `poll` syscall builtin
// (docs/ASYNC-IMPLEMENTATION-PLAN.md Phase 1c) — the real-I/O
// counterpart to std/task's in-memory reactor. Two tasks each wait on
// a regular-file fd (always poll-readable, so deterministic — no
// socket timing), so run_io must drive both to completion and return
// their results in order. Native-only (poll is a native builtin).
func TestReactorRunIO(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.txt")
	fileB := filepath.Join(dir, "b.txt")
	for _, p := range []string{fileA, fileB} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write probe %s: %v", p, err)
		}
	}

	// Two fd-tagged tasks fan out over real poll; success → 42.
	src := fmt.Sprintf(`import "std/reactor";

function start_io(fd: i32): reactor.IoStep {
    function resume(woken_fd: i32): reactor.IoStep { return IoDone(woken_fd); }
    return IoWait(fd, resume);
}

function main(): i32 {
    match (open_reader("%s")) {
        Ok(ra) => {
            match (open_reader("%s")) {
                Ok(rb) => {
                    var tasks: reactor.IoStep[] = [start_io(ra.fd), start_io(rb.fd)];
                    var results: i32[] = reactor.run_io(tasks);
                    if (results.len() != 2) { return 90; }
                    if (results[0] < 0) { return 91; }
                    if (results[1] < 0) { return 92; }
                    return 42;
                },
                Err(e) => { return 98; }
            }
        },
        Err(e) => { return 99; }
    }
}`, fileA, fileB)

	backends := []struct {
		target string
		qemu   func(*testing.T) string
		run    func(qemu, bin string, args ...string) *exec.Cmd
	}{
		{"x86-64", x86QemuOrEmpty, runX86Bin},
		{"arm64", arm64QemuOrEmpty, runArm64Bin},
	}
	for _, be := range backends {
		be := be
		t.Run(be.target, func(t *testing.T) {
			qemu := be.qemu(t) // skips if no runner
			srcPath := filepath.Join(dir, be.target+"_reactor.fern")
			if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			out := filepath.Join(dir, be.target+"_reactor.bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Errorf("%s: run_io exit = %d, want 42", be.target, code)
			}
		})
	}
}

// run_io over two timerfds proves the reactor blocks on REAL readiness
// transitions (not just always-ready files): each task waits on a
// CLOCK_MONOTONIC timerfd, so `poll` actually blocks until the timer
// fires, and the shorter timer is serviced first. Deterministic — a
// timerfd always fires — with no network/socket timing.
func TestReactorTimers(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()

	// Two timer tasks (10ms, 15ms); each resumes to IoDone(ms). run_io
	// blocks in real poll until each fires, in order. Success → 42.
	src := `import "std/reactor";

function start_timer(ms: i32): reactor.IoStep {
    var fd: i32 = timer_fd(ms);
    function resume(woken_fd: i32): reactor.IoStep { return IoDone(ms); }
    return IoWait(fd, resume);
}

function main(): i32 {
    var tasks: reactor.IoStep[] = [start_timer(10), start_timer(15)];
    var results: i32[] = reactor.run_io(tasks);
    if (results.len() != 2) { return 90; }
    if (results[0] != 10) { return 91; }
    if (results[1] != 15) { return 92; }
    return 42;
}`

	backends := []struct {
		target string
		qemu   func(*testing.T) string
		run    func(qemu, bin string, args ...string) *exec.Cmd
	}{
		{"x86-64", x86QemuOrEmpty, runX86Bin},
		{"arm64", arm64QemuOrEmpty, runArm64Bin},
	}
	for _, be := range backends {
		be := be
		t.Run(be.target, func(t *testing.T) {
			qemu := be.qemu(t)
			srcPath := filepath.Join(dir, be.target+"_timers.fern")
			if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			out := filepath.Join(dir, be.target+"_timers.bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Errorf("%s: timer run_io exit = %d, want 42", be.target, code)
			}
		})
	}
}

// run_io_deadline bounds the whole fan-out by a wall-clock deadline:
// a task that completes in time carries its result; one whose timer
// outlives the deadline is abandoned (-1). Both paths are
// deterministic via timerfds + monotonic_ns (no network).
func TestReactorDeadline(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()

	timerHelper := `import "std/reactor";

function start_timer(ms: i32): reactor.IoStep {
    var fd: i32 = timer_fd(ms);
    function resume(w: i32): reactor.IoStep { return IoDone(ms); }
    return IoWait(fd, resume);
}
`
	cases := []struct {
		name, body string
		want       int
	}{
		{
			// 5ms timer, 500ms deadline → completes → result 5.
			name: "completes_in_time",
			body: `function main(): i32 {
    var tasks: reactor.IoStep[] = [start_timer(5)];
    var r: i32[] = reactor.run_io_deadline(tasks, 500);
    return r[0];
}`,
			want: 5,
		},
		{
			// 500ms timer, 20ms deadline → times out → -1 → map to 42.
			name: "times_out",
			body: `function main(): i32 {
    var tasks: reactor.IoStep[] = [start_timer(500)];
    var r: i32[] = reactor.run_io_deadline(tasks, 20);
    if (r[0] < 0) { return 42; }
    return 99;
}`,
			want: 42,
		},
	}

	backends := []struct {
		target string
		qemu   func(*testing.T) string
		run    func(qemu, bin string, args ...string) *exec.Cmd
	}{
		{"x86-64", x86QemuOrEmpty, runX86Bin},
		{"arm64", arm64QemuOrEmpty, runArm64Bin},
	}
	for _, be := range backends {
		be := be
		t.Run(be.target, func(t *testing.T) {
			qemu := be.qemu(t)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					srcPath := filepath.Join(dir, be.target+"_"+tc.name+".fern")
					if err := os.WriteFile(srcPath, []byte(timerHelper+tc.body), 0o644); err != nil {
						t.Fatalf("write src: %v", err)
					}
					out := filepath.Join(dir, be.target+"_"+tc.name+".bin")
					if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
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
