package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Perceus precise drops — slice 4: STRUCT + tuple box types. A dead owned
// struct/tuple local is dropped at its last use via emitOwnedSlotDrop (the
// deep __drop_struct_ / __drop_tuple_ fn — frees the box AND its rc-tracked
// fields), so the whole structure reclaims early. The peak-memory win shows
// when the box holds a large field (an array buffer).
//
// Sound because struct/tuple construction INCs its pointer fields (StructLit /
// TupleLit), so a precise drop is rc-protected — a field aliased into a live
// local only DECs (the same reason slice-2 rc-element arrays are sound). ENUMs
// are NOT precise-dropped: their construction doesn't rc-count payloads, so
// they're excluded (and are built via variant-constructor calls that
// initMayAliasLive already gates).

func bdLit(n int) string {
	p := make([]string, n)
	for i := range p {
		p[i] = "0"
	}
	return "[" + strings.Join(p, ", ") + "]"
}

// boxDead4Src: 4 sequentially-dead structs, each holding a size-class array —
// the box + its array reclaim before the next allocates.
func boxDead4Src() string {
	l := bdLit(100)
	return `struct Box { data: i32[], n: i32 }
function main(): i32 {
    var a: Box = Box { data: ` + l + `, n: 1 }; var sa: i32 = a.data[0] + a.n;
    var b: Box = Box { data: ` + l + `, n: 2 }; var sb: i32 = b.data[0] + b.n;
    var c: Box = Box { data: ` + l + `, n: 3 }; var sc: i32 = c.data[0] + c.n;
    var d: Box = Box { data: ` + l + `, n: 4 }; var sd: i32 = d.data[0] + d.n;
    return (__heap_bump_bytes() as i32) + sa + sb + sc + sd;
}`
}

func boxLive4Src() string {
	l := bdLit(100)
	return `struct Box { data: i32[], n: i32 }
function main(): i32 {
    var a: Box = Box { data: ` + l + `, n: 1 };
    var b: Box = Box { data: ` + l + `, n: 2 };
    var c: Box = Box { data: ` + l + `, n: 3 };
    var d: Box = Box { data: ` + l + `, n: 4 };
    return (__heap_bump_bytes() as i32) + a.n + b.n + c.n + d.n;
}`
}

// boxAliasSrc: a struct's array field aliased into a live local + read after
// the struct's precise drop; the deep struct drop must only DEC the shared
// array (keep survives). Forced interleaved alloc would corrupt a wrong free.
const boxAliasSrc = `struct Box { data: i32[], n: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var b: Box = Box { data: [i, i + 1, i + 2], n: i };
        var keep: i32[] = b.data;
        var junk: i32[] = [9, 9, 9];
        acc = acc + b.n + keep[0] + keep[2] + junk[0];
        i = i + 1;
    }
    // b.n=i, keep[0]=i, keep[2]=i+2, junk=9 -> 3i+11; i=0..199 -> 3*19900 + 2200 = 61900
    if (acc != 61900) { return 999; }
    return __rc_underflow_count();
}`

// boxEnumDead4Src: a heap enum carrying an array payload, sequentially dead.
func boxEnumDead4Src() string {
	l := bdLit(100)
	return `enum E { Wrap(i32[]), Two(i32, i32) }
function main(): i32 {
    var a: E = Wrap(` + l + `); var sa: i32 = match (a) { Wrap(x) => x[0], Two(p, q) => p + q };
    var b: E = Wrap(` + l + `); var sb: i32 = match (b) { Wrap(x) => x[0], Two(p, q) => p + q };
    var c: E = Wrap(` + l + `); var sc: i32 = match (c) { Wrap(x) => x[0], Two(p, q) => p + q };
    var d: E = Wrap(` + l + `); var sd: i32 = match (d) { Wrap(x) => x[0], Two(p, q) => p + q };
    return (__heap_bump_bytes() as i32) + sa + sb + sc + sd;
}`
}

func TestWASMBoxPreciseDrop(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if dead, live := runWasm(t, boxDead4Src()), runWasm(t, boxLive4Src()); dead >= live {
		t.Errorf("precise drops should reclaim sequentially-dead struct boxes: dead4 %d should be < live4 %d", dead, live)
	}
	// Enum boxes are deliberately NOT precise-dropped (enum construction
	// doesn't rc-count payloads), but they must stay value-correct + leak-free
	// — exercise the enum-of-array shape as a no-regression / soundness check.
	if got := runWasm(t, boxEnumDead4Src()); got == 0 {
		t.Errorf("enum-of-array program should produce a non-zero high-water, got 0")
	}
	if got := runWasm(t, boxAliasSrc); got != 0 {
		t.Errorf("aliased struct-field soundness: got %d", got)
	}
}

func TestX86_64BoxPreciseDrop(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, boxAliasSrc); code != 0 {
		t.Errorf("aliased struct-field soundness: code=%d", code)
	}
}

func TestArm64BoxPreciseDrop(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, boxAliasSrc); code != 0 {
		t.Errorf("aliased struct-field soundness: code=%d", code)
	}
}
