package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// --- Phase 3 step-4 flip-readiness gate ---------------------------
//
// Freeing is semantically invisible: a program must produce
// byte-identical stdout + the same exit code whether or not the
// freelist is enabled. These tests run the entire data-driven
// fixture corpus (testdata/cases) BOTH flag-off and flag-on and
// assert the two runs agree. Any divergence is a reclamation bug
// (a freed-then-reused block that was still referenced) surfaced by
// a real program — exactly the evidence needed before flipping
// ast.RcFreeEnabled on by default. This is the plan's step-5
// "every test program runs identically before/after" gate, applied
// to the fixture corpus.

// fixtureFreeOnRunner emits `mainPath` with ast.RcFreeEnabled set,
// using the same per-backend pipeline as the normal fixture runner.
func runFixtureX86_64FreeOn(t *testing.T, mainPath, stdin string) (string, int) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	info, prog := loadCheckMono(t, mainPath)
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	asm, err := x86_64.Emit(prog, info)
	ast.RcFreeEnabled = prev
	if err != nil {
		t.Fatalf("x86_64 emit (free-on): %v", err)
	}
	bin := linkAsm(t, gcc, asm, "-static", "-nostdlib", "-no-pie")
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	return runBin(cmd, stdin)
}

func runFixtureArm64FreeOn(t *testing.T, mainPath, stdin string) (string, int) {
	t.Helper()
	gcc, qemu := arm64Tooling(t)
	info, prog := loadCheckMono(t, mainPath)
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	asm, err := arm64codegen.Emit(prog, info)
	ast.RcFreeEnabled = prev
	if err != nil {
		t.Fatalf("arm64 emit (free-on): %v", err)
	}
	bin := linkAsm(t, gcc, asm, "-static", "-nostdlib")
	cmd := runArm64Bin(qemu, bin)
	return runBin(cmd, stdin)
}

// forEachRunnableFixture walks testdata/cases and invokes fn for
// every non-compile-error fixture that targets `backend`.
func forEachRunnableFixture(t *testing.T, backend string, fn func(t *testing.T, f *fixtureSpec)) {
	t.Helper()
	root := "testdata/cases"
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir, err := filepath.Abs(filepath.Join(root, e.Name()))
		if err != nil {
			t.Fatalf("abs: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "main.fern")); err != nil {
			continue
		}
		f := loadFixture(t, dir)
		if f.compileError || !f.backends[backend] {
			continue
		}
		t.Run(f.name, func(t *testing.T) { fn(t, f) })
	}
}

func TestX86_64FixturesFreeMatchesNoFree(t *testing.T) {
	forEachRunnableFixture(t, "x86_64", func(t *testing.T, f *fixtureSpec) {
		prev := ast.RcFreeEnabled
		ast.RcFreeEnabled = false
		outOff, exitOff := runFixtureX86_64(t, f.mainPath, f.stdin)
		ast.RcFreeEnabled = prev
		outOn, exitOn := runFixtureX86_64FreeOn(t, f.mainPath, f.stdin)
		if outOff != outOn || exitOff != exitOn {
			t.Errorf("free-on diverged from free-off:\n off=(exit %d) %q\n on =(exit %d) %q", exitOff, outOff, exitOn, outOn)
		}
	})
}

func TestArm64FixturesFreeMatchesNoFree(t *testing.T) {
	forEachRunnableFixture(t, "arm64", func(t *testing.T, f *fixtureSpec) {
		prev := ast.RcFreeEnabled
		ast.RcFreeEnabled = false
		outOff, exitOff := runFixtureArm64(t, f.mainPath, f.stdin)
		ast.RcFreeEnabled = prev
		outOn, exitOn := runFixtureArm64FreeOn(t, f.mainPath, f.stdin)
		if outOff != outOn || exitOff != exitOn {
			t.Errorf("free-on diverged from free-off:\n off=(exit %d) %q\n on =(exit %d) %q", exitOff, outOff, exitOn, outOn)
		}
	})
}

