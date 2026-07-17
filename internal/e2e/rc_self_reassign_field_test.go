package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Self-reassign of an owned struct/enum LOCAL through a method or call —
// `s = s.emit(x)` — deep-drops the OLD value (freeing its nested array/struct
// heap) instead of flat-dec'ing the box and orphaning the fields. This is the
// self-host SSA-builder accumulator shape (`s.cur.insts.append(inst)` threaded
// through method calls that rebuild the builder each step): the flat dec leaked
// the old block's instruction buffer every emit → O(N^2) peak memory and OOM.
//
// SOUNDNESS BOUND: only structs that are transitively Map-free qualify
// (typeSelfDropSafe — Map's deep drop is incomplete). STRING fields joined the
// safe set (#3425): they are now inc'd at every sharing construction site
// (field-init emitAliasInc, struct-update base copy) and genStructDropFn's
// per-ABI freeing __fern_str_dec balances the drop side, so a string field
// shared via a functional-copy reassign is a COUNTED alias the rc-gated drop
// releases correctly. (The original exclusion dated from the era before
// strings were rc-tracked, when the same deep-drop was a UAF.) Array /
// nested-struct / enum fields were always inc'd at construction. These pin
// (1) the bounded-heap win for the string-free accumulator, (2) that a
// string-FIELD accumulator is value-correct + over-release-free now that it
// reclaims, and (3) the bounded-heap win for the string-FIELD accumulator —
// the self-host LowerState/EmitState `s = s.emit(op)` shape whose flat-dec'd
// old boxes previously pinned the ops array at rc >= 2 and turned every
// append into a whole-array clone (the #3425 Effect-A quadratic).

func selfReassignFieldBumpSrc(n string) string {
	return `struct Blk { insts: i32[] }
struct St { cur: Blk }
function (s: St) emit(x: i32): St { return St { cur: Blk { insts: s.cur.insts.append(x) } }; }
function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var s: St = St { cur: Blk { insts: [] } };
    var i: i32 = 0;
    while (i < ` + n + `) { s = s.emit(i); i = i + 1; }
    if (s.cur.insts.len() != ` + n + `) { return 999; }
    return (__heap_bump_bytes() - before) / 64;
}`
}

// Value-correct + no over-release for the string-free accumulator.
const selfReassignFieldSoundSrc = `struct Blk { insts: i32[] }
struct St { cur: Blk }
function (s: St) emit(x: i32): St { return St { cur: Blk { insts: s.cur.insts.append(x) } }; }
function main(): i32 {
    var s: St = St { cur: Blk { insts: [] } };
    var i: i32 = 0;
    while (i < 200) { s = s.emit(i * 2); i = i + 1; }
    if (s.cur.insts.len() != 200) { return 100; }
    if (s.cur.insts[199] != 398) { return 101; }
    return __rc_underflow_count();
}`

// A struct with a HEAP STRING field, functional-copy self-reassigned in a loop
// (the EmitState/`s = s.write(..)` shape). The deep-drop now FIRES here
// (strings are rc-counted at construction, so the old box's freeing
// __fern_str_dec is balanced); this must stay value-correct + over-release
// free — the guard that the lifted exclusion doesn't re-introduce the
// pre-rc-tracking UAF.
const selfReassignStringFieldSoundSrc = `struct Acc { tag: string, xs: i32[] }
function (a: Acc) step(v: i32): Acc { return Acc { tag: a.tag, xs: a.xs.append(v) }; }
function tagfor(i: i32): string { if (i % 2 == 0) { return "even-ish-longer"; } return "odd-ish-longer"; }
function main(): i32 {
    var a: Acc = Acc { tag: "seed-" + tagfor(0), xs: [] };
    var i: i32 = 0;
    while (i < 200) { a = a.step(i); i = i + 1; }
    if (a.xs.len() != 200) { return 100; }
    if (a.tag.len() < 5) { return 101; }
    return __rc_underflow_count();
}`

