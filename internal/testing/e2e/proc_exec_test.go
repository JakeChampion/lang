package e2e

import "testing"

const procExecSpawnSrc = `
function main(): i32 {
    let pid: i32 = proc_fork();
    if (pid < 0) { return 70; }
    if (pid == 0) {
        let rc: i32 = proc_exec("/bin/sh", ["-c", "exit 23"]);
        return 71;
    }
    return proc_waitpid(pid);
}`

const procExecFailAndArgvSrc = `
function main(): i32 {
    let rc: i32 = proc_exec("/nonexistent/binary", ["x"]);
    if (rc >= 0) { return 70; }
    let pid: i32 = proc_fork();
    if (pid < 0) { return 71; }
    if (pid == 0) {
        let r2: i32 = proc_exec("/bin/sh", ["-c", "test \"$0\" = zeroname && test \"$1\" = one && exit 17", "zeroname", "one"]);
        return 72;
    }
    return proc_waitpid(pid);
}`

const procExecEmptyArgsSrc = `
function main(): i32 {
    let rc: i32 = proc_exec("/nonexistent/binary", []);
    if (rc >= 0) { return 70; }
    let pid: i32 = proc_fork();
    if (pid < 0) { return 71; }
    if (pid == 0) {
        let r2: i32 = proc_exec("/bin/true", []);
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

func TestInterpProcExecENOSYS(t *testing.T) {
	src := `
function main(): i32 {
    let rc: i32 = proc_exec("/bin/true", []);
    if (rc == 0 - 38) { return 9; }
    return 8;
}`
	if got := runInterpExit(t, src); got != 9 {
		t.Errorf("interp proc_exec = exit %d, want 9 (-38/ENOSYS)", got)
	}
}
