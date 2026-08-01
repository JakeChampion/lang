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
	for _, c := range []string{"x86_64-linux-gnu-gcc", "gcc"} {
		if p, err := exec.LookPath(c); err == nil {
			gcc = p
			break
		}
	}
	if gcc == "" {
		t.Skip("no x86_64-linux-gnu-gcc / gcc on PATH; skipping x86-64 e2e")
	}
	// Pick the runner. Native exec is preferred — no qemu
	// transition overhead — but we'll fall back to
	// qemu-x86_64 on non-x86_64 hosts so the same test suite
	// passes on aarch64 dev boxes.
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return gcc, nil
	}
	if p, err := exec.LookPath("qemu-x86_64"); err == nil {
		return gcc, []string{p}
	}
	if InterpDriverMode() {
		// Interpret-the-driver mode: the caller only wants a driver's STDOUT
		// (an emitted .wat / .s), which InterpDriver produces without ever
		// linking or executing an x86-64 binary. No runner, and gcc is unused
		// downstream. A test that also execs a compiled x86 binary will fail
		// loudly on the missing runner rather than silently skipping.
		return gcc, nil
	}
	t.Skip("non-x86_64 host and no qemu-x86_64 on PATH; skipping x86-64 e2e")
	return "", nil
}

// CompileAndRunX86_64 compiles `src`, links it as a static
// Linux x86-64 ELF, runs it, and returns (combined-output,
// exit-code). Mirrors the arm64 helper's shape so the tests
// look symmetric.
func CompileAndRunX86_64(t *testing.T, src string) (stdout string, exitCode int) {
	t.Helper()
	gcc, runner := X86_64Tooling(t)

	// Route the source through modload (write to temp file, then
	// `modload.Load`) so cross-module qualified calls in the
	// stdlib (`int.int_to_string_radix(…)` etc.) get rewritten.
	// Without this, the bare `parser.Parse` path used previously
	// skipped modload's rewriter entirely. Mirrors the same
	// refactor in `CompileAndRunArm64`.
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
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
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}
