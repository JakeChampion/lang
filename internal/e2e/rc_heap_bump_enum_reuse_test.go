package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Enum reuse-path payload reclamation (RC-Perceus, the enum analog of 5f).
// A self-overwrite `e = Variant(...)` of an owned, uniquely-held enum reuses
// the old box in place (Phase 5e constructor reuse). When the old box holds
// a droppable (rc-tracked) payload, `tryEnumReuseOverwrite` previously kept
// the box but never freed that payload — the normal overwrite-dec that would
// free it is bypassed — so `e = Wrap([..])` in a loop leaked the prior
// payload every iteration (probe: wasm 1616 -> 160016 B, unbounded).
//
// The fix frees the OLD payload on the reuse branch (uniform droppable
// offsets, via emitFieldDropOnStack), mirroring tryStructReuseOverwrite
// step 4. Sound for the same reason the normal enum overwrite-free is: the
// reuse path requires freeEligible[e], a whole-function property that (via
// rhsTainted propagation through variant-constructor args) is false whenever
// any value e holds has a payload aliasing a live local — so the old payload
// here is never a live alias, and the freeing drop (each is_unique-gated)
// reclaims the genuine last reference. Enums with string / non-uniform-
// droppable payloads decline reuse and fall to the normal overwrite path
// (which frees soundly + bounded); scalar-only enums have nothing to free.
//
// wasm is the over-release arbiter (its bump cursor measures the leak
// directly; natives' segregated freelist arena is insensitive to small-block
// reclaim, so they read a flat low high-water either way).

// enumReusePayloadBumpSrc: a uniform pointer-payload enum self-overwritten
// with a fresh array payload each iteration — the old payload must reclaim.
func enumReusePayloadBumpSrc(n string) string {
	return `enum E { Wrap(i32[]), Empty }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var e: E = Empty;
    var i: i32 = 0;
    while (i < ` + n + `) {
        e = Wrap([i, i + 1, i + 2, i + 3]);
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// enumReuseCrossVariantSrc: cross-variant reuse (Keep/Swap, both i32[]) with
// a match read each iteration — value-correctness + 0 over-release.
const enumReuseCrossVariantSrc = `enum Bag { Keep(i32[]), Swap(i32[]) }
function main(): i32 {
    var b: Bag = Keep([0, 0, 0]);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 300) {
        b = Keep([i, i + 1, i + 2]);
        b = Swap([i + 10, i + 11, i + 12]);
        match (b) { Keep(x) => { acc = acc + x[0]; }, Swap(y) => { acc = acc + y[0]; } }
        i = i + 1;
    }
    // each iter ends as Swap([i+10,..]); acc += i+10; sum i=0..299 = 44850 + 3000 = 47850
    if (acc != 47850) { return 999; }
    return __rc_underflow_count();
}`

// enumReuseAliasedPayloadSrc: the payload `a` is aliased into the enum and
// READ after, with a forced interleaved allocation (junk) that would corrupt
// a wrongly-freed buffer. The freeEligible gate must keep this sound (either
// by declining reuse for the tainted enum, or — when reuse fires — by the
// payload not being a live alias). Returns 0 iff value-correct AND no
// over-release.
const enumReuseAliasedPayloadSrc = `enum Bag { Keep(i32[]), Swap(i32[]) }
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var a: i32[] = [i, i + 1, i + 2];
        var b: Bag = Keep(a);
        var junk: i32[] = [99, 99, 99];
        acc = acc + a[0] + a[2] + junk[0];
        i = i + 1;
    }
    // (i)+(i+2)+99 = 2i+101; i=0..199 -> 39800 + 20200 = 60000
    if (acc != 60000) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64EnumReusePayloadReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, enumReusePayloadBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, enumReusePayloadBumpSrc("5000"))
	if small != large {
		t.Errorf("enum reuse payload bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunX86_64FreeOn(t, enumReuseCrossVariantSrc); code != 0 {
		t.Errorf("cross-variant reuse: code=%d (999=value/UAF, >0=over-release)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, enumReuseAliasedPayloadSrc); code != 0 {
		t.Errorf("aliased payload reuse: code=%d", code)
	}
}

func TestArm64EnumReusePayloadReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, enumReusePayloadBumpSrc("50"))
	large := mustRunArm64FreeOn(t, enumReusePayloadBumpSrc("5000"))
	if small != large {
		t.Errorf("enum reuse payload bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunArm64FreeOn(t, enumReuseCrossVariantSrc); code != 0 {
		t.Errorf("cross-variant reuse: code=%d", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, enumReuseAliasedPayloadSrc); code != 0 {
		t.Errorf("aliased payload reuse: code=%d", code)
	}
}

func TestWASMEnumReusePayloadReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, enumReusePayloadBumpSrc("50"))
	large := runWasm(t, enumReusePayloadBumpSrc("5000"))
	if small != large {
		t.Errorf("enum reuse payload bump should be bounded (old-payload reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm heap-allocates; expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, enumReuseCrossVariantSrc); got != 0 {
		t.Errorf("cross-variant reuse: got %d (999=value/UAF, >0=over-release)", got)
	}
	if got := runWasm(t, enumReuseAliasedPayloadSrc); got != 0 {
		t.Errorf("aliased payload reuse: got %d", got)
	}
}
