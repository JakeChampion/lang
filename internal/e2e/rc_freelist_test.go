package e2e

import (
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// freelistReuseSrc is the shared body for the flag-on freelist
// tests across backends: same-size reuse, different-class
// non-aliasing, and LIFO order. Each program returns 0 on success.
var freelistReuseSrc = struct{ reuse, wrongClass, lifo string }{
	reuse: `
function main(): i32 {
    let a: usize = __alloc(64);
    __free(a, 64);
    let b: usize = __alloc(64);
    if (a == b) { return 0; }
    return 1;
}`,
	wrongClass: `
function main(): i32 {
    let a: usize = __alloc(64);
    __free(a, 64);
    let b: usize = __alloc(32);
    if (a == b) { return 1; }
    return 0;
}`,
	lifo: `
function main(): i32 {
    let a: usize = __alloc(48);
    let b: usize = __alloc(48);
    __free(a, 48);
    __free(b, 48);
    let c: usize = __alloc(48);
    let d: usize = __alloc(48);
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
	runner := e2eharness.X86_64Runner(t)
	return e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, src, nil), runner
}

// compileAndRunArm64FreeOn mirrors compileAndRunArm64 but flips
// ast.RcFreeEnabled around codegen. arm64codegen.Emit acquires
// ast.CodegenMu itself, so (as on x86) we must not hold it here.
func compileAndRunArm64FreeOn(t *testing.T, src string) (string, int) {
	t.Helper()
	return compileAndRunArm64FreeOnArgs(t, src)
}

// compileAndRunArm64FreeOnArgs is compileAndRunArm64FreeOn with args handed
// to the program as its argv[1..].
func compileAndRunArm64FreeOnArgs(t *testing.T, src string, args ...string) (string, int) {
	t.Helper()
	binPath, qemu := compileArm64FreeOn(t, src)
	cmd := runArm64Bin(qemu, binPath, args...)
	out, _ := cmd.CombinedOutput()
	_ = out
	return "", cmd.ProcessState.ExitCode()
}

// compileArm64FreeOn is the compile half of compileAndRunArm64FreeOnArgs —
// the arm64 sibling of compileX86_64FreeOn, for tests that need to control
// how the binary is launched. Returns the binary's path and the qemu runner.
func compileArm64FreeOn(t *testing.T, src string) (string, string) {
	t.Helper()
	qemu := e2eharness.Arm64Runner(t)
	return e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Linux, src, nil), qemu
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
    let fs: Foo[] = [Foo { v: n }, Foo { v: n + 1 }, Foo { v: n + 2 }];
    return fs[2].v;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        acc = acc + (consume(i) - (i + 2));
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`

// pushLoopFreeSrc is the main push-loop case: 200 grows of a
// plain i32[]. Flag-on, each copy-grow frees the OLD buffer
// (dec-on-overwrite → __fern_arr_dec → __free), which the next grow
// reuses from the freelist — the O(N²)→O(N) reclamation. If a freed
// buffer were handed out while still referenced, the read-back sum
// would be wrong; 0 only if every free+reuse is sound. Sum
// 0..199 = 19900.
const pushLoopFreeSrc = `function build(): i32 {
    let xs: i32[] = [];
    let i: i32 = 0;
    while (i < 200) {
        xs = xs.append(i);
        i = i + 1;
    }
    let sum: i32 = 0;
    let j: i32 = 0;
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
// the main case for the Phase 1e-strings dec-on-overwrite: a string
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
    let s: string = "";
    let i: i32 = 0;
    while (i < 300) {
        s = s + "x";
        i = i + 1;
    }
    return s.len();
}
function aliasHeap(w: string): i32 {
    let a: string = "a" + w;
    let b: string = "bb" + w;
    a = b;
    return a.len() + b.len();
}
function main(): i32 {
    let grown: i32 = build() - 300;
    let aliased: i32 = aliasHeap("zz") - 8;
    return grown + aliased + __rc_underflow_count();
}`

// Phase 3 step-4: arrays free their buffer when rc hits 0 (flag-on).
// This exercises __fern_drop_arr_ptr's tail-free + freelist reuse
// across 50 build/drop cycles, and re-runs the whole rc-correctness
// corpus with free actually happening — the use-after-free guard for
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
	if got := runWasm(t, stringReassignFreeSrc); got != 0 {
		t.Errorf("string reassign free+reuse: got %d, want 0 (drift / over-release on string dec-on-overwrite)", got)
	}
}

// Wasm mirror of TestX86_64FreelistReuse. Sets ast.RcFreeEnabled
// around runWasm (buildComponent reads it at emit time; wasm
// codegen doesn't take CodegenMu, and this test isn't parallel).
// SKIPs without wasmtime (runs in CI). The fixtures use only the
// `__alloc` / `__free` builtins, so they need no imports.
func TestWASMFreelistReuse(t *testing.T) {

	reuse := `function main(): i32 {
    let a: usize = __alloc(64);
    __free(a, 64);
    let b: usize = __alloc(64);
    if (a == b) { return 0; }
    return 1;
}`
	if got := runWasm(t, reuse); got != 0 {
		t.Errorf("same-size reuse: got %d, want 0 (freed block should be reused)", got)
	}

	wrongClass := `function main(): i32 {
    let a: usize = __alloc(64);
    __free(a, 64);
    let b: usize = __alloc(32);
    if (a == b) { return 1; }
    return 0;
}`
	if got := runWasm(t, wrongClass); got != 0 {
		t.Errorf("wrong-class reuse: got %d, want 0 (32-byte alloc must not reuse a 64-byte free)", got)
	}

	lifo := `function main(): i32 {
    let a: usize = __alloc(48);
    let b: usize = __alloc(48);
    __free(a, 48);
    __free(b, 48);
    let c: usize = __alloc(48);
    let d: usize = __alloc(48);
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
// aarch64 toolchain (runs in CI).
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
    let p: Point = Point { x: 0, y: 0 };
    let i: i32 = 0;
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
    let p: Point = Point { x: 5, y: 7 };
    let q: Point = p;
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
    let p: Pair = Pair { a: 1, b: 2 };
    let i: i32 = 0;
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
    let p: Holder = Holder { id: 0, items: [10, 20, 30] };
    let i: i32 = 0;
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
    let p: Holder = Holder { id: 1, items: [7, 8] };
    let q: Holder = p;
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
    let p: Holder = Holder { id: 0, items: [0] };
    let i: i32 = 0;
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
// `let s = Wrap{ inner: x }` at x's last use moves x's reference into
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
    let x: i32[] = [10, 20, 30];
    let s: Wrap = Wrap { inner: x };
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
    let x: i32[] = [n, n + 1];
    let s: Wrap = Wrap { inner: x };
    return s.inner[0] + s.inner[1];
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
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
    let x: i32[] = [n, n + 1];
    let xs: i32[][] = [x];
    return xs[0][0] + xs[0][1];
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
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
    let x: i32[] = [n, n + 2];
    let t: (i32[], i32) = (x, n);
    return t.0[0] + t.0[1] + t.1;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
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
    let x: i32[] = [n, n + 5];
    function get(): i32 { return x[0] + x[1]; }
    return get();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
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
    let x: i32[] = [n, n + 1, n + 2];
    let s: Wrap = Wrap { inner: x };
    return s;
}
function main(): i32 {
    let w: Wrap = build(5);
    return (w.inner[0] + w.inner[1] + w.inner[2] - 18) + __rc_underflow_count();
}`},
	// Move-on-destructure: t moved into the destructure temp at its last
	// use; the temp frees the tuple box once, the extracted array
	// element gets its own dup so it survives the box free. 100
	// build/destructure/drop/free cycles. once(n) reads a[0]+a[1]+b =
	// n + (n+3) + (n+7) = 3n+10.
	{"destructure", `function once(n: i32): i32 {
    let t: (i32[], i32) = ([n, n + 3], n + 7);
    let (a, b) = t;
    return a[0] + a[1] + b;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
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
	for _, c := range moveOnConstructionCases {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("%s: got %d, want 0", c.name, got)
			}
		})
	}
}
