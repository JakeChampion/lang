package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #4375 item 1: the FFI __c_call<n> family (call a C-ABI function pointer with
// up to four integer/pointer args) now lowers on the self-hosted x86-64 IR path.
// Before this, a module using __c_call bailed to the AST emitter, which emitted
// `call __fn___c_call0` with no body — an undefined-reference link failure, so
// std/jni was uncompilable by the self-host. The shim (emit_ccall_shim1) is
// entered like any stack-ABI callee — the generic call_direct emit reverses the
// args so arg0/fn is on top (param0 @ 16(%rbp), a_i @ (24+8i)(%rbp)) — and
// marshals fn into %r11 + a0..a{n-1} into the System V arg registers, 16-aligns
// %rsp, and `call *%r11`s the pointer. Native's register-arg shim
// (TestSharedLibX86CCallFFI) verifies the C-ABI arg semantics with the same
// layout; this pins the self-host emission + that every arity assembles + links.
func TestSelfHostCCallIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	prog := `function run0(cb: usize): i32 { return __c_call0(cb) as i32; }
function run1(cb: usize, x: usize): i32 { return __c_call1(cb, x) as i32; }
function run2(cb: usize, a: usize, b: usize): i32 { return __c_call2(cb, a, b) as i32; }
function run3(cb: usize, a: usize, b: usize, c: usize): i32 { return __c_call3(cb, a, b, c) as i32; }
function run4(cb: usize, a: usize, b: usize, c: usize, d: usize): i32 { return __c_call4(cb, a, b, c, d) as i32; }
function main(): i32 { return 0; }`
	asm := string(runCapture(t, gcc, runner, driverBin, []byte(prog)))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}

	// The IR path must have been taken — the AST emitter (asm.fern) has no
	// __c_call shim, so its presence proves IR routing (eligibility fix).
	// Each shim marshals fn @ 16(%rbp) into %r11, then a0..a{n-1} from
	// (24+8i)(%rbp) into %rdi/%rsi/%rdx/%rcx, aligns, and `call *%r11`.
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

	// Assemble + link: the shims must resolve every referenced arity (this was
	// the undefined-reference failure before #4375).
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

// TestSelfHostCCallIRArm64 is the arm64 sibling: the __c_call<n> shims lower on
// the self-hosted arm64 IR path (emit_ccall_shims_arm64, emitted by
// asm_arm64.emit_runtime). The shim reads fn @ [x29,#16] and a_i @
// [x29,#(32+16i)] (16-byte rt-stack slots), marshals fn into x9 and a0..a{n-1}
// into the AAPCS64 arg registers x0..x3, and `blr x9`s the pointer.
func TestSelfHostCCallIRArm64(t *testing.T) {
	arm64gcc, _ := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern",
		"asm_ir_run.fern",
	} {
		s, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), s, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	prog := `function run0(cb: usize): i32 { return __c_call0(cb) as i32; }
function run1(cb: usize, x: usize): i32 { return __c_call1(cb, x) as i32; }
function run4(cb: usize, a: usize, b: usize, c: usize, d: usize): i32 { return __c_call4(cb, a, b, c, d) as i32; }
function main(): i32 { return 0; }`
	var cmd *exec.Cmd
	if len(x86runner) == 0 {
		cmd = exec.Command(driverBin, "-target", "arm64-linux", "-ir")
	} else {
		cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64-linux", "-ir")...)
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
	// Assemble + link with the aarch64 toolchain: the shims must resolve.
	_ = buildBinArm64(t, arm64gcc, dir, "ccall_arm64", txt)
}
