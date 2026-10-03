// Crash-only supervised serving (docs/CRASH-ONLY-SERVE.md, plan item D2'):
// the interpreter's single-process fallback and the fork/waitpid builtins
// supervision is made of. The compiled serving scenarios are the self-host
// twins in internal/e2eselfhost/self_host_serve_test.go.
//
// Under `-interp`, proc_fork answers -38 (ENOSYS), so serve.supervise
// degrades to single-process serving: `/ok` still answers 200 and a one-line
// degradation notice lands on stderr.
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

	"github.com/jakechampion/lang/internal/e2eharness"
)

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

// Design-doc "interp parity": the interpreter cannot bare-fork
// (Go's runtime is threaded), so proc_fork answers -38 (ENOSYS)
// and serve.supervise degrades to plain single-process
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

// procForkProbeSrc pins the builtin-level contract on the compiled
// targets: fork's 0-in-child / pid-in-parent split, waitpid's
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
// no child can ever exist). These are what serve.supervise's
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
