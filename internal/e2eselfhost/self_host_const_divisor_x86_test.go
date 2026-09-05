package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// The self-host x86-64 emitter's lowering of division by a literal divisor
// (asm_ir.fern's ir_div_const, the twin of native's emitConstDivRem), and the
// dword forms its dynamic-divisor sequence now uses at i32 width.
//
// Semantics are pinned by conformance/cases/const_divisor_runtime on every
// self-host leg. What is checked here is the SHAPE — that the emitter took the
// specialised path and dropped the guards — since a lowering that quietly
// stopped firing would still compute the right answers, plus an interp-oracled
// run over the dividends the shapes are subtle for.

// selfHostX86Emit runs a program through the asm_ir_run driver and returns
// the emitted asm.
func selfHostX86Emit(t *testing.T, runner []string, driverBin, src string) []byte {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	return out
}

// selfHostFnBody slices one function's body out of the self-host emitter's
// AT&T output: from its label to its .cfi_endproc.
func selfHostFnBody(t *testing.T, asm []byte, fn string) string {
	t.Helper()
	s := string(asm)
	start := strings.Index(s, "\n__fn_"+fn+":\n")
	if start < 0 {
		t.Fatalf("__fn_%s not found in emitted asm", fn)
	}
	rest := s[start+1:]
	end := strings.Index(rest, ".cfi_endproc")
	if end < 0 {
		t.Fatalf("__fn_%s has no .cfi_endproc", fn)
	}
	return rest[:end]
}

func TestSelfHostConstDivisorShapesX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const src = `@noinline function m4093(x: i32): i32 { return x % 4093; }
@noinline function d7(x: i32): i32 { return x / 7; }
@noinline function d8(x: i32): i32 { return x / 8; }
@noinline function m8(x: i32): i32 { return x % 8; }
@noinline function d3u(x: u32): u32 { return x / 3u32; }
@noinline function m7u(x: u32): u32 { return x % 7u32; }
@noinline function dxy(x: i32, y: i32): i32 { return x / y; }
@noinline function mxyu(x: u32, y: u32): u32 { return x % y; }
@noinline function m97w(x: i64): i64 { return x % 97i64; }
@noinline function d8w(x: i64): i64 { return x / 8i64; }
@noinline function mbig(x: i64): i64 { return x % 1099511627776i64; }
@noinline function dneg1(x: i32): i32 { return x / -1; }
function main(): i32 {
  return m4093(9000) + d7(50) + d8(-17) + m8(-17) + (d3u(20u32) as i32) + (m7u(20u32) as i32) + dxy(9, 2)
    + (mxyu(9u32, 2u32) as i32) + (m97w(200i64) as i32) + (d8w(-9i64) as i32) + (mbig(5i64) as i32) + dneg1(5);
}
`
	asm := selfHostX86Emit(t, runner, driverBin, src)

	// The guards and the widening are what every literal-divisor shape must
	// have shed; each case then names the instruction that replaced the divide.
	guards := []string{"testq %rcx", "testl %ecx", "cmpq $-1", "cmpl $-1", "movslq", "jz ", "je "}
	cases := []struct {
		fn          string
		want, avoid []string
	}{
		// i32 reciprocal: the magic (ir.derive_magic_s32(4093) — also pins the
		// derivation), the dividend stashed in ecx, the multiply-back for the
		// remainder, and the sign-extension back into the slot.
		{"m4093", []string{"movl %eax, %ecx", "movl $-2145909631, %eax", "imull %ecx", "addl %ecx, %edx", "sarl $11, %edx", "imull $4093, %eax, %eax", "cltq"}, append([]string{"idiv"}, guards...)},
		{"d7", []string{"movl $-1840700269, %eax", "imull %ecx", "shrl $31, %edx"}, append([]string{"idiv", "imull $7"}, guards...)},
		// Signed power of two: the round-toward-zero bias, then the shift.
		{"d8", []string{"sarl $31, %ecx", "shrl $29, %ecx", "addl %eax, %ecx", "sarl $3, %eax"}, append([]string{"idiv", "imul"}, guards...)},
		{"m8", []string{"andl $-8, %ecx", "subl %ecx, %eax"}, append([]string{"idiv"}, guards...)},
		// Unsigned reciprocal: 2863311531 written as its imm32 bit pattern,
		// and the 33-bit magic's shift-average for 7.
		{"d3u", []string{"movl $-1431655765, %eax", "mull %ecx", "shrl $1, %edx"}, append([]string{"div ", "divl", "divq"}, guards...)},
		{"m7u", []string{"mull %ecx", "subl %edx, %eax", "shrl $1, %eax", "addl %edx, %eax", "shrl $2, %eax", "imull $7, %eax, %eax"}, append([]string{"divl", "divq"}, guards...)},
		// i64 keeps its divide but loses both guards and all four labels.
		{"m97w", []string{"movabsq $97, %rcx", "cqto", "idivq %rcx", "movq %rdx, %rax"}, guards},
		{"d8w", []string{"sarq $63, %rcx", "shrq $61, %rcx", "sarq $3, %rax"}, append([]string{"idiv"}, guards...)},
		// A mask past bit 31 is a shift pair, never an immediate gas refuses.
		{"mbig", []string{"shrq $40, %rcx", "shlq $40, %rcx"}, append([]string{"idiv", "andq $-1099511627776"}, guards...)},
		{"dneg1", []string{"negl %eax", "cltq"}, append([]string{"idiv"}, guards...)},
		// (The most negative divisor is not reachable as a literal here: the
		// self-host lowers -2147483648 as `0 - 2147483648`, which is not one
		// readable constant, so it keeps the guarded path below.)
		// A divisor only known at run time keeps both guards, on the dword
		// forms: nothing is widened, and the result is sign-extended once.
		{"dxy", []string{"testl %ecx, %ecx", "cmpl $-1, %ecx", "cltd", "idivl %ecx", "negl %eax", "cltq"}, []string{"movslq", "idivq", "cqto", "testq", "cmpq"}},
		{"mxyu", []string{"testl %ecx, %ecx", "xorl %edx, %edx", "divl %ecx", "movl %edx, %eax"}, []string{"movslq", "divq", "cmpl", "cltq"}},
	}
	for _, c := range cases {
		t.Run(c.fn, func(t *testing.T) {
			body := selfHostFnBody(t, asm, c.fn)
			for _, w := range c.want {
				if !strings.Contains(body, w) {
					t.Errorf("expected %q:\n%s", w, body)
				}
			}
			for _, a := range c.avoid {
				if strings.Contains(body, a) {
					t.Errorf("%q should be gone:\n%s", a, body)
				}
			}
		})
	}
}

