package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// An `if` or `while` condition built from `&&`, `||` and `!` lowers to a chain
// of branches (semsource.cond), each comparison fused with the branch that
// takes it, rather than a boolean joined in a phi and tested again. `both` and
// `either` must therefore materialise no flag; `value` returns the same kind of
// expression as a boolean, so it still does, which is what keeps the absence
// in the other two from passing vacuously.
const condBranchProg = `@noinline function both(a: i32, b: i32, c: i32): i32 {
    if (a < b && b < c) { return 1; }
    return 2;
}
@noinline function either(a: i32, b: i32, c: i32): i32 {
    var n: i32 = 0;
    while (a < b || !(b < c)) { a = a + 1; n = n + 1; }
    return n;
}
@noinline function value(a: i32, b: i32): boolean { return a < b && b < 10; }
function main(): i32 {
    if (both(1, 2, 3) != 1 || both(2, 1, 3) != 2 || both(1, 3, 2) != 2) { return 1; }
    if (either(0, 3, 5) != 3 || either(4, 3, 5) != 0) { return 2; }
    if (!value(1, 2) || value(1, 20) || value(3, 2)) { return 3; }
    return 42;
}
`

func TestSelfHostCondBranch(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "cond.fern")
	if err := os.WriteFile(src, []byte(condBranchProg), 0o644); err != nil {
		t.Fatal(err)
	}
	flag := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`\n\s+set[a-z]+ `),
		"arm64-linux":  regexp.MustCompile(`\n\s+cset `),
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
		for _, fn := range []string{"both", "either"} {
			if body := condBranchBody(t, string(asm), fn); flag[target].MatchString(body) {
				t.Errorf("%s: %s materialises its condition:\n%s", target, fn, body)
			}
		}
		if body := condBranchBody(t, string(asm), "value"); !flag[target].MatchString(body) {
			t.Errorf("%s: value materialises no flag either, so the absence above proves nothing:\n%s", target, body)
		}
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 42 {
			t.Errorf("%s: exit %d, want 42 (1: both, 2: either, 3: value)", tg.target, got)
		}
	}
}

// condBranchBody is the CLI's listing of `name` up to the next function's
// label, so every return the function has is inside it.
func condBranchBody(t *testing.T, asm, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)\n__fn_` + name + `\.r:\n(.*?)\n__fn_`).FindStringSubmatch(asm)
	if m == nil {
		t.Fatalf("__fn_%s.r not found in the listing", name)
	}
	return m[1]
}
