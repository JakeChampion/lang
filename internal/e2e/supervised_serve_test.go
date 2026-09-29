// Crash-only supervised native serving (docs/CRASH-ONLY-SERVE.md,
// plan item D2') — end-to-end tests for `tcp_serve_supervised` on
// the native x86-64 backend plus the interp ENOSYS fallback.
//
// The supervised shape: the parent owns the listener (created
// before the first fork, inherited by every worker), a forked
// worker runs the accept loop, and the parent waitpid's / logs /
// reforks with bounded backoff. A handler trap therefore kills
// one worker's in-flight connections, not the service. The tests
// pin exactly the design doc's test bar:
//
//  1. a handler that traps (array out-of-bounds) on `/boom` and
//     answers 200 on `/ok`;
//  2. `/boom` → connection reset / no response + a worker-death
//     line on stderr; `/ok` again → 200 (the service survived);
//  3. a crash-looping worker (8 consecutive fast deaths) makes
//     the supervisor give up with the child's exit code instead
//     of spinning forever;
//  4. under `-interp`, proc_fork's -38 (ENOSYS) degrades to
//     single-process serving — `/ok` still answers 200 and the
//     one-line degradation notice lands on stderr.
package e2e

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/e2eharness"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// supervisedServeSrc is the handler-under-test: 200 on /ok, an
// array out-of-bounds trap (exit 134) on /boom. The index is
// computed from a.len() so neither the checker's static bounds
// check nor constfold can reject/fold it — the trap must fire at
// runtime, in the worker.
const supervisedServeSrc = `
import "std/http";
import "std/tcp";
import "core/int";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    if (req.path == "/boom") {
        var a: i32[] = [1, 2, 3];
        var i: i32 = a.len() + 5;
        var x: i32 = a[i];
        return http.http_response_ok("unreachable " + int.int_to_string(x));
    }
    return http.http_response_ok("ok");
}
function main(): i32 {
    return tcp.tcp_serve_supervised_opts(%d, tcp.ServeOptions { ...tcp.serve_options(), workers: 1 }, handle);
}`

// buildSupervisedServeBin compiles src (a full program with its
// own main) for x86-64 and returns the binary path plus the
// runner prefix (empty = native exec, else qemu-x86_64). Mirrors
// TestX86_64HttpHandler's modload → constfold → check → monomorph
// → emit → gcc pipeline.
func buildSupervisedServeBin(t *testing.T, src string) (bin string, runner []string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	return binPath, runner
}

// freeLoopbackPort asks the kernel for a free TCP port and
// releases it — the standard e2e probe-listener trick.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no free TCP port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	return port
}

// startSupervisedServer launches bin (via runner) through
// e2eharness.StartServerProcess, in its own process group with stderr
// in a file the caller can poll. extraEnv entries are `KEY=VALUE`
// additions to the child's environment, for a server whose port comes
// from `PORT` rather than from a literal baked into its source.
func startSupervisedServer(t *testing.T, bin string, runner []string, extraEnv ...string) (cmd *exec.Cmd, stderrPath string) {
	t.Helper()
	cmd = e2eharness.RunX86_64Bin(runner, bin)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	return cmd, e2eharness.StartServerProcess(t, cmd)
}

// httpRoundTrip sends one GET on a fresh connection and returns
// whatever bytes came back ("" = reset / no response).
func httpRoundTrip(t *testing.T, addr, path string, timeout time.Duration) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		t.Fatalf("dial for %s: %v", path, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	// The read runs to end of stream, which a persistent connection
	// never gives, so the request asks for the close.
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", path)
	if _, err := conn.Write([]byte(req)); err != nil {
		// Write can race the worker death on /boom — treat as
		// "no response", same as a read-side reset.
		return ""
	}
	resp, _ := io.ReadAll(conn) // reset surfaces as err; bytes-so-far still returned
	return string(resp)
}

