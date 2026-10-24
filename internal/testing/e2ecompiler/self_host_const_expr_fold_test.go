package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"
)

var constExprFoldCases = []struct {
	name string
	main string
}{
	{"nested-i32", `function main(): i32 { return 1 + 2 * 3; }`},
	{"nested-deeper", `function main(): i32 { return (1 + 2) * (3 + 4) - 4; }`},
	{"i32-wraps-at-32-bits", `function main(): i32 { let x: i32 = 2147483647 + 1; if (x < 0) { return 11; } return 1; }`},
	{"u8-masks", `function main(): i32 { let a: u8 = 255 as u8; let b: u8 = a + (1 as u8); return (b as i32) + 5; }`},
	{"u8-mul-masks", `function main(): i32 { let a: u8 = 16 as u8; let b: u8 = a * (17 as u8); return (b as i32) + 3; }`},
	{"u32-max-survives", `function main(): i32 { let x: u32 = 4294967295 as u32; let y: u32 = x + (1 as u32); return (y as i32) + 7; }`},
	{"u32-wraps-at-32-bits", `function main(): i32 { let x: u32 = 4294967290 as u32; let y: u32 = x + (10 as u32); return (y as i32) + 13; }`},
	{"u32-positive-folds", `function main(): i32 { let x: u32 = (3 + 4) as u32; return (x as i32) * 2; }`},
	{"i64-extend-refused", `function main(): i32 { let a: i64 = (1 + 2 * 3) as i64; return (a as i32) + 1; }`},
	{"u64-extend-refused", `function main(): i32 { let a: u64 = (1 + 2 * 3) as u64; return (a as i32) + 2; }`},
	{"i64-narrow-refused", `function main(): i32 { let a: i64 = 9 as i64; let b: i32 = a as i32; return b + 3; }`},
	{"shl-folds", `function main(): i32 { return (1 << 3) + (2 << 2); }`},
}

func TestSelfHostConstExprFoldX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
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
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range constExprFoldCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			want := interpExit(t, interpBin, src)
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("%s on %s exited %d, want %d (interp oracle)\n%s", tc.name, target, code, want, stderr)
				}
			}
		})
	}
}

func TestSelfHostConstExprFoldShape(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "asm_ir_run")
	src := []byte("@noinline function folded(): i32 { return (1 + 2 * 3) * (4 + 5); }\n" +
		"@noinline function opaque(x: i32): i32 { return (x + 2 * 3) * (4 + 5); }\n" +
		"function main(): i32 { return folded() - opaque(1); }\n")

	for _, tc := range []struct {
		target  string
		extra   []string
		want    string
		arith   []string
		backend string
	}{
		{
			target:  "x86-64-linux",
			want:    `movl \$63, %e[a-z0-9]+`,
			arith:   []string{`addq \$6, %r[a-z0-9]+`, `imulq \$9, %r[a-z0-9]+, %r[a-z0-9]+`},
			backend: "asm_ir.fern",
		},
		{
			target:  "arm64-linux",
			extra:   []string{"-target", "arm64-linux"},
			want:    `mov x[0-9]+, #63`,
			arith:   []string{`add x[0-9]+, x[0-9]+, #6`, `mul x[0-9]+, x[0-9]+, x[0-9]+`},
			backend: "asm_arm64_ir.fern",
		},
	} {
		t.Run(tc.target, func(t *testing.T) {
			asm := string(runCapture(t, gcc, runner, driverBin, src, tc.extra...))
			folded := asmFuncBody(t, asm, "__fn_folded")
			opaque := asmFuncBody(t, asm, "__fn_opaque")
			if !matchShape(folded, tc.want) {
				t.Errorf("%s emitted no %q for `(1 + 2 * 3) * (4 + 5)`:\n%s", tc.backend, tc.want, folded)
			}
			for _, a := range tc.arith {
				if matchShape(folded, a) {
					t.Errorf("%s still emits %q for a wholly constant expression; the fold\n"+
						"is not seeing through the width normalise between the operands:\n%s", tc.backend, a, folded)
				}
				if !matchShape(opaque, a) {
					t.Errorf("%s emits no %q for the OPAQUE twin either, so the assertion\n"+
						"above proves nothing — update the expected mnemonic:\n%s", tc.backend, a, opaque)
				}
			}
		})
	}
}

func asmFuncBody(t *testing.T, asm, label string) string {
	t.Helper()
	start := strings.Index(asm, "\n"+label+":\n")
	if start < 0 {
		t.Fatalf("no %s: in emitted asm:\n%s", label, asm)
	}
	body := asm[start+1:]
	end := strings.Index(body, "ret\n")
	unwind := strings.Index(body, ".cfi_endproc")
	if unwind >= 0 && (end < 0 || unwind < end) {
		end = unwind
	}
	if end < 0 {
		t.Fatalf("no return or unwind boundary after %s: in emitted asm:\n%s", label, body)
	}
	return body[:end]
}
