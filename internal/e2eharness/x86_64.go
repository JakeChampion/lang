// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/x86_64_test.go.
package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// X86_64Tooling locates the gcc cross-compiler used to link
// the emitted asm. `qemu-x86_64` is optional — when the host
// is already x86_64 Linux the binary runs natively. Returns
// the binary executor command line (qemu prefix or empty).
func X86_64Tooling(t *testing.T) (gcc string, exec_ []string) {
	t.Helper()
	gcc, exec_, ok := LookupX86_64Tooling()
	if !ok {
		if gcc == "" {
			t.Skip("no x86_64-linux-gnu-gcc / gcc on PATH; skipping x86-64 e2e")
		}
		t.Skip("non-x86_64 host and no qemu-x86_64 on PATH; skipping x86-64 e2e")
	}
	return gcc, exec_
}

// LookupX86_64Tooling is X86_64Tooling's discovery half without the skip. See
// LookupArm64Tooling for why a caller would want it.
func LookupX86_64Tooling() (gcc string, exec_ []string, ok bool) {
	for _, c := range []string{"x86_64-linux-gnu-gcc", "gcc"} {
		if p, err := exec.LookPath(c); err == nil {
			gcc = p
			break
		}
	}
	if gcc == "" {
		return "", nil, false
	}
	// Pick the runner. Native exec is preferred — no qemu
	// transition overhead — but we'll fall back to
	// qemu-x86_64 on non-x86_64 hosts so the same test suite
	// passes on aarch64 dev boxes.
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return gcc, nil, true
	}
	if p, err := exec.LookPath("qemu-x86_64"); err == nil {
		return gcc, []string{p}, true
	}
	if InterpDriverMode() {
		// Interpret-the-driver mode: the caller only wants a driver's STDOUT
		// (an emitted .wat / .s), which InterpDriver produces without ever
		// linking or executing an x86-64 binary. No runner, and gcc is unused
		// downstream. A test that also execs a compiled x86 binary will fail
		// loudly on the missing runner rather than silently skipping.
		return gcc, nil, true
	}
	return gcc, nil, false
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
func CompileAndRunX86_64(t *testing.T, src string) (stdout string, exitCode int) {
	t.Helper()
	binPath, runner := CompileX86_64Bin(t, src)
	cmd := RunX86_64Bin(runner, binPath)
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

// CompileX86_64Bin compiles src with the x86-64 backend and links it,
// returning the binary path and the runner (empty on native x86-64 hosts).
// Callers exec it via RunX86_64Bin when they need to wire the child's
// streams themselves. The arm64 sibling is CompileArm64Bin.
func CompileX86_64Bin(t *testing.T, src string) (binPath string, runner []string) {
	t.Helper()
	gcc, runner := X86_64Tooling(t)

	// Route the source through modload (write to temp file, then
	// `modload.Load`) so cross-module qualified calls in the stdlib
	// (`int.int_to_string_radix(…)` etc.) get rewritten. A bare
	// `parser.Parse` skips modload's rewriter entirely. Mirrors
	// `CompileAndRunArm64`.
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
	// Monomorphise generic functions before codegen — the production
	// driver (cmd/fern) always runs this, and x86_64.Emit documents that
	// it expects a checked + monomorphised program. This harness was
	// missing the pass (the arm64 sibling CompileAndRunArm64 already runs
	// it). Feeding Emit an un-monomorphised program leaves generic
	// instantiations unspecialised; that latent gap only surfaced as a
	// wrong differential result once a heap-layout shift (the core/int
	// to_string rewrite) perturbed it into view. Mirrors CompileAndRunArm64.
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	asmPath := filepath.Join(dir, "prog.s")
	binPath = filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	return binPath, runner
}
