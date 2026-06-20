package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sliceViewIRCases exercise slice views — `arr[lo:hi]` yielding the distinct
// `[T]` slice-view type (as opposed to an owned `T[]`), plus string slicing
// `s[lo:hi]` — through the self-host IR path on x86-64 + wasm (the
// `Slice views [T]` row was unaudited). These are pure language builtins (no
// imports), so each case is a bare `main`. This verifies that the IR path's
// `ExprSlice` lowering (`arr_slice` / `str_slice`) handles: binding an array
// slice to a `[i32]` local, `.len()` on a slice view, indexed reads of a slice
// view (`m[i]`), iterating a slice view by index, string slicing to a fresh
// string, byte indexing of a string slice, and the full / empty slice edges.
// Each program returns a small deterministic int (<= 126), pinned to the `"ir"`
// path; expectations are oracle-checked against the native interpreter.
// FEATURE-AUDIT "Slice views [T]" row.
var sliceViewIRCases = []struct {
	name string
	main string
	want int
}{
	// .len() of an array slice view a[1:4] over [10..50] -> 3.
	{"arr-len", `var a: i32[] = [10, 20, 30, 40, 50]; var m: [i32] = a[1:4]; return m.len();`, 3},
	// indexed read of a slice view: m[0] == a[1] == 20.
	{"arr-idx0", `var a: i32[] = [10, 20, 30, 40, 50]; var m: [i32] = a[1:4]; return m[0];`, 20},
	// indexed read at the end of the view: m[2] == a[3] == 40.
	{"arr-idx2", `var a: i32[] = [10, 20, 30, 40, 50]; var m: [i32] = a[1:4]; return m[2];`, 40},
	// iterate the slice view by index, summing: 20 + 30 + 40 = 90.
	{"arr-sum", `var a: i32[] = [10, 20, 30, 40, 50]; var m: [i32] = a[1:4]; var s: i32 = 0; var i: i32 = 0; while (i < m.len()) { s = s + m[i]; i = i + 1; } return s;`, 90},
	// string slice s[0:5] of "hello world" -> "hello" (len 5).
	{"str-len", `var s: string = "hello world"; var sub: string = s[0:5]; return sub.len();`, 5},
	// byte index of a string slice: s[6:11] is "world"; first byte 'w' = 119.
	{"str-byte", `var s: string = "hello world"; var sub: string = s[6:11]; return sub[0];`, 119},
	// full-width slice a[0:5] keeps every element -> len 5.
	{"full", `var a: i32[] = [1, 2, 3, 4, 5]; var m: [i32] = a[0:5]; return m.len();`, 5},
	// empty slice a[2:2] -> len 0.
	{"empty", `var a: i32[] = [1, 2, 3, 4, 5]; var m: [i32] = a[2:2]; return m.len();`, 0},
}

func sliceViewIRSrc(mainBody string) string {
	return "function main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostSliceViewIRX86_64 routes each case through the self-hosted x86-64
// IR driver, with the routing pinned to the "ir" path.
func TestSelfHostSliceViewIRX86_64(t *testing.T) {
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
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range sliceViewIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(sliceViewIRSrc(tc.main))
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

// TestSelfHostSliceViewIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostSliceViewIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host slice-view wasm IR e2e")
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

	for _, tc := range sliceViewIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(sliceViewIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "slice_view_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("slice-view wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
