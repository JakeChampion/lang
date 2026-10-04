package e2e

import (
	"strings"
	"testing"
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
// escape SSO-inline, so these use 17-char literals. The first element of each
// array goes through `ids`, which hides it from the static-box plan, so every
// array is a heap box.

func strArrLit(n int) string {
	p := make([]string, n)
	for i := range p {
		p[i] = `"aaaaaaaaaaaaaaaaa"` // 17 chars -> heap (past the SSO threshold)
	}
	p[0] = "ids(" + p[0] + ")"
	return "[" + strings.Join(p, ", ") + "]"
}

// strArrDead4Src: 4 sequentially-dead string[] arrays — each fully reclaimed
// (buffer + element strings) before the next allocates.
func strArrDead4Src() string {
	l := strArrLit(40)
	return `function ids(s: string): string { return s; }
function main(): i32 {
    let a: string[] = ` + l + `; let sa: i32 = a[0].len();
    let b: string[] = ` + l + `; let sb: i32 = b[0].len();
    let c: string[] = ` + l + `; let sc: i32 = c[0].len();
    let d: string[] = ` + l + `; let sd: i32 = d[0].len();
    return (__heap_bump_bytes() as i32) + sa + sb + sc + sd;
}`
}

func strArrLive4Src() string {
	l := strArrLit(40)
	return `function ids(s: string): string { return s; }
function main(): i32 {
    let a: string[] = ` + l + `;
    let b: string[] = ` + l + `;
    let c: string[] = ` + l + `;
    let d: string[] = ` + l + `;
    return (__heap_bump_bytes() as i32) + a[0].len() + b[0].len() + c[0].len() + d[0].len();
}`
}

// strArrAliasSrc: a string element aliased into `keep` and read AFTER the
// array's precise drop, with a forced interleaved allocation (junk). The
// per-element str_dec must only DEC the aliased string (keep survives).
const strArrAliasSrc = `function ids(s: string): string { return s; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let xs: string[] = [ids("aaaaaaaaaaaaaaaaa"), "bbbbbbbbbbbbbbbbb", "ccccccccccccccccc"];
        let keep: string = xs[1];
        let junk: string[] = [ids("ddddddddddddddddd"), "eeeeeeeeeeeeeeeee", "fffffffffffffffff"];
        acc = acc + keep.len() + xs[0].len() + junk[0].len();
        i = i + 1;
    }
    // each string is 17 chars: keep + xs[0] + junk[0] = 51 per iter * 200 = 10200
    if (acc != 10200) { return 999; }
    return __rc_underflow_count();
}`

func TestWASMStringArrayPreciseDrop(t *testing.T) {
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
