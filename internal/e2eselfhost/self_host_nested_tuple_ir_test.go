package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nestedTupleIRCases widen the self-host IR subset: a tuple element that is itself
// a tuple — `(1, (2, 3))`, accessed `t.1.1` — now lowers on the IR path. Before,
// the tuple-element tag encoding split on commas, so any element whose own tag
// contained a comma (a nested tuple) was rejected and the whole module bailed.
// The fix makes the tag decoders (`tuple_elem_tag`, `csv_nth`)
// depth-aware — counting `(`/`[` … `)`/`]` so inner commas don't split the outer
// tag — adds an `ExprTuple` arm to `elem_type_tag` (a nested element gets its own
// `(t0,t1,…)` spelling), admits a tuple element at construction (it is a leak-only
// heap-tuple pointer, one slot like a struct/string/array element), and recovers
// the `t.N.M` element type via `expr_tuple_elem_tag`.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir", and
// returns a value <= 126 (cf. the wasmtime exit-code gap #2908).
var nestedTupleIRCases = []struct {
	name string
	main string
}{
	// Right-nested: element 1 is (i32, i32); read its element 1.
	{"right-nest", `function main(): i32 { var t: (i32, (i32, i32)) = (1, (2, 3)); return t.1.1; }`},
	// Sum across the nesting boundary.
	{"sum-across", `function main(): i32 { var t: (i32, (i32, i32)) = (1, (2, 3)); return t.0 + t.1.0 + t.1.1; }`},
	// Left-nested: element 0 is the inner tuple.
	{"left-nest", `function main(): i32 { var t: ((i32, i32), i32) = ((4, 5), 6); return t.0.0 + t.0.1 + t.1; }`},
	// Triple nesting: t.1.1.1.
	{"triple-nest", `function main(): i32 { var t: (i32, (i32, (i32, i32))) = (1, (2, (3, 4))); return t.1.1.1; }`},
	// A string inside a nested tuple (pointer element) round-trips.
	{"nest-with-string", `function main(): i32 { var t: (i32, (string, i32)) = (1, ("ab", 9)); return t.1.0.len() + t.1.1; }`},
	// An i64 sibling after a nested tuple element exercises the depth-aware kind
	// decode (the nested element must not shift the i64's store width).
	{"nest-then-i64", `function main(): i32 { var t: ((i32, i32), i64) = ((1, 2), 5000000000); return t.0.0 + t.0.1 + (t.1 / 1000000000) as i32; }`},
}

// TestSelfHostNestedTupleIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostNestedTupleIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range nestedTupleIRCases {
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

// TestSelfHostNestedTupleIRWasm runs the same cases through the wasm IR backend
// (whose per-element store width decode is the depth-aware-sensitive path).
func TestSelfHostNestedTupleIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host nested-tuple wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range nestedTupleIRCases {
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
			watFile := filepath.Join(dir, "nested_tuple_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("nested-tuple wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
