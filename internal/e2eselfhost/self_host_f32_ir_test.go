package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// f32IRCases exercise f32 scalar params / returns / locals through the self-host
// IR path on x86-64 + wasm. Fern represents f32 as an f64 internally (f32<->f64
// casts are no-ops and every float op runs at double width), so an f32 value is
// an 8-byte IEEE double at runtime. The bug these guard against: lower_func and
// f64_ret_fns_of only recognised the literal type name "f64", so an f32 param /
// return / local slipped through as a plain i32 slot — its 8-byte float bit
// pattern was then passed/returned/cast through the 4-byte integer path and
// miscompiled (e.g. `id32(5.5 as f32)` returned 0 instead of 5). The fix routes
// "f32"/"float" through the same 8-byte-float slot marking as "f64"
// (is_f64_scalar_type_name).
//
// These are the std/float f32-method shapes (abs/sqrt/floor/round via
// `__*_f64(x as f64) as f32`), written as free functions — value-RECEIVER f32
// methods still route through the AST path, so only the free-function surface is
// pinned to "ir" here.
//
// Each case casts its f32 result to i32 and returns a non-negative value kept
// <= 126 (the wasmtime exit-code truncation gap, cf. #2908), oracle-checked
// against the interpreter. FEATURE-AUDIT std/float row.
var f32IRCases = []struct {
	name string
	main string
}{
	// Pass-through param + return: the core "f32 slipped through as i32" bug.
	{"id", `function id32(x: f32): f32 { return x; }
function main(): i32 { var a: f32 = 5.5 as f32; return id32(a) as i32; }`},
	// f32 arithmetic across a call boundary.
	{"add", `function add32(a: f32, b: f32): f32 { return a + b; }
function main(): i32 { return add32(2.5 as f32, 3.0 as f32) as i32; }`},
	{"mul", `function mul32(a: f32, b: f32): f32 { return a * b; }
function main(): i32 { return mul32(2.0 as f32, 3.5 as f32) as i32; }`},
	// std/float f32-method shapes as free functions: __*_f64(x as f64) as f32.
	{"abs", `function fabs32(x: f32): f32 { return __abs_f64(x as f64) as f32; }
function main(): i32 { var a: f32 = 0.0 - 5.5 as f32; return fabs32(a) as i32; }`},
	{"sqrt", `function fsqrt32(x: f32): f32 { return __sqrt_f64(x as f64) as f32; }
function main(): i32 { return fsqrt32(16.0 as f32) as i32; }`},
	{"floor", `function ffloor32(x: f32): f32 { return __floor_f64(x as f64) as f32; }
function main(): i32 { return ffloor32(7.8 as f32) as i32; }`},
	{"round", `function fround32(x: f32): f32 { return __round_f64(x as f64) as f32; }
function main(): i32 { return fround32(2.5 as f32) as i32; }`},
	// f32 local round-trip feeding an intrinsic.
	{"via-local", `function main(): i32 { var y: f32 = 9.99 as f32; var f: f32 = __floor_f64(y as f64) as f32; return f as i32; }`},
	// f32 ARRAY: element load round-trips an 8-byte (f64-backed) f32 slot.
	{"array", `function main(): i32 { var a: f32[] = [1.0 as f32, 2.0 as f32, 3.0 as f32]; return a[1] as i32; }`},
	// f32 array summed in a loop — exercises repeated 8-byte element load + f32 add.
	{"array-sum", `function main(): i32 {
    var a: f32[] = [1.5 as f32, 2.5 as f32, 3.0 as f32];
    var s: f32 = 0.0 as f32; var i: i32 = 0;
    while (i < a.len()) { s = s + a[i]; i = i + 1; }
    return s as i32;
}`},
	// f32 STRUCT FIELDS: two f32 fields read back and added (8-byte field slots).
	{"struct-field", `struct P { x: f32, y: f32 }
function main(): i32 { var p: P = P { x: 3.0 as f32, y: 4.0 as f32 }; return (p.x + p.y) as i32; }`},
	// f32 in a TUPLE alongside an i32 — mixed-width tuple element layout.
	{"tuple", `function main(): i32 { var t: (f32, i32) = (3.5 as f32, 2); return (t.0 as i32) + t.1; }`},
	// f32 passed THROUGH a struct field into a call and back.
	{"struct-field-call", `struct P { v: f32 }
function dbl(p: P): f32 { return p.v + p.v; }
function main(): i32 { var p: P = P { v: 5.5 as f32 }; return dbl(p) as i32; }`},
	// u64 STRUCT FIELD with bits above 2^32: guards the SAME struct_make 8-byte
	// field store the f32 fix touches — before it, a u64 field stored via the
	// 4-byte i32.store path, truncating the high word, so the read-back != the
	// literal. 4294967297 == 0x1_0000_0001; a low-word-only store reads back 1.
	{"u64-struct-field", `struct B { v: u64 }
function main(): i32 { var b: B = B { v: 4294967297 as u64 }; if (b.v == 4294967297 as u64) { return 7; } return 0; }`},
}

// TestSelfHostF32IRX86_64 routes each case through the self-hosted x86-64 IR
// driver, oracle-checked, with routing pinned to the "ir" path.
func TestSelfHostF32IRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range f32IRCases {
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

// TestSelfHostF32IRWasm runs the same cases through the wasm IR backend.
func TestSelfHostF32IRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host f32 wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range f32IRCases {
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
			watFile := filepath.Join(dir, "f32_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("f32 wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostF32IRArm64 runs the same cases through the self-hosted arm64
// auto-decide driver (asm_ir_run.fern (-target arm64)), oracle-checked under qemu. The arm64
// IR path shares eligibility with x86 (the asmcore frontend is common), so these
// route IR there too; correctness is the gate. Mirrors TestSelfHostFloatArm64.
func TestSelfHostF32IRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range f32IRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			asm := runCapture(t, x86gcc, x86runner, driverBin, src, "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("f32 arm64 IR %q exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