// selfReassignStringFieldBumpSrc is the bump-scaling twin of the soundness
// case above: a string-fielded struct LOCAL threaded through `s = s.step(v)`
// with a growing i32[] field. Before the typeSelfDropSafe string admission
// (#3425) the old box was flat-dec'd (never freed) each rebind, so every
// superseded box kept a live reference to the CURRENT xs buffer — each append
// then cloned the whole accumulated array (rc >= 2), O(N^2) bytes. With the
// deep-drop firing the shape is O(N); this pins the sub-quadratic scaling.
func selfReassignStringFieldBumpSrc(n string) string {
	return `struct Acc { tag: string, xs: i32[] }
function (a: Acc) step(v: i32): Acc { return Acc { tag: a.tag, xs: a.xs.append(v) }; }
function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var a: Acc = Acc { tag: "seed-" + "tag", xs: [] };
    var i: i32 = 0;
    while (i < ` + n + `) { a = a.step(i); i = i + 1; }
    if (a.xs.len() != ` + n + `) { return 999; }
    if (a.tag.len() != 8) { return 998; }
    return (__heap_bump_bytes() - before) / 64;
}`
}

// ssaAccumBumpSrc is the SSA-builder / emitter accumulator shape the self-host
// actually uses: a struct with a STRING field AND a growing i32[], threaded
// through a PARAMETER that is self-reassigned each step (`s = s.emit(x)`), then
// returned. The borrow model kept such a string-bearing param borrowed, so the
// reassignment-overwrite only flat-dec'd the old value — orphaning the old
// instruction buffer every step → O(N^2) peak (the `-ssa` OOM). The
// consumed-threaded-param promotion (computeConsumedParams) deep-drops the old
// value, restoring O(N). Returns the bump high-water / 64.
func ssaAccumBumpSrc(n string) string {
	return `struct Bld { name: string, insts: i32[] }
function (s: Bld) emit(x: i32): Bld { return Bld { name: s.name, insts: s.insts.append(x) }; }
function build(s: Bld, n: i32): Bld {
    var i: i32 = 0;
    while (i < n) { s = s.emit(i); i = i + 1; }
    return s;
}
function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var s: Bld = build(Bld { name: "fn", insts: [] }, ` + n + `);
    if (s.insts.len() != ` + n + `) { return 200; }
    return (__heap_bump_bytes() - before) / 64;
}`
}

// ssaAccumSoundSrc: value-correct + no over-release for the threaded-param
// string-bearing accumulator (the consumed-param promotion must keep the rc
// accurate — a still-shared value stays rc>1 and is only flat-dec'd).
const ssaAccumSoundSrc = `struct Bld { name: string, insts: i32[] }
function (s: Bld) emit(x: i32): Bld { return Bld { name: s.name, insts: s.insts.append(x) }; }
function build(s: Bld, n: i32): Bld {
    var i: i32 = 0;
    while (i < n) { s = s.emit(i * 2); i = i + 1; }
    return s;
}
function main(): i32 {
    var s: Bld = build(Bld { name: "seed", insts: [] }, 200);
    if (s.insts.len() != 200) { return 100; }
    if (s.insts[199] != 398) { return 101; }
    return __rc_underflow_count();
}`

func assertSubQuadratic(t *testing.T, backend string, n1, n2 int) {
	t.Helper()
	if n1 <= 0 {
		t.Errorf("%s: expected non-zero bump, got %d", backend, n1)
		return
	}
	if n2 > n1*3 {
		t.Errorf("%s: bump grew %dx (N -> 2N: %d -> %d); want ~2x (O(N)), not quadratic", backend, n2/n1, n1, n2)
	}
}

func TestX86_64SelfReassignFieldSound(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, selfReassignFieldSoundSrc); code != 0 {
		t.Errorf("string-free accumulator: got %d, want 0 (100/101=value, >0=over-release)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, selfReassignStringFieldSoundSrc); code != 0 {
		t.Errorf("string-field accumulator: got %d, want 0 (UAF regression guard)", code)
	}
}