func TestWASMFixturesFreeMatchesNoFree(t *testing.T) {
	forEachRunnableFixture(t, "wasm", func(t *testing.T, f *fixtureSpec) {
		prev := ast.RcFreeEnabled
		ast.RcFreeEnabled = false
		outOff, exitOff := runFixtureWasm(t, f.mainPath, f.stdin)
		ast.RcFreeEnabled = true
		outOn, exitOn := runFixtureWasm(t, f.mainPath, f.stdin)
		ast.RcFreeEnabled = prev
		if outOff != outOn || exitOff != exitOn {
			t.Errorf("free-on diverged from free-off:\n off=(exit %d) %q\n on =(exit %d) %q", exitOff, outOff, exitOn, outOn)
		}
	})
}

// --- end flip-readiness gate --------------------------------------

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
	// Route through modload (not bare parser.Parse) so the program's
	// std/ + core/ imports resolve — without it core/map's runtime
	// impls (map_new_impl / __map_*_impl) never load and the link
	// fails. Mirrors compileAndRunX86_64 / compileAndRunArm64FreeOn.
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
	// x86_64.Emit acquires ast.CodegenMu itself, so we must NOT hold
	// it here (the mutex isn't re-entrant). These tests don't call
	// t.Parallel, so they run in the sequential phase with no other
	// Emit racing the flag.
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	asm, emitErr := x86_64.Emit(prog, info)
	ast.RcFreeEnabled = prev
	if emitErr != nil {
		t.Fatalf("emit: %v", emitErr)
	}
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
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	asm, emitErr := arm64codegen.Emit(prog, info)
	ast.RcFreeEnabled = prev
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

// arrayDropFreeReuseSrc builds a 3-element array of structs
// (rc-tracked elements → __fern_drop_arr_ptr) inside a helper,
// returns a scalar so the array is dropped at the helper's exit,
// and calls it 50× from a loop. Flag-on, each call's buffer is
// freed and the next same-size call reuses it; if free/reuse
// corrupted memory the read-back value would drift, so the
// folded check stays 0 only if every reuse is sound. Also asserts
// 0 over-releases.
const arrayDropFreeReuseSrc = `struct Foo { v: i32 }
function consume(n: i32): i32 {
    var fs: Foo[] = [Foo { v: n }, Foo { v: n + 1 }, Foo { v: n + 2 }];
    return fs[2].v;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        acc = acc + (consume(i) - (i + 2));
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`

// pushLoopFreeSrc is the headline push-loop case: 200 grows of a
// plain i32[]. Flag-on, each copy-grow frees the OLD buffer
// (dec-on-overwrite → __fern_arr_dec → __free), which the next grow
// reuses from the freelist — the O(N²)→O(N) reclamation. If a freed
// buffer were handed out while still referenced, the read-back sum
// would be wrong; 0 only if every free+reuse is sound. Sum
// 0..199 = 19900.
const pushLoopFreeSrc = `function build(): i32 {
    var xs: i32[] = [];
    var i: i32 = 0;
    while (i < 200) {
        xs = xs.push(i);
        i = i + 1;
    }
    var sum: i32 = 0;
    var j: i32 = 0;
    while (j < xs.len()) {
        sum = sum + xs[j];
        j = j + 1;
    }
    return sum;
}
function main(): i32 {
    return (build() - 19900) + __rc_underflow_count();
}`

// Phase 3 step-4: arrays free their buffer when rc hits 0 (flag-on).
// This exercises __fern_drop_arr_ptr's tail-free + freelist reuse
// across 50 build/drop cycles, and re-runs the whole rc-correctness
// corpus with free actually happening — the use-after-free net for
// the eventual flag flip.
func TestX86_64ArrayDropFree(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, arrayDropFreeReuseSrc); code != 0 {
		t.Errorf("drop+free+reuse: got %d, want 0 (a corrupted reuse or over-release would drift)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, pushLoopFreeSrc); code != 0 {
		t.Errorf("push-loop free+reuse: got %d, want 0", code)
	}
	for _, c := range rcCorpus {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s (free-on): got %d, want 0 (UAF / corruption when blocks are freed+reused)", c.name, code)
			}
		})
	}
}

