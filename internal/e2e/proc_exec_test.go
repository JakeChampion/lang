package e2e

import "testing"

// proc_exec(path, args) — execve(2), the third leg of the crash-only process
// trio next to proc_fork / proc_waitpid, and the piece that lets a forked child
// become another program.
//
// Two properties are worth pinning, because both were wrong in the first cut:
//
//   - argv layout. proc_exec supplies argv[0] itself (the program path), so
//     callers pass only real arguments. `sh -c SCRIPT name a b` sets $0 from the
//     first OPERAND after the script, so the assertion below checks $0/$1
//     against the operands, not against the path.
//   - the failure path RETURNS. On success execve replaces the process, so the
//     i32 result only ever reports failure, as -errno. A helper that instead
//     corrupted the caller's frame on the way back would show up here as a wrong
//     exit code rather than a crash.
//
// The arm64 leg is not redundant with x86-64: arm64 runs the two-word string
// ABI (docs/SSO-NATIVE-FLIP-STATUS.md) where a `string` occupies TWO argument
// slots and a `string[]` element is a 16-byte (data, len) pair, while x86-64
// uses the single-word SSO form. The first version of this helper read arm64's
// path LENGTH as the args box and segfaulted on every call; only a
// per-architecture run catches that.

// A successful exec: the child becomes /bin/sh, which exits 23. Reaching the
// `return 71` after proc_exec would mean exec failed.
const procExecSpawnSrc = `
function main(): i32 {
    var pid: i32 = proc_fork();
    if (pid < 0) { return 70; }
    if (pid == 0) {
        var rc: i32 = proc_exec("/bin/sh", ["-c", "exit 23"]);
        return 71;
    }
    return proc_waitpid(pid);
}`

// The failure path returns -errno, and argv ordering puts the caller's
// arguments after the implicit argv[0].
const procExecFailAndArgvSrc = `
function main(): i32 {
    var rc: i32 = proc_exec("/nonexistent/binary", ["x"]);
    if (rc >= 0) { return 70; }
    var pid: i32 = proc_fork();
    if (pid < 0) { return 71; }
    if (pid == 0) {
        var r2: i32 = proc_exec("/bin/sh", ["-c", "test \"$0\" = zeroname && test \"$1\" = one && exit 17", "zeroname", "one"]);
        return 72;
    }
    return proc_waitpid(pid);
}`

// An empty argument list still has to produce a well-formed argv
// ([path, NULL]) — the array-length load runs even when the copy loop does not.
const procExecEmptyArgsSrc = `
function main(): i32 {
    var rc: i32 = proc_exec("/nonexistent/binary", []);
    if (rc >= 0) { return 70; }
    var pid: i32 = proc_fork();
    if (pid < 0) { return 71; }
    if (pid == 0) {
        var r2: i32 = proc_exec("/bin/true", []);
        return 72;
    }
    return proc_waitpid(pid);
}`

func runProcExecChecks(t *testing.T, run func(*testing.T, string) int) {
	t.Helper()
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"spawn", procExecSpawnSrc, 23},
		{"failure-path-and-argv-order", procExecFailAndArgvSrc, 17},
		{"empty-args", procExecEmptyArgsSrc, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			switch got := run(t, c.src); got {
			case c.want:
			case 70:
				t.Fatalf("exit 70: proc_exec did not report failure as a negative errno (or fork failed)")
			case 71, 72:
				t.Fatalf("exit %d: proc_exec returned instead of replacing the process — exec failed", got)
			default:
				t.Fatalf("exit %d, want %d", got, c.want)
			}
		})
	}
}

func TestX86_64ProcExec(t *testing.T) {
	runProcExecChecks(t, func(t *testing.T, src string) int {
		_, exit := compileAndRunX86_64(t, src)
		return exit
	})
}

func TestArm64ProcExec(t *testing.T) {
	runProcExecChecks(t, func(t *testing.T, src string) int {
		_, exit := compileAndRunArm64(t, src)
		return exit
	})
}

// Under -interp there is no process control (exec'ing would replace the
// interpreter itself), so proc_exec answers -38/ENOSYS exactly as proc_fork
// does, letting a caller detect the absence and degrade rather than vanish.
func TestInterpProcExecENOSYS(t *testing.T) {
	src := `
function main(): i32 {
    var rc: i32 = proc_exec("/bin/true", []);
    if (rc == 0 - 38) { return 9; }
    return 8;
}`
	if got := runInterpExit(t, src); got != 9 {
		t.Errorf("interp proc_exec = exit %d, want 9 (-38/ENOSYS)", got)
	}
}