// constDivisorOracleCases are the dividends the literal-divisor shapes are
// subtle for, each reduced to a small exit code and oracled against interp.
// Every dividend is read out of an array so the folder cannot see it.
var constDivisorOracleCases = []struct {
	name string
	main string
}{
	// INT_MIN / -1 wraps to INT_MIN and INT_MIN % -1 is 0, through `neg`
	// and the zeroing arm rather than a guard.
	{"int-min-by-neg1", `function main(): i32 { var xs: i32[] = [-2147483648]; var x: i32 = xs[0]; if (x / -1 == -2147483648 && x % -1 == 0) { return 5; } return 9; }`},
	// The biased shift: -17 / 8 is -2 (not -3) and -17 % 8 is -1 (not 7).
	{"neg-by-pow2", `function main(): i32 { var xs: i32[] = [-17]; var x: i32 = xs[0]; return (x / 8 + 10) * 10 + (x % 8 + 5); }`},
	{"int-min-by-pow2", `function main(): i32 { var xs: i32[] = [-2147483648]; var x: i32 = xs[0]; if (x / 1024 == -2097152 && x % 1024 == 0 && x / -1024 == 2097152) { return 5; } return 9; }`},
	// The reciprocal on both signs, INT_MIN and INT_MAX, and a negative divisor.
	{"reciprocal", `function main(): i32 { var xs: i32[] = [9000, -9000, -2147483648, 2147483647]; var h: i32 = 0; var i: i32 = 0; while (i < 4) { var x: i32 = xs[i]; h = h * 31 + x / 4093 + x % 4093 + x / -7 + x % -7 + x / 3; i = i + 1; } if (h < 0) { h = 0 - h; } return h % 200; }`},
	// u32 above 2^31 through the 33-bit magic (7) and the plain one (3).
	{"u32-highbit", `function main(): i32 { var us: u32[] = [3000000000u32, 4294967295u32]; var h: u32 = 0u32; var i: i32 = 0; while (i < 2) { var u: u32 = us[i]; h = h * 31u32 + u / 7u32 + u % 7u32 + u / 3u32 + u % 3u32 + u / 1024u32 + u % 1024u32; i = i + 1; } return (h % 200u32) as i32; }`},
	// i64: the unguarded divide, and a power-of-two mask past bit 31.
	{"i64-wide", `function main(): i32 { var ws: i64[] = [-9223372036854775808i64, -7i64, 1099511627783i64]; var h: i64 = 0i64; var i: i32 = 0; while (i < 3) { var w: i64 = ws[i]; h = h * 31i64 + w / 97i64 + w % 97i64 + w / 1099511627776i64 + w % 1099511627776i64 + w / -1099511627776i64; i = i + 1; } if (h < 0i64) { h = 0i64 - h; } return (h % 200i64) as i32; }`},
	// Zero and one literals, which need no arithmetic at all.
	{"degenerate", `function main(): i32 { var xs: i32[] = [-17]; var x: i32 = xs[0]; if (x / 0 == 0 && x % 0 == -17 && x / 1 == -17 && x % 1 == 0) { return 5; } return 9; }`},
	// The dynamic path on its dword forms: INT_MIN and -1 from arrays.
	{"dynamic-int-min", `function main(): i32 { var xs: i32[] = [-2147483648, -1, 0, 7]; var a: i32 = xs[0]; var b: i32 = xs[1]; var z: i32 = xs[2]; var s: i32 = xs[3]; if (a / b == -2147483648 && a % b == 0 && s / z == 0 && s % z == 7 && a / s == -306783378 && a % s == -2) { return 5; } return 9; }`},
	{"dynamic-u32-highbit", `function main(): i32 { var us: u32[] = [3000000003u32, 10u32, 0u32]; var u: u32 = us[0]; var d: u32 = us[1]; var z: u32 = us[2]; if (u / d == 300000000u32 && u % d == 3u32 && u / z == 0u32 && u % z == u) { return 5; } return 9; }`},
}

func TestSelfHostConstDivisorRuntimeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range constDivisorOracleCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			want := interpExit(t, interpBin, src)
			asm := selfHostX86Emit(t, runner, driverBin, src)
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("%s did not exit normally", tc.name)
			}
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
