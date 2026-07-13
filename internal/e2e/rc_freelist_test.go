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
		// t.Cleanup, not a straight-line restore: a t.Fatalf inside the
		// free-off runner (a link failure) exits this subtest with the flag
		// still false, poisoning every later test in the package — the CI
		// failure mode was one bad fixture cascading into unrelated
		// TestArm64* link errors.
		t.Cleanup(func() { ast.RcFreeEnabled = prev })
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
		t.Cleanup(func() { ast.RcFreeEnabled = prev }) // see the x86_64 sibling
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
		t.Cleanup(func() { ast.RcFreeEnabled = prev }) // see the x86_64 sibling
		outOff, exitOff := runFixtureWasm(t, f.mainPath, f.stdin)
		ast.RcFreeEnabled = true
		outOn, exitOn := runFixtureWasm(t, f.mainPath, f.stdin)
		ast.RcFreeEnabled = prev
		if outOff != outOn || exitOff != exitOn {
			t.Errorf("free-on diverged from free-off:\n off=(exit %d) %q\n on =(exit %d) %q", exitOff, outOff, exitOn, outOn)
		}
	})
}

// Reuse differential gate (RC-Perceus test contract): with free ON, the
// constructor-reuse / general FBIP layer (RcReuseEnabled) must produce
// byte-identical OUTPUT whether reuse fires or every reuse site falls back to a
// fresh alloc + the normal drop. This isolates a reuse bug from a plain free
// bug — reuse-on == reuse-off the same way free-on == free-off. Same corpus,
// same helpers; only RcReuseEnabled flips (free stays on for both runs).
func TestX86_64ReuseMatchesNoReuse(t *testing.T) {
	forEachRunnableFixture(t, "x86_64", func(t *testing.T, f *fixtureSpec) {
		outOn, exitOn := runFixtureX86_64FreeOn(t, f.mainPath, f.stdin)
		prev := ast.RcReuseEnabled
		ast.RcReuseEnabled = false
		t.Cleanup(func() { ast.RcReuseEnabled = prev }) // Fatalf-safe restore, like the free tests
		outOff, exitOff := runFixtureX86_64FreeOn(t, f.mainPath, f.stdin)
		ast.RcReuseEnabled = prev
		if outOff != outOn || exitOff != exitOn {
			t.Errorf("reuse-on diverged from reuse-off:\n off=(exit %d) %q\n on =(exit %d) %q", exitOff, outOff, exitOn, outOn)
		}
	})
}

func TestArm64ReuseMatchesNoReuse(t *testing.T) {
	forEachRunnableFixture(t, "arm64", func(t *testing.T, f *fixtureSpec) {
		outOn, exitOn := runFixtureArm64FreeOn(t, f.mainPath, f.stdin)
		prev := ast.RcReuseEnabled
		ast.RcReuseEnabled = false
		t.Cleanup(func() { ast.RcReuseEnabled = prev }) // Fatalf-safe restore, like the free tests
		outOff, exitOff := runFixtureArm64FreeOn(t, f.mainPath, f.stdin)
		ast.RcReuseEnabled = prev
		if outOff != outOn || exitOff != exitOn {
			t.Errorf("reuse-on diverged from reuse-off:\n off=(exit %d) %q\n on =(exit %d) %q", exitOff, outOff, exitOn, outOn)
		}
	})
}

func TestWASMReuseMatchesNoReuse(t *testing.T) {
	forEachRunnableFixture(t, "wasm", func(t *testing.T, f *fixtureSpec) {
		prevFree := ast.RcFreeEnabled
		ast.RcFreeEnabled = true
		prevReuse := ast.RcReuseEnabled
		// Fatalf-safe restore for both flags, like the free tests.
		t.Cleanup(func() {
			ast.RcReuseEnabled = prevReuse
			ast.RcFreeEnabled = prevFree
		})
		outOn, exitOn := runFixtureWasm(t, f.mainPath, f.stdin)
		ast.RcReuseEnabled = false
		outOff, exitOff := runFixtureWasm(t, f.mainPath, f.stdin)
		ast.RcReuseEnabled = prevReuse
		ast.RcFreeEnabled = prevFree
		if outOff != outOn || exitOff != exitOn {
			t.Errorf("reuse-on diverged from reuse-off:\n off=(exit %d) %q\n on =(exit %d) %q", exitOff, outOff, exitOn, outOn)
		}
	})
}

// --- end flip-readiness gate --------------------------------------

