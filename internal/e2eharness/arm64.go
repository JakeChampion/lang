// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/arm64_test.go.
package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	nativearm64 "github.com/jakechampion/lang/internal/native/arm64"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// Arm64Tooling locates the C linker used to assemble the
// generated asm and the runner used to execute the resulting
// binary. On a native arm64 Linux host the system `gcc` is
// already arm64 and the binary runs without an emulator, so
// `qemu` comes back empty. On x86 hosts (the historical CI
// shape) we need the aarch64 cross-toolchain and qemu-aarch64;
// the test SKIPs cleanly if neither path is available.
func Arm64Tooling(t *testing.T) (gcc, qemu string) {
	t.Helper()
	gcc, qemu, ok := LookupArm64Tooling()
	if !ok {
		t.Skipf("aarch64 cross toolchain not available (gcc=%q qemu=%q)", gcc, qemu)
	}
	return gcc, qemu
}

// LookupArm64Tooling is Arm64Tooling's discovery half without the skip, for a
// caller that has to decide for itself what a missing toolchain means — a test
// needing BOTH register backends at once has no lane where a skip is the honest
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

// CompileArm64Bin compiles src with the arm64 backend and links it
// (gcc, or the native backend under FERN_NATIVE_ASM=1), returning the
// binary path and the qemu runner ("" on native arm64 hosts). Callers
// exec it via RunArm64Bin — with extra argv when the test needs it
// (e.g. the args()-rc regression gate).
func CompileArm64Bin(t *testing.T, src string) (binPath, qemu string) {
	t.Helper()
	gcc, qemu := Arm64Tooling(t)

	// Route the source through modload so cross-module qualified
	// imports inside the stdlib (e.g. `int.int_to_string_radix(…)`
	// in std/i32) get the proper rewriting — that's the same
	// pipeline `cmd/fern` uses. Without this, bare in-source
	// qualified calls would hit "undefined identifier" because
	// modload's rewriter is the only thing that recognises the
	// `mod.fn(args)` shape.
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	// Monomorphise generic functions before codegen — the
	// production driver (cmd/fern) always runs this; the e2e
	// harness was missing it which only mattered once OpCallDirect
	// started consulting per-arg types for SysV register allocation
	// under the two-word string ABI.
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := arm64codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	asmPath := filepath.Join(dir, "prog.s")
	binPath = filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	// FERN_NATIVE_ASM=1 routes the assemble+link step through the pure-Go
	// native backend instead of gcc — used to audit native coverage across
	// the whole arm64 e2e suite. Default (unset) keeps the gcc path.
	if os.Getenv("FERN_NATIVE_ASM") != "" {
		text, rodata, err := nativearm64.AssembleProgram(asm, nativeelf.TextVAddr)
		if err != nil {
			t.Fatalf("NATIVE-ASM-FAIL: %v\n--- asm ---\n%s", err, asm)
		}
		if err := os.WriteFile(binPath, nativeelf.StaticExecutableData(text, rodata), 0o755); err != nil {
			t.Fatalf("write native bin: %v", err)
		}
	} else if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	return binPath, qemu
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
