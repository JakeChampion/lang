package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// strengthIRCases exercise the strength-reduction peephole
// (examples/self_host/ir.fern's reduce_strength, #6638) end to end: each
// program's arithmetic hits a rewrite, and the interpreter is the oracle for
// what the rewritten op stream must still compute.
//
// The signed cases are the ones that matter. `-9 / 8` is -1 (rounds toward
// zero) where `-9 >> 3` is -2, and `-9 % 8` is -1 where `-9 & 7` is 7 — so a
// pass that reduced the signed forms would produce a different answer here.
// The unsigned high-bit case is the mirror: 4294967288 /u 8 is 536870911,
// which only comes out right if the rewrite selected shr_u over shr_s.
//
// Each case is routing-pinned to "ir" and returns a value <= 120 (cf. the
// wasmtime exit-code gap #2908).
var strengthIRCases = []struct {
	name string
	main string
}{
	{"mul-pow2", `function main(): i32 { var x: i32 = 5; return x * 8; }`},
	{"mul-pow2-chain", `function main(): i32 { var x: i32 = 3; return x * 4 * 2; }`},
	// `f() * 0` becomes `drop ; const_i32 0`: the drop is what keeps the stack
	// balanced after the call's result stops being consumed, so g()'s result
	// still lands in b rather than reading f()'s leftover.
	{"mul-zero-stack-balance", `function f(): i32 { return 3; }
function g(): i32 { return 7; }
function main(): i32 { var a: i32 = f() * 0; var b: i32 = g(); return a + b; }`},
	{"identities", `function main(): i32 { var x: i32 = 42; return (((x + 0) - 0) | 0) ^ 0; }`},
	{"and-all-bits", `function main(): i32 { var x: i32 = 42; return x & (0 - 1); }`},
	{"shift-by-zero", `function main(): i32 { var x: i32 = 42; return (x << 0) >> 0; }`},
	{"div-rem-one", `function main(): i32 { var x: i32 = 42; return (x / 1) + (x % 1); }`},
	{"signed-negative-div-rem", `function main(): i32 { var x: i32 = 0 - 9; return (0 - (x / 8)) + (0 - (x % 8)); }`},
	{"unsigned-div-pow2", `function main(): i32 { var x: u32 = 40 as u32; return (x / (8 as u32)) as i32; }`},
	{"unsigned-rem-pow2", `function main(): i32 { var x: u32 = 41 as u32; return (x % (8 as u32)) as i32; }`},
	{"unsigned-div-high-bit", `function main(): i32 { var x: u32 = 4294967288 as u32; return ((x / (8 as u32)) % (100 as u32)) as i32; }`},
	// The rewrites above hand the backends a shift whose count is a constant,
	// which asm_ir / asm_arm64_ir select an immediate-form shift for. These
	// pin that selection's masking: an i32 shift count is taken mod 32, so
	// `1 << 33` is 2 — folding the mask into the immediate has to reproduce
	// that. The variable-count case keeps the register form covered.
	{"const-shift-count-masked", `function main(): i32 { var x: i32 = 1; return x << 33; }`},
	{"const-shift-arith-right", `function main(): i32 { var x: i32 = 0 - 16; return 0 - (x >> 34); }`},
	{"const-shift-logical-right", `function main(): i32 { var x: u32 = 4294967232 as u32; return ((x >> (3 as u32)) % (100 as u32)) as i32; }`},
	{"variable-shift-count", `function main(): i32 { var x: i32 = 1; var n: i32 = 33; return x << n; }`},
}

func TestSelfHostStrengthIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")
	for _, tc := range strengthIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
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

// TestSelfHostStrengthShiftImmediateShape pins the instruction the two register
// backends actually select for the shift ir.reduce_strength hands them. The
// behavioural tests above pass either way — a masked variable-count shift
// computes the same answer — so without this the peephole could quietly stop
// firing and `x * 8` would be strength-reduced into something SLOWER than the
// imul/mul it replaced.
//
// Only the emitted text is checked, so neither qemu nor a linker is needed.
//
// The multiplicand is a PARAMETER, not a local bound to a literal. It used to be
// `var x: i32 = 5; return x * 8`, and #6638's copy propagation made that program
// stop exercising this assertion: dropping the dead tee let the fold see the
// constant, so the whole body collapses to `const_i32 40 ; return` with no shift
// to inspect. That is better code and still returns 40 — but it is not this
// test's subject. A parameter keeps the multiplicand opaque to the fold so the
// peephole's output is what actually reaches the backend.
func TestSelfHostStrengthShiftImmediateShape(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "asm_ir_run")
	src := []byte("function shift8(x: i32): i32 { return x * 8; }\nfunction main(): i32 { return shift8(5); }\n")

	for _, tc := range []struct {
		target  string
		want    string // the immediate-form shift the multiply must become
		unwant  []string
		extra   []string
		backend string
	}{
		{
			target:  "x86-64-linux",
			want:    "shlq $3, %rax",
			unwant:  []string{"imulq", "andl $31, %ecx"},
			backend: "asm_ir.fern",
		},
		{
			target:  "arm64-linux",
			want:    "lsl x0, x0, #3",
			unwant:  []string{"mul x0, x0, x1", "and x1, x1, #31"},
			extra:   []string{"-target", "arm64-linux"},
			backend: "asm_arm64_ir.fern",
		},
	} {
		t.Run(tc.target, func(t *testing.T) {
			asm := string(runCapture(t, gcc, runner, driverBin, src, tc.extra...))
			if !strings.Contains(asm, tc.want) {
				t.Errorf("%s emitted no %q for `x * 8`:\n%s", tc.backend, tc.want, asm)
			}
			for _, u := range tc.unwant {
				if strings.Contains(asm, u) {
					t.Errorf("%s still emits %q for `x * 8`; the strength rewrite\n"+
						"or the constant-shift-count selection is not firing:\n%s", tc.backend, u, asm)
				}
			}
		})
	}
}

func TestSelfHostStrengthIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host strength-reduction wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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
	for _, tc := range strengthIRCases {
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
			watFile := filepath.Join(dir, "strength_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("strength wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
