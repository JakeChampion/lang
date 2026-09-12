package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/platforms"
)

// proc_exec_as(path, argv, envp) — execve(2) with NEITHER vector derived from
// the calling process, which is the whole difference from proc_exec: that one
// prepends `path` as argv[0] and hands the child the __fern_envp snapshot.
//
// Two facts carry the feature, and a helper can get either one wrong on its
// own:
//
//   - argv reaches the kernel VERBATIM. `sh -c SCRIPT` with no command_name
//     operand sets $0 from the shell's own argv[0], so the script below reads
//     back exactly what the caller put in slot 0. A helper that still prepended
//     the path would report /bin/sh.
//   - envp REPLACES the environment rather than adding to it. FERNX is in no
//     parent environment, so seeing it proves the caller's vector reached
//     execve; HOME is in every one, so its absence proves the parent's did not.
//     PATH is deliberately not asserted on: `sh` invents a default for that one
//     when the environment it is handed has none.
//
// The arm64 leg is not redundant with x86-64: arm64 runs the two-word string
// ABI, where a `string` takes TWO argument slots (so envp arrives in x3, not
// x2) and a `string[]` element is a 16-byte (data, len) pair. arm64-ssa is a
// third, independently hand-written helper, and arm64-darwin reaches the same
// emitter through XNU's execve trap number and its carry-flag error convention.

// argv[0] as typed, and an environment built by the caller.
const procExecAsArgvEnvpSrc = `
function main(): i32 {
    var pid: i32 = proc_fork();
    if (pid < 0) { return 70; }
    if (pid == 0) {
        var script: string = "test \"$0\" = myname || exit 61; test \"$FERNX\" = 42 || exit 62; test -z \"${HOME+set}\" || exit 63; exit 29";
        var rc: i32 = proc_exec_as("/bin/sh", ["myname", "-c", script], ["FERNX=42"]);
        return 71;
    }
    return proc_waitpid(pid);
}`

// The failure path RETURNS, as -errno; and an EMPTY envp is a well-formed
// vector (the NULL terminator alone), not a missing one.
const procExecAsFailAndEmptySrc = `
function main(): i32 {
    var rc: i32 = proc_exec_as("/nonexistent/binary", ["x"], ["A=1"]);
    if (rc >= 0) { return 70; }
    var pid: i32 = proc_fork();
    if (pid < 0) { return 71; }
    if (pid == 0) {
        var r2: i32 = proc_exec_as("/bin/sh", ["sh", "-c", "test -z \"${HOME+set}\" && exit 31"], []);
        return 72;
    }
    return proc_waitpid(pid);
}`

// An argv of exactly one element still has to produce [argv0, NULL]: the
// element loop runs once and the terminator lands at slot 1, not slot 2. A
// duplicate name in envp is legal and the kernel keeps both, so the vector is
// passed through rather than deduplicated.
const procExecAsSingleArgvSrc = `
function main(): i32 {
    var pid: i32 = proc_fork();
    if (pid < 0) { return 70; }
    if (pid == 0) {
        var rc: i32 = proc_exec_as("%s", ["true"], ["A=1", "A=2"]);
        return 71;
    }
    return proc_waitpid(pid);
}`

// trueBin is where a no-argument, exit-0 program lives on the host the compiled
// binary will run on: /bin/true everywhere the Linux legs run, /usr/bin/true on
// macOS, which has no /bin/true at all.
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

func TestArm64SSAProcExecAs(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	runProcExecAsChecks(t, "linux", func(t *testing.T, src string) int {
		bin := compileArm64SSA(t, fern, src, os.Environ())
		code, _ := runArm64SSABin(t, qemu, bin, t.TempDir(), os.Environ())
		return code
	})
}

// The Mach-O leg, which runs natively on Apple Silicon rather than under qemu.
// XNU takes execve as BSD trap 59 and reports failure by setting the carry
// flag with a POSITIVE errno, so the emitter negates it; a helper that skipped
// that would return a large positive number and the failure case would see
// rc >= 0.
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

// Under -interp there is no process control — exec'ing would replace the
// interpreter itself — so proc_exec_as answers -38/ENOSYS exactly as proc_exec
// and proc_fork do, letting a caller detect the absence and degrade.
func TestInterpProcExecAsENOSYS(t *testing.T) {
	src := `
function main(): i32 {
    var rc: i32 = proc_exec_as("/bin/true", ["true"], []);
    if (rc == 0 - 38) { return 9; }
    return 8;
}`
	if got := runInterpExit(t, src); got != 9 {
		t.Errorf("interp proc_exec_as = exit %d, want 9 (-38/ENOSYS)", got)
	}
}

// Neither WASI preview has execve: a component is one instance with no process
// image to replace. The refusal has to arrive from the capability scan rather
// than as an `unknown callee` out of the emitter — the difference between
// "this target cannot exec" and "the backend forgot one".
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
