package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Self-reassign of an owned struct/enum LOCAL through a method or call —
// `s = s.emit(x)` — must deep-drop the OLD value (freeing its nested
// array/struct heap), not flat-dec the box and orphan the fields. This is the
// self-host SSA-builder accumulator shape (`s.cur.insts.push(inst)` threaded
// through method calls rebuilding the builder struct each step): the flat dec
// leaked the old block's instruction buffer every emit → O(N^2) peak memory and
// OOM on large programs. The rc-gated deep-drop (is_unique on the box, rc-gated
// field drops) reclaims it without over-releasing a buffer the new value may
// share. These pin BOTH the bounded-heap win and rc balance, on all backends.

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

// Value-correct + no over-release across the accumulator loop.
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

// assertSubQuadratic: O(N) bump grows ~2x from N to 2N; an O(N^2) leak grows
// ~4x. Allow generous slack but reject quadratic.
func assertSubQuadratic(t *testing.T, backend string, n1, n2 int) {
	t.Helper()
	if n1 <= 0 {
		t.Errorf("%s: expected non-zero bump, got %d", backend, n1)
		return
	}
	if n2 > n1*3 {
		t.Errorf("%s: bump grew %dx (N -> 2N: %d -> %d); want ~2x (O(N)), not quadratic — old struct's array field is leaking", backend, n2/n1, n1, n2)
	}
}

func TestX86_64SelfReassignFieldSound(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, selfReassignFieldSoundSrc); code != 0 {
		t.Errorf("self-reassign field: got %d, want 0 (100/101=value, >0=over-release)", code)
	}
}

func TestArm64SelfReassignFieldSound(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, selfReassignFieldSoundSrc); code != 0 {
		t.Errorf("self-reassign field: got %d, want 0", code)
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
		t.Errorf("self-reassign field: got %d, want 0", got)
	}
}
