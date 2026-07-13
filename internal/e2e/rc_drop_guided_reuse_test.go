package e2e

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// E3 drop-guided reuse evaluation (ast.RcReuseDropGuided,
// internal/ir/rc_dropguided.go): the runtime half of the comparison
// harness. The flag changes only WHICH reuse pairs are proposed — every
// pair still runs behind the shared is_unique guard + degrade-to-fresh-
// alloc lowering — so with the flag ON:
//
//   1. every general-reuse runtime contract (genReuseCases) must still
//      pass with zero rc underflows,
//   2. the leak guards must stay bounded,
//   3. the drop-guided-only shape (donor dropped INSIDE a dominated arm)
//      must be value-correct on both the taken and not-taken paths, and
//   4. allocation traffic (heap bump high-water) must never exceed the
//      pairing's — drop-guided proposes a superset of pairs on these
//      shapes — and must WIN on the arm shape.

func withDropGuidedE2E(t *testing.T, fn func()) {
	t.Helper()
	prev := ast.RcReuseDropGuided
	ast.RcReuseDropGuided = true
	defer func() { ast.RcReuseDropGuided = prev }()
	fn()
}

// The full general-reuse runtime contract table under the drop-guided
// strategy — same programs, same expectations as TestX86_64GeneralReuse.
func TestX86_64GeneralReuseDropGuided(t *testing.T) {
	withDropGuidedE2E(t, func() {
		for _, c := range genReuseCases {
			t.Run(c.name, func(t *testing.T) {
				if _, code := compileAndRunX86_64FreeOn(t, c.src); code != 0 {
					t.Errorf("%s: got %d, want 0", c.name, code)
				}
			})
		}
	})
}

// dgArmShapeRuntimeSrc exercises the drop-guided-only pairing at runtime:
// a's last use sits inside the if arm before b's construction (taken
// path reuses a's box; not-taken path leaves a to the exit sweep — the
// adversarial double-free alternation), with a pointer field so the old
// array is deep-freed on the reuse branch.
const dgArmShapeRuntimeSrc = `struct Holder { id: i32, items: i32[] }
function run(go_: boolean): i32 {
    var a: Holder = Holder { id: 1, items: [7, 8] };
    var acc: i32 = 0;
    if (go_) {
        var s: i32 = a.id + a.items[0] + a.items[1];
        var b: Holder = Holder { id: s, items: [3, 4] };
        acc = b.id + b.items[0] + b.items[1];
    }
    return acc;
}
function main(): i32 {
    var t: i32 = run(true);    // s=16; b={16,[3,4]} -> 23
    var f: i32 = run(false);   // a exit-swept
    if (t != 23) { return 1; }
    if (f != 0) { return 2; }
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var m: Holder = Holder { id: i, items: [i, i + 1] };
        if (i % 2 == 0) {
            var s: i32 = m.id + m.items[0] + m.items[1];
            var b: Holder = Holder { id: s, items: [i + 2, i + 3] };
            acc = acc + b.id + b.items[0] + b.items[1];
        }
        i = i + 1;
    }
    // even i: s = 3i+1; b.id + b.items = (3i+1) + (2i+5) = 5i+6
    // i=2j, j=0..99: 10j+6; sum = 49500 + 600 = 50100
    if (acc != 50100) { return 3; }
    return __rc_underflow_count();
}`

func TestX86_64DropGuidedArmShapeRuntime(t *testing.T) {
	withDropGuidedE2E(t, func() {
		if out, code := compileAndRunX86_64FreeOn(t, dgArmShapeRuntimeSrc); code != 0 {
			t.Errorf("arm-shape runtime: exit %d, want 0 (out %q)", code, out)
		}
	})
}

func TestArm64DropGuidedArmShapeRuntime(t *testing.T) {
	withDropGuidedE2E(t, func() {
		if out, code := compileAndRunArm64FreeOn(t, dgArmShapeRuntimeSrc); code != 0 {
			t.Errorf("arm-shape runtime: exit %d, want 0 (out %q)", code, out)
		}
	})
}

func TestWASMDropGuidedArmShapeRuntime(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	withDropGuidedE2E(t, func() {
		if got := runWasm(t, dgArmShapeRuntimeSrc); got != 0 {
			t.Errorf("arm-shape runtime (wasm): got %d, want 0", got)
		}
	})
}

// The request-loop leak guard and append-copy leak bound under the flag:
// drop-guided must not disturb reclamation.
func TestX86_64RcRequestLoopLeakGuardDropGuided(t *testing.T) {
	withDropGuidedE2E(t, func() {
		if _, code := compileAndRunX86_64FreeOn(t, rcRequestLoopLeakGuard); code != 0 {
			t.Errorf("request-loop leak guard with drop-guided on: exit %d, want 0", code)
		}
	})
}

