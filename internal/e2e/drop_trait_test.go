// `core/mem.Drop` — user finalizers hooked into the RC drop glue (#2705).
//
// These are COMPILED-BACKEND-ONLY, deliberately, and not differential
// against the interpreter: the interpreter has no refcounts (see
// interp.go's "the interpreter has no refcounts to underflow"), so a
// value never reaches rc-zero there and `drop` never runs. Same shape as
// dynCompiledBackends, which exists because the interpreter cannot
// dispatch `dyn` over i64. Expectations here are literals, and the three
// backends must agree with each other.
package e2e

import (
	"strings"
	"testing"
)

// dropAllCompiled runs src on wasm, x86-64 and arm64 and asserts each
// stdout equals want.
func dropAllCompiled(t *testing.T, src, want string) {
	t.Helper()
	t.Run("wasm32-wasi", func(t *testing.T) {
		if got := runWasmCapturingStdout(t, src); got != want {
			t.Errorf("wasm = %q, want %q", got, want)
		}
	})
	t.Run("x86_64", func(t *testing.T) {
		got, code := compileAndRunX86_64(t, src)
		got = strings.TrimSpace(got)
		if code != 0 {
			t.Fatalf("x86-64 exit = %d, want 0; stdout:\n%s", code, got)
		}
		if got != want {
			t.Errorf("x86-64 = %q, want %q", got, want)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		got, code := compileAndRunArm64(t, src)
		got = strings.TrimSpace(got)
		if code != 0 {
			t.Fatalf("arm64 exit = %d, want 0; stdout:\n%s", code, got)
		}
		if got != want {
			t.Errorf("arm64 = %q, want %q", got, want)
		}
	})
}

const dropPrelude = `import "core/mem";
import "std/i32";
struct W { n: i32 }
impl mem.Drop for W {
    function drop(self: Self): void { print("drop " + self.n.to_string()); }
}
`

// The finalizer runs at all, and reads its fields — it must fire before
// the field releases and the box free, or `self.n` would read freed
// memory.
func TestDropTraitRunsAndReadsFields(t *testing.T) {
	src := dropPrelude + `function main(): i32 {
    var a: W = W { n: 7 };
    print("use " + a.n.to_string());
    return 0;
}
`
	dropAllCompiled(t, src, "use 7\ndrop 7")
}

// Every container shape funnels its element/field/payload release through
// `__drop_struct_W`, so one hook covers all of them. Each value must be
// finalized exactly once.
func TestDropTraitAcrossContainerShapes(t *testing.T) {
	src := dropPrelude + `struct Holder { w: W }
enum E { Wrap(W), Empty }
function make(n: i32): W { return W { n: n }; }
function main(): i32 {
    var xs: W[] = [W { n: 1 }, W { n: 2 }];
    print("len " + xs.len().to_string());
    var h: Holder = Holder { w: W { n: 3 } };
    print("field " + h.w.n.to_string());
    var e: E = E.Wrap(W { n: 4 });
    match (e) { Wrap(v) => { print("payload " + v.n.to_string()); }, Empty => {} }
    var r: W = make(5);
    print("returned " + r.n.to_string());
    return 0;
}
`
	dropAllCompiled(t, src,
		"len 2\nfield 3\npayload 4\nreturned 5\ndrop 1\ndrop 2\ndrop 3\ndrop 4\ndrop 5")
}

// Two names for one value is ONE value: the finalizer fires once, at the
// last reference, not once per binding. This is the is_unique gate doing
// its job — an aliased struct takes the plain dec path.
func TestDropTraitAliasFinalizesOnce(t *testing.T) {
	src := dropPrelude + `function main(): i32 {
    var a: W = W { n: 1 };
    var b: W = a;
    print("both " + a.n.to_string() + b.n.to_string());
    return 0;
}
`
	dropAllCompiled(t, src, "both 11\ndrop 1")
}

// A value moved into a callee is finalized there, once — the caller must
// not sweep it again.
func TestDropTraitMovedIntoCallee(t *testing.T) {
	src := dropPrelude + `function consume(w: W): i32 { print("consume " + w.n.to_string()); return w.n; }
function main(): i32 {
    var x: i32 = consume(W { n: 1 });
    print("back " + x.to_string());
    return 0;
}
`
	dropAllCompiled(t, src, "consume 1\ndrop 1\nback 1")
}

// A loop body's temporary is finalized once per iteration, at the
// reinit that displaces it — not accumulated to function exit.
func TestDropTraitLoopTemporary(t *testing.T) {
	src := dropPrelude + `function main(): i32 {
    var i: i32 = 0;
    while (i < 3) {
        var t: W = W { n: 10 + i };
        print("iter " + t.n.to_string());
        i = i + 1;
    }
    print("end");
    return 0;
}
`
	dropAllCompiled(t, src, "iter 10\ndrop 10\niter 11\ndrop 11\niter 12\nend\ndrop 12")
}

// The reuse interaction, pinned. Drop-guided reuse hands a dying value's
// box shell to the next same-shaped constructor instead of freeing it,
// which skipped the finalizer entirely — a destructor silently lost on a
// value that really did die. A `Drop` implementor is now excluded from
// reuse (reuseClassOf), so `p` is finalized even though the loop below it
// constructs the same shape.
func TestDropTraitNotSwallowedByReuse(t *testing.T) {
	src := dropPrelude + `function passthru(w: W): W { return w; }
function main(): i32 {
    var p: W = passthru(W { n: 2 });
    print("p " + p.n.to_string());
    var i: i32 = 0;
    while (i < 2) {
        var t: W = W { n: 10 + i };
        print("iter " + t.n.to_string());
        i = i + 1;
    }
    print("end");
    return 0;
}
`
	dropAllCompiled(t, src, "p 2\niter 10\ndrop 10\niter 11\nend\ndrop 2\ndrop 11")
}

// An enum carries its own impl. `enumNeedsDrop` says an all-scalar enum
// needs no glue; a Drop impl is itself the reason to generate it, so the
// gate has to admit this one.
func TestDropTraitOnAllScalarEnum(t *testing.T) {
	src := `import "core/mem";
import "std/i32";
enum Sig { Open(i32), Closed }
impl mem.Drop for Sig {
    function drop(self: Self): void { print("drop Sig"); }
}
function main(): i32 {
    var s: Sig = Sig.Open(7);
    match (s) { Open(v) => { print("v " + v.to_string()); }, Closed => {} }
    print("end");
    return 0;
}
`
	dropAllCompiled(t, src, "v 7\ndrop Sig\nend")
}

// Self-overwrite is the other reuse shape: `w = W { … }` in a loop keeps
// the OLD box and overwrites its fields in place, which displaced a value
// without running its finalizer. Both the struct and the enum
// self-overwrite paths now decline a Drop implementor, and the exit sweep
// routes a Drop enum through the generated glue rather than its inline
// variant plan — so every value constructed here is finalized.
func TestDropTraitSelfOverwriteFinalizesEveryValue(t *testing.T) {
	src := `import "core/mem";
import "std/i32";
struct W { v: i32[] }
impl mem.Drop for W {
    function drop(self: Self): void { print("drop W" + self.v.len().to_string()); }
}
enum B { Keep(i32[]), Swap(i32[]) }
impl mem.Drop for B {
    function drop(self: Self): void { print("drop B"); }
}
function main(): i32 {
    var w: W = W { v: [0, 0] };
    var i: i32 = 0;
    while (i < 2) { w = W { v: [i] }; print("wi"); i = i + 1; }
    var b: B = B.Keep([0, 0]);
    var j: i32 = 0;
    while (j < 2) { b = B.Keep([j]); print("bj"); j = j + 1; }
    print("end");
    return 0;
}
`
	// Three W values (initial + two rebinds) and three B values, each
	// finalized exactly once: two during each loop, the survivor at exit.
	dropAllCompiled(t, src,
		"drop W2\nwi\ndrop W1\nwi\ndrop B\nbj\ndrop B\nbj\nend\ndrop W1\ndrop B")
}
