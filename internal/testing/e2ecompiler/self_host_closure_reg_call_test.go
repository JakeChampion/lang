package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A call through a function value enters the callee's `.r` entry with the
// arguments in the pool registers, as a register call passes them, and pushes
// them only past ssa_nregs(). `apply` calls a closure with its environment and
// two arguments, so it pushes nothing. `seven`'s closure takes its environment
// and seven arguments, one past the pool, so every argument is also pushed,
// where `.r` reads the ones past the registers.
const closureRegCallProg = `@noinline function apply(f: (i32, i32) => i32, a: i32, b: i32): i32 { return f(a, b); }
@noinline function seven(f: (i32, i32, i32, i32, i32, i32, i32) => i32): i32 { return f(1, 2, 3, 4, 5, 6, 7); }
function main(): i32 {
    let k: i32 = 3;
    let add = (x: i32, y: i32): i32 => x + y + k;
    let mul = (x: i32, y: i32): i32 => x * y;
    let s7 = (a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32): i32 => a + b * 2 + c * 3 + d * 4 + e * 5 + f * 6 + g * 7 + k;
    return apply(add, 4, 5) + apply(mul, 6, 7) + seven(s7);
}
`

func TestSelfHostClosureCallInRegisters(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "clo.fern")
	if err := os.WriteFile(src, []byte(closureRegCallProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "clo.s")
	if combined, err := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// A push the CFI tracks is a frame save, not an argument.
	argPush := regexp.MustCompile(`\n\s+pushq [^\n]+(\n\s+\.cfi_def_cfa_offset)?`)
	pushes := func(body string) int {
		n := 0
		for _, m := range argPush.FindAllStringSubmatch(body, -1) {
			if m[1] == "" {
				n++
			}
		}
		return n
	}
	apply := condBranchBody(t, string(asm), "apply")
	if !strings.Contains(apply, "call *%rdx") || pushes(apply) != 0 {
		t.Errorf("apply: want a call through %%rdx with no argument pushed:\n%s", apply)
	}
	sev := condBranchBody(t, string(asm), "seven")
	if !strings.Contains(sev, "call *%rdx") || pushes(sev) != 8 {
		t.Errorf("seven: want its eight arguments pushed for a call through %%rdx:\n%s", sev)
	}
	if !regexp.MustCompile(`__fn_\S*clo\d+\.r\b`).Match(asm) {
		t.Errorf("no function value names a closure's .r entry")
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		if combined, err := exec.Command(h.cli, "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 197 {
			t.Errorf("%s: exit %d, want 197 (the interpreter's)", tg.target, got)
		}
	}
}
