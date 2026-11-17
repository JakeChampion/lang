package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const callerRangeProg = `@noinline function get(a: i32[], i: i32): i32 {
    if (i < a.len()) {
        return a[i];
    }
    return 0;
}
@noinline function pick(a: i32[], i: i32): i32 {
    if (i < a.len()) {
        return a[i];
    }
    return 0;
}
function main(): i32 {
    let a = [1, 2, 3];
    let t = 0;
    for k in 0..a.len() {
        t += get(a, k);
    }
    return t + get(a, 7) + pick(a, 1 - args().len() * 2);
}
`

// Each caller of `get` gives it an index that is not negative, so its read
// keeps no test of the sign. A caller of `pick` can give it a negative
// index, so its read keeps the test, and the run aborts.
func TestSelfHostCallerRange(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "range.fern")
	if err := os.WriteFile(src, []byte(callerRangeProg), 0o644); err != nil {
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
		if body := condBranchBody(t, string(asm), "get"); strings.Contains(body, "oob_abort") {
			t.Errorf("%s: get keeps a check that its callers prove:\n%s", target, body)
		}
		if body := condBranchBody(t, string(asm), "pick"); !strings.Contains(body, "oob_abort") {
			t.Errorf("%s: pick lost the test of the sign of its index:\n%s", target, body)
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
			t.Errorf("%s: exit %d, output %q; want the abort of the negative index", tg.target, code, got)
		}
	}
}
