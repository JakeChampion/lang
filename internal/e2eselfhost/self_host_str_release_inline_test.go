package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// A string release renders the helper's guards inline (ssa_rc_dec): an
// inline string's tag bit, the heap floor and a negative count skip it, a
// count above one is decremented in place, and only a count of one or zero
// calls __fern_str_free. `g` releases the concatenation it built.
const strReleaseProg = `@noinline function g(a: string): i32 { var t: string = a + "x"; return t.len(); }
function main(): i32 { return g("abc" + "") + g("") * 10; }
`

func TestSelfHostStrReleaseInline(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "strel.fern")
	if err := os.WriteFile(src, []byte(strReleaseProg), 0o644); err != nil {
		t.Fatal(err)
	}
	inline := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`testb \$1, %[a-z0-9]+\n\s+jnz \S+\n(?:.*\n){0,10}\s+call __fn___fern_str_free\.r\n(?:.*\n){0,4}\s+subl \$1, -8\(`),
		"arm64-linux":  regexp.MustCompile(`tbnz x[0-9]+, #0, \S+\n(?:.*\n){0,10}\s+bl __fn___fern_str_free\.r\n(?:.*\n){0,4}\s+sub w5, w5, #1\n`),
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
		if body := condBranchBody(t, string(asm), "g"); !inline[target].MatchString(body) {
			t.Errorf("%s: g does not release its string through the inline guards:\n%s", target, body)
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
		if got := run.ProcessState.ExitCode(); got != 14 {
			t.Errorf("%s: exit %d, want 14 (the interpreter's)", tg.target, got)
		}
	}
}
