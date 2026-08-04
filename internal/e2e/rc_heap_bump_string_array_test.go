package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Perceus precise drops — slice 3: string[] arrays. Completes the array
// element scope (primitive in slice 1, rc-element in slice 2). A dead string[]
// is dropped at its last use via __fern_drop_arr_str (two-word wasm/arm64) /
// __fern_drop_arr_ptr (native single-word) — str_dec'ing each element string
// then freeing the buffer — so the whole structure (buffer + heap strings)
// reclaims early, not just the outer buffer. (emitOwnedSlotDrop gained the
// string-element branch, which also fixes loop-reinit string[] drops.)
//
// Soundness is the same invariant + alias gates as slices 1/2: each element's
// str_dec is is_unique-gated, so a string element aliased into a live local
// only DECs (the alias keeps its buffer). Heap strings need >15 bytes to
// escape SSO-inline, so these use 17-char literals.

func strArrLit(n int) string {
	p := make([]string, n)
	for i := range p {
		p[i] = `"aaaaaaaaaaaaaaaaa"` // 17 chars -> heap (past the SSO threshold)
	}
	return "[" + strings.Join(p, ", ") + "]"
}

// strArrDead4Src: 4 sequentially-dead string[] arrays — each fully reclaimed
// (buffer + element strings) before the next allocates.
func strArrDead4Src() string {
	l := strArrLit(40)
	return `function main(): i32 {
    var a: string[] = ` + l + `; var sa: i32 = a[0].len();
    var b: string[] = ` + l + `; var sb: i32 = b[0].len();
    var c: string[] = ` + l + `; var sc: i32 = c[0].len();
    var d: string[] = ` + l + `; var sd: i32 = d[0].len();
    return (__heap_bump_bytes() as i32) + sa + sb + sc + sd;
}`
}

func strArrLive4Src() string {
	l := strArrLit(40)
	return `function main(): i32 {
    var a: string[] = ` + l + `;
    var b: string[] = ` + l + `;
    var c: string[] = ` + l + `;
    var d: string[] = ` + l + `;
    return (__heap_bump_bytes() as i32) + a[0].len() + b[0].len() + c[0].len() + d[0].len();
}`
}

// strArrAliasSrc: a string element aliased into `keep` and read AFTER the
// array's precise drop, with a forced interleaved allocation (junk). The
// per-element str_dec must only DEC the aliased string (keep survives).
const strArrAliasSrc = `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var xs: string[] = ["aaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbb", "ccccccccccccccccc"];
        var keep: string = xs[1];
        var junk: string[] = ["ddddddddddddddddd", "eeeeeeeeeeeeeeeee", "fffffffffffffffff"];
        acc = acc + keep.len() + xs[0].len() + junk[0].len();
        i = i + 1;
    }
    // each string is 17 chars: keep + xs[0] + junk[0] = 51 per iter * 200 = 10200
    if (acc != 10200) { return 999; }
    return __rc_underflow_count();
}`

func TestWASMStringArrayPreciseDrop(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	dead := runWasm(t, strArrDead4Src())
	live := runWasm(t, strArrLive4Src())
	if dead >= live {
		t.Errorf("precise drops should reclaim sequentially-dead string[]: dead4 %d should be < live4 %d", dead, live)
	}
	if got := runWasm(t, strArrAliasSrc); got != 0 {
		t.Errorf("aliased string-element soundness: got %d", got)
	}
}

func TestX86_64StringArrayPreciseDrop(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, strArrAliasSrc); code != 0 {
		t.Errorf("aliased string-element soundness: code=%d", code)
	}
}

func TestArm64StringArrayPreciseDrop(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, strArrAliasSrc); code != 0 {
		t.Errorf("aliased string-element soundness: code=%d", code)
	}
}
