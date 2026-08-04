package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Perceus precise drops (garbage-free, straight-line subset). An owned
// primitive-element array local (i32[] / u8[] / f32[] ...) is dropped right
// AFTER its last top-level use instead of at the function-exit sweep, so a
// later same-shaped allocation reuses the freed block — lowering peak memory.
// Slice 1 scopes to primitive arrays: the large data buffers whose drop is a
// pure buffer free (no element/field decs). See computePreciseDrops.
//
// The win is demonstrated by comparing 4 SEQUENTIALLY-DEAD arrays (each used
// then dead before the next is allocated) against 4 SIMULTANEOUSLY-LIVE ones
// (all read at the end): with precise drops the dead version reclaims to ~1
// block while the live version holds all 4. The wasm bump probe measures it
// directly; natives' segregated-freelist arena is insensitive to the bump
// cursor (it reads a flat low high-water either way), so for natives the
// tests assert value-correctness + 0 over-release. The aliasing case pins
// the soundness invariant: a local inc'd into a container that outlives its
// last bare use is only DEC'd by the precise drop (the container's reference
// survives), never freed early.

func pdLit(n int) string {
	p := make([]string, n)
	for i := range p {
		p[i] = "0"
	}
	return "[" + strings.Join(p, ", ") + "]"
}

// seqDead4Src: 4 arrays each dead before the next allocates (precise drop
// reclaims each) — peak ~1 block.
func seqDead4Src() string {
	l := pdLit(100) // 400-byte payload -> size-class block (recyclable)
	return `function main(): i32 {
    var a: i32[] = ` + l + `; var sa: i32 = a[0];
    var b: i32[] = ` + l + `; var sb: i32 = b[0];
    var c: i32[] = ` + l + `; var sc: i32 = c[0];
    var d: i32[] = ` + l + `; var sd: i32 = d[0];
    return (__heap_bump_bytes() as i32) + sa + sb + sc + sd;
}`
}

// live4Src: the same 4 arrays, all read at the END (all live to function
// exit) — peak ~4 blocks. The control that precise drops must beat.
func live4Src() string {
	l := pdLit(100)
	return `function main(): i32 {
    var a: i32[] = ` + l + `;
    var b: i32[] = ` + l + `;
    var c: i32[] = ` + l + `;
    var d: i32[] = ` + l + `;
    return (__heap_bump_bytes() as i32) + a[0] + b[0] + c[0] + d[0];
}`
}

// pdValuesSrc: distinct values across sequential precise-dropped arrays.
const pdValuesSrc = `function main(): i32 {
    var a: i32[] = [10, 20, 30]; var sa: i32 = a[0] + a[2];
    var b: i32[] = [1, 2, 3]; var sb: i32 = b[1];
    var c: i32[] = [100, 200]; var sc: i32 = c[0] + c[1];
    if (sa != 40) { return 901; }
    if (sb != 2) { return 902; }
    if (sc != 300) { return 903; }
    return __rc_underflow_count();
}`

// pdAliasSrc: `a` is inc'd into a struct that outlives a's last bare use;
// the precise drop must only dec (the struct keeps it). Forced interleaved
// allocation (junk) would corrupt a wrongly-freed buffer.
const pdAliasSrc = `struct Holder { items: i32[], n: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var a: i32[] = [i, i + 1, i + 2];
        var h: Holder = Holder{ items: a, n: 0 };
        var sa: i32 = a[0];
        var junk: i32[] = [7, 7, 7];
        acc = acc + sa + h.items[2] + junk[0];
        i = i + 1;
    }
    // sa=i, h.items[2]=i+2, junk=7 -> 2i+9; i=0..199 -> 39800 + 1800 = 41600
    if (acc != 41600) { return 999; }
    return __rc_underflow_count();
}`

// pdArgReturnSrc: a non-inlinable function that ALWAYS returns its argument,
// so `b` aliases `a`'s buffer. `a`'s last bare use is `biggy(a)` — but
// because biggy returns its param, the borrow model makes the param OWNED and
// incs `a` at the call, so the precise drop only DECs (b's reference
// survives). The forced `junk` allocation would corrupt a wrongly-freed
// buffer. This pins the "precise drop is just a dec; a counted alias
// survives" invariant for the function-return-of-arg shape.
const pdArgReturnSrc = `function biggy(xs: i32[]): i32[] {
    var w: i32 = 0;
    var j: i32 = 0;
    while (j < 3) { w = w + xs[j]; j = j + 1; }
    if (w < -999999) { return xs; }
    return xs;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var a: i32[] = [i, i + 1, i + 2];
        var b: i32[] = biggy(a);
        var junk: i32[] = [7, 7, 7];
        acc = acc + b[0] + b[2] + junk[0];
        i = i + 1;
    }
    // b[0]=i, b[2]=i+2, junk=7 -> 2i+9; i=0..199 -> 41600
    if (acc != 41600) { return 999; }
    return __rc_underflow_count();
}`

