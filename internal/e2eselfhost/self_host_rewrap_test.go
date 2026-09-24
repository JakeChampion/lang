package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `(j - i) as i64` wraps the i32 difference and then extends it. The wrap
// already leaves the register sign-extended, so the extend flows through the
// lift (ssa_lift.sign_extended32) and the difference is sign-extended once.
// `f(9, 4, 10)` is 15 and `f(3, 8, 100)`, a negative difference, is 95. The
// negative case is compared at i64: a zero-extended wrap is off by 2^32, which
// a truncation to the exit code would discard.
const rewrapProg = `@noinline function f(j: i32, i: i32, p: i64): i64 { return p + (j - i) as i64; }
function main(): i32 {
    if (f(3, 8, 100i64) == 95i64) { return f(9, 4, 10i64) as i32; }
    return 1;
}
`

func TestSelfHostRedundantWrapFlowsThrough(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "rewrap.fern")
	if err := os.WriteFile(src, []byte(rewrapProg), 0o644); err != nil {
		t.Fatal(err)
	}
	extend := map[string]string{"x86-64-linux": "movslq ", "arm64-linux": "sxtw "}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-O", "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		body := condBranchBody(t, string(asm), "f")
		if n := strings.Count(body, extend[target]); n != 1 {
			t.Errorf("%s: f sign-extends %d times, want once:\n%s", target, n, body)
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
		if got := run.ProcessState.ExitCode(); got != 15 {
			t.Errorf("%s: exit %d, want 15 (1 means f(3, 8, 100) was not 95)", tg.target, got)
		}
	}
}
