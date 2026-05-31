package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStage2FixedPointArm64 is the arm64 mirror of the x86
// fixed-point gate. It proves the arm64 self-host emit is a fixed
// point of itself: a mmc-arm64-stage2 binary (built by feeding
// asm_arm64_load_run.fern back through the stage-1 cross-compiler-
// on-host mmc-arm64) and the stage-1 mmc-arm64 emit byte-identical
// aarch64 assembly for the same input.
//
// stage-1 mmc-arm64 runs as a native x86 host binary (the same
// cross-compiler-on-host pattern the differential gate uses) and
// emits aarch64 asm; stage-2 is a real aarch64 binary running under
// qemu-aarch64 (or native arm64 if the host is arm64). If they
// diverge, the arm64 emit has a non-deterministic path OR a real
// emit bug that compiles-but-mis-translates code on the more-
// expensive arm64-running-arm64 side.
//
// Scope is narrower than the x86 version (4 cases vs 11) because
// running mmc-arm64-stage2 under qemu is slower (it's emitting ~6
// MB of arm64 asm per case). The 4 cases span enough of the emit
// surface that a divergence anywhere shows up — fixed-point holds
// for one program iff it holds for any.
func TestSelfHostStage2FixedPointArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 stage-2 fixed-point needs a native x86 host for the cross-compiler-on-host driver")
	}

	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"flatten.fern", "asm_arm64.fern", "asm_arm64_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// stage 1: Go-built mmc-arm64 (x86 host binary emitting arm64 asm).
	mmc1 := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_load_run.fern", "mmc_arm64_s1")

	// stage 2: mmc1 compiles asm_arm64_load_run.fern → arm64 asm,
	// aarch64-gcc links → aarch64 binary running under qemu-aarch64.
	selfSrc := filepath.Join(dir, "asm_arm64_load_run.fern")
	stage2Asm, err := exec.Command(mmc1, selfSrc).Output()
	if err != nil {
		t.Fatalf("mmc1 compile self failed: %v", err)
	}
	if len(stage2Asm) == 0 {
		t.Fatal("mmc1 emitted 0 bytes for asm_arm64_load_run.fern")
	}
	t.Logf("stage 2 arm64 self-asm = %d bytes", len(stage2Asm))
	mmc2Bin := buildBinArm64(t, arm64gcc, dir, "mmc_arm64_s2", string(stage2Asm))

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	// The fixed-point inputs. `self` is the heaviest input (the
	// arm64 self-host source — every emit path the compiler uses on
	// itself). The other three span the arm64-specific emit surface
	// that landed across the parity work: unsigned cset (lo/ls/hi/hs)
	// for wider-int sort, IEEE NaN compares for f64, subprocess /
	// clone(SIGCHLD) syscall fork.
	cases := []struct {
		name string
		args []string
	}{
		{"self", []string{selfSrc}},
		{"sort_wider", []string{langSrcAbs(t, "examples/tests/sort_wider_test.fern"), stdlibRoot}},
		{"float_math", []string{langSrcAbs(t, "examples/tests/float_math_test.fern"), stdlibRoot}},
		{"process_assertions", []string{langSrcAbs(t, "examples/tests/process_assertions_test.fern"), stdlibRoot}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// stage 1: native x86 mmc1 emits arm64 asm.
			asm1, err := exec.Command(mmc1, tc.args...).Output()
			if err != nil {
				t.Fatalf("mmc1: %v", err)
			}
			// stage 2: aarch64 mmc2 (under qemu or native arm64)
			// emits arm64 asm.
			asm2, err := runArm64Bin(qemu, mmc2Bin, tc.args...).Output()
			if err != nil {
				t.Fatalf("mmc2: %v", err)
			}
			if !bytes.Equal(asm1, asm2) {
				divLine := firstDivergentLine(asm1, asm2)
				t.Errorf("stage-1 / stage-2 arm64 asm differ (%d vs %d bytes); first divergent line: %d",
					len(asm1), len(asm2), divLine)
			}
		})
	}
}
