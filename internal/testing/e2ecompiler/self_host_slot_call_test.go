package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A call through a function value whose body address is read only by the
// call reads it out of the environment the call passes (ssa.slot_calls):
// `call *8(%rax)` on x86-64 once the moves have put the environment in %rax,
// and a load from x0 before the `blr` on arm64. `wide` passes more arguments
// than the pool holds, so the environment also rides the stack; `add` is a
// lambda with a capture, so its environment is a box of its own.
const slotCallProg = `@noinline function apply(f: (i32) => i32, x: i32): i32 { return f(x) + f(x + 1); }
@noinline function wide(f: (i32, i32, i32, i32, i32, i32, i32, i32, i32) => i32): i32 { return f(1, 2, 3, 4, 5, 6, 7, 8, 9); }
function inc(x: i32): i32 { return x + 1; }
function sum9(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32, i: i32): i32 { return a + b + c + d + e + f + g + h + i; }
function main(): i32 {
    let k: i32 = args().len() + 2;
    let add = (x: i32): i32 => x + k;
    return apply(inc, 3) + apply(add, 10) + wide(sum9);
}
`

func TestSelfHostSlotCall(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "slot.fern")
	if err := os.WriteFile(src, []byte(slotCallProg), 0o644); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"x86-64-linux": "call *8(%rax)", "arm64-linux": "ldr x6, [x0, #8]"}
	reg := map[string]string{"x86-64-linux": "call *%rdx", "arm64-linux": "mov x6, "}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-O", "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, fn := range []string{"apply", "wide"} {
			body := condBranchBody(t, string(asm), fn)
			if !strings.Contains(body, want[target]) {
				t.Errorf("%s: %s does not read its target out of the environment (%q):\n%s", target, fn, want[target], body)
			}
			if strings.Contains(body, reg[target]) {
				t.Errorf("%s: %s still reads its target into a register of its own:\n%s", target, fn, body)
			}
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
		// args() holds the program alone, so k is 3: 9 + 27 + 45.
		if got := run.ProcessState.ExitCode(); got != 81 {
			t.Errorf("%s: exit %d, want 81 (the interpreter's)", tg.target, got)
		}
	}
}