// --- Slice 2: rc-element arrays (arrays of boxes / nested arrays). Their
// precise drop is the deep __drop_arr_* loop (frees the element boxes / inner
// buffers + the outer buffer), so a sequentially-dead rc-element array
// reclaims its WHOLE structure early, not just the outer buffer. ---

// rcArrDead4Src: 4 sequentially-dead i32[][] (array-of-arrays) — each fully
// reclaimed before the next allocates.
func rcArrDead4Src() string {
	row := pdLit(64) // inner buffer, size-class
	mk := "[" + row + ", " + row + ", " + row + ", " + row + "]"
	return `function main(): i32 {
    var a: i32[][] = ` + mk + `; var sa: i32 = a[0][0];
    var b: i32[][] = ` + mk + `; var sb: i32 = b[0][0];
    var c: i32[][] = ` + mk + `; var sc: i32 = c[0][0];
    var d: i32[][] = ` + mk + `; var sd: i32 = d[0][0];
    return (__heap_bump_bytes() as i32) + sa + sb + sc + sd;
}`
}

func rcArrLive4Src() string {
	row := pdLit(64)
	mk := "[" + row + ", " + row + ", " + row + ", " + row + "]"
	return `function main(): i32 {
    var a: i32[][] = ` + mk + `;
    var b: i32[][] = ` + mk + `;
    var c: i32[][] = ` + mk + `;
    var d: i32[][] = ` + mk + `;
    return (__heap_bump_bytes() as i32) + a[0][0] + b[0][0] + c[0][0] + d[0][0];
}`
}

// rcArrValuesSrc: array-of-struct (P[]), distinct values, with an aliased
// element kept live — the deep struct-array drop must only DEC the shared
// element box, not free it.
const rcArrValuesSrc = `struct P { x: i32, y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var ps: P[] = [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }];
        var keep: P = ps[0];
        var junk: i32[] = [7, 7, 7];
        acc = acc + ps[1].x + keep.y + junk[0];
        i = i + 1;
    }
    // ps[1].x=i+2, keep.y=i+1, junk=7 -> 2i+10; i=0..199 -> 39800+2000 = 41800
    if (acc != 41800) { return 999; }
    return __rc_underflow_count();
}`

func TestWASMPreciseDrops(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	dead := runWasm(t, seqDead4Src())
	live := runWasm(t, live4Src())
	if dead >= live {
		t.Errorf("precise drops should reclaim sequentially-dead arrays: dead4 high-water %d should be < live4 %d", dead, live)
	}
	rcDead := runWasm(t, rcArrDead4Src())
	rcLive := runWasm(t, rcArrLive4Src())
	if rcDead >= rcLive {
		t.Errorf("precise drops should reclaim sequentially-dead rc-element arrays: dead4 %d should be < live4 %d", rcDead, rcLive)
	}
	if pdValues := runWasm(t, pdValuesSrc); pdValues != 0 {
		t.Errorf("value correctness / over-release: got %d", pdValues)
	}
	if got := runWasm(t, pdAliasSrc); got != 0 {
		t.Errorf("aliased-into-container soundness: got %d", got)
	}
	if got := runWasm(t, pdArgReturnSrc); got != 0 {
		t.Errorf("function-return-of-arg soundness: got %d", got)
	}
	if got := runWasm(t, rcArrValuesSrc); got != 0 {
		t.Errorf("rc-element array value/alias soundness: got %d", got)
	}
}

func TestX86_64PreciseDrops(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, pdValuesSrc); code != 0 {
		t.Errorf("value correctness / over-release: code=%d", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, pdAliasSrc); code != 0 {
		t.Errorf("aliased-into-container soundness: code=%d", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, pdArgReturnSrc); code != 0 {
		t.Errorf("function-return-of-arg soundness: code=%d", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, rcArrValuesSrc); code != 0 {
		t.Errorf("rc-element array value/alias soundness: code=%d", code)
	}
}

func TestArm64PreciseDrops(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, pdValuesSrc); code != 0 {
		t.Errorf("value correctness / over-release: code=%d", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, pdAliasSrc); code != 0 {
		t.Errorf("aliased-into-container soundness: code=%d", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, pdArgReturnSrc); code != 0 {
		t.Errorf("function-return-of-arg soundness: code=%d", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, rcArrValuesSrc); code != 0 {
		t.Errorf("rc-element array value/alias soundness: code=%d", code)
	}
}
