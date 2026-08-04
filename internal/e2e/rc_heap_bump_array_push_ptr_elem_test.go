package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Self-append reclamation for POINTER-element arrays — `a = a.append(x)` over
// string[] / struct[].
//
// The owned-array self-reassign fix (selfReassignOwnedLocal) is gated by
// typeSelfDropSafe, which historically excluded strings (uncounted at
// construction back then; the exclusion is lifted now that strings are
// rc-tracked, #3425) and still excludes Maps. Either way the ARRAY
// reassignment-overwrite frees the old buffer with a buffer-only
// __fern_arr_dec (it never walks elements), so for the `a = a.append(x)` form
// no element-type gate is needed at all: isSelfArrayPushLocal recognises
// the rc-safe push pattern (push_grow's rc-gating frees only a uniquely-owned
// orphan; a borrowed-derived buffer at rc≥2 is never freed) and lets every
// element type reclaim its per-grow buffers.
//
// build(300) over string[] / struct[] grows past 2048 B and drops every
// iteration; a leak would scale the bump pointer with the iteration count.
//
// This is also the SKIP half of the `_move_` grow helpers' contract (#3457).
// `__fern_arr_push_grow_move_ptr` / `_move_str` retain the copied elements only
// when the incoming rc != 1; here every grow is uniquely owned, so the retain
// must NOT fire — the assign's buffer-only `__fern_arr_dec` frees the old
// buffer without walking it and the elements transfer. An unconditional retain
// leaks one reference per element per grow, which shows up here as a working
// set that scales with the iteration count. The RETAIN half (rc != 1, the
// shared-alias pair) is `TestAliasGrowNoOverRelease`.
//
// All three backends. The wasm `string[]` leg used to fail (20 iters -> 64480
// bytes, 400 -> 1231840) and the cause was NOT string-specific: wasm's freelist
// had no large tier, so `__free` dropped every block over 2048 B. A `string[]`
// grow buffer is 16 + 8*cap bytes and crosses that at cap 254, while the
// single-word `struct[]` sibling (16 + 4*cap) stayed under it — which is why
// only the string leg leaked. Fixed by mirroring the native two-tier freelist
// (#3425) in wasmbin; this leg is its end-to-end guard.

func strArrPushSrc(iters string) string {
	return `
function build(k: i32): string[] {
    var a: string[] = [];
    var i: i32 = 0;
    while (i < k) { a = a.append("item"); i = i + 1; }
    return a;
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    var n: i32 = 0;
    while (j < ` + iters + `) {
        var a: string[] = build(300);
        n = n + a.len();
        j = j + 1;
    }
    if (n != 300 * ` + iters + `) { return 201; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func structArrPushSrc(iters string) string {
	return `
struct Item { tag: i32 }
function build(k: i32): Item[] {
    var a: Item[] = [];
    var i: i32 = 0;
    while (i < k) { a = a.append(Item { tag: i }); i = i + 1; }
    return a;
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    var sum: i32 = 0;
    while (j < ` + iters + `) {
        var a: Item[] = build(300);
        sum = sum + a[0].tag;
        j = j + 1;
    }
    if (sum != 0) { return 202; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func runArrayPushPtrElemChecks(t *testing.T, run func(*testing.T, string) int) {
	t.Helper()
	for _, c := range []struct {
		name string
		src  func(string) string
	}{
		{"string", strArrPushSrc},
		{"struct", structArrPushSrc},
	} {
		t.Run(c.name, func(t *testing.T) {
			small := run(t, c.src("20"))
			large := run(t, c.src("400"))
			if small == 201 || small == 202 || large == 201 || large == 202 {
				t.Fatalf("value-incorrect run: small=%d large=%d", small, large)
			}
			if small != large {
				t.Errorf("%s[] self-append must be O(1) heap: iters=20 -> %d, iters=400 -> %d (leak)", c.name, small, large)
			}
			if small == 0 {
				t.Errorf("expected a non-zero working set for build(300); got 0")
			}
		})
	}
}

func TestX86_64ArrayPushPtrElemReclaim(t *testing.T) {
	ast.RcFreeEnabled = true
	runArrayPushPtrElemChecks(t, mustRunX86_64FreeOn)
}

func TestArm64ArrayPushPtrElemReclaim(t *testing.T) {
	ast.RcFreeEnabled = true
	runArrayPushPtrElemChecks(t, mustRunArm64FreeOn)
}

func TestWASMArrayPushPtrElemReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	runArrayPushPtrElemChecks(t, func(t *testing.T, src string) int {
		return runWasm(t, src)
	})
}
