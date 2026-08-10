package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// std/async port of the former std/reactor tests: gather / race /
// with_deadline driving fd-tagged `Pending` futures through the real
// `poll` builtin (docs/ASYNC-FUTURE-UNIFICATION.md PR5c). These cover
// the real-fd / timerfd / deadline paths that the socket-fanout combinator
// tests (async_combinators_test.go) don't — readiness transitions and
// the deadline-timeout abandon. Native-only (poll is a native builtin).

// gather over two regular-file fds (always poll-readable, so
// deterministic — no socket timing): both Pending futures resolve and
// gather returns results in order. Success → 42.
func TestAsyncGatherFileFds(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.txt")
	fileB := filepath.Join(dir, "b.txt")
	for _, p := range []string{fileA, fileB} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write probe %s: %v", p, err)
		}
	}

	src := fmt.Sprintf(`import "std/async";

function start_io(fd: i32): async.Future[i32] {
    function resume(woken_fd: i32): async.Future[i32] { return Ready(woken_fd); }
    return Pending(fd, resume);
}

function main(): i32 {
    match (open_reader("%s")) {
        Ok(ra) => {
            match (open_reader("%s")) {
                Ok(rb) => {
                    var tasks: async.Future[i32][] = [start_io(ra.fd), start_io(rb.fd)];
                    var results: i32[] = async.gather(tasks, -1);
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

	runAsyncIOProgram(t, bin, dir, "gather_files", src, 42)
}

// gather over two timerfds proves the wait blocks on REAL readiness
// transitions (not just always-ready files): each future waits on a
// CLOCK_MONOTONIC timerfd, so `poll` actually blocks until the timer
// fires, and the shorter is serviced first. Deterministic — a timerfd
// always fires — with no network/socket timing. Success → 42.
func TestAsyncGatherTimers(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()

	src := `import "std/async";

function start_timer(ms: i32): async.Future[i32] {
    var fd: i32 = timer_fd(ms);
    function resume(woken_fd: i32): async.Future[i32] { return Ready(ms); }
    return Pending(fd, resume);
}

function main(): i32 {
    var tasks: async.Future[i32][] = [start_timer(10), start_timer(15)];
    var results: i32[] = async.gather(tasks, -1);
    if (results.len() != 2) { return 90; }
    if (results[0] != 10) { return 91; }
    if (results[1] != 15) { return 92; }
    return 42;
}`

	runAsyncIOProgram(t, bin, dir, "gather_timers", src, 42)
}

// race drives the fd-tagged futures until the FIRST resolves and returns
// (winnerIndex, value). Two timerfds (slow 50ms at index 0, fast 10ms at
// index 1): race returns the fast one (value 10, index 1), blocking in
// real poll only until the first fires. Deterministic via timerfds.
func TestAsyncRaceTimers(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()

	src := `import "std/async";

function start_timer(ms: i32): async.Future[i32] {
    var fd: i32 = timer_fd(ms);
    function resume(woken_fd: i32): async.Future[i32] { return Ready(ms); }
    return Pending(fd, resume);
}

function main(): i32 {
    var tasks: async.Future[i32][] = [start_timer(50), start_timer(10)];
    var (winner, value) = async.race(tasks, -1);
    if (value != 10) { return 91; }
    if (winner != 1) { return 92; }
    return 42;
}`

	runAsyncIOProgram(t, bin, dir, "race_timers", src, 42)
}

// with_deadline bounds the whole fan-out by a wall-clock deadline,
// returning Option[T] per future: Some(result) for one that resolves in
// time, None for one whose timer outlives the deadline. Both paths
// deterministic via timerfds + monotonic_ns (no network).
func TestAsyncWithDeadline(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()

	helper := `import "std/async";

function start_timer(ms: i32): async.Future[i32] {
    var fd: i32 = timer_fd(ms);
    function resume(w: i32): async.Future[i32] { return Ready(ms); }
    return Pending(fd, resume);
}
`
	cases := []struct {
		name, body string
		want       int
	}{
		{
			// 5ms timer, 500ms deadline → completes → Some(5).
			name: "completes_in_time",
			body: `function main(): i32 {
    var tasks: async.Future[i32][] = [start_timer(5)];
    var r: Option[i32][] = async.with_deadline(500, tasks);
    match (r[0]) { Some(v) => { return v; }, None => { return 99; } }
}`,
			want: 5,
		},
		{
			// 500ms timer, 20ms deadline → times out → None → map to 42.
			name: "times_out",
			body: `function main(): i32 {
    var tasks: async.Future[i32][] = [start_timer(500)];
    var r: Option[i32][] = async.with_deadline(20, tasks);
    match (r[0]) { Some(v) => { return 99; }, None => { return 42; } }
}`,
			want: 42,
		},
	}
	for _, tc := range cases {
		tc := tc
		runAsyncIOProgram(t, bin, dir, "deadline_"+tc.name, helper+tc.body, tc.want)
	}
}

// runAsyncIOProgram builds `src` for both native backends and asserts the
// process exit code. Native-only (poll); arm64 runs under qemu.
func runAsyncIOProgram(t *testing.T, bin, dir, name, src string, want int) {
	t.Helper()
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
		t.Run(name+"/"+be.target, func(t *testing.T) {
			qemu := be.qemu(t) // skips if no runner
			srcPath := filepath.Join(dir, name+"_"+be.target+".fern")
			if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			out := filepath.Join(dir, name+"_"+be.target+".bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s/%s: exit = %d, want %d", name, be.target, code, want)
			}
		})
	}
}
