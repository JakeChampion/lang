package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSupervisedServeInterpFallback(t *testing.T) {
	bin := buildLangBinForInterp(t)
	srcPath := filepath.Join(t.TempDir(), "srv.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.TrappingServerSource(0)), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	addr, stderrPath := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", srcPath))
	e2eharness.WaitServerReady(t, addr)

	if resp := e2eharness.HTTPRoundTrip(t, addr, "/ok", 10*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("interp fallback /ok: want 200, got\n%s", resp)
	}
	e2eharness.WaitStderrContains(t, stderrPath, "supervision unavailable; serving single-process", 10*time.Second)
}

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
