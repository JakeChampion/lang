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

// Design-doc test bar items 1–4 on the native backend; the scenarios are
// e2eharness's, shared with the self-host twins.

// Two workers over one listener (#9854): a request on a second
// connection is answered while the first worker is deep in /slow, and a
// worker's death leaves the other serving.
func TestSupervisedServeWorkersServeSideBySide(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.WorkersServerSource(port))
	_, stderrPath := startSupervisedServer(t, bin, runner)
	e2eharness.CheckWorkersServeSideBySide(t, fmt.Sprintf("127.0.0.1:%d", port), stderrPath)
}

// /ok answers 200, /boom traps the worker (a reset and a worker-death
// line with the raw exit code 134), and /ok answers 200 again: the
// service survived what one request did.
func TestSupervisedServeSurvivesHandlerTrap(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.TrappingServerSource(port))
	_, stderrPath := startSupervisedServer(t, bin, runner)
	e2eharness.CheckSurvivesHandlerTrap(t, fmt.Sprintf("127.0.0.1:%d", port), stderrPath)
}

// Per-worker SO_REUSEPORT listeners (#9854): two workers bind their own,
// a trap takes one worker and its listener, and the replacement binds
// anew, so /ok answers again.
func TestSupervisedServeReusePortWorkers(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.ReusePortWorkersServerSource(port))
	_, stderrPath := startSupervisedServer(t, bin, runner)
	e2eharness.CheckSurvivesHandlerTrap(t, fmt.Sprintf("127.0.0.1:%d", port), stderrPath)
}

// A crash-looping worker makes the supervisor give up with the child's
// code instead of reforking forever. Slow by design: the doubling
// backoff sleeps sum to about 11 s.
func TestSupervisedServeCrashLoopGivesUp(t *testing.T) {
	if testing.Short() {
		t.Skip("crash-loop giveup waits out ~11s of supervisor backoff")
	}
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.TrappingServerSource(port))
	cmd, stderrPath := startSupervisedServer(t, bin, runner)
	e2eharness.CheckCrashLoopGivesUp(t, cmd, fmt.Sprintf("127.0.0.1:%d", port), stderrPath)
}

// Two workers over one listener, one crash-looping while the other is
// held in a handler: the give-up stops the worker the count did not
// come from, so nothing holds the port after the supervisor exits.
func TestSupervisedServeCrashLoopStopsSurvivor(t *testing.T) {
	if testing.Short() {
		t.Skip("crash-loop giveup waits out ~11s of supervisor backoff")
	}
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.StalledSurvivorServerSource(port))
	cmd, stderrPath := startSupervisedServer(t, bin, runner)
	e2eharness.CheckCrashLoopStopsSurvivor(t, cmd, fmt.Sprintf("127.0.0.1:%d", port), stderrPath)
}

// A worker's death while serving is not the exit code of the clean
// shutdown that follows it.
func TestSupervisedServeTrapThenShutdownExitsClean(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.TrappingServerSource(port))
	cmd, stderrPath := startSupervisedServer(t, bin, runner)
	e2eharness.CheckTrapThenShutdownExitsClean(t, cmd, fmt.Sprintf("127.0.0.1:%d", port), stderrPath)
}

// `max_connections: 0` serves as 1 rather than never reading the
// listener.
func TestServeMaxConnectionsFloor(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.MaxConnectionsFloorServerSource(port))
	startSupervisedServer(t, bin, runner)
	e2eharness.CheckMaxConnectionsFloor(t, fmt.Sprintf("127.0.0.1:%d", port))
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
	if err := os.WriteFile(srcPath, []byte(e2eharness.TrappingServerSource(port)), 0o644); err != nil {
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

	if resp := e2eharness.HTTPRoundTrip(t, addr, "/ok", 10*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("interp fallback /ok: want 200, got\n%s", resp)
	}
	e2eharness.WaitStderrContains(t, stderrPath, "supervision unavailable; serving single-process", 10*time.Second)
}

// procForkProbeSrc pins the builtin-level contract on the native
// backends: fork's 0-in-child / pid-in-parent split, waitpid's
// normal-exit decode ((status>>8)&0xff — the child exits 7), and
// the trap taxonomy surfacing raw through supervision (a bounds
// trap in a forked child reads back as 134).
const procForkProbeSrc = `
function boom(i: i32): i32 {
    let a: i32[] = [1, 2, 3];
    return a[i];
}
function main(): i32 {
    let pid: i32 = proc_fork();
    if (pid < 0) { return 90; }
    if (pid == 0) {
        exit(7);
        return 7;
    }
    let code: i32 = proc_waitpid(pid);
    if (code != 7) { return 91; }
    let pid2: i32 = proc_fork();
    if (pid2 < 0) { return 92; }
    if (pid2 == 0) {
        let x: i32 = boom(9);
        exit(x);
        return x;
    }
    let code2: i32 = proc_waitpid(pid2);
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

// A handler that blocks stalls its worker (#9854): with one worker, a
// request behind /slow waits for it. The phase that runs handlers off the
// worker's thread of control inherits this as a failing test.
func TestSupervisedServeHandlerStallsItsWorker(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.StallServerSource(port))
	startSupervisedServer(t, bin, runner)
	e2eharness.CheckHandlerStallsItsWorker(t, fmt.Sprintf("127.0.0.1:%d", port))
}
