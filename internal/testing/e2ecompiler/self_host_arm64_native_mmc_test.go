package e2ecompiler

import (
	"bytes"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"path/filepath"
	"testing"
)

// TestSelfHostArm64NativeMmcMatchesCrossHost guards the "arm64 compiler
// compiling the arm64 self-host source" path. The differential gate
// (TestSelfHostStdTestE2EArm64) runs mmc-arm64 as an x86-64 host binary —
// convenient but it leaves the compiled-for-arm64 self-host source untested
// as a program in its own right. Two real bugs hid here until someone
// manually probed:
//
//  1. `strbuf_take` was missing from the two-word-return set, so its byte
//     length went through as garbage from the stack — any program using
//     strbuf silently mis-rendered its output. (PR #1676.)
//
//  2. `"ProcessResult"` wasn't pre-interned in the arm64 rodata-dump
//     prelude, so a program that used `subprocess()` without otherwise
//     mentioning `ProcessResult` by name had an unresolved `.S<idx>` label
//     at the `__fern_subprocess` helper's shape-pointer store. (PR #1678.)
//
// This test pins the path: build mmc-arm64 with the pinned stage0 compiler
// for arm64-linux, run it under qemu-aarch64 against `arithmetic_test.fern`,
// then assert the emitted aarch64 asm is byte-identical to what the same
// driver built for x86-64-linux produces. If they diverge, the PIN's arm64
// codegen has an emit bug on the self-host source (or a silent runtime
// helper gap in the strbuf / shape-name family). The current source's own
// arm64 compile is gated by the module self-tests (TestSelfHostParserArm64
// and its siblings), which build the current modules for arm64 with the pin
// and run them under qemu.
//
// SKIPs cleanly when the host can run no arm64 binary (no qemu-aarch64);
// no cross toolchain is needed, the pin emits the binaries.
func TestSelfHostArm64NativeMmcMatchesCrossHost(t *testing.T) {
	qemu := arm64Runner(t)
	x86runner := x86_64Runner(t)

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")

	// The same driver built by the pin for both hosts: an aarch64 binary
	// running under qemu, and an x86-64 binary running on the host.
	mmcNative := buildSelfHostBinFor(t, dir, "drivers/asm_load_run.fern", "mmc_arm64_native", e2eharness.TargetArm64Linux)
	mmcCross := buildSelfHostBinFor(t, dir, "drivers/asm_load_run.fern", "mmc_x86_cross", e2eharness.TargetX86_64Linux)

	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	// Pick programs spanning the emit surface: a trivial baseline
	// + large programs with extensive stdlib transitive imports.
	// The json + http suites OOM the native mmc at a 64-MiB heap
	// (vs 512 MiB on x86), so they run in the gate at heap parity.
	// The strings / string_prelude_migrated / process_assertions
	// suites were once dropped for the args() rc-header corruption
	// (argv strings allocated without an L2 header, so rc ops hit
	// neighbouring argv bytes — path-length-dependent openat
	// failures); that bug is fixed and pinned by
	// TestArm64ArgvStringsRcSafe, so they are back on the gate.
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
				t.Fatal("mmc_arm64_native emitted 0 bytes — the bugs the gate guards against (strbuf return shape, ProcessResult rodata, arm64 heap size)")
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

// firstDivergentLine returns the 1-based line number where `a` and `b` first
// differ, or 0 when they are identical — the diagnostic that makes a
// byte-identity failure readable instead of a full asm dump. It lived alongside
// TestSelfHostStage2FixedPoint until that merged-bundle fixpoint retired with
// the AST emitters (#3457 slice 5); this is its remaining caller.
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
