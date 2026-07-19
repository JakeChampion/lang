package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// structTupleFieldIRCases widen the self-host IR subset: a struct field whose type
// is a nested tuple (`(i32, (i32, i32))`) — or a tuple carrying an Option/Result —
// now lowers on the IR path. Flat-tuple struct fields already lowered; the gate
// `is_leaksafe_tuple_field` rejected any element that wasn't a bare scalar/string,
// so a nested-tuple / Option / Result element bailed the whole struct (and thus
// the module) to AST. The fix recurses that gate on a nested-tuple element and
// accepts an Option/Result element (all leak-only one-pointer boxes); the field
// access `p.t.N.M` already typed correctly via the depth-aware `expr_tuple_elem_tag`.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir", and
// returns a value <= 126 (cf. the wasmtime exit-code gap #2908).
var structTupleFieldIRCases = []struct {
	name string
	main string
}{
	// Nested-tuple struct field, read the deep element.
	{"nested-deep", `struct P { t: (i32, (i32, i32)) } function main(): i32 { var p = P{t: (1, (2, 3))}; return p.t.1.1; }`},
	// Sum across the nesting boundary of a struct field.
	{"nested-sum", `struct P { t: (i32, (i32, i32)) } function main(): i32 { var p = P{t: (1, (2, 3))}; return p.t.0 + p.t.1.0 + p.t.1.1; }`},
	// A tuple struct field carrying an Option element.
	{"tuple-option", `struct P { t: (Option[i32], i32) } function main(): i32 { var p = P{t: (Some(5), 3)}; match (p.t.0) { Some(n) => { return n + p.t.1; }, None => { return 0; } } }`},
	// A tuple struct field carrying a Result element.
	{"tuple-result", `struct P { t: (Result[i32, string], i32) } function main(): i32 { var p = P{t: (Ok(5), 3)}; match (p.t.0) { Ok(n) => { return n + p.t.1; }, Err(e) => { return 0; } } }`},
	// Flat-tuple struct field regression (must stay on the IR path).
	{"flat-regress", `struct P { t: (i32, i32) } function main(): i32 { var p = P{t: (5, 6)}; return p.t.0 + p.t.1; }`},
	// A tuple struct field carrying a STRING-ARRAY element `(i32, string[])`: the
	// array is a heap pointer in one tuple slot, leaking with the (leak-only) tuple
	// field — the set tuple_elems_lowerable already admits for tuple construction /
	// return, now aligned in the struct-field admission gate. `p.t.1.len()` reads
	// the array length → 5 + 2 = 7.
	{"tuple-strarr", `struct P { t: (i32, string[]) } function main(): i32 { var p = P{t: (5, ["a", "bb"])}; return p.t.0 + p.t.1.len(); }`},
	// A SCALAR-array element `(i32, i32[])`, indexing an element too → 5 + 3 + 30 = 38.
	{"tuple-i32arr", `struct P { t: (i32, i32[]) } function main(): i32 { var p = P{t: (5, [10, 20, 30])}; return p.t.0 + p.t.1.len() + p.t.1[2]; }`},
	// Reading a STRING element OUT of the tuple field's string array — `p.t.1[1]`
	// is a string whose `.len()` must resolve → 5 + 3 = 8.
	{"tuple-strarr-elem", `struct P { t: (i32, string[]) } function main(): i32 { var p = P{t: (5, ["a", "bbb"])}; return p.t.0 + p.t.1[1].len(); }`},
	// An 8-byte f64-array element alongside a plain scalar field → 3 + 5 + 2 = 10.
	{"tuple-f64arr", `struct P { n: i32, t: (i32, f64[]) } function main(): i32 { var p = P{n: 3, t: (5, [1.0, 2.0])}; return p.n + p.t.0 + p.t.1.len(); }`},
}

// TestSelfHostStructTupleFieldIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostStructTupleFieldIRX86_64(t *testing.T) {
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

	for _, tc := range structTupleFieldIRCases {
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

// TestSelfHostStructTupleFieldIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostStructTupleFieldIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host struct-tuple-field wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range structTupleFieldIRCases {
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
			watFile := filepath.Join(dir, "struct_tuple_field_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("struct-tuple-field wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
