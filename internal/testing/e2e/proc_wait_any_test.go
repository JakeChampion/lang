package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Three children exit with 3, 5 and 7 after 800, 0 and 400 ms. Each line is
// the index of the child that proc_wait_any names and its raw status word.
// The last line is the result when no child is left: -ECHILD.
const procWaitAnySource = `function spawn(code: i32, delay: i64): i32 {
    let pid = proc_fork();
    if (pid == 0) {
        sleep_ms(delay);
        exit(code);
    }
    return pid;
}

function main(): i32 {
    let kids: i32[] = [spawn(3, 800 as i64), spawn(5, 0 as i64), spawn(7, 400 as i64)];
    for k in kids {
        if (k <= 0) {
            return 2;
        }
    }
    for n in 0..3 {
        let r = proc_wait_any();
        if (r < 0) {
            print(f"wait {n}: {r}");
            return 3;
        }
        let pid = (r >> 32) as i32;
        let status = (r & 4294967295) as i32;
        let found = -1;
        for i in 0..3 {
            if (kids[i] == pid) {
                found = i;
            }
        }
        print(f"{found} {status}");
    }
    print(proc_wait_any().to_string());
    return 0;
}
`

const procWaitAnyWant = "1 1280\n2 1792\n0 768\n-10\n"

func checkProcWaitAny(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	out, err := cmd.CombinedOutput()
	if code := exitCodeOf(err); code != 0 || string(out) != procWaitAnyWant {
		t.Fatalf("exit = %d, output %q; want 0 and %q (the exit code names the failing step)", code, out, procWaitAnyWant)
	}
}

func TestX86_64ProcWaitAny(t *testing.T) {
	runner := e2eharness.X86_64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, procWaitAnySource, nil)
	checkProcWaitAny(t, runX86_64Bin(runner, bin))
}

func TestArm64ProcWaitAny(t *testing.T) {
	qemu := e2eharness.Arm64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Linux, procWaitAnySource, nil)
	checkProcWaitAny(t, runArm64Bin(qemu, bin))
}

func TestArm64DarwinProcWaitAny(t *testing.T) {
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Darwin, procWaitAnySource, nil)
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	checkProcWaitAny(t, exec.Command(bin))
}

func TestInterpProcWaitAny(t *testing.T) {
	p := filepath.Join(t.TempDir(), "prog.fern")
	src := `function main(): i32 {
    if (proc_wait_any() != -10) { return 2; }
    return 0;
}
`
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	checkStatuses(t, exec.Command(buildLangBinForInterp(t), "-interp", p))
}

func TestWASMProcWaitAnyRefused(t *testing.T) {
	const src = `function main(): i32 { return (proc_wait_any() >> 32) as i32; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{e2eharness.TargetWasm32Wasi, "wasm32-wasi-http"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 || vs[0].Builtin != "proc_wait_any" || vs[0].Capability != "proc" {
			t.Errorf("platforms.Enforce on %s: %v, want proc_wait_any refused on proc", target, vs)
		}
	}
	out, err := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetWasm32Wasi, path, filepath.Join(dir, "prog.wasm")).CombinedOutput()
	if err == nil {
		t.Errorf("the compiler built proc_wait_any for wasm32-wasi, which has no processes")
	}
	if !strings.Contains(string(out), "required by `proc_wait_any`; targets providing it") {
		t.Errorf("the wasm32-wasi refusal does not name the builtin:\n%s", out)
	}
}
