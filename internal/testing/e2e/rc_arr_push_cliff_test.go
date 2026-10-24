package e2e

import (
	"testing"
)

const arrPushCliffHealthySrc = `function step(acc: i32[], v: i32): i32[] { return acc.append(v); }
function main(): i32 {
    let acc: i32[] = [];
    let i: i32 = 0;
    while (i < 200) { acc = step(acc, i); i = i + 1; }
    if (acc.len() != 200) { return 254; }
    if (acc[7] != 7 || acc[199] != 199) { return 253; }
    return __arr_push_shared_count();
}`

const arrPushCliffSharedSrc = `function main(): i32 {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < 5) { a = a.append(i); i = i + 1; }
    let b: i32[] = a;
    let c: i32[] = a.append(99);
    if (b.len() != 5 || c.len() != 6) { return 250; }
    if (c[5] != 99 || b[4] != 4) { return 251; }
    return __arr_push_shared_count();
}`

const arrPushCliffFieldBorrowSrc = `struct Asm { code: i32[], labels: i32[] }
function label_off(a: Asm, want: i32): i32 {
    let i: i32 = 0;
    while (i < a.labels.len()) { if (a.labels[i] == want) { return i; } i = i + 1; }
    return 0 - 1;
}
function emit(own a: Asm, v: i32): Asm {
    let t: i32 = label_off(a, v);
    a = Asm { ...a, code: a.code.append(v + t) };
    return a;
}
function main(): i32 {
    let a: Asm = Asm { code: [], labels: [1, 2, 3] };
    let i: i32 = 0;
    while (i < 200) { a = emit(a, i); i = i + 1; }
    if (a.code.len() != 200) { return 254; }
    if (a.code[7] != 6 || a.code[199] != 198 || a.code[2] != 3) { return 253; }
    return __arr_push_shared_count();
}`

const arrPushCliffFieldAliasSrc = `struct Asm { code: i32[], labels: i32[] }
function grown(): i32[] {
    let c: i32[] = [];
    let i: i32 = 0;
    while (i < 5) { c = c.append(i); i = i + 1; }
    return c;
}
function emit(a: Asm, v: i32): i32 {
    let keep: Asm = a;
    a = Asm { ...a, code: a.code.append(v) };
    return a.code.len() - keep.code.len() + keep.code[a.code.len() - 6];
}
function main(): i32 {
    let a: Asm = Asm { code: grown(), labels: [] };
    if (emit(a, 9) != 1) { return 252; }
    return __arr_push_shared_count();
}`

const arrPushCliffPtrHealthySrc = `struct Item { v: i32 }
function step(acc: Item[], v: i32): Item[] { return acc.append(Item { v: v }); }
function main(): i32 {
    let acc: Item[] = [];
    let i: i32 = 0;
    while (i < 200) { acc = step(acc, i); i = i + 1; }
    if (acc.len() != 200) { return 254; }
    if (acc[7].v != 7 || acc[199].v != 199) { return 253; }
    return __arr_push_shared_count();
}`

const arrPushCliffPtrSharedSrc = `struct Item { v: i32 }
struct Bld { xs: Item[], tag: i32 }
function step(b: Bld, v: i32): Bld { return Bld { xs: b.xs.append(Item { v: v }), tag: b.tag }; }
function with_tag(b: Bld, t: i32): Bld { return Bld { xs: b.xs, tag: t }; }
function main(): i32 {
    let c: Bld = Bld { xs: [], tag: 0 };
    let i: i32 = 0;
    let sink: i32 = 0;
    while (i < 50) {
        let d: Bld = with_tag(c, i);
        c = step(c, i);
        sink = sink + d.tag + d.xs.len();
        if (i > 0) { sink = sink + d.xs[c.xs.len() - 2].v; }
        i = i + 1;
    }
    if (c.xs.len() != 50) { return 254; }
    if (sink != 3626) { return 253; }
    return __arr_push_shared_count();
}`

const arrPushCliffStrHealthySrc = `function step(acc: string[], v: i32): string[] { return acc.append("item"); }
function main(): i32 {
    let acc: string[] = [];
    let i: i32 = 0;
    while (i < 200) { acc = step(acc, i); i = i + 1; }
    if (acc.len() != 200) { return 254; }
    if (acc[7] != "item" || acc[199] != "item") { return 253; }
    return __arr_push_shared_count();
}`

const arrPushCliffStrSharedSrc = `struct Names { xs: string[], tag: i32 }
function step(b: Names, t: i32): Names { return Names { xs: b.xs.append("item"), tag: b.tag }; }
function with_tag(b: Names, t: i32): Names { return Names { xs: b.xs, tag: t }; }
function main(): i32 {
    let c: Names = Names { xs: [], tag: 0 };
    let i: i32 = 0;
    let sink: i32 = 0;
    while (i < 50) {
        let d: Names = with_tag(c, i);
        c = step(c, i);
        sink = sink + d.tag + d.xs.len();
        if (i > 0 && d.xs[c.xs.len() - 2] != "item") { return 251; }
        i = i + 1;
    }
    if (c.xs.len() != 50) { return 254; }
    if (c.xs[49] != "item") { return 252; }
    if (sink != 2450) { return 253; }
    return __arr_push_shared_count();
}`

func TestX86_64ArrPushCliffCounter(t *testing.T) {
	if _, got := compileAndRunX86_64FreeOn(t, arrPushCliffHealthySrc); got != 0 {
		t.Errorf("x86-64 healthy accumulator: __arr_push_shared_count() = %d, want 0 — "+
			"a threaded accumulator is copying its whole buffer per append (the rc==1 cliff)", got)
	}
	if _, got := compileAndRunX86_64FreeOn(t, arrPushCliffSharedSrc); got != 1 {
		t.Errorf("x86-64 shared buffer: __arr_push_shared_count() = %d, want 1 — "+
			"the counter is not reporting a copy that spare capacity could have avoided", got)
	}
	if _, got := compileAndRunX86_64FreeOn(t, arrPushCliffFieldBorrowSrc); got != 0 {
		t.Errorf("x86-64 field accumulator past a scalar-returning read: "+
			"__arr_push_shared_count() = %d, want 0", got)
	}
	if _, got := compileAndRunX86_64FreeOn(t, arrPushCliffFieldAliasSrc); got != 1 {
		t.Errorf("x86-64 field accumulator with a live container alias: "+
			"__arr_push_shared_count() = %d, want 1 — the in-place grow is observable "+
			"through the alias and must have been refused", got)
	}
	if _, got := compileAndRunX86_64FreeOn(t, arrPushCliffPtrHealthySrc); got != 0 {
		t.Errorf("x86-64 pointer-element accumulator: __arr_push_shared_count() = %d, want 0", got)
	}
	if _, got := compileAndRunX86_64FreeOn(t, arrPushCliffPtrSharedSrc); got != 45 {
		t.Errorf("x86-64 pointer-element accumulator behind a live second box: "+
			"__arr_push_shared_count() = %d, want 45 — the pointer grow helper is not "+
			"tallying the cliff it crosses", got)
	}
	if _, got := compileAndRunX86_64FreeOn(t, arrPushCliffStrHealthySrc); got != 0 {
		t.Errorf("x86-64 string-element accumulator: __arr_push_shared_count() = %d, want 0", got)
	}
	if _, got := compileAndRunX86_64FreeOn(t, arrPushCliffStrSharedSrc); got != 45 {
		t.Errorf("x86-64 string-element accumulator behind a live second box: "+
			"__arr_push_shared_count() = %d, want 45", got)
	}
}
