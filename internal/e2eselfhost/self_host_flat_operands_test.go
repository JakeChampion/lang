package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// An op the register path runs through the stack machine's own arm takes the
// operands the arm pops in the registers the pops name, rather than pushing
// them for it (ssa_flat_op). `nl` is __count_byte, whose arm also ends in one
// push, so its result comes back in that register and the function moves
// nothing through the stack. `stop` is __scan_set, whose arm pushes its result
// on two paths: the operands still skip the stack.
const flatOperandsProg = `@noinline function stop(s: string, from: i32, set: u8[]): i32 { return __scan_set(s, from, set); }
@noinline function nl(s: string): i32 { return __count_byte(s, 10); }
function main(): i32 {
    var set: u8[] = __alloc_u8(256);
    set = set.with(44, 1 as u8);
    var s: string = "ab,cd\nef,g\n" + "";
    return stop(s, 0, set) * 10 + stop(s, 3, set) + nl(s) * 100;
}
`

func TestSelfHostFlatOperandsInRegisters(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "flat.fern")
	if err := os.WriteFile(src, []byte(flatOperandsProg), 0o644); err != nil {
		t.Fatal(err)
	}
	stack := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`\n\s+(pushq|popq) (%r[a-z0-9]+)`),
		"arm64-linux":  regexp.MustCompile(`\n\s+(str x[0-9]+, \[sp, #-16\]!|ldr x[0-9]+, \[sp\], #16)`),
	}
	firstPop := map[string]string{"x86-64-linux": "popq %rdx", "arm64-linux": "ldr x2, [sp], #16"}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-O", "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		nl := condBranchBody(t, string(asm), "nl")
		for _, m := range stack[target].FindAllStringSubmatch(nl, -1) {
			if len(m) > 2 && m[2] == "%rbp" {
				continue
			}
			t.Errorf("%s: nl moves a value through the stack (%s):\n%s", target, strings.TrimSpace(m[0]), nl)
		}
		if stop := condBranchBody(t, string(asm), "stop"); strings.Contains(stop, firstPop[target]) {
			t.Errorf("%s: stop pops __scan_set's operands from the stack:\n%s", target, stop)
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
		if got := run.ProcessState.ExitCode(); got != 228 {
			t.Errorf("%s: exit %d, want 228 (the interpreter's)", tg.target, got)
		}
	}
}