// freelistReuseSrc is the shared body for the flag-on freelist
// tests across backends: same-size reuse, different-class
// non-aliasing, and LIFO order. Each program returns 0 on success.
var freelistReuseSrc = struct{ reuse, wrongClass, lifo string }{
	reuse: `
function main(): i32 {
    var a: usize = __alloc(64);
    __free(a, 64);
    var b: usize = __alloc(64);
    if (a == b) { return 0; }
    return 1;
}`,
	wrongClass: `
function main(): i32 {
    var a: usize = __alloc(64);
    __free(a, 64);
    var b: usize = __alloc(32);
    if (a == b) { return 1; }
    return 0;
}`,
	lifo: `
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
	binPath, runner := compileX86_64FreeOn(t, src)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

// compileX86_64FreeOn is the compile half of compileAndRunX86_64FreeOn:
// it returns the built binary's path (in a test temp dir) plus the
// runner argv prefix, for tests that need to control HOW the binary is
// launched (e.g. rc_trmc_test.go pins RLIMIT_STACK before exec).
func compileX86_64FreeOn(t *testing.T, src string) (string, []string) {
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
	return binPath, runner
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
        xs = xs.append(i);
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

// stringReassignFreeSrc is the string analogue of pushLoopFreeSrc and
// the headline case for the Phase 1e-strings dec-on-overwrite: a string
// local reassigned in a loop frees its OLD heap buffer before taking the
// new one, so 300 concat-growth iterations reclaim+reuse instead of
// orphaning every intermediate. The concat RHS is a fresh rc=1 buffer
// (no alias-inc), so the dec-on-overwrite this slice adds is the only
// release of the prior buffer — a double-free or corrupted reuse would
// drift the length read-back or bump __rc_underflow_count.
//
// aliasHeap additionally covers the inc+dec-old balance when the RHS IS
// an aliased string ident (needsRcIncOnAlias fires): `a = b` frees a's
// old "a"+w buffer AND retains b's, leaving both bindings readable and
// the exit double-dec of b's shared buffer balanced. w="zz" → a="azz",
// b="bbzz", a=b ⇒ a.len()+b.len() = 4+4 = 8. Folded to 0 on success.
const stringReassignFreeSrc = `function build(): i32 {
    var s: string = "";
    var i: i32 = 0;
    while (i < 300) {
        s = s + "x";
        i = i + 1;
    }
    return s.len();
}
function aliasHeap(w: string): i32 {
    var a: string = "a" + w;
    var b: string = "bb" + w;
    a = b;
    return a.len() + b.len();
}
function main(): i32 {
    var grown: i32 = build() - 300;
    var aliased: i32 = aliasHeap("zz") - 8;
    return grown + aliased + __rc_underflow_count();
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
			if c.skipWasm != "" {
				t.Skip(c.skipWasm)
			}
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("%s (free-on): got %d, want 0 (UAF / corruption)", c.name, got)
			}
		})
	}
}

// Phase 1e-strings: a string local frees its old heap buffer on
// reassignment (dec-on-overwrite in assign(), gated RcFreeEnabled &&
// freeEligible). These run the concat-loop reclaim + the aliased-ident
// inc/dec balance with free actually happening; 0 only if every
// free+reuse is sound and no release underflows.
//
// x86_64 (native single-word rc_dec) and wasm (two-word str_dec under
// wasmtime) only: native-arm64 heap-string reclamation is the deferred
// RC-perceus slice 5g (SSO-blocked), so the arm64 string dec-on-overwrite
// is gated off in ir.go and there's no arm64 variant here — asserting
// reclaim there would force the unproven native str_dec path (qemu masks
// the over-release real hardware hits).
func TestX86_64StringReassignFree(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, stringReassignFreeSrc); code != 0 {
		t.Errorf("string reassign free+reuse: got %d, want 0 (drift / over-release on string dec-on-overwrite)", code)
	}
}

func TestWASMStringReassignFree(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, stringReassignFreeSrc); got != 0 {
		t.Errorf("string reassign free+reuse: got %d, want 0 (drift / over-release on string dec-on-overwrite)", got)
	}
}

