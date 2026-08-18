package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mapReclaimIRCases exercise the Perceus map-local reclaim helper
// (__fern_map_free / $__fern_map_release). Each `main` builds one or more FRESH,
// borrow-only (method-call receivers are borrows), non-escaping map locals that
// slot_is_reclaimable_map admits — so emit_map_buffers_free fires and frees the
// keys/values buffers + the mapbox at scope exit. The cases that build a SECOND
// map after the first goes dead stress the freelist: a double-free or corrupted
// mapbox from the reclaim would poison the recycled block and skew the result.
//
// This is the regression gate for the wasm landmine: before the __fern_map_free
// helper, emit_map_buffers_free emitted `op_raw_load_ptr`, which the wasm backend
// did not select, leaving the operand stack imbalanced so wasmtime rejected the
// module ("values remaining on stack at end of block"). The helper routes wasm to
// $__fern_map_release instead, so a reclaimable map local now compiles+runs on
// every backend — and the emit no longer has a comment fallback to slip through
// (#6917 / #6946), so a regression fails the driver rather than the run.
var mapReclaimIRCases = []struct {
	name string
	main string
	want int
}{
	// Basic i32-keyed borrow-only map: fresh, read via get_or, never reassigned
	// or returned -> reclaimable. 2 + 4 = 6.
	{"i32-borrow-only", `var m: Map[i32, i32] = Map { 1: 2, 3: 4 }; return m.get_or(1, 0) + m.get_or(3, 0);`, 6},
	// Two sequential reclaimable maps: the first is freed at its last use, the
	// second must allocate cleanly (possibly reusing the freed blocks). 5 + 9 = 14.
	{"two-sequential", `var a: Map[i32, i32] = Map { 1: 5 }; var x: i32 = a.get_or(1, 0); var b: Map[i32, i32] = Map { 2: 9 }; return x + b.get_or(2, 0);`, 14},
	// Grown map (past the initial cap of 8) then reclaimed: exercises the freed
	// keys/vals buffers being the grown (larger) allocations. sum 1..10 = 55.
	{"grown-then-reclaimed", `var m: Map[i32, i32] = Map {}; var i: i32 = 1; while (i <= 10) { m = m.insert(i, i); i = i + 1; } var s: i32 = 0; var j: i32 = 1; while (j <= 10) { s = s + m.get_or(j, 0); j = j + 1; } return s;`, 55},
}

func mapReclaimIRSrc(mainBody string) string {
	return "function main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostMapReclaimIRX86_64 routes each case through the self-hosted x86-64
// IR driver, pinned to the "ir" path, and checks the runtime result.
func TestSelfHostMapReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range mapReclaimIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(mapReclaimIRSrc(tc.main))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostMapReclaimIRWasm runs the same cases through the wasm IR backend —
// the backend the raw_load_ptr landmine broke. A reclaimable map local must now
// yield valid wat that wasmtime accepts and runs to the right exit code.
func TestSelfHostMapReclaimIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
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

	for _, tc := range mapReclaimIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(mapReclaimIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "mapreclaim_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("map-reclaim wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
