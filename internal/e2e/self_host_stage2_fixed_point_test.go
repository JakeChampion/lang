package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStage2FixedPoint proves the self-host is a fixed point
// of its own emit: a mmc-stage2 binary (built by the Go-compiled
// stage-1 mmc compiling asm_load_run.fern) and the stage-1 mmc
// produce byte-identical assembly for the same input. If they
// diverge, the self-host has a non-deterministic emit OR a real-
// world emit bug that compiles-but-mis-translates code that
// happens to land inside asm_load_run / the differential gate.
//
// Steps:
//
//  1. Build mmc-stage1: Go x86_64.Emit on asm_load_run.fern.
//  2. Build mmc-stage2: feed asm_load_run.fern back through mmc-stage1
//     (this exercises the full Fern emitter on its own source — the
//     hardest input it has, ~9k lines).
//  3. For asm_load_run.fern AND a representative subset of the
//     differential gate cases: compile with mmc-stage1 and mmc-stage2,
//     assert byte-identical asm.
//
// Native x86_64 only (mirrors the file-loading driver tests — argv
// paths). The subset is chosen to span the emit surface that the
// gate covers (basic arith, struct shape dispatch, Option/Result
// payload typing, f64, wider-int / unsigned compare, subprocess,
// std/fuzz, bench fn-arg boxing) without paying for all 49 cases
// — fixed-point holds for one program iff it holds for any.
func TestSelfHostStage2FixedPoint(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("stage-2 fixed-point test runs only natively (argv paths)")
	}

	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"flatten.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// stage 1: Go-built mmc.
	mmc1 := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc1")

	// stage 2: mmc1 compiles asm_load_run.fern → asm, gcc links.
	selfSrc := filepath.Join(dir, "asm_load_run.fern")
	stage2Asm, err := exec.Command(mmc1, selfSrc).Output()
	if err != nil {
		t.Fatalf("mmc1 compile self failed: %v", err)
	}
	if len(stage2Asm) == 0 {
		t.Fatal("mmc1 emitted 0 bytes for asm_load_run.fern")
	}
	t.Logf("stage 2 self-asm = %d bytes", len(stage2Asm))
	mmc2 := buildBin(t, gcc, dir, "mmc2", string(stage2Asm))

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	// The fixed-point inputs. asm_load_run is the heaviest (the self-
	// host source — exercises every emit path the compiler uses on
	// itself). The rest are picked to span the emit surface that
	// landed in the differential gate over the last ~25 PRs.
	cases := []struct {
		name string
		// args are passed to mmc1 / mmc2; the first is the entry
		// source path, the rest are extra args (stdlib root for
		// test programs that import std/test).
		args []string
	}{
		{"self", []string{selfSrc}},
		{"arithmetic", []string{langSrcAbs(t, "examples/tests/arithmetic_test.fern"), stdlibRoot}},
		{"runner_self", []string{langSrcAbs(t, "examples/tests/runner_self_test.fern"), stdlibRoot}},
		{"result_assertions", []string{langSrcAbs(t, "examples/tests/result_assertions_test.fern"), stdlibRoot}},
		{"fuzz_example", []string{langSrcAbs(t, "examples/tests/fuzz_example_test.fern"), stdlibRoot}},
		{"float_math", []string{langSrcAbs(t, "examples/tests/float_math_test.fern"), stdlibRoot}},
		{"sort_wider", []string{langSrcAbs(t, "examples/tests/sort_wider_test.fern"), stdlibRoot}},
		{"json_field_eq", []string{langSrcAbs(t, "examples/tests/json_field_eq_test.fern"), stdlibRoot}},
		{"bench", []string{langSrcAbs(t, "examples/tests/bench_test.fern"), stdlibRoot}},
		{"process_assertions", []string{langSrcAbs(t, "examples/tests/process_assertions_test.fern"), stdlibRoot}},
		{"http_response_headers_migrated", []string{langSrcAbs(t, "examples/tests/http_response_headers_migrated_test.fern"), stdlibRoot}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm1, err := exec.Command(mmc1, tc.args...).Output()
			if err != nil {
				t.Fatalf("mmc1: %v", err)
			}
			asm2, err := exec.Command(mmc2, tc.args...).Output()
			if err != nil {
				t.Fatalf("mmc2: %v", err)
			}
			if !bytes.Equal(asm1, asm2) {
				// Find the first diverging line so the diagnostic
				// is useful even when the asm is huge.
				divLine := firstDivergentLine(asm1, asm2)
				t.Errorf("stage-1 / stage-2 asm differ (%d vs %d bytes); first divergent line: %d",
					len(asm1), len(asm2), divLine)
			}
		})
	}
}

// firstDivergentLine returns the 1-based line number where a and b
// first differ, or 0 if they're equal.
func firstDivergentLine(a, b []byte) int {
	la := bytes.Split(a, []byte{'\n'})
	lb := bytes.Split(b, []byte{'\n'})
	n := len(la)
	if len(lb) < n {
		n = len(lb)
	}
	for i := 0; i < n; i++ {
		if !bytes.Equal(la[i], lb[i]) {
			return i + 1
		}
	}
	if len(la) != len(lb) {
		return n + 1
	}
	return 0
}
