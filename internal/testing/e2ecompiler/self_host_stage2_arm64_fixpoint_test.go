package e2ecompiler

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
)

func childFailure(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return fmt.Sprintf("%v\n%s", err, ee.Stderr)
	}
	return err.Error()
}

func TestSelfHostStage2FixpointArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("needs a native x86 host to run the aarch64-emitting driver")
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverSrc := filepath.Join(dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	stage1Asm, err := exec.Command(mmc, driverSrc, stdlibRoot, "-target", "arm64-linux").Output()
	if err != nil {
		t.Fatalf("stage 1: mmc could not emit aarch64 for its own source: %s", childFailure(err))
	}
	if len(stage1Asm) == 0 {
		t.Fatal("stage 1: emitted 0 bytes for the self-hosted arm64 compiler")
	}
	t.Logf("stage 1: self-hosted arm64 compiler asm = %d bytes", len(stage1Asm))
	mmcArm64 := buildBinArm64(t, armgcc, dir, "mmc_arm64_stage2", string(stage1Asm))

	cases := []struct {
		name    string
		src     string
		stdlib  bool
		selfEnv bool
	}{
		{name: "lexer", src: "compiler/lexer.fern", stdlib: true},
		{name: "sort_wider", src: "tests/stdlib/sort_wider_test.fern", stdlib: true},
		{name: "float_math", src: "tests/stdlib/float_math_test.fern", stdlib: true},
		{name: "process_assertions", src: "tests/stdlib/process_assertions_test.fern", stdlib: true},
		{name: "self", src: "compiler/drivers/asm_load_run.fern", stdlib: true, selfEnv: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.selfEnv && !envflag.On("FERN_STAGE2_SELF") {
				t.Skip("set FERN_STAGE2_SELF=1 to run the whole-compiler stage-2 case")
			}
			src := langSrcAbs(t, tc.src)
			args := []string{src}
			if tc.stdlib {
				args = append(args, stdlibRoot)
			}
			args = append(args, "-target", "arm64-linux")

			gen1, err := exec.Command(mmc, args...).Output()
			if err != nil {
				t.Fatalf("gen1 (x86 host): %s", childFailure(err))
			}
			if len(gen1) == 0 {
				t.Fatal("gen1 emitted 0 bytes")
			}
			gen2, err := runArm64Bin(qemu, mmcArm64, args...).Output()
			if err != nil {
				t.Fatalf("gen2 (self-host-built aarch64, under qemu): %s", childFailure(err))
			}
			if !bytes.Equal(gen1, gen2) {
				t.Errorf("stage-2 fixpoint broken: gen1 %d bytes, gen2 %d bytes; first divergent line: %d",
					len(gen1), len(gen2), firstDivergentLine(gen1, gen2))
			}
		})
	}
}
