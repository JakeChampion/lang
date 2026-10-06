// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/testing/e2e and
// internal/testing/e2ecompiler (#4398 part 3). Extracted verbatim from
// internal/testing/e2e/x86_64_test.go.
package e2eharness

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// X86_64Tooling locates the gcc cross-compiler used to link
// the emitted asm. `qemu-x86_64` is optional — when the host
// is already x86_64 Linux the binary runs natively. Returns
// the binary executor command line (qemu prefix or empty).
//
// FERN_REQUIRE_X86_64_TOOLING=1 turns the skip into a failure, for a lane that
// has the toolchain and is the only place a test runs — the mirror of
// FERN_REQUIRE_ARM64_TOOLING. It belongs on the x86_64 leg alone: the aarch64
// leg carries no x86 cross-compiler by design, so requiring it there would
// trade a legitimate skip for an infrastructure red.
func X86_64Tooling(t testing.TB) (gcc string, exec_ []string) {
	t.Helper()
	gcc, exec_, ok := LookupX86_64Tooling()
	if !ok {
		why := "non-x86_64 host and no qemu-x86_64 on PATH"
		if gcc == "" {
			why = "no x86_64-linux-gnu-gcc / gcc on PATH"
		}
		if os.Getenv("FERN_REQUIRE_X86_64_TOOLING") == "1" {
			t.Fatalf("%s and FERN_REQUIRE_X86_64_TOOLING=1: this lane has the "+
				"toolchain and is the only one running this test, so a skip here covers nothing", why)
		}
		t.Skipf("%s; skipping x86-64 e2e", why)
	}
	return gcc, exec_
}

// X86_64Runner is the runner half of X86_64Tooling alone: nil on a native
// x86-64 host, else qemu-x86_64, for a test that runs a pin-built x86-64
// binary and assembles nothing. It skips (or fails under
// FERN_REQUIRE_X86_64_TOOLING=1) when the host can run no x86-64 binary at all.
func X86_64Runner(t testing.TB) []string {
	t.Helper()
	if runner, ok := lookupX86_64Runner(); ok {
		return runner
	}
	if os.Getenv("FERN_REQUIRE_X86_64_TOOLING") == "1" {
		t.Fatal("non-x86_64 host and no qemu-x86_64 on PATH, and FERN_REQUIRE_X86_64_TOOLING=1: this lane has the toolchain and is the only one running this test, so a skip here covers nothing")
	}
	t.Skip("non-x86_64 host and no qemu-x86_64 on PATH; skipping x86-64 e2e")
	return nil
}

// LookupX86_64Tooling is X86_64Tooling's discovery half without the skip. See
// LookupArm64Tooling for why a caller would want it.
func LookupX86_64Tooling() (gcc string, exec_ []string, ok bool) {
	if p, err := exec.LookPath("x86_64-linux-gnu-gcc"); err == nil {
		gcc = p
	} else if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		// Bare `gcc` is x86-64 tooling only on an x86-64 host. Anywhere else
		// it produces the HOST's binaries, and handing it x86-64 asm gets
		// "unknown mnemonic" from the host assembler rather than a link.
		// LookupArm64Tooling gates its own bare-gcc fallback the same way.
		if p, err := exec.LookPath("gcc"); err == nil {
			gcc = p
		}
	}
	if gcc == "" {
		return "", nil, false
	}
	if runner, ok := lookupX86_64Runner(); ok {
		return gcc, runner, true
	}
	if InterpDriverMode() {
		// Interpret-the-driver mode: the caller only wants a driver's STDOUT
		// (an emitted .wat / .s), which InterpDriver produces without ever
		// linking or executing an x86-64 binary. No runner, and gcc is unused
		// downstream. A test that also execs a compiled x86 binary will fail
		// loudly on the missing runner rather than silently skipping. This arm
		// stays out of lookupX86_64Runner: X86_64Runner's callers exec a real
		// binary, so for them no runner is a skip, not a pass.
		return gcc, nil, true
	}
	return gcc, nil, false
}

// lookupX86_64Runner decides how an x86-64 Linux binary is run, for both
// X86_64Tooling and X86_64Runner: natively on an x86-64 host (no qemu
// transition overhead), else through qemu-x86_64 so the same suite passes on
// an aarch64 dev box. ok is false when the host can run one neither way.
func lookupX86_64Runner() (runner []string, ok bool) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return nil, true
	}
	if p, err := exec.LookPath("qemu-x86_64"); err == nil {
		return []string{p}, true
	}
	return nil, false
}

// RunX86_64Bin builds the exec.Cmd for running an x86-64 Linux binary either
// natively (when `runner` is empty — we're already on x86-64) or via the
// qemu-x86_64 prefix X86_64Tooling returned. Centralises the "qemu prefix or
// not" dispatch so callers don't sprinkle the same conditional through every
// test. Mirrors RunArm64Bin.
//
// Every exec of an emitted x86-64 binary (or of a self-host driver, which is
// one) goes through here. binfmt_misc can make a bare exec appear to work on an
// aarch64 host, but a program that mmaps its arena then SIGSEGVs where the
// explicit qemu-x86_64 prefix runs it correctly.
func RunX86_64Bin(runner []string, binPath string, args ...string) *exec.Cmd {
	if len(runner) == 0 {
		return exec.Command(binPath, args...)
	}
	argv := append(append(append([]string{}, runner[1:]...), binPath), args...)
	return exec.Command(runner[0], argv...)
}

// CompileAndRunX86_64 compiles `src`, links it as a static
// Linux x86-64 ELF, runs it, and returns (combined-output,
// exit-code). Mirrors the arm64 helper's shape so the tests
// look symmetric.
func CompileAndRunX86_64(t testing.TB, src string) (stdout string, exitCode int) {
	t.Helper()
	binPath, runner := CompileX86_64Bin(t, src)
	cmd := RunX86_64Bin(runner, binPath)
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

// CompileX86_64Bin compiles src with the current self-host compiler for
// x86-64 Linux, returning the binary path and the runner (empty on native
// x86-64 hosts). Callers exec it via RunX86_64Bin when they need to wire the
// child's streams themselves. The arm64 sibling is CompileArm64Bin.
func CompileX86_64Bin(t testing.TB, src string) (binPath string, runner []string) {
	t.Helper()
	runner = X86_64Runner(t)
	return CompileSelfHostSource(t, TargetX86_64Linux, src, nil), runner
}

// x86MachinePrefix is what `gcc -dumpmachine` starts with for a compiler that
// targets x86-64: `x86_64-linux-gnu`, `x86_64-pc-linux-gnu`, and so on.
const x86MachinePrefix = "x86_64-"
