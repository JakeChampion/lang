package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const callerBelowProg = `@noinline function at(a: i32[], i: i32): i32 {
    return a[i];
}
@noinline function near(a: i32[], i: i32): i32 {
    return a[i];
}
function main(): i32 {
    let a = [1, 2, 3];
    let t = 0;
    for k in 0..a.len() {
        t += at(a, k) + near(a, k);
    }
    return t + near(a, a.len() - 1 + args().len());
}
`

// Each caller of `at` gives it an index below the length of the array that
// it gives with it, so its read keeps no check. One caller of `near` can
// give it the length itself, so its read keeps the check, and the run
// aborts.
func TestSelfHostCallerBelowLength(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "below.fern")
	if err := os.WriteFile(src, []byte(callerBelowProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-O", "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if body := condBranchBody(t, string(asm), "at"); strings.Contains(body, "oob_abort") {
			t.Errorf("%s: at keeps a check that its callers prove:\n%s", target, body)
		}
		if body := condBranchBody(t, string(asm), "near"); !strings.Contains(body, "oob_abort") {
			t.Errorf("%s: near lost the check of its index:\n%s", target, body)
		}
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		cmd := exec.Command(bin)
		if len(tg.runner) > 0 {
			cmd = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		got, _ := cmd.CombinedOutput()
		if code := cmd.ProcessState.ExitCode(); code != 134 || !strings.Contains(string(got), "array index out of range") {
			t.Errorf("%s: exit %d, output %q; want the abort of the index at the length", tg.target, code, got)
		}
	}
}
