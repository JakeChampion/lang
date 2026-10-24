package e2ecompiler

import (
	"bytes"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"path/filepath"
	"testing"
)

func TestSelfHostArm64NativeMmcMatchesCrossHost(t *testing.T) {
	qemu := arm64Runner(t)
	x86runner := x86_64Runner(t)

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")

	mmcNative := buildSelfHostBinFor(t, dir, "drivers/asm_load_run.fern", "mmc_arm64_native", e2eharness.TargetArm64Linux)
	mmcCross := buildSelfHostBinFor(t, dir, "drivers/asm_load_run.fern", "mmc_x86_cross", e2eharness.TargetX86_64Linux)

	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	cases := []string{
		"tests/stdlib/arithmetic_test.fern",
		"tests/stdlib/json_field_eq_test.fern",
		"tests/stdlib/http_response_headers_migrated_test.fern",
		"tests/stdlib/sort_wider_test.fern",
		"tests/stdlib/strings_test.fern",
		"tests/stdlib/string_prelude_migrated_test.fern",
		"tests/stdlib/process_assertions_test.fern",
	}
	for _, rel := range cases {
		t.Run(filepath.Base(rel), func(t *testing.T) {
			testSrc := langSrcAbs(t, rel)
			nativeOut, err := runArm64Bin(qemu, mmcNative, testSrc, stdlibRoot, "-target", "arm64-linux").Output()
			if err != nil {
				t.Fatalf("mmc_arm64_native: %v", err)
			}
			if len(nativeOut) == 0 {
				t.Fatal("mmc_arm64_native emitted 0 bytes — the bugs the gate guards against (string return shape, ProcessResult rodata, arm64 heap size)")
			}
			crossOut, err := runX86_64Bin(x86runner, mmcCross, testSrc, stdlibRoot, "-target", "arm64-linux").Output()
			if err != nil {
				t.Fatalf("mmc_x86_cross: %v", err)
			}
			if !bytes.Equal(nativeOut, crossOut) {
				divLine := firstDivergentLine(nativeOut, crossOut)
				t.Errorf("native arm64 / cross-host arm64 asm differ (%d vs %d bytes); first divergent line: %d",
					len(nativeOut), len(crossOut), divLine)
			}
		})
	}
}

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
