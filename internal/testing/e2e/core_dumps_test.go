package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const coreDumpsSource = `function main(): i32 {
    let a: i32 = proc_fork();
    if (a < 0) { return 2; }
    if (a == 0) { exit(5); }
    if (proc_waitpid_status(a) != 1280) { return 3; }
    let b: i32 = proc_fork();
    if (b == 0) {
        signal_raise(15);
        exit(9);
    }
    if (proc_waitpid_status(b) != 15) { return 4; }
    if (proc_waitpid_status(a) != 0 - 10) { return 5; }
    if (!disable_core_dumps()) { return 6; }
    if (args().len() > 1) { return 0; }
    signal_raise(3);
    return 7;
}
`

func checkNoCore(t *testing.T, cmd *exec.Cmd, emulated bool) {
	t.Helper()
	dir := t.TempDir()
	wrapped := exec.Command("/bin/sh", append([]string{"-c", `ulimit -S -c "$(ulimit -H -c)"; exec "$@"`, "sh"}, cmd.Args...)...)
	wrapped.Dir = dir
	var out bytes.Buffer
	wrapped.Stdout = &out
	wrapped.Stderr = &out
	wrapped.Run()
	ws, ok := wrapped.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGQUIT {
		t.Fatalf("%v, want death by SIGQUIT (an exit code names the failing step)\n%s", wrapped.ProcessState, out.String())
	}
	if ws.CoreDump() {
		t.Errorf("the program dumped core after disable_core_dumps()")
	}
	if entries, _ := os.ReadDir(dir); !emulated && len(entries) != 0 {
		t.Errorf("the run left %s in its working directory", entries[0].Name())
	}
}

func checkStatuses(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	out, err := cmd.CombinedOutput()
	if code := exitCodeOf(err); code != 0 {
		t.Fatalf("exit = %d, want 0 (the code names the failing step)\n%s", code, out)
	}
}

func TestX86_64CoreDumps(t *testing.T) {
	runner := e2eharness.X86_64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, coreDumpsSource, nil)
	checkStatuses(t, runX86_64Bin(runner, bin, "stop"))
	checkNoCore(t, runX86_64Bin(runner, bin), len(runner) != 0)
}

func TestArm64CoreDumps(t *testing.T) {
	qemu := e2eharness.Arm64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Linux, coreDumpsSource, nil)
	checkStatuses(t, runArm64Bin(qemu, bin, "stop"))
	checkNoCore(t, runArm64Bin(qemu, bin), qemu != "")
}

func TestArm64DarwinCoreDumps(t *testing.T) {
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Darwin, coreDumpsSource, nil)
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	checkStatuses(t, exec.Command(bin, "stop"))
	checkNoCore(t, exec.Command(bin), false)
}

func TestInterpCoreDumps(t *testing.T) {
	src := `function main(): i32 {
    if (proc_waitpid_status(0 - 1) != 0 - 10) { return 2; }
    if (!disable_core_dumps()) { return 3; }
    return 0;
}
`
	p := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	checkStatuses(t, exec.Command(buildLangBinForInterp(t), "-interp", p))
}

func TestWASMCoreDumpsRefused(t *testing.T) {
	for _, tc := range []struct{ builtin, capability, src string }{
		{"disable_core_dumps", "rlimit", `function main(): i32 { if (disable_core_dumps()) { return 0; } return 1; }`},
		{"proc_waitpid_status", "proc", `function main(): i32 { return proc_waitpid_status(1); }`},
	} {
		prog, err := parser.Parse(tc.src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if _, err := checker.Check(prog); err != nil {
			t.Fatalf("check: %v", err)
		}
		for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
			vs := platforms.Enforce(prog, target)
			if len(vs) == 0 {
				t.Errorf("%s accepted %s", target, tc.builtin)
				continue
			}
			if vs[0].Builtin != tc.builtin || vs[0].Capability != tc.capability {
				t.Errorf("%s refused %q on %q, want %s on %s", target, vs[0].Builtin, vs[0].Capability, tc.builtin, tc.capability)
			}
		}
	}
}
