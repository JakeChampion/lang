package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const sharedLengthProg = `@noinline function pair(a: i32[], i: i32, j: i32): i32 { return a[i] + a[j]; }
@noinline function chars(s: string, i: i32, j: i32): i32 { return s[i] as i32 + s[j] as i32; }
function main(): i32 {
    let n: i32 = args().len();
    let a = [10, 20, 30];
    if (n == 2) {
        return pair(a, 0, 3);
    }
    if (n == 3) {
        return chars("abc", 0, 5);
    }
    return pair(a, n, n + 1) + chars("abc", n - 1, n + 1) - 200;
}
`

// Two checked reads of one array, or of one string, load its length once
// on arm64, and each check compares with that value. An index out of range
// still aborts with the message of an array or of a string.
func TestSelfHostSharedLengthLoad(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "shared.fern")
	if err := os.WriteFile(src, []byte(sharedLengthProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "arm64.s")
	if combined, err := exec.Command(h.cli, "-O", "-target", "arm64-linux", "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ fn, length, trap string }{
		{"pair", `ldr x\d+, \[x0(, #0)?\]\n`, "__fern_oob_abort"},
		{"chars", `ldr x\d+, \[x0, #8\]\n`, "__fern_str_oob_abort"},
	} {
		body := condBranchBody(t, string(asm), c.fn)
		if n := len(regexp.MustCompile(c.length).FindAllString(body, -1)); n != 1 {
			t.Errorf("%s loads the length %d times, want once:\n%s", c.fn, n, body)
		}
		if n := strings.Count(body, "b "+c.trap); n != 2 {
			t.Errorf("%s has %d checks that go to %s, want 2:\n%s", c.fn, n, c.trap, body)
		}
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		for _, run := range []struct {
			args []string
			code int
			text string
		}{
			{nil, 246 - 200, ""},
			{[]string{"x"}, 134, "array index out of range"},
			{[]string{"x", "y"}, 134, "string index out of range"},
		} {
			cmd := exec.Command(bin, run.args...)
			if len(tg.runner) > 0 {
				cmd = exec.Command(tg.runner[0], append(append(tg.runner[1:], bin), run.args...)...)
			}
			got, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != run.code || !strings.Contains(string(got), run.text) {
				t.Errorf("%s %v: exit %d, output %q; want exit %d with %q", tg.target, run.args, code, got, run.code, run.text)
			}
		}
	}
}
