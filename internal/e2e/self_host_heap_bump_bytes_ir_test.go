package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// heapBumpBytesIRCases exercise the `__heap_bump_bytes()` introspection builtin
// — the bump allocator's high-water mark in bytes (cursor − region base; 0
// before the first allocation) — through the IR path. Before this slice the
// builtin had no self-host lowering and bailed the whole module to the legacy
// AST emitter (#3534); it now lowers to op_heap_bump_bytes, which each backend
// emits as an inline `cursor − base` read (x86-64 / arm64 read __fern_heap_ptr
// against the __fern_heap base symbol / __fern_heap_end − heap_size; wasm reads
// the $heap global minus heap_base).
//
// The interpreter has no bump-allocator model, so it can't be the oracle here
// (mirrors the native rc_heap_bump_* exit-code style): each program instead
// encodes a relational contract — the pre-alloc reading is 0, a reading taken
// after an allocation exceeds it, and successive readings are non-decreasing —
// as a fixed exit code (all <= 120 for the wasmtime exit-code gap #2908).
var heapBumpBytesIRCases = []struct {
	name string
	src  string
	exit int
}{
	// Before any allocation the cursor is still 0, so the reading is 0.
	{"before-zero", `function main(): i32 { var before: i32 = __heap_bump_bytes(); if (before == 0) { return 11; } return 1; }`, 11},
	// An allocation moves the cursor forward, so a reading taken after it
	// exceeds the pre-alloc reading (the issue's worked example).
	{"grows-after-alloc", `function main(): i32 { var before: i32 = __heap_bump_bytes(); var a: i32[] = [1, 2, 3, 4, 5]; var after: i32 = __heap_bump_bytes(); if (after > before && a[0] == 1) { return 7; } return 1; }`, 7},
	// The cursor only moves forward, so the high-water mark is non-decreasing
	// across successive allocations (and strictly above 0 once seeded).
	{"monotonic-nondecreasing", `function main(): i32 { var a: i32[] = [1, 2, 3]; var m1: i32 = __heap_bump_bytes(); var b: i32[] = [4, 5, 6, 7]; var m2: i32 = __heap_bump_bytes(); if (m1 > 0 && m2 >= m1 && a[0] + b[0] == 5) { return 9; } return 1; }`, 9},
}

// TestSelfHostHeapBumpBytesIRX86_64 proves each case (a) routes through the IR
// path (asm_pathprobe_run prints "ir", not "ast") and (b) compiles + runs to its
// expected exit code through the production x86-64 driver (asm_run, IR
// default-on).
func TestSelfHostHeapBumpBytesIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
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
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "probe")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range heapBumpBytesIRCases {
		t.Run(tc.name, func(t *testing.T) {
			// (a) path probe: the module must be fully IR-eligible.
			probe := runCapture(t, gcc, runner, probeBin, []byte(tc.src))
			if strings.TrimSpace(string(probe)) != "ir" {
				t.Fatalf("%s: path probe = %q, want \"ir\" (__heap_bump_bytes bailed the module to the AST path)", tc.name, probe)
			}
			// (b) compile + run through the IR-default driver.
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "hbb_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: exit %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestNativeHeapBumpBytesX86_64 is the native-backend half: the same programs
// compiled through the Go compiler's x86-64 emitter (OpCallDirect
// __fern_heap_bump_bytes) must produce the same exit codes the self-host
// lowering mirrors.
func TestNativeHeapBumpBytesX86_64(t *testing.T) {
	for _, tc := range heapBumpBytesIRCases {
		t.Run(tc.name, func(t *testing.T) {
			_, code := compileAndRunX86_64(t, tc.src+"\n")
			if code != tc.exit {
				t.Errorf("%s native exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostHeapBumpBytesIRArm64 runs the same cases through the arm64 IR
// backend under qemu (CI-gated). The arm64 op handler reads __fern_heap_ptr /
// __fern_heap_end and pins the pre-alloc reading to 0 with a csel, marking the
// heap need so asm_arm64.emit_runtime defines the heap symbols.
func TestSelfHostHeapBumpBytesIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64.fern", "asm_arm64_ir.fern",
		"asm_arm64_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_ir_run.fern", "driver")

	for _, tc := range heapBumpBytesIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "hbb_"+tc.name, string(asm))
			run := runArm64Bin(qemu, bin)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("heap-bump arm64 IR %q: exit %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostHeapBumpBytesIRWasm runs the same cases through the wasm IR
// backend. wasm reads the $heap global minus heap_base (computed from the
// module's string literals) — a distinct lowering from the register backends'
// __fern_heap_ptr read, so a per-backend regression in wasm_ir surfaces here.
func TestSelfHostHeapBumpBytesIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host heap-bump wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range heapBumpBytesIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			if !bytes.Contains(wat, []byte("global.get $heap")) {
				t.Fatalf("%s: emitted wat has no `global.get $heap` — __heap_bump_bytes did not lower through the wasm IR path", tc.name)
			}
			watFile := filepath.Join(dir, "heap_bump_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("heap-bump wasm IR %q = %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
