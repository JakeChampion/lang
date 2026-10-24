package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

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
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
@noinline function d10uw(x: u64): u64 { return x / 10u64; }
@noinline function mtw(x: i64): i64 { return x % 1000000000000i64; }
@noinline function d100uw(x: u64): u64 { return x / 100u64; }
@noinline function m14u(x: u32): u32 { return x % 14u32; }
@noinline function dm100uw(x: u64): u64 { return x / 100u64 + x % 100u64; }
@noinline function dmxyuw(x: u64, y: u64): u64 { return x / y + x % y; }
@noinline function dmxyb(x: i32, y: i32): i32 { let r: i32 = x % y; if (r > 3) { return x / y; } return r; }
function main(): i32 {
  return m4093(9000) + d7(50) + d8(-17) + m8(-17) + (d3u(20u32) as i32) + (m7u(20u32) as i32) + dxy(9, 2)
    + (mxyu(9u32, 2u32) as i32) + (m97w(200i64) as i32) + (d8w(-9i64) as i32) + (mbig(5i64) as i32) + dneg1(5)
    + (d10uw(70u64) as i32) + (mtw(5i64) as i32) + (d100uw(700u64) as i32) + (m14u(30u32) as i32) + (dm100uw(705u64) as i32)
    + (dmxyuw(705u64, 9u64) as i32) + dmxyb(705, 9);
}
`
	asm := selfHostX86Emit(t, runner, driverBin, src)

	guards := []string{"testq %rcx", "testl %ecx", "cmpq $-1", "cmpl $-1", "movslq", "jz ", "je "}
	cases := []struct {
		fn          string
		want, avoid []string
	}{
		{"m4093", []string{"movl %eax, %ecx", "movl $-2145909631, %eax", "imull %ecx", "addl %ecx, %edx", "sarl $11, %edx", "imull $4093, %eax, %eax", "cltq"}, append([]string{"idiv"}, guards...)},
		{"d7", []string{"movl $-1840700269, %eax", "imull %ecx", "shrl $31, %edx"}, append([]string{"idiv", "imull $7"}, guards...)},
		{"d8", []string{"sarl $31, %ecx", "shrl $29, %ecx", "addl %eax, %ecx", "sarl $3, %eax"}, append([]string{"idiv", "imul"}, guards...)},
		{"m8", []string{"andl $-8, %ecx", "subl %ecx, %eax"}, append([]string{"idiv"}, guards...)},
		{"d3u", []string{"movl $-1431655765, %eax", "mull %ecx", "shrl $1, %edx"}, append([]string{"div ", "divl", "divq"}, guards...)},
		{"m7u", []string{"mull %ecx", "subl %edx, %eax", "shrl $1, %eax", "addl %edx, %eax", "shrl $2, %eax", "imull $7, %eax, %eax"}, append([]string{"divl", "divq"}, guards...)},
		{"m97w", []string{"movq %rax, %rcx", "movabsq $-6275696437447579415, %rax", "imulq %rcx", "addq %rcx, %rdx", "sarq $6, %rdx", "shrq $63, %rdx", "imulq $97, %rax, %rax"}, append([]string{"idiv", "cqto"}, guards...)},
		{"d10uw", []string{"movabsq $-3689348814741910323, %rax", "mulq %rcx", "shrq $3, %rdx"}, append([]string{"divq", "idiv"}, guards...)},
		{"d100uw", []string{"shrq $2, %rdx", "movabsq $2951479051793528259, %rax", "mulq %rdx"}, append([]string{"divq", "subq %rdx, %rax", "shrq $1, %rax"}, guards...)},
		{"m14u", []string{"shrl $1, %edx", "mull %edx", "imull $14, %eax, %eax"}, append([]string{"divl", "subl %edx, %eax", "shrl $1, %eax"}, guards...)},
		{"dm100uw", []string{"mulq %rdx", "imulq $100"}, append([]string{"divq"}, guards...)},
		{"mtw", []string{"movabsq $2535301200456458803, %rax", "sarq $37, %rdx", "movabsq $1000000000000, %rdx", "imulq %rdx, %rax"}, append([]string{"idiv"}, guards...)},
		{"d8w", []string{"sarq $63, %rcx", "shrq $61, %rcx", "sarq $3, %rax"}, append([]string{"idiv"}, guards...)},
		{"mbig", []string{"shrq $40, %rcx", "shlq $40, %rcx"}, append([]string{"idiv", "andq $-1099511627776"}, guards...)},
		{"dneg1", []string{"negl %eax", "cltq"}, append([]string{"idiv"}, guards...)},
		{"dxy", []string{"testl %ecx, %ecx", "cmpl $-1, %ecx", "cltd", "idivl %ecx", "negl %eax", "cltq"}, []string{"movslq", "idivq", "cqto", "testq", "cmpq"}},
		{"mxyu", []string{"testl %ecx, %ecx", "xorl %edx, %edx", "divl %ecx", "movl %edx, %eax"}, []string{"movslq", "divq", "cmpl", "cltq"}},
	}
	if n := strings.Count(selfHostFnBody(t, asm, "dm100uw"), " mulq "); n != 1 {
		t.Errorf("dm100uw multiplies %d times, want 1: the remainder reuses the quotient", n)
	}
	if n := strings.Count(selfHostFnBody(t, asm, "dmxyuw"), "divq"); n != 1 {
		t.Errorf("dmxyuw divides %d times, want 1: the remainder reuses the quotient", n)
	}
	if n := strings.Count(selfHostFnBody(t, asm, "dmxyb"), "idivl"); n != 1 {
		t.Errorf("dmxyb divides %d times, want 1: the division reuses the remainder's quotient", n)
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

var constDivisorOracleCases = []struct {
	name string
	main string
}{
	{"int-min-by-neg1", `function main(): i32 { let xs: i32[] = [-2147483648]; let x: i32 = xs[0]; if (x / -1 == -2147483648 && x % -1 == 0) { return 5; } return 9; }`},
	{"neg-by-pow2", `function main(): i32 { let xs: i32[] = [-17]; let x: i32 = xs[0]; return (x / 8 + 10) * 10 + (x % 8 + 5); }`},
	{"int-min-by-pow2", `function main(): i32 { let xs: i32[] = [-2147483648]; let x: i32 = xs[0]; if (x / 1024 == -2097152 && x % 1024 == 0 && x / -1024 == 2097152) { return 5; } return 9; }`},
	{"reciprocal", `function main(): i32 { let xs: i32[] = [9000, -9000, -2147483648, 2147483647]; let h: i32 = 0; let i: i32 = 0; while (i < 4) { let x: i32 = xs[i]; h = h * 31 + x / 4093 + x % 4093 + x / -7 + x % -7 + x / 3; i = i + 1; } if (h < 0) { h = 0 - h; } return h % 200; }`},
	{"u32-highbit", `function main(): i32 { let us: u32[] = [3000000000u32, 4294967295u32]; let h: u32 = 0u32; let i: i32 = 0; while (i < 2) { let u: u32 = us[i]; h = h * 31u32 + u / 7u32 + u % 7u32 + u / 3u32 + u % 3u32 + u / 1024u32 + u % 1024u32; i = i + 1; } return (h % 200u32) as i32; }`},
	{"i64-wide", `function main(): i32 { let ws: i64[] = [-9223372036854775808i64, -7i64, 1099511627783i64]; let h: i64 = 0i64; let i: i32 = 0; while (i < 3) { let w: i64 = ws[i]; h = h * 31i64 + w / 97i64 + w % 97i64 + w / 1099511627776i64 + w % 1099511627776i64 + w / -1099511627776i64; i = i + 1; } if (h < 0i64) { h = 0i64 - h; } return (h % 200i64) as i32; }`},
	{"u64-reciprocal", `function main(): i32 { let us: u64[] = [18446744073709551615u64, 9223372036854775808u64, 12345678901234567u64]; let h: u64 = 0u64; let i: i32 = 0; while (i < 3) { let u: u64 = us[i]; h = h * 31u64 + u / 7u64 + u % 7u64 + u / 10u64 + u % 10u64 + u / 100u64; i = i + 1; } return (h % 200u64) as i32; }`},
	{"even-preshift", `function main(): i32 { let us: u64[] = [18446744073709551615u64, 18446744073709551516u64, 9223372036854775899u64, 99u64]; let vs: u32[] = [4294967295u32, 4294967282u32, 13u32]; let h: u64 = 0u64; let i: i32 = 0; while (i < 4) { let u: u64 = us[i]; h = h * 31u64 + u / 100u64 + u % 100u64 + u / 6u64 + u % 6u64; i = i + 1; } i = 0; while (i < 3) { let v: u32 = vs[i]; h = h * 31u64 + (v / 14u32 + v % 14u32) as u64; i = i + 1; } return (h % 200u64) as i32; }`},
	{"i64-wide-reciprocal", `function main(): i32 { let ws: i64[] = [9223372036854775807i64, -9223372036854775808i64, -1234567890123i64]; let h: i64 = 0i64; let i: i32 = 0; while (i < 3) { let w: i64 = ws[i]; h = h * 31i64 + w / 1000000000000i64 + w % 1000000000000i64 + w / -7i64 + w % -7i64 + w / 10i64; i = i + 1; } if (h < 0i64) { h = 0i64 - h; } return (h % 200i64) as i32; }`},
	{"degenerate", `function main(): i32 { let xs: i32[] = [-17]; let x: i32 = xs[0]; if (x / 0 == 0 && x % 0 == -17 && x / 1 == -17 && x % 1 == 0) { return 5; } return 9; }`},
	{"dynamic-int-min", `function main(): i32 { let xs: i32[] = [-2147483648, -1, 0, 7]; let a: i32 = xs[0]; let b: i32 = xs[1]; let z: i32 = xs[2]; let s: i32 = xs[3]; if (a / b == -2147483648 && a % b == 0 && s / z == 0 && s % z == 7 && a / s == -306783378 && a % s == -2) { return 5; } return 9; }`},
	{"dynamic-u32-highbit", `function main(): i32 { let us: u32[] = [3000000003u32, 10u32, 0u32]; let u: u32 = us[0]; let d: u32 = us[1]; let z: u32 = us[2]; if (u / d == 300000000u32 && u % d == 3u32 && u / z == 0u32 && u % z == u) { return 5; } return 9; }`},
}

func TestSelfHostConstDivisorRuntimeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
