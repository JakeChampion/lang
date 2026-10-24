package e2e

import (
	"strings"
	"testing"
)

func strArrLit(n int) string {
	p := make([]string, n)
	for i := range p {
		p[i] = `"aaaaaaaaaaaaaaaaa"`
	}
	p[0] = "ids(" + p[0] + ")"
	return "[" + strings.Join(p, ", ") + "]"
}

func strArrDead4Src() string {
	l := strArrLit(40)
	return `@noinline function ids(s: string): string { return s; }
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
	return `@noinline function ids(s: string): string { return s; }
function main(): i32 {
    let a: string[] = ` + l + `;
    let b: string[] = ` + l + `;
    let c: string[] = ` + l + `;
    let d: string[] = ` + l + `;
    return (__heap_bump_bytes() as i32) + a[0].len() + b[0].len() + c[0].len() + d[0].len();
}`
}

const strArrAliasSrc = `@noinline function ids(s: string): string { return s; }
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
