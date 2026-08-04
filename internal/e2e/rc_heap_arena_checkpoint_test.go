package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// One-level bump-arena checkpoint — `__heap_mark()` / `__heap_release_to(m)`.
//
// The pair exists for the self-host per-module emit: each unit leaves ~0.4 GB
// that nothing reclaims, so emitting ~35 units in one process walks off the end
// of the 16 GiB arena (__fern_alloc's bounds check, exit 137). Marking before a
// unit and releasing after it is written out bounds the peak at one unit, which
// is what lets the batched per-module path run from the ordinary compile path
// instead of only from the test harness (#3457 slice 3).
//
// WHY THESE ASSERTIONS AND NOT A LEAK TEST. The obvious shape — "run the loop
// 20x and 400x, assert the working set matches" — is what the other
// reclamation tests here do, and it has NO power for this feature: the natural
// probe shapes (`a = a.append(..)` over string[] / struct[], nested arrays)
// already reclaim through RC, so the assertion passes identically with the
// release calls deleted. Measured: the leak-shaped version of this test returns
// the same verdict either way. Mark/release only shows up where RC does not
// reclaim, which is diffuse compiler code, not a fixture.
//
// So this asserts the MECHANISM directly against the bump cursor:
//
//	b0 = bump; m = mark(); <allocate>; b1 = bump; release(m); b2 = bump
//	b1 > b0   the window really allocated (else the test is vacuous)
//	b2 == b0  the release rewound exactly
//
// Both halves discriminate: with `release_to` deleted the program returns 83
// (verified), and with a window that allocates nothing it returns 82.
//
// THE RELEASE POINT IS A REAL CONSTRAINT, not a style choice. RC drops for a
// scope's locals are emitted at scope END — i.e. AFTER a release call written
// in that same scope. Marking and releasing around locals in one scope
// therefore rewinds the arena and then walks the just-unallocated buffers,
// which SEGFAULTS (observed on the struct-array shape below when `a` was a
// sibling local of the release). Hence `window()`: confining the window's
// allocation to a call means its locals are dropped before it returns, so
// nothing above the mark is reachable at the caller's release. That is the
// pattern the driver uses, and the reason the intrinsic is not a general-
// purpose scoped-arena API.

// A shape whose allocation is unambiguously non-trivial (struct boxes, an
// inner array per element, a string field) and entirely confined to the call.
const arenaCheckpointSrc = `
struct Node { name: string, kids: string[] }
function mk(k: i32): Node[] {
    var out: Node[] = [];
    var i: i32 = 0;
    while (i < k) {
        var ks: string[] = [];
        var j: i32 = 0;
        while (j < 8) { ks = ks.append("kid"); j = j + 1; }
        out = out.append(Node { name: "n", kids: ks });
        i = i + 1;
    }
    return out;
}
function window(k: i32): i32 {
    var a: Node[] = mk(k);
    var sum: i32 = 0;
    var i: i32 = 0;
    while (i < a.len()) { sum = sum + a[i].kids.len(); i = i + 1; }
    return sum;
}
function main(): i32 {
    // Warm the runtime so the cursor is seeded and b0 is a real address:
    // a mark of 0 means "no checkpoint" and release_to would ignore it.
    if (window(4) != 32) { return 80; }
    var b0: i32 = (__heap_bump_bytes() as i32);
    var m: i64 = __heap_mark();
    if (window(400) != 3200) { return 81; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    __heap_release_to(m);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (b1 <= b0) { return 82; }
    if (b2 != b0) { return 83; }
    return 0;
}`

// Repeated cycles: 300 mark/allocate/release rounds, each validating the
// window's computed value, then a fresh allocation AFTER the releases. This is
// what exercises the freelist-head snapshot. Restoring the pre-mark heads is
// required for CORRECTNESS, not just to avoid a leak: a block allocated AND
// freed inside a window leaves a head pointing above the mark, so once the
// cursor rewinds, a later freelist pop and a later bump would both hand out
// that same address. A cleared or unrestored table shows up here as a wrong
// value (aliased live objects) or a crash.
const arenaCheckpointCyclesSrc = `
struct Node { name: string, kids: string[] }
function mk(k: i32): Node[] {
    var out: Node[] = [];
    var i: i32 = 0;
    while (i < k) {
        var ks: string[] = [];
        var j: i32 = 0;
        while (j < 8) { ks = ks.append("kid"); j = j + 1; }
        out = out.append(Node { name: "n", kids: ks });
        i = i + 1;
    }
    return out;
}
function window(k: i32): i32 {
    var a: Node[] = mk(k);
    var sum: i32 = 0;
    var i: i32 = 0;
    while (i < a.len()) { sum = sum + a[i].kids.len(); i = i + 1; }
    return sum;
}
function main(): i32 {
    if (window(4) != 32) { return 80; }
    var b0: i32 = (__heap_bump_bytes() as i32);
    var c: i32 = 0;
    while (c < 300) {
        var m: i64 = __heap_mark();
        if (window(200) != 1600) { return 81; }
        __heap_release_to(m);
        c = c + 1;
    }
    // 300 windows of ~200 nodes each, and the cursor has not moved at all.
    if ((__heap_bump_bytes() as i32) != b0) { return 82; }
    // The arena is still usable after the releases.
    if (window(50) != 400) { return 83; }
    return 0;
}`

func runArenaCheckpointChecks(t *testing.T, run func(*testing.T, string) int) {
	t.Helper()
	for _, c := range []struct {
		name string
		src  string
	}{
		{"rewind", arenaCheckpointSrc},
		{"cycles", arenaCheckpointCyclesSrc},
	} {
		t.Run(c.name, func(t *testing.T) {
			switch got := run(t, c.src); got {
			case 0:
			case 80, 81:
				t.Fatalf("value-incorrect run (exit %d): the window computed the wrong result, so the checkpoint corrupted live data", got)
			case 82:
				t.Fatalf("exit 82: the cursor did not behave as expected (vacuous window, or a release that did not rewind)")
			case 83:
				t.Fatalf("exit 83: the release did not rewind the cursor exactly, or the arena was unusable afterwards")
			default:
				t.Fatalf("unexpected exit %d", got)
			}
		})
	}
}

func TestX86_64ArenaCheckpoint(t *testing.T) {
	ast.RcFreeEnabled = true
	runArenaCheckpointChecks(t, mustRunX86_64FreeOn)
}

func TestArm64ArenaCheckpoint(t *testing.T) {
	ast.RcFreeEnabled = true
	runArenaCheckpointChecks(t, mustRunArm64FreeOn)
}