func TestX86_64AppendCopyLeakBoundDropGuided(t *testing.T) {
	withDropGuidedE2E(t, func() {
		if _, code := compileAndRunX86_64(t, appendCopyLeakBoundProgram); code != 0 {
			t.Errorf("append-copy leak bound with drop-guided on: exit %d, want 0", code)
		}
	})
}

// --- Allocation-traffic comparison (numbers for the E3 verdict) ---

// Each program prints its steady-state heap-bump growth. They are run
// twice — flag OFF then ON — and the deltas are logged for the verdict
// table in docs/RC-PERCEUS-PLAN.md; the assertions pin the direction
// (ON never allocates more; the arm shape allocates strictly less).
var dgTrafficPrograms = []struct {
	name string
	src  string
	win  bool // drop-guided must strictly reduce bump growth
}{
	// Array build-up loop: reuse pairing doesn't apply to array buffers —
	// both strategies must measure identical growth.
	{"array_buildup", `import "std/i32";
function main(): i32 {
    var warm: i32 = 0;
    var w: i32 = 0;
    while (w < 100) {
        var row: i32[] = [w, w + 1, w + 2];
        warm = warm + row[0];
        w = w + 1;
    }
    var before: i32 = __heap_bump_bytes();
    var acc: i32 = warm;
    var i: i32 = 0;
    while (i < 2000) {
        var row: i32[] = [i, i + 1, i + 2];
        acc = acc + row[0];
        i = i + 1;
    }
    print((__heap_bump_bytes() - before).to_string());
    if (acc < 0) { return 1; }
    return 0;
}`, false},
	// Per-iteration struct churn (the R3 loop shape): both strategies pair
	// dead a -> b, so growth must match.
	{"struct_churn", `import "std/i32";
struct Point { x: i32, y: i32 }
function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 2000) {
        var a: Point = Point { x: i, y: i + 1 };
        var s: i32 = a.x + a.y;
        var b: Point = Point { x: s, y: i };
        acc = acc + b.x + b.y;
        i = i + 1;
    }
    print((__heap_bump_bytes() - before).to_string());
    if (acc < 0) { return 1; }
    return 0;
}`, false},
	// The R3 straight-line general-pairing shape (dead chain of three).
	{"r3_dead_chain", `import "std/i32";
struct Box { a: i32, b: i32, c: i32, d: i32 }
function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var p: Box = Box { a: 1, b: 2, c: 3, d: 4 };
    var s: i32 = p.a + p.d;
    var q: Box = Box { a: s, b: 0, c: 0, d: 0 };
    var u: i32 = q.a;
    var r: Box = Box { a: u, b: 0, c: 0, d: 0 };
    print((__heap_bump_bytes() - before).to_string());
    if (r.a != 5) { return 1; }
    return 0;
}`, false},
	// The drop-guided-only arm shape in a loop: the pairing misses it, so
	// flag OFF holds two live boxes per iteration (a + b, a freed only
	// after the whole if) while flag ON holds one (b takes a's box).
	{"arm_shape_loop", `import "std/i32";
struct Wide { a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32 }
function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 2000) {
        var a: Wide = Wide { a: i, b: 1, c: 2, d: 3, e: 4, f: 5, g: 6, h: 7 };
        if (i >= 0) {
            var s: i32 = a.a + a.h;
            var b: Wide = Wide { a: s, b: 0, c: 0, d: 0, e: 0, f: 0, g: 0, h: i };
            acc = acc + b.a + b.h;
        }
        i = i + 1;
    }
    print((__heap_bump_bytes() - before).to_string());
    if (acc < 0) { return 1; }
    return 0;
}`, true},
}

// runBumpX86 compiles+runs src (free on) and parses the printed bump
// growth.
func runBumpX86(t *testing.T, src string) int {
	t.Helper()
	out, code := compileAndRunX86_64FreeOn(t, src)
	if code != 0 {
		t.Fatalf("traffic program exited %d (out %q)", code, out)
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("parse bump output %q: %v", out, err)
	}
	return n
}

func TestX86_64DropGuidedAllocTraffic(t *testing.T) {
	for _, p := range dgTrafficPrograms {
		t.Run(p.name, func(t *testing.T) {
			off := runBumpX86(t, p.src)
			on := 0
			withDropGuidedE2E(t, func() { on = runBumpX86(t, p.src) })
			t.Logf("%s: bump growth flag OFF = %d B, flag ON = %d B", p.name, off, on)
			if on > off {
				t.Errorf("%s: drop-guided must never allocate more (OFF %d, ON %d)", p.name, off, on)
			}
			if p.win && on >= off {
				t.Errorf("%s: drop-guided should strictly reduce bump growth (OFF %d, ON %d)", p.name, off, on)
			}
		})
	}
}
