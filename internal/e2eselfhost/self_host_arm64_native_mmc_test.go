package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostArm64NativeMmcMatchesCrossHost guards the
// "arm64 Go backend compiling the arm64 self-host source" path.
// The differential gate (TestSelfHostStdTestE2EArm64) builds mmc-
// arm64 as an x86 host binary via the Go x86 backend (cross-
// compiler-on-host) — convenient but it leaves the arm64-backend-
// compiles-arm64-self-host path untested. Two real bugs hid here
// until now and only surfaced when someone manually probed:
//
//  1. `strbuf_take` was missing from `returnIsString`, so its
//     two-word return decayed to single-word and the byte length
//     went through as garbage from the stack — any program using
//     strbuf silently mis-rendered its output. (PR #1676.)
//
//  2. `"ProcessResult"` wasn't pre-interned in `asm_arm64.fern`'s
//     `needs_heap` rodata-dump prelude, so a program that used
//     `subprocess()` without otherwise mentioning `ProcessResult`
//     by name had an unresolved `.S<idx>` label at the
//     `__fern_subprocess` helper's shape-pointer store. (PR #1678.)
//
// This test pins the path: build mmc-arm64 with the Go arm64
// backend, run it under qemu-aarch64 against
// `arithmetic_test.fern`, then assert the emitted aarch64 asm is
// byte-identical to what mmc built via the cross-compiler-on-host
// pattern produces. If they diverge, the arm64 Go backend has a
// new emit bug on the self-host source (or a new silent runtime
// helper gap in the strbuf / shape-name family).
//
// SKIPs cleanly when the aarch64 cross-toolchain / qemu-aarch64
// aren't installed (same shape as the other arm64-gated tests).
func TestSelfHostArm64NativeMmcMatchesCrossHost(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("test needs a native x86 host for the cross-compiler-on-host driver")
	}

	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "flatten.fern", "checker.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "treeshake.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverSrc := filepath.Join(dir, "asm_load_run.fern")

	// Build mmc via the Go arm64 backend → aarch64 binary running
	// under qemu.
	prog, _, err := modload.Load(driverSrc)
	if err != nil {
		t.Fatalf("modload arm64: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold arm64: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check arm64: %v", err)
	}
	arm64Asm, err := arm64codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("arm64 emit: %v", err)
	}
	mmcNative := buildBinArm64(t, arm64gcc, dir, "mmc_arm64_native", arm64Asm)

	// Build mmc via the Go x86 backend → x86 binary running natively.
	prog2, _, err := modload.Load(driverSrc)
	if err != nil {
		t.Fatalf("modload x86: %v", err)
	}
	if err := constfold.Fold(prog2); err != nil {
		t.Fatalf("constfold x86: %v", err)
	}
	info2, err := checker.Check(prog2)
	if err != nil {
		t.Fatalf("check x86: %v", err)
	}
	x86Asm, err := x86_64.Emit(prog2, info2)
	if err != nil {
		t.Fatalf("x86 emit: %v", err)
	}
	mmcCross := buildBin(t, x86gcc, dir, "mmc_x86_cross", x86Asm)

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	// Pick programs spanning the emit surface: a trivial baseline
	// + large programs with extensive stdlib transitive imports.
	// The json + http suites previously OOM'd the native mmc at
	// the 64-MiB heap that arm64's __fern_alloc reserved (vs 512
	// MiB on x86) — they ride the gate post the heap-parity bump.
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
			nativeOut, err := runArm64Bin(qemu, mmcNative, testSrc, stdlibRoot, "-target", "arm64").Output()
			if err != nil {
				t.Fatalf("mmc_arm64_native: %v", err)
			}
			if len(nativeOut) == 0 {
				t.Fatal("mmc_arm64_native emitted 0 bytes — the bugs the gate guards against (strbuf return shape, ProcessResult rodata, arm64 heap size)")
			}
			crossOut, err := exec.Command(mmcCross, testSrc, stdlibRoot, "-target", "arm64").Output()
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
// byte-identity failure readable instead of a wall of asm. It lived alongside
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
