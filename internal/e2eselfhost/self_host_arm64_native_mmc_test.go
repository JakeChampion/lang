package e2eselfhost

import (
	"bytes"
	"github.com/jakechampion/lang/internal/e2eharness"
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
// driver built for x86-64-linux produces. If they diverge, the pin's arm64
// codegen has an emit bug on the self-host source (or a silent runtime
// helper gap in the strbuf / shape-name family).
//
// SKIPs cleanly when the aarch64 cross-toolchain / qemu-aarch64
// aren't installed (same shape as the other arm64-gated tests).
func TestSelfHostArm64NativeMmcMatchesCrossHost(t *testing.T) {
	_, qemu := arm64Tooling(t)
	_, x86runner := x86_64Tooling(t)

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")

	// The same driver built by the pin for both hosts: an aarch64 binary
	// running under qemu, and an x86-64 binary running on the host.
	mmcNative := buildSelfHostBinFor(t, dir, "asm_load_run.fern", "mmc_arm64_native", e2eharness.TargetArm64Linux)
	mmcCross := buildSelfHostBinFor(t, dir, "asm_load_run.fern", "mmc_x86_cross", e2eharness.TargetX86_64Linux)

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
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
		"examples/tests/arithmetic_test.fern",
		"examples/tests/json_field_eq_test.fern",
		"examples/tests/http_response_headers_migrated_test.fern",
		"examples/tests/sort_wider_test.fern",
		"examples/tests/strings_test.fern",
		"examples/tests/string_prelude_migrated_test.fern",
		"examples/tests/process_assertions_test.fern",
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
