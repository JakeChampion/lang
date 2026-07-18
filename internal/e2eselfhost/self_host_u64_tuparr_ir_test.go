package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// u64TupArrIRCases exercise a u64 element of an array-of-tuples (`(u64, i32)[]`)
// read via `xs[i].N` and chained in an unsigned op. An all-scalar tuple array is
// NOT deep-droppable, so arrtup_elem_tuple_type recorded no element tuple type
// and the read side fell back to elem_type_tag, which coarsens a u64 element to
// "i64" (width only) — so `xs[i].N >> k` / `xs[i].N > x` used a SIGNED shift /
// compare and diverged once bit 63 was set. The fix records the ANNOTATION's
// tuple type (which preserves "u64") into the read-side arrarr_elem, reclaim-inert
// (an all-scalar tuple array never earns the ARRTUP credit), so expr_is_u64's
// nested read recovers the sign and selects shr_u / gt_u. Sibling of the u64[][]
// arrarr fix (#5206).
//
// Routing-pinned to "ir", oracle-checked against the interpreter, values <= 120
// (the wasmtime exit-code gap #2908). The wide element 18000000000000000000 has
// bit 63 set, so signed vs unsigned give DIFFERENT results.
var u64TupArrIRCases = []struct {
	name string
	main string
}{
	// xs[0].0 >> 58: unsigned = 0xF9CCD8A1C5080000 >> 58 = 62; signed (arith) = 254.
	{"index-field-shr", `function main(): i32 { var xs: (u64, i32)[] = [(18000000000000000000 as u64, 1)]; return (xs[0].0 >> 58) as i32; }`},
	// Same, but a BARE oversized literal typed only by the (u64, i32)[] annotation
	// (no `as u64`) — exercises the annotation-driven tuple-type recording.
	{"index-field-shr-bare", `function main(): i32 { var xs: (u64, i32)[] = [(18000000000000000000, 1)]; return (xs[0].0 >> 58) as i32; }`},
	// u64 as the SECOND tuple element: the sign must be recovered per-position.
	{"second-elem-shr", `function main(): i32 { var xs: (i32, u64)[] = [(1, 18000000000000000000 as u64)]; return (xs[0].1 >> 58) as i32; }`},
	// Unsigned compare: unsigned true (7); signed (negative) false (9).
	{"index-field-cmp", `function main(): i32 { var xs: (u64, i32)[] = [(18000000000000000000 as u64, 1)]; if (xs[0].0 > (100 as u64)) { return 7; } return 9; }`},
	// Bind the tuple out of the array first (`var t = xs[0]`), then read `t.0`: the
	// "u64" tag must propagate across the element-binding.
	{"bound-elem-shr", `function main(): i32 { var xs: (u64, i32)[] = [(18000000000000000000 as u64, 1)]; var t = xs[0]; return (t.0 >> 58) as i32; }`},
	// i64 tuple-array element width regression: the 8-byte read must stay full-width
	// (value fits, so signed/unsigned agree — this guards against truncation). 7.
	{"i64-elem-width-regress", `function main(): i32 { var xs: (i64, i32)[] = [(5000000007, 1)]; return (xs[0].0 % 1000) as i32; }`},
}

func TestSelfHostU64TupArrIRX86_64(t *testing.T) {
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
	for _, tc := range u64TupArrIRCases {
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

func TestSelfHostU64TupArrIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host u64 tuple-array wasm IR e2e")
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
	for _, tc := range u64TupArrIRCases {
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
			watFile := filepath.Join(dir, "u64_tuparr_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("u64 tuple-array wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
