// x86-64 (amd64) Linux end-to-end tests. Third native backend
// alongside arm64 and the WASM family — emits System V AMD64
// assembly + Linux x86-64 syscall wiring.
//
// Tests run the compiled binary natively when the host is
// x86_64 Linux (no qemu needed); otherwise fall back to
// qemu-x86_64 in user-mode-emulation. Each test SKIPs (rather
// than fails) when the cross-compiler or qemu isn't installed,
// so CI on aarch64-only hosts doesn't hard-fail.
//
// First-PR scope (the "scaffolding + exit code" cut from the
// docs/LANGUAGE-DIRECTION.md x86-64 plan): exactly one
// shape — `function main(): i32 { return N; }`. Subsequent PRs
// add arithmetic / control flow, then strings + the alloc
// runtime, then composite types, then TCP + HTTP, then the
// parked `ir.TailCallOptimize` pass.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/parser"
)

// x86_64Tooling locates the gcc cross-compiler used to link
// the emitted asm. `qemu-x86_64` is optional — when the host
// is already x86_64 Linux the binary runs natively. Returns
// the binary executor command line (qemu prefix or empty).
func x86_64Tooling(t *testing.T) (gcc string, exec_ []string) {
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
	t.Skip("non-x86_64 host and no qemu-x86_64 on PATH; skipping x86-64 e2e")
	return "", nil
}

// compileAndRunX86_64 compiles `src`, links it as a static
// Linux x86-64 ELF, runs it, and returns (combined-output,
// exit-code). Mirrors the arm64 helper's shape so the tests
// look symmetric.
func compileAndRunX86_64(t *testing.T, src string) (stdout string, exitCode int) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)

	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	dir := t.TempDir()
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

// `function main(): i32 { return N; }` is the smallest
// program shape that exercises the entire emit pipeline:
// _start prologue, main's prologue/epilogue, OpConstI32,
// OpReturn, the exit_group syscall wiring. If this passes
// the toolchain + test harness are wired correctly and
// follow-up PRs can layer on arithmetic / control flow.
func TestX86_64ExitCode(t *testing.T) {
	for _, want := range []int{0, 1, 42, 255} {
		want := want
		t.Run("", func(t *testing.T) {
			src := "function main(): i32 { return " + intToString(want) + "; }"
			_, code := compileAndRunX86_64(t, src)
			if code != want {
				t.Errorf("return %d → exit = %d", want, code)
			}
		})
	}
}
