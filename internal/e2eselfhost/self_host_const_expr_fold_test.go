package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// constExprFoldCases exercise ir.fern's constant fold across a WIDTH
// NORMALISE — the int_cast / u32_wrap the lowering emits after every i32,
// u32 and sub-word arithmetic op to wrap the result back into its type.
//
// That op lands between the constant one fold round produces and the op the
// next round would consume, so `1 + 2 * 3` used to fold the `mul` and then
// stop, shipping a runtime add of two constants in every default build.
//
// The i64 / u64 rows are the refusal half: `as i64` lowers to int_extend,
// which CHANGES the width, and on wasm changes the value's type
// (i64.extend_i32_s). A fold that absorbed it would leave an i64 consumer
// reading an i32 — a module wasmtime rejects rather than mis-runs, which is
// why the wasm variant below is not redundant with the x86 one.
//
// Every case returns <= 120 (cf. the wasmtime exit-code gap #2908), and the
// interpreter is the oracle for what each must compute.
var constExprFoldCases = []struct {
	name string
	main string
}{
	{"nested-i32", `function main(): i32 { return 1 + 2 * 3; }`},
	{"nested-deeper", `function main(): i32 { return (1 + 2) * (3 + 4) - 4; }`},
	// The wrap the int_cast exists for: i32 arithmetic is 32-bit, so
	// 2147483647 + 1 is negative. Folding through the cast must reproduce
	// that rather than computing it wide.
	{"i32-wraps-at-32-bits", `function main(): i32 { var x: i32 = 2147483647 + 1; if (x < 0) { return 11; } return 1; }`},
	// `as u8` is a MASK, not an identity — the folded constant is v & 255.
	{"u8-masks", `function main(): i32 { var a: u8 = 255 as u8; var b: u8 = a + (1 as u8); return (b as i32) + 5; }`},
	{"u8-mul-masks", `function main(): i32 { var a: u8 = 16 as u8; var b: u8 = a * (17 as u8); return (b as i32) + 3; }`},
	// u32's max lowers as `const_i32 -1 ; u32_wrap`, and zero-extending -1 is
	// 2^32 - 1, which no i32 constant carries — so that pair must NOT fold.
	{"u32-max-survives", `function main(): i32 { var x: u32 = 4294967295 as u32; var y: u32 = x + (1 as u32); return (y as i32) + 7; }`},
	{"u32-wraps-at-32-bits", `function main(): i32 { var x: u32 = 4294967290 as u32; var y: u32 = x + (10 as u32); return (y as i32) + 13; }`},
	{"u32-positive-folds", `function main(): i32 { var x: u32 = (3 + 4) as u32; return (x as i32) * 2; }`},
	// int_extend / int_wrap refusals.
	{"i64-extend-refused", `function main(): i32 { var a: i64 = (1 + 2 * 3) as i64; return (a as i32) + 1; }`},
	{"u64-extend-refused", `function main(): i32 { var a: u64 = (1 + 2 * 3) as u64; return (a as i32) + 2; }`},
	{"i64-narrow-refused", `function main(): i32 { var a: i64 = 9 as i64; var b: i32 = a as i32; return b + 3; }`},
	// Shifts take the same wrap, and the shift count is masked mod 32.
	{"shl-folds", `function main(): i32 { return (1 << 3) + (2 << 2); }`},
}

func TestSelfHostConstExprFoldX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	for _, tc := range constExprFoldCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostConstExprFoldWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host const-expr fold wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern",
		"asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	for _, tc := range constExprFoldCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "constfold_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("const-expr fold wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostConstExprFoldShape pins what reaches the BACKEND, which the
// behavioural tests above cannot see: a program that computes the right answer
// with a runtime add of two constants passes every one of them.
//
// `folded` and `opaque` are the same expression, differing only in whether the
// leftmost operand is a literal or a parameter. The opaque twin is what makes
// the assertion non-vacuous — the mnemonics are absent from `folded` because
// the fold fired, not because the backend spells them some other way.
//
// Only the emitted text is checked, so neither qemu nor a linker is needed.
func TestSelfHostConstExprFoldShape(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "asm_ir_run")
	src := []byte("function folded(): i32 { return (1 + 2 * 3) * (4 + 5); }\n" +
		"function opaque(x: i32): i32 { return (x + 2 * 3) * (4 + 5); }\n" +
		"function main(): i32 { return folded() - opaque(1); }\n")

	for _, tc := range []struct {
		target  string
		extra   []string
		want    string   // the single constant the whole expression becomes
		arith   []string // what the opaque twin emits and the folded one must not
		backend string
	}{
		{
			target:  "x86-64-linux",
			want:    "movq $63, %rax",
			arith:   []string{"addq %rcx, %rax", "imulq %rcx, %rax"},
			backend: "asm_ir.fern",
		},
		{
			target:  "arm64-linux",
			extra:   []string{"-target", "arm64-linux"},
			want:    "mov x0, #63",
			arith:   []string{"add x0, x0, x1", "mul x0, x0, x1"},
			backend: "asm_arm64_ir.fern",
		},
	} {
		t.Run(tc.target, func(t *testing.T) {
			asm := string(runCapture(t, gcc, runner, driverBin, src, tc.extra...))
			folded := asmFuncBody(t, asm, "__fn_folded")
			opaque := asmFuncBody(t, asm, "__fn_opaque")
			if !strings.Contains(folded, tc.want) {
				t.Errorf("%s emitted no %q for `(1 + 2 * 3) * (4 + 5)`:\n%s", tc.backend, tc.want, folded)
			}
			for _, a := range tc.arith {
				if strings.Contains(folded, a) {
					t.Errorf("%s still emits %q for a wholly constant expression; the fold\n"+
						"is not seeing through the width normalise between the operands:\n%s", tc.backend, a, folded)
				}
				if !strings.Contains(opaque, a) {
					t.Errorf("%s emits no %q for the OPAQUE twin either, so the assertion\n"+
						"above proves nothing — update the expected mnemonic:\n%s", tc.backend, a, opaque)
				}
			}
		})
	}
}

// asmFuncBody returns the emitted text between `label:` and the first `ret`
// after it, so an assertion about one function cannot be satisfied (or
// defeated) by another's instructions.
func asmFuncBody(t *testing.T, asm, label string) string {
	t.Helper()
	start := strings.Index(asm, "\n"+label+":\n")
	if start < 0 {
		t.Fatalf("no %s: in emitted asm:\n%s", label, asm)
	}
	body := asm[start+1:]
	end := strings.Index(body, "ret\n")
	if end < 0 {
		t.Fatalf("no ret after %s: in emitted asm:\n%s", label, body)
	}
	return body[:end]
}
