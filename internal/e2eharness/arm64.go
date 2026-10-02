// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/arm64_test.go.
package e2eharness

import (
	"os"
	"os/exec"
	"runtime"
	"testing"

	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
)

// Arm64Tooling locates the C linker used to assemble the
// generated asm and the runner used to execute the resulting
// binary. On a native arm64 Linux host the system `gcc` is
// already arm64 and the binary runs without an emulator, so
// `qemu` comes back empty. On x86 hosts (the historical CI
// shape) we need the aarch64 cross-toolchain and qemu-aarch64;
// the test SKIPs cleanly if neither path is available.
//
// FERN_REQUIRE_ARM64_TOOLING=1 turns that skip into a failure, for a lane that
// installs the toolchain and is the ONLY place a test runs: there a silent skip
// reports green while covering nothing, which is how the cross-emit tests came
// to run nowhere before #6849. The same guarantee FERN_REQUIRE_CROSS_BACKENDS
// gives internal/e2e's two-backend comparison.
func Arm64Tooling(t *testing.T) (gcc, qemu string) {
	t.Helper()
	gcc, qemu, ok := LookupArm64Tooling()
	if !ok {
		if os.Getenv("FERN_REQUIRE_ARM64_TOOLING") == "1" {
			t.Fatalf("aarch64 cross toolchain not available (gcc=%q qemu=%q) and FERN_REQUIRE_ARM64_TOOLING=1: "+
				"this lane installs it and is the only one running this test, so a skip here covers nothing", gcc, qemu)
		}
		t.Skipf("aarch64 cross toolchain not available (gcc=%q qemu=%q)", gcc, qemu)
	}
	return gcc, qemu
}

// Arm64Runner is the runner half of Arm64Tooling alone: "" on a native arm64
// host, else qemu-aarch64, for a test that runs a pin-built arm64 binary and
// assembles nothing. It skips (or fails under FERN_REQUIRE_ARM64_TOOLING=1)
// when the host can run no arm64 binary at all.
func Arm64Runner(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		return ""
	}
	_, qemu, _ := LookupArm64Tooling()
	if qemu == "" {
		if os.Getenv("FERN_REQUIRE_ARM64_TOOLING") == "1" {
			t.Fatal("no qemu-aarch64 on PATH and FERN_REQUIRE_ARM64_TOOLING=1: this lane installs it and is the only one running this test, so a skip here covers nothing")
		}
		t.Skip("no qemu-aarch64 on PATH")
	}
	return qemu
}

// LookupArm64Tooling is Arm64Tooling's discovery half without the skip, for a
// caller that has to decide for itself what a missing toolchain means — a test
// needing BOTH register backends at once has no lane where a skip is the correct
// answer (#6849).
func LookupArm64Tooling() (gcc, qemu string, ok bool) {
	// Native arm64 Linux: plain `gcc` produces arm64 binaries,
	// no emulator needed.
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		if p, err := exec.LookPath("gcc"); err == nil {
			return p, "", true
		}
	}
	for _, c := range []string{"aarch64-linux-gnu-gcc", "aarch64-unknown-linux-gnu-gcc"} {
		if p, err := exec.LookPath(c); err == nil {
			gcc = p
			break
		}
	}
	for _, c := range []string{"qemu-aarch64", "qemu-aarch64-static"} {
		if p, err := exec.LookPath(c); err == nil {
			qemu = p
			break
		}
	}
	return gcc, qemu, gcc != "" && qemu != ""
}

// RunArm64Bin builds the exec.Cmd for running an arm64 Linux
// binary either natively (when `qemu` is empty — we're already
// on arm64) or via qemu-aarch64 (cross-host case). Centralises
// the "qemu prefix or not" dispatch so callers don't sprinkle
// the same conditional through every test.
func RunArm64Bin(qemu, binPath string, args ...string) *exec.Cmd {
	if qemu == "" {
		return exec.Command(binPath, args...)
	}
	return exec.Command(qemu, append([]string{binPath}, args...)...)
}

func CompileAndRunArm64(t *testing.T, src string) (stdout string, exitCode int) {
	t.Helper()
	binPath, qemu := CompileArm64Bin(t, src)
	cmd := RunArm64Bin(qemu, binPath)
	out, _ := cmd.CombinedOutput()
	return finishArm64Run(t, cmd, out)
}

// CompileAndRunArm64HighHeap is CompileAndRunArm64 with the arena's mmap
// address hint raised above 4 GiB (arm64codegen.Options.HighHeapProbe), so
// every heap pointer the program produces has a non-zero high 32 bits.
//
// That is the address regime arm64-darwin runs in and Linux never reaches,
// which is why a pointer truncated to 32 bits — a narrow load of a heap
// handle, a 32-bit compare of two pointers — used to be findable only on
// Apple hardware. qemu-aarch64 honours the raised hint, so the same class is
// reproducible here: run any pointer-round-trip-sensitive program through
// this and a truncation SIGSEGVs or reads the wrong value.
func CompileAndRunArm64HighHeap(t *testing.T, src string) (stdout string, exitCode int) {
	t.Helper()
	binPath, qemu := compileArm64BinOpts(t, src, arm64codegen.Options{HighHeapProbe: true})
	cmd := RunArm64Bin(qemu, binPath)
	out, _ := cmd.CombinedOutput()
	return finishArm64Run(t, cmd, out)
}

// CompileArm64Bin compiles src with the arm64 backend and links it
// (gcc, or the native backend under FERN_NATIVE_ASM=1), returning the
// binary path and the qemu runner ("" on native arm64 hosts). Callers
// exec it via RunArm64Bin — with extra argv when the test needs it
// (e.g. the args()-rc regression gate).
func CompileArm64Bin(t *testing.T, src string) (binPath, qemu string) {
	t.Helper()
	return compileArm64BinOpts(t, src, arm64codegen.Options{})
}

// compileArm64BinOpts is CompileArm64Bin with the emit options spelled out —
// the seam the high-heap gate uses (FERN_HIGH_HEAP=1 on the compiler).
func compileArm64BinOpts(t *testing.T, src string, opts arm64codegen.Options) (binPath, qemu string) {
	t.Helper()
	qemu = Arm64Runner(t)
	var env []string
	if opts.HighHeapProbe {
		env = []string{"FERN_HIGH_HEAP=1"}
	}
	return compileSelfHostProgram(t, TargetArm64Linux, src, env), qemu
}

// finishArm64Run turns a completed run into (stdout, exit code), failing
// the test on an abnormal (non-exited) end.
func finishArm64Run(t *testing.T, cmd *exec.Cmd, out []byte) (string, int) {
	t.Helper()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("arm64 binary did not exit normally (out=%q)", out)
	}
	return string(out), cmd.ProcessState.ExitCode()
}
