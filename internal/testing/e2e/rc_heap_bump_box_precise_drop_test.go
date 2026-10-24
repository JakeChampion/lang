package e2e

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"strings"
	"testing"
)

func bdLit(n int) string {
	p := make([]string, n)
	for i := range p {
		p[i] = "0"
	}
	return "[" + strings.Join(p, ", ") + "]"
}

func boxDead4Src() string {
	l := bdLit(100)
	return `struct Box { data: i32[], n: i32 }
@noinline function id(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    let a: Box = Box { data: id(` + l + `), n: 1 }; let sa: i32 = a.data[0] + a.n;
    let b: Box = Box { data: id(` + l + `), n: 2 }; let sb: i32 = b.data[0] + b.n;
    let c: Box = Box { data: id(` + l + `), n: 3 }; let sc: i32 = c.data[0] + c.n;
    let d: Box = Box { data: id(` + l + `), n: 4 }; let sd: i32 = d.data[0] + d.n;
    return (__heap_bump_bytes() as i32) + sa + sb + sc + sd;
}`
}

func boxLive4Src() string {
	l := bdLit(100)
	return `struct Box { data: i32[], n: i32 }
@noinline function id(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    let a: Box = Box { data: id(` + l + `), n: 1 };
    let b: Box = Box { data: id(` + l + `), n: 2 };
    let c: Box = Box { data: id(` + l + `), n: 3 };
    let d: Box = Box { data: id(` + l + `), n: 4 };
    return (__heap_bump_bytes() as i32) + a.n + b.n + c.n + d.n;
}`
}

const boxAliasSrc = `struct Box { data: i32[], n: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let b: Box = Box { data: [i, i + 1, i + 2], n: i };
        let keep: i32[] = b.data;
        let junk: i32[] = [9, 9, 9];
        acc = acc + b.n + keep[0] + keep[2] + junk[0];
        i = i + 1;
    }
    // b.n=i, keep[0]=i, keep[2]=i+2, junk=9 -> 3i+11; i=0..199 -> 3*19900 + 2200 = 61900
    if (acc != 61900) { return 999; }
    return __rc_underflow_count();
}`

func boxEnumDead4Src() string {
	l := bdLit(100)
	return `enum E { Wrap(i32[]), Two(i32, i32) }
@noinline function id(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    let a: E = Wrap(id(` + l + `)); let sa: i32 = match (a) { Wrap(x) => x[0], Two(p, q) => p + q };
    let b: E = Wrap(id(` + l + `)); let sb: i32 = match (b) { Wrap(x) => x[0], Two(p, q) => p + q };
    let c: E = Wrap(id(` + l + `)); let sc: i32 = match (c) { Wrap(x) => x[0], Two(p, q) => p + q };
    let d: E = Wrap(id(` + l + `)); let sd: i32 = match (d) { Wrap(x) => x[0], Two(p, q) => p + q };
    return (__heap_bump_bytes() as i32) + sa + sb + sc + sd;
}`
}

func TestWASMBoxPreciseDrop(t *testing.T) {
	e2eharness.BoxedProbes(t)
	if dead, live := runWasm(t, boxDead4Src()), runWasm(t, boxLive4Src()); dead >= live {
		t.Errorf("precise drops should reclaim sequentially-dead struct boxes: dead4 %d should be < live4 %d", dead, live)
	}
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
