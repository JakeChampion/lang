package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Self-reassign of an owned struct/enum LOCAL through a method or call —
// `s = s.emit(x)` — deep-drops the OLD value (freeing its nested array/struct
// heap) instead of flat-dec'ing the box and orphaning the fields. This is the
// self-host SSA-builder accumulator shape (`s.cur.insts.push(inst)` threaded
// through method calls that rebuild the builder each step): the flat dec leaked
// the old block's instruction buffer every emit → O(N^2) peak memory and OOM.
//
// SOUNDNESS BOUND: only structs that are transitively string- and Map-free
// qualify (typeSelfDropSafe). Strings aren't rc-tracked — never inc'd at struct
// construction — so a string field shared via a functional-copy reassign is an
// UNCOUNTED alias the deep-drop would over-release (the self-host EmitState UAF
// that an earlier, unrestricted attempt hit). Array / nested-struct / enum
// fields ARE inc'd at construction, so their rc-gated drop is balanced. These
// pin (1) the bounded-heap win for the string-free accumulator and (2) that a
// string-FIELD accumulator stays correct (kept on the safe-leak flat dec).

func selfReassignFieldBumpSrc(n string) string {
	return `struct Blk { insts: i32[] }
struct St { cur: Blk }
function (s: St) emit(x: i32): St { return St { cur: Blk { insts: s.cur.insts.push(x) } }; }
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
function (s: St) emit(x: i32): St { return St { cur: Blk { insts: s.cur.insts.push(x) } }; }
function main(): i32 {
    var s: St = St { cur: Blk { insts: [] } };
    var i: i32 = 0;
    while (i < 200) { s = s.emit(i * 2); i = i + 1; }
    if (s.cur.insts.len() != 200) { return 100; }
    if (s.cur.insts[199] != 398) { return 101; }
    return __rc_underflow_count();
}`

// A struct with a HEAP STRING field, functional-copy self-reassigned in a loop
// (the EmitState/`s = s.write(..)` shape). The deep-drop is correctly WITHHELD
// here (strings are uncounted), so this must stay value-correct + over-release
// free — the regression guard for the earlier UAF.
const selfReassignStringFieldSoundSrc = `struct Acc { tag: string, xs: i32[] }
function (a: Acc) step(v: i32): Acc { return Acc { tag: a.tag, xs: a.xs.push(v) }; }
function tagfor(i: i32): string { if (i % 2 == 0) { return "even-ish-longer"; } return "odd-ish-longer"; }
function main(): i32 {
    var a: Acc = Acc { tag: "seed-" + tagfor(0), xs: [] };
    var i: i32 = 0;
    while (i < 200) { a = a.step(i); i = i + 1; }
    if (a.xs.len() != 200) { return 100; }
    if (a.tag.len() < 5) { return 101; }
    return __rc_underflow_count();
}`

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
function (s: Bld) emit(x: i32): Bld { return Bld { name: s.name, insts: s.insts.push(x) }; }
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
function (s: Bld) emit(x: i32): Bld { return Bld { name: s.name, insts: s.insts.push(x) }; }
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