// The string-fielded LOCAL accumulator (`s = s.step(v)` on a struct with a
// string field + growing i32[]) reclaims O(N) now that typeSelfDropSafe admits
// strings (#3425) — the LowerState/EmitState threading shape.
func TestX86_64SelfReassignStringFieldBounded(t *testing.T) {
	_, n1 := compileAndRunX86_64FreeOn(t, selfReassignStringFieldBumpSrc("200"))
	_, n2 := compileAndRunX86_64FreeOn(t, selfReassignStringFieldBumpSrc("400"))
	assertSubQuadratic(t, "x86-64", n1, n2)
}

// The SSA-builder-shaped accumulator (string field + growing i32[], threaded
// through a self-reassigned parameter) is O(N) and over-release-free — the
// borrow-inference fix for the `-ssa` OOM.
func TestX86_64SSAAccumThreadedParam(t *testing.T) {
	_, n1 := compileAndRunX86_64FreeOn(t, ssaAccumBumpSrc("200"))
	_, n2 := compileAndRunX86_64FreeOn(t, ssaAccumBumpSrc("400"))
	assertSubQuadratic(t, "x86-64", n1, n2)
	if _, code := compileAndRunX86_64FreeOn(t, ssaAccumSoundSrc); code != 0 {
		t.Errorf("threaded-param accumulator: got %d, want 0 (100/101=value, >0=over-release)", code)
	}
}

func TestArm64SelfReassignFieldSound(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, selfReassignFieldSoundSrc); code != 0 {
		t.Errorf("string-free accumulator: got %d, want 0", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, selfReassignStringFieldSoundSrc); code != 0 {
		t.Errorf("string-field accumulator: got %d, want 0", code)
	}
}

// arm64 twin of TestX86_64SelfReassignStringFieldBounded (two-word strings:
// the construction __fern_str_inc + genStructDropFn's WidthString str_dec arm).
func TestArm64SelfReassignStringFieldBounded(t *testing.T) {
	_, n1 := compileAndRunArm64FreeOn(t, selfReassignStringFieldBumpSrc("200"))
	_, n2 := compileAndRunArm64FreeOn(t, selfReassignStringFieldBumpSrc("400"))
	assertSubQuadratic(t, "arm64", n1, n2)
}

func TestWASMSelfReassignFieldBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	n1 := runWasm(t, selfReassignFieldBumpSrc("200"))
	n2 := runWasm(t, selfReassignFieldBumpSrc("400"))
	assertSubQuadratic(t, "wasm", n1, n2)
	if got := runWasm(t, selfReassignFieldSoundSrc); got != 0 {
		t.Errorf("string-free accumulator: got %d, want 0", got)
	}
	if got := runWasm(t, selfReassignStringFieldSoundSrc); got != 0 {
		t.Errorf("string-field accumulator: got %d, want 0 (UAF regression guard)", got)
	}
	// String-fielded LOCAL accumulator reclaims O(N) (typeSelfDropSafe string
	// admission, #3425) — two-word strings ride __fern_str_inc / the
	// WidthString str_dec arm of __drop_struct_.
	sn1 := runWasm(t, selfReassignStringFieldBumpSrc("200"))
	sn2 := runWasm(t, selfReassignStringFieldBumpSrc("400"))
	assertSubQuadratic(t, "wasm string-field", sn1, sn2)
	// The threaded-param accumulator (the self-host's actual shape) is
	// over-release-free on wasm. Its O(N) reclamation is verified on the native
	// `-ssa` backends (TestX86_64SSAAccumThreadedParam); wasm keeps a separate,
	// pre-existing function-boundary reclamation limitation for this shape, so
	// only soundness is pinned here.
	if got := runWasm(t, ssaAccumSoundSrc); got != 0 {
		t.Errorf("threaded-param accumulator: got %d, want 0 (over-release)", got)
	}
}

// arm64 counterpart of the threaded-param SSA accumulator soundness check.
func TestArm64SSAAccumThreadedParam(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, ssaAccumSoundSrc); code != 0 {
		t.Errorf("threaded-param accumulator: got %d, want 0 (over-release)", code)
	}
}
