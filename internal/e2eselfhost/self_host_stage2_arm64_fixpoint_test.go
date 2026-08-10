package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStage2FixpointArm64 restores the arm64 stage-2 fixpoint that
// #5972 removed without a successor (#6327).
//
//	stage 0  the Go x86-64 backend builds `asm_load_run.fern` into `mmc`, an
//	         x86 host binary that emits aarch64 (the cross-compiler-on-host
//	         pattern).
//	stage 1  `mmc` emits aarch64 asm for `asm_load_run.fern` — ITSELF — and
//	         the aarch64 cross-gcc links that into `mmc-arm64`.
//	stage 2  `mmc-arm64` runs under qemu-aarch64 over the same inputs as
//	         `mmc`, and the emitted aarch64 asm must be byte-identical.
//
// Two properties in one comparison: the emit does not depend on the HOST
// architecture, and it is a fixed point under self-recompilation — the
// compiler the self-host built reproduces the self-host's own output.
//
// Why arm64 is the leg worth having it on: `-target arm64` is the only path
// where the self-host produces the finished binary itself, emit + assemble +
// link in-process, so it is the only gate on `arm64_native.fern`. Generation 2
// runs THROUGH the assembler's own output, which is what separates "the
// emitter is wrong" from "the assembler is wrong" — three arm64 assembler bugs
// (numeric local labels, the missing `.text` symbol case, an i32 literal pool)
// were each mis-attributed to codegen first.
//
// #6327 predicted this needed a new `asm_arm64_ir_load_run.fern` driver. It
// does not: #4398 part 1 folded the arm64 loader mirror into `asm_load_run.fern`
// behind `-target arm64`, so the driver the deleted test wanted already exists.
//
// COST. Stage 1 is the expensive half and it runs NATIVELY (~3 min for 35 MB of
// asm); the aarch64 link is ~5 s. Stage 2 is qemu, and the per-case cost is
// dominated by stdlib LOADING rather than emit: ~55 s for a stdlib-importing
// test against ~2 s for a bare compiler module. The `self` case — gen2
// recompiling the whole compiler under qemu — is far heavier again and is gated
// behind FERN_STAGE2_SELF=1 rather than paid on every run.
func TestSelfHostStage2FixpointArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("needs a native x86 host to run the aarch64-emitting driver")
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	driverSrc := filepath.Join(dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, x86gcc, dir, "asm_load_run.fern", "mmc")

	// stage 1: the driver emits aarch64 for its own source, and that asm
	// becomes a real aarch64 compiler. Reading the STAGED copy rather than
	// examples/self_host keeps the two generations on identical bytes.
	stage1Asm, err := exec.Command(mmc, driverSrc, "-target", "arm64").Output()
	if err != nil {
		t.Fatalf("stage 1: mmc could not emit aarch64 for its own source: %v", err)
	}
	if len(stage1Asm) == 0 {
		t.Fatal("stage 1: emitted 0 bytes for the self-hosted arm64 compiler")
	}
	t.Logf("stage 1: self-hosted arm64 compiler asm = %d bytes", len(stage1Asm))
	mmcArm64 := buildBinArm64(t, armgcc, dir, "mmc_arm64_stage2", string(stage1Asm))

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	// A span, not a sample. `lexer.fern` is a compiler module with no stdlib
	// (cheap, and the shape gen1 itself is made of); the three test suites are
	// the survivors of the deleted test's four inputs and carry stdlib
	// transitive imports, floats and process/string helpers between them.
	cases := []struct {
		name    string
		src     string
		stdlib  bool
		selfEnv bool
	}{
		{name: "lexer", src: "examples/self_host/lexer.fern"},
		{name: "sort_wider", src: "examples/tests/sort_wider_test.fern", stdlib: true},
		{name: "float_math", src: "examples/tests/float_math_test.fern", stdlib: true},
		{name: "process_assertions", src: "examples/tests/process_assertions_test.fern", stdlib: true},
		// The heavyweight: gen2 compiling the whole compiler under qemu. This
		// is the case the deleted test measured at ~709 s on the AST path, and
		// it is the strongest form of the property — but it is not worth its
		// wall-clock on every run, so it rides an env var.
		{name: "self", src: "examples/self_host/asm_load_run.fern", selfEnv: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.selfEnv && os.Getenv("FERN_STAGE2_SELF") != "1" {
				t.Skip("set FERN_STAGE2_SELF=1 to run the whole-compiler stage-2 case")
			}
			src := langSrcAbs(t, tc.src)
			args := []string{src}
			if tc.stdlib {
				args = append(args, stdlibRoot)
			}
			args = append(args, "-target", "arm64")

			gen1, err := exec.Command(mmc, args...).Output()
			if err != nil {
				t.Fatalf("gen1 (x86 host): %v", err)
			}
			if len(gen1) == 0 {
				t.Fatal("gen1 emitted 0 bytes")
			}
			gen2, err := runArm64Bin(qemu, mmcArm64, args...).Output()
			if err != nil {
				t.Fatalf("gen2 (self-host-built aarch64, under qemu): %v", err)
			}
			if !bytes.Equal(gen1, gen2) {
				t.Errorf("stage-2 fixpoint broken: gen1 %d bytes, gen2 %d bytes; first divergent line: %d",
					len(gen1), len(gen2), firstDivergentLine(gen1, gen2))
			}
		})
	}
}
