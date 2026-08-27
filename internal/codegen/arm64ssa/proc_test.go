package arm64ssa

import (
	"fmt"
	"strings"
	"testing"
)

func helperText(t *testing.T, emit func(func(string, ...any))) string {
	t.Helper()
	var b strings.Builder
	emit(func(f string, a ...any) { b.WriteString(fmt.Sprintf(f, a...) + "\n") })
	return b.String()
}

// The IR calls these by their bare builtin names — proc_fork, not
// __fern_proc_fork, which is the flat backend's spelling. Keying the table the
// other way emits three helpers nobody calls and leaves the three calls
// dangling, which is exactly what the first attempt at this did.
func TestProcHelpersUseTheNamesTheIrCalls(t *testing.T) {
	for name, emit := range map[string]func(func(string, ...any)){
		"proc_fork":    emitProcForkHelper,
		"proc_waitpid": emitProcWaitpidHelper,
		"proc_exec":    emitProcExecHelper,
	} {
		if runtimeHelperEmitters[name] == nil {
			t.Errorf("%s has no emitter registered under the name the IR calls", name)
		}
		if body := helperText(t, emit); !strings.Contains(body, fnLabel(name)+":") {
			t.Errorf("%s defines a label other than %s:\n%s", name, fnLabel(name), body)
		}
	}
}

// waitpid returns what a shell would: the exit code for a normal exit, 128 plus
// the signal for a death by signal. A bounds-trapped worker has to surface as
// 134, not as the raw status word.
func TestWaitpidDecodesTheStatusWordLikeAShell(t *testing.T) {
	body := helperText(t, emitProcWaitpidHelper)
	for _, want := range []string{
		"and w10, w9, #0x7f", // termination signal
		"lsr w0, w9, #8",     // normal exit: status >> 8
		"and w0, w0, #0xff",
		"add w0, w10, #128", // signal death
	} {
		if !strings.Contains(body, want) {
			t.Errorf("waitpid is missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "b.lt .Lssa_wait_done") {
		t.Errorf("a failing wait4 does not return -errno as-is:\n%s", body)
	}
}

// execve does not prepend argv[0], and it wants a NULL-terminated argv. Getting
// either wrong is a subprocess that runs with its arguments shifted by one, or
// a kernel read past the end of the array.
func TestProcExecBuildsArgvWithPathFirstAndNullLast(t *testing.T) {
	body := helperText(t, emitProcExecHelper)
	if !strings.Contains(body, "str x26, [x21]") {
		t.Errorf("argv[0] is not the path copy:\n%s", body)
	}
	if !strings.Contains(body, "str xzr, [x21, x1, lsl #3]") {
		t.Errorf("argv is not NULL-terminated:\n%s", body)
	}
	if !strings.Contains(body, "add w0, w20, #2") {
		t.Errorf("argv is not sized for argc plus argv[0] plus the terminator:\n%s", body)
	}
	// Both the path and every argument are copied NUL-terminated: execve takes
	// C strings, and this backend's are length-prefixed without one.
	if n := strings.Count(body, "strb wzr, ["); n != 2 {
		t.Errorf("%d NUL terminations, want one for the path and one per argument:\n%s", n, body)
	}
}

// proc_exec hands envp to execve, so the child inherits the environment — which
// means the snapshot _start takes has to be emitted for a program that only
// ever spawns, never calls env().
func TestProcExecPullsInTheEnvpSnapshot(t *testing.T) {
	if !usesEnv([]string{"proc_exec"}) {
		t.Error("a program that calls proc_exec does not get the envp snapshot")
	}
	if body := helperText(t, emitProcExecHelper); !strings.Contains(body, envpSym) {
		t.Errorf("proc_exec does not read %s:\n%s", envpSym, body)
	}
}
