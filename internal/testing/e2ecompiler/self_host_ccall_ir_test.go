package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostCCallIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	prog := `function run0(cb: usize): i32 { return __c_call0(cb) as i32; }
function run1(cb: usize, x: usize): i32 { return __c_call1(cb, x) as i32; }
function run2(cb: usize, a: usize, b: usize): i32 { return __c_call2(cb, a, b) as i32; }
function run3(cb: usize, a: usize, b: usize, c: usize): i32 { return __c_call3(cb, a, b, c) as i32; }
function run4(cb: usize, a: usize, b: usize, c: usize, d: usize): i32 { return __c_call4(cb, a, b, c, d) as i32; }
function main(): i32 { return run0(0 as usize) + run1(0 as usize, 1 as usize) + run2(0 as usize, 1 as usize, 2 as usize) + run3(0 as usize, 1 as usize, 2 as usize, 3 as usize) + run4(0 as usize, 1 as usize, 2 as usize, 3 as usize, 4 as usize); }`
	asm := string(runCapture(t, gcc, runner, driverBin, []byte(prog)))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}

	shimBody := func(n int) []string {
		want := []string{"movq 16(%rbp), %r11"}
		regs := []string{"%rdi", "%rsi", "%rdx", "%rcx"}
		for i := 0; i < n; i++ {
			want = append(want, "movq "+itoa(24+8*i)+"(%rbp), "+regs[i])
		}
		want = append(want, "andq $-16, %rsp", "call *%r11")
		return want
	}
	for n := 0; n <= 4; n++ {
		label := "__fn___c_call" + itoa(n) + ":"
		idx := strings.Index(asm, label)
		if idx < 0 {
			t.Errorf("arity %d: shim %s not emitted (IR path not taken / shim gap)", n, label)
			continue
		}
		body := asm[idx:]
		if e := strings.Index(body, "ret\n"); e >= 0 {
			body = body[:e]
		}
		for _, w := range shimBody(n) {
			if !strings.Contains(body, w) {
				t.Errorf("arity %d shim missing %q; body:\n%s", n, w, body)
			}
		}
	}

	_ = buildBin(t, gcc, dir, "ccall", asm)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestSelfHostCCallIRArm64(t *testing.T) {
	arm64gcc, _ := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	prog := `function run0(cb: usize): i32 { return __c_call0(cb) as i32; }
function run1(cb: usize, x: usize): i32 { return __c_call1(cb, x) as i32; }
function run4(cb: usize, a: usize, b: usize, c: usize, d: usize): i32 { return __c_call4(cb, a, b, c, d) as i32; }
function main(): i32 { return run0(0 as usize) + run1(0 as usize, 1 as usize) + run4(0 as usize, 1 as usize, 2 as usize, 3 as usize, 4 as usize); }`
	var cmd *exec.Cmd
	if len(x86runner) == 0 {
		cmd = exec.Command(driverBin, "-target", "arm64-linux")
	} else {
		cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64-linux")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(prog))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	txt := string(asm)
	shimBody := func(n int) []string {
		want := []string{"ldr x9, [x29, #16]"}
		regs := []string{"x0", "x1", "x2", "x3"}
		for i := 0; i < n; i++ {
			want = append(want, "ldr "+regs[i]+", [x29, #"+itoa(32+16*i)+"]")
		}
		want = append(want, "blr x9")
		return want
	}
	for _, n := range []int{0, 1, 4} {
		label := "__fn___c_call" + itoa(n) + ":"
		idx := strings.Index(txt, label)
		if idx < 0 {
			t.Errorf("arity %d: arm64 shim %s not emitted (IR path not taken / shim gap)", n, label)
			continue
		}
		body := txt[idx:]
		if e := strings.Index(body, "\n    ret\n"); e >= 0 {
			body = body[:e]
		}
		for _, w := range shimBody(n) {
			if !strings.Contains(body, w) {
				t.Errorf("arity %d arm64 shim missing %q; body:\n%s", n, w, body)
			}
		}
	}
	_ = buildBinArm64(t, arm64gcc, dir, "ccall_arm64", txt)
}