// Wasm mirror of TestX86_64FreelistReuse. Sets ast.RcFreeEnabled
// around runWasm (buildComponent reads it at emit time; wasm
// codegen doesn't take CodegenMu, and this test isn't parallel).
// SKIPs without wasmtime (rides CI). The fixtures use only the
// `__alloc` / `__free` builtins, so they need no imports.
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
// Each program returns 0 on success. The fixtures use only the
// `__alloc` / `__free` builtins, so they need no imports.
var allocReuseSrc = struct{ sameClass, nullToken, mismatch string }{
	sameClass: `
function main(): i32 {
    var a: usize = __alloc(64);
    var b: usize = __alloc_reuse(a, 64, 64);
    if (a == b) { return 0; }
    return 1;
}`,
	nullToken: `
function main(): i32 {
    var z: usize = 0;
    var a: usize = __alloc(64);
    var b: usize = __alloc_reuse(z, 0, 64);
    if (b == 0) { return 1; }
    if (b == a) { return 2; }
    return 0;
}`,
	mismatch: `
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

// --- Phase 4: move-on-construction (FBIP pair-cancellation) -------
//
// `var s = Wrap{ inner: x }` at x's last use moves x's reference into
// the field — the field-init inc and x's exit dec are both elided. The
// struct's own field-drop then releases x exactly once. Correctness
// can't distinguish this from the inc/dec version (same values), so the
// IR test TestMoveOnConstructionElidesIncForLastUse pins that the inc
// is gone; these pin that eliding it stays value-correct and 0
// over-release under free, including the build+free churn.
var moveOnConstructionCases = []struct{ name, src string }{
	// One-shot: x moved into s.inner; s drops at scope exit, freeing the
	// array; x's own dec is elided. sum == 60, 0 over-releases.
	{"once", `struct Wrap { inner: i32[] }
function build(): i32 {
    var x: i32[] = [10, 20, 30];
    var s: Wrap = Wrap { inner: x };
    return s.inner[0] + s.inner[1] + s.inner[2];
}
function main(): i32 {
    return (build() - 60) + __rc_underflow_count();
}`},
	// 100 build/move/drop/free cycles: each iteration builds a fresh
	// array, moves it into a Wrap, reads it back, drops + frees. If the
	// move mis-counted, a freed array would be reused and corrupt the
	// read-back; folds __rc_underflow_count().
	{"churn", `struct Wrap { inner: i32[] }
function once(n: i32): i32 {
    var x: i32[] = [n, n + 1];
    var s: Wrap = Wrap { inner: x };
    return s.inner[0] + s.inner[1];
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        acc = acc + (once(i) - (2 * i + 1));
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`},
	// Array element: x moved into a nested array [x]; the outer array's
	// drop_arr_ptr dec's the element, balancing the elided inc. 100
	// build/move/drop/free cycles.
	{"array_elem", `function once(n: i32): i32 {
    var x: i32[] = [n, n + 1];
    var xs: i32[][] = [x];
    return xs[0][0] + xs[0][1];
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        acc = acc + (once(i) - (2 * i + 1));
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`},
	// Tuple element: x moved into (x, n); the tuple's __drop_tuple_ dec's
	// the element, balancing the elided inc. 100 build/move/drop/free
	// cycles.
	{"tuple_elem", `function once(n: i32): i32 {
    var x: i32[] = [n, n + 2];
    var t: (i32[], i32) = (x, n);
    return t.0[0] + t.0[1] + t.1;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        acc = acc + (once(i) - (3 * i + 2));
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`},
	// Closure capture: x moved into the closure env; the closure's drop
	// thunk dec's the capture, balancing the elided inc. The closure is
	// built, called, and dropped each iteration. 100 cycles.
	{"closure_capture", `function once(n: i32): i32 {
    var x: i32[] = [n, n + 5];
    function get(): i32 { return x[0] + x[1]; }
    return get();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        acc = acc + (once(i) - (2 * i + 5));
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`},
	// Composes with move-on-return: x moved into s, s moved out to the
	// caller; the caller owns and frees the whole thing.
	{"returned", `struct Wrap { inner: i32[] }
function build(n: i32): Wrap {
    var x: i32[] = [n, n + 1, n + 2];
    var s: Wrap = Wrap { inner: x };
    return s;
}
function main(): i32 {
    var w: Wrap = build(5);
    return (w.inner[0] + w.inner[1] + w.inner[2] - 18) + __rc_underflow_count();
}`},
	// Move-on-destructure: t moved into the destructure temp at its last
	// use; the temp frees the tuple box once, the extracted array
	// element gets its own dup so it survives the box free. 100
	// build/destructure/drop/free cycles. once(n) reads a[0]+a[1]+b =
	// n + (n+3) + (n+7) = 3n+10.
	{"destructure", `function once(n: i32): i32 {
    var t: (i32[], i32) = ([n, n + 3], n + 7);
    var (a, b) = t;
    return a[0] + a[1] + b;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        acc = acc + (once(i) - (3 * i + 10));
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`},
}

func TestX86_64MoveOnConstruction(t *testing.T) {
	for _, c := range moveOnConstructionCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}

func TestArm64MoveOnConstruction(t *testing.T) {
	for _, c := range moveOnConstructionCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}

func TestWASMMoveOnConstruction(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, c := range moveOnConstructionCases {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("%s: got %d, want 0", c.name, got)
			}
		})
	}
}