// waitStderrContains polls the server's stderr file until the
// needle shows up (the supervisor's log write races the client's
// observation of the reset).
func waitStderrContains(t *testing.T, path, needle string, deadline time.Duration) string {
	t.Helper()
	limit := time.Now().Add(deadline)
	var last string
	for time.Now().Before(limit) {
		b, _ := os.ReadFile(path)
		last = string(b)
		if strings.Contains(last, needle) {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("stderr never contained %q within %v\n--- stderr ---\n%s", needle, deadline, last)
	return last
}

// Design-doc test bar items 1–3 (partial: N=1 crash/restart round;
// the crash-loop giveup is the next test): /ok answers 200, /boom
// traps the worker (reset + worker-death line, raw exit code 134),
// and /ok answers 200 again — the service survived what one
// request did.
// workersServeSrc serves with two forked workers: /slow burns pure work
// for well over a second, /boom traps, /ok answers at once.
const workersServeSrc = `
import "std/http";
import "std/tcp";
import "core/int";
function burn(n: i32): i32 {
    var x: i32 = 12345;
    var i: i32 = 0;
    while (i < n) {
        x = (x * 1103515245 + 12345) & 2147483647;
        i = i + 1;
    }
    return x;
}
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    if (req.path == "/boom") {
        var a: i32[] = [1, 2, 3];
        var i: i32 = a.len() + 5;
        var x: i32 = a[i];
        return http.http_response_ok("unreachable " + int.int_to_string(x));
    }
    if (req.path == "/slow") {
        if (burn(400000000) == 0 - 1) { return http.http_response_ok("never"); }
        return http.http_response_ok("slow");
    }
    return http.http_response_ok("ok");
}
function main(): i32 {
    return tcp.tcp_serve_supervised_opts(%d, tcp.ServeOptions { ...tcp.serve_options(), workers: 2 }, handle);
}`

// Two workers over one listener (#9854): a request on a second
// connection is answered while the first worker is deep in /slow, which
// one worker could not do, since its handlers run to completion; and a
// worker's death leaves the other serving, before and after the refork.
func TestSupervisedServeWorkersServeSideBySide(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, fmt.Sprintf(workersServeSrc, port))
	_, stderrPath := startSupervisedServer(t, bin, runner)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	e2eharness.WaitServerReady(t, addr, 10*time.Second)

	if resp := httpRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("first /ok: want 200, got\n%s", resp)
	}
	// /slow on one connection holds its worker for seconds; /ok on
	// another must come back from the other worker long before that.
	slow, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	if _, err := io.WriteString(slow, "GET /slow HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	time.Sleep(200 * time.Millisecond)
	if resp := httpRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/ok beside /slow: want 200, got\n%s", resp)
	}
	if waited := time.Since(started); waited > 500*time.Millisecond {
		t.Fatalf("/ok beside /slow took %v: it waited behind the slow worker rather than being served by the other", waited)
	}
	b, err := io.ReadAll(slow)
	if err != nil || !strings.Contains(string(b), "slow") {
		t.Fatalf("/slow itself: %q, %v", b, err)
	}
	// The proof needs /slow to have outlasted the /ok round trip by far;
	// a burn the machine finishes in a blink would prove nothing.
	if held := time.Since(started); held < 500*time.Millisecond {
		t.Fatalf("/slow held its worker for only %v; too short to show the other worker answered /ok", held)
	}
	// A worker's death leaves the other one serving.
	if resp := httpRoundTrip(t, addr, "/boom", 5*time.Second); strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/boom unexpectedly answered 200:\n%s", resp)
	}
	waitStderrContains(t, stderrPath, "worker died with exit code 134", 10*time.Second)
	if resp := httpRoundTrip(t, addr, "/ok", 10*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("post-crash /ok: want 200 (the other worker should have answered), got\n%s", resp)
	}
}

func TestSupervisedServeSurvivesHandlerTrap(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, fmt.Sprintf(supervisedServeSrc, port))
	_, stderrPath := startSupervisedServer(t, bin, runner)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	e2eharness.WaitServerReady(t, addr, 10*time.Second)

	if resp := httpRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("first /ok: want 200, got\n%s", resp)
	}

	// /boom: the worker traps mid-request — no response bytes (or
	// at least no 200) come back on this connection.
	if resp := httpRoundTrip(t, addr, "/boom", 5*time.Second); strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/boom unexpectedly answered 200:\n%s", resp)
	}
	// The supervisor logs the death with the RAW exit code — a
	// bounds trap is 134 (the taxonomy in docs/CRASH-ONLY-SERVE.md).
	waitStderrContains(t, stderrPath, "worker died with exit code 134", 10*time.Second)

	// The refork backoff starts at 100ms; the parent-owned
	// listener keeps this connection in its backlog meanwhile, so
	// a generous timeout is all the client needs.
	if resp := httpRoundTrip(t, addr, "/ok", 10*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("post-crash /ok: want 200 (service should have survived), got\n%s", resp)
	}
}