func TestArm64ArrayDropFree(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, arrayDropFreeReuseSrc); code != 0 {
		t.Errorf("drop+free+reuse: got %d, want 0", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, pushLoopFreeSrc); code != 0 {
		t.Errorf("push-loop free+reuse: got %d, want 0", code)
	}
	for _, c := range rcCorpus {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s (free-on): got %d, want 0 (UAF / corruption)", c.name, code)
			}
		})
	}
}

func TestWASMArrayDropFree(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, arrayDropFreeReuseSrc); got != 0 {
		t.Errorf("drop+free+reuse: got %d, want 0", got)
	}
	if got := runWasm(t, pushLoopFreeSrc); got != 0 {
		t.Errorf("push-loop free+reuse: got %d, want 0", got)
	}
	for _, c := range rcCorpus {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("%s (free-on): got %d, want 0 (UAF / corruption)", c.name, got)
			}
		})
	}
}

// Wasm mirror of TestX86_64FreelistReuse. Sets ast.RcFreeEnabled
// around runWasm (buildComponent reads it at emit time; wasm
// codegen doesn't take CodegenMu, and this test isn't parallel).
// SKIPs without wasmtime (rides CI). Uses the auto-prelude (no
// core/no_prelude import) to dodge the wasm harness's
// no_prelude output-parsing quirk.
func TestWASMFreelistReuse(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

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

// --- Phase 5 slice 5a: __fern_alloc_reuse in isolation ------------
//
// allocReuseSrc is the shared body for the flag-on drop-reuse (FBIP)
// primitive across backends. The pairing analysis (slices 5b+) is not
// wired yet, so these call the `__alloc_reuse(token, tokenSize, size)`
// shim directly to prove the three runtime branches:
//
//   - sameClass: a live token whose size class matches `size` is
//     handed straight back — in-place storage reuse (b == a).
//   - nullToken: a 0 token degrades to a plain allocation, returning a
//     fresh block distinct from any live one (b != 0, b != a).
//   - mismatch: a token whose class differs is freed (not leaked) and
//     a fresh block of the requested class is returned (b != a); the
//     freed token then reappears from its own class's freelist on the
//     next same-class __alloc (c == a) — the slow-not-wrong backstop.
//
// Each program returns 0 on success. Natives import core/no_prelude
// (matching freelistReuseSrc); the wasm variants omit it (the wasm
// harness uses the auto-prelude).
var allocReuseSrc = struct{ sameClass, nullToken, mismatch string }{
	sameClass: `import "core/no_prelude";
function main(): i32 {
    var a: usize = __alloc(64);
    var b: usize = __alloc_reuse(a, 64, 64);
    if (a == b) { return 0; }
    return 1;
}`,
	nullToken: `import "core/no_prelude";
function main(): i32 {
    var z: usize = 0;
    var a: usize = __alloc(64);
    var b: usize = __alloc_reuse(z, 0, 64);
    if (b == 0) { return 1; }
    if (b == a) { return 2; }
    return 0;
}`,
	mismatch: `import "core/no_prelude";
function main(): i32 {
    var a: usize = __alloc(64);
    var b: usize = __alloc_reuse(a, 64, 32);
    if (a == b) { return 1; }
    var c: usize = __alloc(64);
    if (a == c) { return 0; }
    return 2;
}`,
}

func TestX86_64AllocReuse(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, allocReuseSrc.sameClass); code != 0 {
		t.Errorf("same-class reuse: got %d, want 0 (token should be returned in place)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, allocReuseSrc.nullToken); code != 0 {
		t.Errorf("null-token alloc: got %d, want 0 (must allocate a fresh distinct block)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, allocReuseSrc.mismatch); code != 0 {
		t.Errorf("class-mismatch: got %d, want 0 (free token + fresh alloc; freed block reusable)", code)
	}
}

func TestArm64AllocReuse(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, allocReuseSrc.sameClass); code != 0 {
		t.Errorf("same-class reuse: got %d, want 0 (token should be returned in place)", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, allocReuseSrc.nullToken); code != 0 {
		t.Errorf("null-token alloc: got %d, want 0 (must allocate a fresh distinct block)", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, allocReuseSrc.mismatch); code != 0 {
		t.Errorf("class-mismatch: got %d, want 0 (free token + fresh alloc; freed block reusable)", code)
	}
}

// Wasm mirror. SKIPs without wasmtime (rides CI). Sets RcFreeEnabled
// around runWasm like TestWASMFreelistReuse.
func TestWASMAllocReuse(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	sameClass := `function main(): i32 {
    var a: usize = __alloc(64);
    var b: usize = __alloc_reuse(a, 64, 64);
    if (a == b) { return 0; }
    return 1;
}`
	if got := runWasm(t, sameClass); got != 0 {
		t.Errorf("same-class reuse: got %d, want 0 (token should be returned in place)", got)
	}

	nullToken := `function main(): i32 {
    var z: usize = 0;
    var a: usize = __alloc(64);
    var b: usize = __alloc_reuse(z, 0, 64);
    if (b == 0) { return 1; }
    if (b == a) { return 2; }
    return 0;
}`
	if got := runWasm(t, nullToken); got != 0 {
		t.Errorf("null-token alloc: got %d, want 0 (must allocate a fresh distinct block)", got)
	}

	mismatch := `function main(): i32 {
    var a: usize = __alloc(64);
    var b: usize = __alloc_reuse(a, 64, 32);
    if (a == b) { return 1; }
    var c: usize = __alloc(64);
    if (a == c) { return 0; }
    return 2;
}`
	if got := runWasm(t, mismatch); got != 0 {
		t.Errorf("class-mismatch: got %d, want 0 (free token + fresh alloc; freed block reusable)", got)
	}
}

// --- Phase 5b: self-overwrite struct reuse (FBIP) end-to-end -------
//
// These exercise `p = T{ ... }` reusing p's box in place. Correctness
// alone doesn't prove reuse fired (a fresh-alloc lowering gives the
// same values) — the IR test TestStructReuseFiresForSelfOverwrite pins
// that the __alloc_reuse path is taken; these pin that taking it stays
// value-correct and over-release-free, including the runtime alias
// decision and the read-before-overwrite ordering.
var structReuseSrc = struct{ churn, aliased, swap string }{
	// 200 self-overwrites reusing one box. churn(200).x == 200; folds
	// __rc_underflow_count() so any over-release in the reuse path trips.
	churn: `struct Point { x: i32, y: i32 }
function churn(n: i32): i32 {
    var p: Point = Point { x: 0, y: 0 };
    var i: i32 = 0;
    while (i < n) {
        p = Point { x: p.x + 1, y: p.y };
        i = i + 1;
    }
    return p.x;
}
function main(): i32 {
    return (churn(200) - 200) + __rc_underflow_count();
}`,
	// p is aliased (rc 2) before the overwrite, so the runtime is_unique
	// check must decline the in-place reuse and allocate a fresh box —
	// the alias q must still see the original {5,7}.
	aliased: `struct Point { x: i32, y: i32 }
function main(): i32 {
    var p: Point = Point { x: 5, y: 7 };
    var q: Point = p;
    p = Point { x: p.x + 1, y: p.y };
    if (q.x != 5) { return 1; }
    if (p.x != 6) { return 2; }
    return __rc_underflow_count();
}`,
	// Field swap: the reuse path must read BOTH source fields into temps
	// before overwriting either, or the second store reads a clobbered
	// field. Even churn → {1,2} (a==1); odd churn → {2,1} (a==2).
	swap: `struct Pair { a: i32, b: i32 }
function churn(n: i32): i32 {
    var p: Pair = Pair { a: 1, b: 2 };
    var i: i32 = 0;
    while (i < n) {
        p = Pair { a: p.b, b: p.a };
        i = i + 1;
    }
    return p.a;
}
function main(): i32 {
    return (churn(200) - 1) + (churn(201) - 2) + __rc_underflow_count();
}`,
}

// Phase 5c: pointer-field struct reuse. A single-word rc-tracked
// pointer field (array here) is carried over or replaced across the
// reuse. The rc balance is the delicate part — the carried-over array's
// eval-inc must cancel the reuse-branch dec-old, or it either
// over-releases (underflow != 0) or gets freed+reused (corrupt values).
var structPtrReuseSrc = struct{ carried, aliased, replaced string }{
	// 200 reuses carrying the SAME array field over unchanged. items
	// stays [10,20,30] (sum 60), id == n. Any rc drift corrupts items.
	carried: `struct Holder { id: i32, items: i32[] }
function churn(n: i32): i32 {
    var p: Holder = Holder { id: 0, items: [10, 20, 30] };
    var i: i32 = 0;
    while (i < n) {
        p = Holder { id: p.id + 1, items: p.items };
        i = i + 1;
    }
    return (p.id - n) + (p.items[0] + p.items[1] + p.items[2] - 60);
}
function main(): i32 {
    return churn(200) + __rc_underflow_count();
}`,
	// Aliased holder: q shares p's box (rc 2), so reuse declines and a
	// fresh box is allocated. q keeps its view; the array field is shared
	// (both see [7,8]); rc stays balanced.
	aliased: `struct Holder { id: i32, items: i32[] }
function main(): i32 {
    var p: Holder = Holder { id: 1, items: [7, 8] };
    var q: Holder = p;
    p = Holder { id: p.id + 1, items: p.items };
    if (q.id != 1) { return 1; }
    if (q.items[0] != 7) { return 2; }
    if (p.id != 2) { return 3; }
    if (p.items[1] != 8) { return 4; }
    return __rc_underflow_count();
}`,
	// Each iteration REPLACES the array field with a fresh one. The old
	// array's reference is released on the reuse branch (flat dec). Final
	// items == [n, n], id == n.
	replaced: `struct Holder { id: i32, items: i32[] }
function churn(n: i32): i32 {
    var p: Holder = Holder { id: 0, items: [0] };
    var i: i32 = 0;
    while (i < n) {
        p = Holder { id: p.id + 1, items: [p.id + 1, p.id + 1] };
        i = i + 1;
    }
    return (p.id - n) + (p.items[0] - n) + (p.items[1] - n);
}
function main(): i32 {
    return churn(100) + __rc_underflow_count();
}`,
}

// structReuseCases is the shared table every backend's struct-reuse
// test iterates: the 5b all-scalar shapes plus the 5c pointer-field
// shapes.
var structReuseCases = []struct{ name, src string }{
	{"churn", structReuseSrc.churn},
	{"aliased", structReuseSrc.aliased},
	{"swap", structReuseSrc.swap},
	{"ptr_carried", structPtrReuseSrc.carried},
	{"ptr_aliased", structPtrReuseSrc.aliased},
	{"ptr_replaced", structPtrReuseSrc.replaced},
}

func TestX86_64StructReuse(t *testing.T) {
	for _, c := range structReuseCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}

func TestArm64StructReuse(t *testing.T) {
	for _, c := range structReuseCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}

func TestWASMStructReuse(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, c := range structReuseCases {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("%s: got %d, want 0", c.name, got)
			}
		})
	}
}
