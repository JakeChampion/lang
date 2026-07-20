package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostArm64StrForeachAST pins the string-shaped `for b in STR` byte
// iteration on the arm64 AST backend. A string box is { data_ptr @0, len @8 }
// with byte elements, but the AST StmtFor emitter assumed the array layout
// (len @0, 8-byte elements @ base + (idx+1)*8) — so a string foreach read the
// header as data and produced garbage (#2822). The IR path desugars this in
// lower_string_foreach, but the AST path (which emits the runtime helpers via
// emit_runtime_fern_fn — e.g. __fern_str_to_i32's `for b in s`) did not, so
// str_to_i32("42") returned 0 on arm64 (TestSelfHostAsmArm64Bootstrap/
// str-to-i32-*).
//
// __fern_str_to_i32 is AST-emitted, so compiling a str_to_i32 program to
// aarch64 exercises the AST string-foreach. This asserts its emitted loop uses
// the string-shaped length (ldr from [x0, #8]) and a byte load (ldrb), not the
// array element load (ldr [x0, x1, lsl #3]). Pure emission check — no qemu.
func TestSelfHostArm64StrForeachAST(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("needs a native x86 host to run the aarch64-emitting driver")
	}
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mmc := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "mmc_arm64")

	cmd := exec.Command(mmc, "-target", "arm64")
	cmd.Stdin = strings.NewReader("function main(): i32 { return str_to_i32(\"42\"); }\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("self-host arm64 emit failed: %v", err)
	}
	body := arm64FnBody(t, string(out), "__fn___fern_str_to_i32")
	if body == "" {
		t.Fatal("__fern_str_to_i32 helper not emitted")
	}
	if !strings.Contains(body, "ldr x1, [x0, #8]") {
		t.Errorf("str foreach did not load the string length from [x0, #8]; body:\n%s", body)
	}
	if !strings.Contains(body, "ldrb") {
		t.Errorf("str foreach did not byte-load elements (ldrb); body:\n%s", body)
	}
	if strings.Contains(body, "ldr x0, [x0, x1, lsl #3]") {
		t.Errorf("str foreach still uses the 8-byte array element load; body:\n%s", body)
	}
}
