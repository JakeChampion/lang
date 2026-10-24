package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

const procExecAsArgvEnvpSrc = `
function main(): i32 {
    let pid: i32 = proc_fork();
    if (pid < 0) { return 70; }
    if (pid == 0) {
        let script: string = "test \"$0\" = myname || exit 61; test \"$FERNX\" = 42 || exit 62; test -z \"${HOME+set}\" || exit 63; exit 29";
        let rc: i32 = proc_exec_as("/bin/sh", ["myname", "-c", script], ["FERNX=42"]);
        return 71;
    }
    return proc_waitpid(pid);
}`

const procExecAsFailAndEmptySrc = `
function main(): i32 {
    let rc: i32 = proc_exec_as("/nonexistent/binary", ["x"], ["A=1"]);
    if (rc >= 0) { return 70; }
    let pid: i32 = proc_fork();
    if (pid < 0) { return 71; }
    if (pid == 0) {
        let r2: i32 = proc_exec_as("/bin/sh", ["sh", "-c", "test -z \"${HOME+set}\" && exit 31"], []);
        return 72;
    }
    return proc_waitpid(pid);
}`

const procExecAsSingleArgvSrc = `
function main(): i32 {
    let pid: i32 = proc_fork();
    if (pid < 0) { return 70; }
    if (pid == 0) {
        let rc: i32 = proc_exec_as("%s", ["true"], ["A=1", "A=2"]);
        return 71;
    }
    return proc_waitpid(pid);
}`

func trueBin(goos string) string {
	if goos == "darwin" {
		return "/usr/bin/true"
	}
	return "/bin/true"
}

func runProcExecAsChecks(t *testing.T, goos string, run func(*testing.T, string) int) {
	t.Helper()
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"argv0-and-envp", procExecAsArgvEnvpSrc, 29},
		{"failure-path-and-empty-envp", procExecAsFailAndEmptySrc, 31},
		{"single-element-argv", fmt.Sprintf(procExecAsSingleArgvSrc, trueBin(goos)), 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			switch got := run(t, c.src); got {
			case c.want:
			case 61:
				t.Fatalf("exit 61: argv[0] did not reach the child as given")
			case 62:
				t.Fatalf("exit 62: the caller's envp did not reach the child")
			case 63:
				t.Fatalf("exit 63: the parent's environment leaked into the child")
			case 70:
				t.Fatalf("exit 70: proc_exec_as did not report failure as a negative errno (or fork failed)")
			case 71, 72:
				t.Fatalf("exit %d: proc_exec_as returned instead of replacing the process — exec failed", got)
			default:
				t.Fatalf("exit %d, want %d", got, c.want)
			}
		})
	}
}

func TestX86_64ProcExecAs(t *testing.T) {
	runProcExecAsChecks(t, "linux", func(t *testing.T, src string) int {
		_, exit := compileAndRunX86_64(t, src)
		return exit
	})
}

func TestArm64ProcExecAs(t *testing.T) {
	runProcExecAsChecks(t, "linux", func(t *testing.T, src string) int {
		_, exit := compileAndRunArm64(t, src)
		return exit
	})
}

func TestArm64DarwinProcExecAs(t *testing.T) {
	bin := buildFernCLI(t)
	runProcExecAsChecks(t, "darwin", func(t *testing.T, src string) int {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "prog.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out := filepath.Join(dir, "prog")
		if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, srcPath).CombinedOutput(); err != nil {
			t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
		}
		if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
			t.Skip("execution check only runs on Apple Silicon")
		}
		cmd := exec.Command(out)
		cmd.Dir = dir
		_ = cmd.Run()
		ps := cmd.ProcessState
		if ps == nil || !ps.Exited() {
			t.Fatalf("native Mach-O did not run to a normal exit (state=%v)", ps)
		}
		return ps.ExitCode()
	})
}

func TestInterpProcExecAsENOSYS(t *testing.T) {
	src := `
function main(): i32 {
    let rc: i32 = proc_exec_as("/bin/true", ["true"], []);
    if (rc == 0 - 38) { return 9; }
    return 8;
}`
	if got := runInterpExit(t, src); got != 9 {
		t.Errorf("interp proc_exec_as = exit %d, want 9 (-38/ENOSYS)", got)
	}
}

func TestWASMProcExecAsRefused(t *testing.T) {
	src := `
function main(): i32 {
    return proc_exec_as("/bin/true", ["true"], []);
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 {
			t.Errorf("%s accepted proc_exec_as; it has no process image to replace", target)
			continue
		}
		if vs[0].Builtin != "proc_exec_as" || vs[0].Capability != "proc" {
			t.Errorf("%s refused %q on %q, want proc_exec_as on proc", target, vs[0].Builtin, vs[0].Capability)
		}
	}
}
