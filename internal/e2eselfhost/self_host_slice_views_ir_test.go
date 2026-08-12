package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sliceViewIRCases exercise slice views `[T]` — a borrowed window `a[i:j]` over
// an owned array — through the self-host IR path on x86-64 + wasm. A slice is a
// leak-only (fat-pointer) view: `.len()`, element indexing (`s[k]`), `for x in
// s` iteration, slice-of-slice (`s[a:b]`), empty windows, and `[string]`
// element slices all stay on the IR path.
//
// This pins the foundational "Slice views `[T]`" audit row (docs/FEATURE-AUDIT.md)
// on the self-hosted compiler. Each case is oracle-checked against the
// interpreter, routing-pinned to "ir", and returns a value <= 126 (wasmtime
// exit-code truncation, cf. #2908).
var sliceViewIRCases = []struct {
	name string
	main string
}{
	// a[1:3] of [10,20,30,40] -> [20,30], len 2.
	{"slice-len", `function main(): i32 { var a: i32[] = [10,20,30,40]; var s: [i32] = a[1:3]; return s.len(); }`},
	// s[0] of that window is 20.
	{"slice-index", `function main(): i32 { var a: i32[] = [10,20,30,40]; var s: [i32] = a[1:3]; return s[0]; }`},
	// Sum a window via `for x in s`: a[1:4] of [1..5] = [2,3,4] -> 9.
	{"slice-iter-sum", `function main(): i32 { var a: i32[] = [1,2,3,4,5]; var s: [i32] = a[1:4]; var t: i32 = 0; for x in s { t = t + x; } return t; }`},
	// A slice of a slice: a[1:5]=[2,3,4,5], then s[0:2]=[2,3] -> len 2.
	{"slice-of-slice", `function main(): i32 { var a: i32[] = [1,2,3,4,5]; var s: [i32] = a[1:5]; var s2: [i32] = s[0:2]; return s2.len(); }`},
	// A slice passed as a `[i32]` parameter and consumed by the callee.
	{"slice-as-param", `function sum(s: [i32]): i32 { var t: i32 = 0; for x in s { t = t + x; } return t; } function main(): i32 { var a: i32[] = [4,5,6,7]; return sum(a[1:3]); }`},
	// Index with a computed offset off the slice length: last element.
	{"slice-last", `function main(): i32 { var a: i32[] = [9,8,7]; var s: [i32] = a[0:3]; return s[s.len()-1]; }`},
	// An empty window a[2:2] has length 0.
	{"empty-slice", `function main(): i32 { var a: i32[] = [1,2,3]; var s: [i32] = a[2:2]; return s.len(); }`},
	// A `[string]` element slice: strs[0:2] = ["ab","cde"], s[1].len() = 3.
	{"string-elem-slice", `function main(): i32 { var a: string[] = ["ab","cde","f"]; var s: [string] = a[0:2]; return s[1].len(); }`},
}

// TestSelfHostSliceViewsIRX86_64 routes each slice-view case through the
// self-hosted x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostSliceViewsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range sliceViewIRCases {
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

// TestSelfHostSliceViewsIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostSliceViewsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host slice-views wasm IR e2e")
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

	for _, tc := range sliceViewIRCases {
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
			watFile := filepath.Join(dir, "sliceview_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("slice-views wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
