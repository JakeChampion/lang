package e2ecompiler

import (
	"strings"
	"testing"
)

// A call with more arguments than the register count goes to the register
// entry: the first six (x86-64) or eight (arm64) arguments go in registers,
// and only the rest go on the stack, above a gap where the stack entry
// reads the others (#15749). The weights catch arguments out of order, and
// the strings at stack positions catch a count lost across the call.
const registerArgsSrc = `@noinline
function seven(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32): i32 {
    return a + 2 * b + 3 * c + 4 * d + 5 * e + 6 * f + 7 * g;
}

@noinline
function ten(a: i32, b: string, c: i32, d: i32, e: i32, f: i32, g: i32, h: string, i: i32, j: string): i32 {
    return a + 2 * b.len() + 3 * c + 4 * d + 5 * e + 6 * f + 7 * g + 8 * h.len() + 9 * i + 10 * j.len();
}

@noinline
function alt(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32, i: i32): i32 {
    return a - b + c - d + e - f + g - h + 9 * i;
}

@noinline
function call_seven(x: i32): i32 {
    return seven(x, x + 1, x + 2, x + 3, x + 4, x + 5, x + 6);
}

@noinline
function call_nine(x: i32): i32 {
    return alt(x, x + 1, x + 2, x + 3, x + 4, x + 5, x + 6, x + 7, x + 8);
}

@noinline
function call_ten(s: string, x: i32): i32 {
    let t: string = s + "!";
    return ten(x, t, x + 1, x + 2, x + 3, x + 4, x + 5, s + "??", x + 6, t + "...") * 2;
}

@noinline
function apply_nine(f: (i32, i32, i32, i32, i32, i32, i32, i32, i32) => i32, x: i32): i32 {
    return f(x, x + 1, x + 2, x + 3, x + 4, x + 5, x + 6, x + 7, x + 8);
}

@noinline
function word(n: i32): string {
    let w: string = "";
    let i: i32 = 0;
    while (i < n) {
        w = w + "a";
        i = i + 1;
    }
    return w;
}

function main(): i32 {
    if (call_seven(1) != 140) { return 1; }
    if (call_seven(5) != 252) { return 2; }
    if (call_nine(1) != 77) { return 3; }
    let s: string = word(5);
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        total = total + call_ten(s, 2);
        i = i + 1;
    }
    if (total != 50 * 734) { return 4; }
    if (apply_nine(alt, 1) != 77) { return 5; }
    let k: i32 = 3;
    let nine = (a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32, i: i32): i32 => a - b + c - d + e - f + g - h + i * k;
    if (apply_nine(nine, 1) != 23) { return 6; }
    return 42;
}
`

const registerArgsExit = 42

// registerArgsCall returns the instructions of fn's body from the last
// `reserve` before the first `call` to the line after the call, and fails
// when the body has no such call.
func registerArgsCall(t *testing.T, asm, fn, reserve, call string) []string {
	t.Helper()
	lines := strings.Split(leafFunction(t, asm, fn), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != call {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if strings.TrimSpace(lines[j]) == reserve {
				if i+1 < len(lines) {
					return lines[j : i+2]
				}
				return lines[j : i+1]
			}
		}
		t.Fatalf("%s: no %q ahead of %q:\n%s", fn, reserve, call, strings.Join(lines, "\n"))
	}
	t.Fatalf("%s: no %q:\n%s", fn, call, strings.Join(lines, "\n"))
	return nil
}

// checkRegisterArgsCall requires that nothing between the reserve and the
// call goes on the stack, and that the call gives back release bytes.
func checkRegisterArgsCall(t *testing.T, asm, fn, reserve, call, push, release string) {
	t.Helper()
	window := registerArgsCall(t, asm, fn, reserve, call)
	for _, line := range window[1 : len(window)-1] {
		if strings.Contains(line, push) {
			t.Errorf("%s: a register argument goes on the stack:\n%s", fn, strings.Join(window, "\n"))
		}
	}
	if got := strings.TrimSpace(window[len(window)-1]); got != release {
		t.Errorf("%s: the call gives back %q, want %q:\n%s", fn, got, release, strings.Join(window, "\n"))
	}
}

func TestSelfHostRegisterArgsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	asm := hevCompile(t, runner, driverBin, registerArgsSrc, nil)
	for _, stack := range []string{"call __fn_seven\n", "call __fn_alt\n", "call __fn_ten\n"} {
		if strings.Contains(asm, stack) {
			t.Errorf("a direct call goes to the stack entry: %q", strings.TrimSpace(stack))
		}
	}
	checkRegisterArgsCall(t, asm, "call_seven", "subq $48, %rsp", "call __fn_seven.r", "push", "addq $56, %rsp")
	checkRegisterArgsCall(t, asm, "call_nine", "subq $48, %rsp", "call __fn_alt.r", "push", "addq $72, %rsp")
	checkRegisterArgsCall(t, asm, "call_ten", "subq $48, %rsp", "call __fn_ten.r", "push", "addq $80, %rsp")
	if body := leafFunction(t, asm, "apply_nine"); !strings.Contains(body, "subq $48, %rsp") {
		t.Errorf("apply_nine: the indirect call pushes every argument:\n%s", body)
	}
	for _, mode := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
		probe := hevCompile(t, runner, driverBin, registerArgsSrc, []string{mode})
		bin := buildBin(t, gcc, dir, "register_args", probe)
		stderr, exit := hevRun(t, runner, bin)
		if exit != registerArgsExit {
			t.Fatalf("%s: exit = %d, want %d\n%s", mode, exit, registerArgsExit, stderr)
		}
		assertBalancedCensus(t, stderr)
	}
}

func TestSelfHostRegisterArgsArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")
	compile := func(env ...string) string {
		asm := string(runCaptureEnv(t, x86runner, driverBin, []byte(registerArgsSrc), append([]string{"PATH=/usr/bin:/bin"}, env...), "-target", "arm64-linux"))
		if len(asm) == 0 {
			t.Fatal("self-host arm64 compiler emitted 0 bytes")
		}
		return asm
	}
	asm := compile()
	for _, stack := range []string{"bl __fn_seven\n", "bl __fn_alt\n", "bl __fn_ten\n"} {
		if strings.Contains(asm, stack) {
			t.Errorf("a direct call goes to the stack entry: %q", strings.TrimSpace(stack))
		}
	}
	if body := leafFunction(t, asm, "call_seven"); !strings.Contains(body, "b __fn_seven.r\n") || strings.Contains(body, "sp, sp") {
		t.Errorf("call_seven: seven arguments do not all go in registers:\n%s", body)
	}
	checkRegisterArgsCall(t, asm, "call_nine", "sub sp, sp, #128", "bl __fn_alt.r", "[sp, #-16]!", "add sp, sp, #144")
	checkRegisterArgsCall(t, asm, "call_ten", "sub sp, sp, #128", "bl __fn_ten.r", "[sp, #-16]!", "add sp, sp, #160")
	if body := leafFunction(t, asm, "apply_nine"); !strings.Contains(body, "sub sp, sp, #128") {
		t.Errorf("apply_nine: the indirect call pushes every argument:\n%s", body)
	}
	for _, mode := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
		bin := buildBinArm64(t, arm64gcc, dir, "register_args_arm64", compile(mode))
		cmd := runArm64Bin(qemu, bin)
		var errBuf strings.Builder
		cmd.Stderr = &errBuf
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != registerArgsExit {
			t.Fatalf("%s: exit = %d, want %d\n%s", mode, code, registerArgsExit, errBuf.String())
		}
		assertBalancedCensus(t, errBuf.String())
	}
}
