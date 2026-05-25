package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
)

// freelistReuseSrc is the shared body for the flag-on freelist
// tests across backends: same-size reuse, different-class
// non-aliasing, and LIFO order. Each program returns 0 on success.
var freelistReuseSrc = struct{ reuse, wrongClass, lifo string }{
	reuse: `import "core/no_prelude";
function main(): i32 {
    var a: usize = __alloc(64);
    __free(a, 64);
    var b: usize = __alloc(64);
    if (a == b) { return 0; }
    return 1;
}`,
	wrongClass: `import "core/no_prelude";
function main(): i32 {
    var a: usize = __alloc(64);
    __free(a, 64);
    var b: usize = __alloc(32);
    if (a == b) { return 1; }
    return 0;
}`,
	lifo: `import "core/no_prelude";
function main(): i32 {
    var a: usize = __alloc(48);
    var b: usize = __alloc(48);
    __free(a, 48);
    __free(b, 48);
    var c: usize = __alloc(48);
    var d: usize = __alloc(48);
    if (c == b) {
        if (d == a) { return 0; }
        return 1;
    }
    return 2;
}`,
}

// compileAndRunX86_64FreeOn compiles + runs `src` with the Phase 3
// step-4 freelist enabled (ast.RcFreeEnabled = true) for the
// duration of codegen. Mirrors compileAndRunX86_64 but flips the
// flag under CodegenMu so __fern_alloc reuses freed blocks and
// __fern_free populates the freelist.
func compileAndRunX86_64FreeOn(t *testing.T, src string) (string, int) {
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
	// x86_64.Emit acquires ast.CodegenMu itself, so we must NOT hold
	// it here (the mutex isn't re-entrant). These tests don't call
	// t.Parallel, so they run in the sequential phase with no other
	// Emit racing the flag.
	ast.RcFreeEnabled = true
	asm, emitErr := x86_64.Emit(prog, info)
	ast.RcFreeEnabled = false
	if emitErr != nil {
		t.Fatalf("emit: %v", emitErr)
	}
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
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

// compileAndRunArm64FreeOn mirrors compileAndRunArm64 but flips
// ast.RcFreeEnabled around codegen. arm64codegen.Emit acquires
// ast.CodegenMu itself, so (as on x86) we must not hold it here.
func compileAndRunArm64FreeOn(t *testing.T, src string) (string, int) {
	t.Helper()
	gcc, qemu := arm64Tooling(t)
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
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	ast.RcFreeEnabled = true
	asm, emitErr := arm64codegen.Emit(prog, info)
	ast.RcFreeEnabled = false
	if emitErr != nil {
		t.Fatalf("emit: %v", emitErr)
	}
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	cmd := runArm64Bin(qemu, binPath)
	out, _ := cmd.CombinedOutput()
	_ = out
	return "", cmd.ProcessState.ExitCode()
}

// Phase 3 step-4: the freelist allocator, in isolation. A freed
// block is reused by the next same-size __alloc; a different size
// class is not aliased; and the bump path still works for sizes
// outside the freelist range. Validates the mechanism end-to-end
// before it's wired into the rc dec sites.
func TestX86_64FreelistReuse(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, freelistReuseSrc.reuse); code != 0 {
		t.Errorf("same-size reuse: got %d, want 0 (freed block should be reused)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, freelistReuseSrc.wrongClass); code != 0 {
		t.Errorf("wrong-class reuse: got %d, want 0 (32-byte alloc must not reuse a 64-byte free)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, freelistReuseSrc.lifo); code != 0 {
		t.Errorf("LIFO reuse: got %d, want 0 (c==b, d==a)", code)
	}
}

// Wasm mirror of TestX86_64FreelistReuse. Sets ast.RcFreeEnabled
// around runWasm (buildComponent reads it at emit time; wasm
// codegen doesn't take CodegenMu, and this test isn't parallel).
// SKIPs without wasmtime (rides CI). Uses the auto-prelude (no
// core/no_prelude import) to dodge the wasm harness's
// no_prelude output-parsing quirk.
func TestWASMFreelistReuse(t *testing.T) {
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = false }()

	reuse := `function main(): i32 {
    var a: usize = __alloc(64);
    __free(a, 64);
    var b: usize = __alloc(64);
    if (a == b) { return 0; }
    return 1;
}`
	if got := runWasm(t, reuse); got != 0 {
		t.Errorf("same-size reuse: got %d, want 0 (freed block should be reused)", got)
	}

	wrongClass := `function main(): i32 {
    var a: usize = __alloc(64);
    __free(a, 64);
    var b: usize = __alloc(32);
    if (a == b) { return 1; }
    return 0;
}`
	if got := runWasm(t, wrongClass); got != 0 {
		t.Errorf("wrong-class reuse: got %d, want 0 (32-byte alloc must not reuse a 64-byte free)", got)
	}

	lifo := `function main(): i32 {
    var a: usize = __alloc(48);
    var b: usize = __alloc(48);
    __free(a, 48);
    __free(b, 48);
    var c: usize = __alloc(48);
    var d: usize = __alloc(48);
    if (c == b) {
        if (d == a) { return 0; }
        return 1;
    }
    return 2;
}`
	if got := runWasm(t, lifo); got != 0 {
		t.Errorf("LIFO reuse: got %d, want 0 (c==b, d==a)", got)
	}
}

// Arm64 mirror of TestX86_64FreelistReuse. SKIPs without an
// aarch64 toolchain (rides CI).
func TestArm64FreelistReuse(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, freelistReuseSrc.reuse); code != 0 {
		t.Errorf("same-size reuse: got %d, want 0 (freed block should be reused)", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, freelistReuseSrc.wrongClass); code != 0 {
		t.Errorf("wrong-class reuse: got %d, want 0 (32-byte alloc must not reuse a 64-byte free)", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, freelistReuseSrc.lifo); code != 0 {
		t.Errorf("LIFO reuse: got %d, want 0 (c==b, d==a)", code)
	}
}
