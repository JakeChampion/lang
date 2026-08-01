package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSliceBoundsIR pins the slice-CONSTRUCTION bounds contract
// (#5419, docs/ARRAY-BOUNDS.md "Slice construction") on the self-host x86-64
// IR path: `a[lo:hi]` with hi > len, lo > hi, or lo < 0 ABORTS with exit 134
// instead of copying (arrays) or viewing (strings) past the source. Before
// this, __fern_arr_slice walked its copy loop to an unchecked `end` and the
// str_slice view emit built [data+start, end-start] with no length compare.
// Both now trap via the shared __fern_oob_abort. `lo == hi == len` stays
// legal (empty slice at the boundary).
func TestSelfHostSliceBoundsIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
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
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostSliceBoundsCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("%s: driver failed: %v", tc.name, err)
			}
			if !strings.Contains(string(asm), ".Lir_") {
				t.Fatalf("%s: fell back to the AST path (no .Lir_ labels)", tc.name)
			}
			if tc.want == 134 && !strings.Contains(string(asm), "__fern_oob_abort") {
				t.Fatalf("%s: no __fern_oob_abort in emitted asm — bounds check missing", tc.name)
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var run *exec.Cmd
			if len(runner) == 0 {
				run = exec.Command(bin)
			} else {
				run = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostSliceBoundsIRArm64 — CI-gated arm64 counterpart of
// TestSelfHostSliceBoundsIR: the checks live in asm_arm64.fern's
// __fern_arr_slice helper and asm_arm64_ir.fern's inline str_slice
// (kind_tag 102) emit, branching to __fern_oob_abort. Verified under qemu.
func TestSelfHostSliceBoundsIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
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

	for _, tc := range selfHostSliceBoundsCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			if tc.want == 134 && !strings.Contains(string(asm), "__fern_oob_abort") {
				t.Fatalf("%s: no __fern_oob_abort in emitted arm64 asm — bounds check missing", tc.name)
			}
			progBin := buildBin(t, arm64gcc, dir, "slice_bounds_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

func selfHostSliceBoundsCases() []struct {
	name string
	src  string
	want int
} {
	return []struct {
		name string
		src  string
		want int
	}{
		{"arr-high-past-end",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; var s: i32[] = a[0:4]; return s.len(); }`, 134},
		{"arr-high-far-past-end",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; var s: i32[] = a[1:100]; return s.len(); }`, 134},
		{"arr-reversed",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; var s: i32[] = a[2:1]; return s.len(); }`, 134},
		{"arr-negative-low",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; var lo: i32 = 0 - 1; var s: i32[] = a[lo:2]; return s.len(); }`, 134},
		{"str-high-past-end",
			`function main(): i32 { var s: string = "abc"; var t: string = s[0:9]; return t.len(); }`, 134},
		{"str-reversed",
			`function main(): i32 { var s: string = "abc"; var t: string = s[2:1]; return t.len(); }`, 134},
		// In-range: boundary forms stay legal. a[1:3] sums 2+3 = 5,
		// a[3:3] is empty, s[1:3] = "bc" has len 2 → exit 5+0+2 = 7.
		{"in-range-ok",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; var w: i32[] = a[1:3]; var t: i32 = 0; var i: i32 = 0; while (i < w.len()) { t = t + w[i]; i = i + 1; } var e: i32[] = a[3:3]; var s: string = "abc"; return t + e.len() + (s[1:3]).len(); }`, 7},
	}
}