// Design-doc test bar item 4: a crash-looping worker — every
// request traps, so each refork dies within 100ms of spawn — hits
// the bounded-backoff giveup (8 consecutive fast deaths) and the
// supervisor EXITS with the child's code (134) instead of
// spinning forever. A worker death needs a request to trigger the
// trap, so the driver fires /boom in a reconnect loop over a single
// worker: each queued connection is accepted by the next worker as
// soon as it forks, keeping every death well inside the 100ms
// fast-death window (with a worker per processing unit, the default,
// a request kills whichever worker took it, which is a death at the
// request's pace, not a crash loop). Deterministic but slow by design — the doubling backoff
// sleeps sum to ~11.3s before giveup.
func TestSupervisedServeCrashLoopGivesUp(t *testing.T) {
	if testing.Short() {
		t.Skip("crash-loop giveup waits out ~11s of supervisor backoff")
	}
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, fmt.Sprintf(supervisedServeSrc, port))
	cmd, stderrPath := startSupervisedServer(t, bin, runner)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	e2eharness.WaitServerReady(t, addr, 10*time.Second)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	// Keep /boom requests flowing until the supervisor gives up.
	// Dial errors are expected once it exits (and transiently
	// while a dead worker is being replaced) — just keep going.
	deadline := time.Now().Add(90 * time.Second)
	var exited bool
	for !exited && time.Now().Before(deadline) {
		select {
		case <-done:
			exited = true
		default:
			conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
			if err != nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			_, _ = conn.Write([]byte("GET /boom HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\n\r\n"))
			_, _ = io.ReadAll(conn) // wait for the reset so requests serialise
			conn.Close()
		}
	}
	if !exited {
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatalf("supervisor never gave up within the deadline\n--- stderr ---\n%s", readFileString(stderrPath))
		}
	}

	if code := cmd.ProcessState.ExitCode(); code != 134 {
		t.Errorf("supervisor exit = %d, want 134 (the crash-looping child's code)\n--- stderr ---\n%s", code, readFileString(stderrPath))
	}
	stderr := readFileString(stderrPath)
	if !strings.Contains(stderr, "giving up after 8 consecutive fast worker deaths") {
		t.Errorf("giveup line missing from stderr:\n%s", stderr)
	}
	if n := strings.Count(stderr, "worker died with exit code 134"); n < 8 {
		t.Errorf("worker-death lines = %d, want >= 8:\n%s", n, stderr)
	}
}

// Design-doc "interp parity": the interpreter cannot bare-fork
// (Go's runtime is threaded), so proc_fork answers -38 (ENOSYS)
// and tcp_serve_supervised degrades to plain single-process
// serving — /ok still answers 200 and the one-line degradation
// notice lands on stderr. Drives the real `fern -interp` binary
// over a real socket (runInterpByte-style in-process interp can't
// host a long-running server).
func TestSupervisedServeInterpFallback(t *testing.T) {
	bin := buildLangBinForInterp(t)
	port := freeLoopbackPort(t)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "srv.fern")
	if err := os.WriteFile(srcPath, []byte(fmt.Sprintf(supervisedServeSrc, port)), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", srcPath)
	stderrPath := filepath.Join(dir, "stderr.log")
	errFile, err := os.Create(stderrPath)
	if err != nil {
		t.Fatalf("create stderr file: %v", err)
	}
	cmd.Stderr = errFile
	if err := cmd.Start(); err != nil {
		errFile.Close()
		t.Fatalf("start interp server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		errFile.Close()
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	e2eharness.WaitServerReady(t, addr, 30*time.Second) // interp startup is slower than a native binary

	if resp := httpRoundTrip(t, addr, "/ok", 10*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("interp fallback /ok: want 200, got\n%s", resp)
	}
	waitStderrContains(t, stderrPath, "supervision unavailable; serving single-process", 10*time.Second)
}

func readFileString(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// procForkProbeSrc pins the builtin-level contract on the native
// backends: fork's 0-in-child / pid-in-parent split, waitpid's
// normal-exit decode ((status>>8)&0xff — the child exits 7), and
// the trap taxonomy surfacing raw through supervision (a bounds
// trap in a forked child reads back as 134).
const procForkProbeSrc = `
function boom(i: i32): i32 {
    var a: i32[] = [1, 2, 3];
    return a[i];
}
function main(): i32 {
    var pid: i32 = proc_fork();
    if (pid < 0) { return 90; }
    if (pid == 0) {
        exit(7);
        return 7;
    }
    var code: i32 = proc_waitpid(pid);
    if (code != 7) { return 91; }
    var pid2: i32 = proc_fork();
    if (pid2 < 0) { return 92; }
    if (pid2 == 0) {
        var x: i32 = boom(9);
        exit(x);
        return x;
    }
    var code2: i32 = proc_waitpid(pid2);
    if (code2 != 134) { return 93; }
    return 0;
}`

func TestProcForkWaitpidX86_64(t *testing.T) {
	if _, code := compileAndRunX86_64(t, procForkProbeSrc); code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
}

func TestProcForkWaitpidArm64(t *testing.T) {
	if _, code := compileAndRunArm64(t, procForkProbeSrc); code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
}

// The interp constants: proc_fork = -38 (ENOSYS — the Go runtime
// is threaded, bare fork is UB) and proc_waitpid = -10 (ECHILD —
// no child can ever exist). These are what tcp_serve_supervised's
// fallback detection keys on, so they're pinned exactly.
func TestProcForkWaitpidInterpENOSYS(t *testing.T) {
	src := `function main(): i32 {
    if (proc_fork() != -38) { return 1; }
    if (proc_waitpid(12345) != -10) { return 2; }
    return 0;
}`
	if code := runInterpExit(t, src); code != 0 {
		t.Errorf("interp exit = %d, want 0", code)
	}
}
