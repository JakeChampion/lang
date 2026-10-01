// `core/mem.Drop` — user finalizers hooked into the RC drop glue (#2705).
//
// These check a LITERAL rather than an interpOracle: the interpreter has no
// refcounts (interp.go — "the interpreter has no refcounts to underflow"), so
// no value reaches rc-zero there and `drop` never runs. See
// backends_agree_test.go for when that exemption applies; it is narrow, and
// this is one of the two cases.
//
// What they check is the contract core/mem.fern documents: a finalizer runs
// exactly once, when the value dies, with its fields still readable. Where a
// value dies is the compiler's release point — native's early-release points,
// the self-host's last use (#10756) — so where a drop line falls among the
// program's other output is compared only where the program fixes it (a loop
// temporary dies inside its iteration); otherwise the drop lines are compared
// as a set and the program's own lines in order.
package e2e

import (
	"sort"
	"strings"
	"testing"
)

// dropsAgree runs src on every backend and checks that its non-drop output is
// exactly `output`, in order, and that its `drop ...` lines are exactly
// `drops`, in any order.
func dropsAgree(t *testing.T, src, output string, drops ...string) {
	t.Helper()
	check := func(t *testing.T, backend, got string) {
		t.Helper()
		var lines, dropped []string
		for _, l := range strings.Split(strings.TrimSpace(got), "\n") {
			if strings.HasPrefix(l, "drop ") {
				dropped = append(dropped, l)
			} else if l != "" {
				lines = append(lines, l)
			}
		}
		if got := strings.Join(lines, "\n"); got != output {
			t.Errorf("%s output = %q, want %q", backend, got, output)
		}
		want := append([]string(nil), drops...)
		sort.Strings(want)
		sort.Strings(dropped)
		if strings.Join(dropped, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s finalizers = %q, want %q (each value once)", backend, dropped, want)
		}
	}
	t.Run("wasm32-wasi", func(t *testing.T) { check(t, "wasm", runWasmCapturingStdout(t, src)) })
	t.Run("x86_64", func(t *testing.T) {
		got, code := compileAndRunX86_64(t, src)
		if code != 0 {
			t.Fatalf("x86-64 exit = %d, want 0; stdout:\n%s", code, got)
		}
		check(t, "x86-64", got)
	})
	t.Run("arm64-linux", func(t *testing.T) {
		got, code := compileAndRunArm64(t, src)
		if code != 0 {
			t.Fatalf("arm64 exit = %d, want 0; stdout:\n%s", code, got)
		}
		check(t, "arm64", got)
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
	dropsAgree(t, src, "use 7", "drop 7")
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
	dropsAgree(t, src, "len 2\nfield 3\npayload 4\nreturned 5", "drop 1", "drop 2", "drop 3", "drop 4", "drop 5")
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
	dropsAgree(t, src, "both 11", "drop 1")
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
	dropsAgree(t, src, "consume 1\nback 1", "drop 1")
}

// A loop body's temporary is finalized once per iteration, not accumulated
// to function exit and finalized once.
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
	dropsAgree(t, src, "iter 10\niter 11\niter 12\nend", "drop 10", "drop 11", "drop 12")
	// A temporary dies before the next iteration's is built, so its finalizer
	// runs before that iteration prints. The last one has no successor, and
	// where it falls against "end" is release timing again.
	ordered := func(t *testing.T, backend, got string) {
		t.Helper()
		for _, pair := range [][2]string{{"drop 10", "iter 11"}, {"drop 11", "iter 12"}} {
			if strings.Index(got, pair[0]) > strings.Index(got, pair[1]) {
				t.Errorf("%s: %q runs after %q:\n%s", backend, pair[0], pair[1], got)
			}
		}
	}
	t.Run("per-iteration", func(t *testing.T) {
		t.Run("wasm32-wasi", func(t *testing.T) { ordered(t, "wasm", runWasmCapturingStdout(t, src)) })
		t.Run("x86_64", func(t *testing.T) { got, _ := compileAndRunX86_64(t, src); ordered(t, "x86-64", got) })
		t.Run("arm64-linux", func(t *testing.T) { got, _ := compileAndRunArm64(t, src); ordered(t, "arm64", got) })
	})
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
	dropsAgree(t, src, "p 2\niter 10\niter 11\nend", "drop 2", "drop 10", "drop 11")
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
	dropsAgree(t, src, "v 7\nend", "drop Sig")
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
	// finalized exactly once.
	dropsAgree(t, src, "wi\nwi\nbj\nbj\nend", "drop W2", "drop W1", "drop W1", "drop B", "drop B", "drop B")
}
