package e2e

import (
	"testing"
)

const genReuseChurnSrc = `struct Point { x: i32, y: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 300) {
        let a: Point = Point { x: i, y: i + 1 };
        let s: i32 = a.x + a.y;          // a's last use
        let b: Point = Point { x: s + 1, y: i };   // reuses a's box
        acc = acc + b.x + b.y;
        i = i + 1;
    }
    // s = 2i+1, b.x = 2i+2, b.y = i -> b.x+b.y = 3i+2; sum i=0..299 = 3*44850 + 600 = 135150
    if (acc != 135150) { return 999; }
    return __rc_underflow_count();
}`

const genReuseAliasedSrc = `struct Point { x: i32, y: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let a: Point = Point { x: i, y: i + 1 };
        let keep: Point = a;              // alias -> rc 2, reuse declines
        let s: i32 = a.x;
        let b: Point = Point { x: s, y: 7 };
        acc = acc + keep.x + keep.y + b.y;   // keep sees a's [i, i+1]; b.y=7
        i = i + 1;
    }
    // keep.x + keep.y + b.y = i + (i+1) + 7 = 2i+8; sum i=0..199 = 2*19900 + 1600 = 41400
    if (acc != 41400) { return 999; }
    return __rc_underflow_count();
}`

const genReusePtrChurnSrc = `struct Holder { id: i32, items: i32[] }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let a: Holder = Holder { id: i, items: [i, i + 1] };
        let s: i32 = a.id + a.items[0] + a.items[1];   // a's last use
        let b: Holder = Holder { id: s, items: [i + 2, i + 3] };   // reuses a's box
        acc = acc + b.id + b.items[0] + b.items[1];
        i = i + 1;
    }
    // s = i + i + (i+1) = 3i+1; b.id=3i+1, b.items sum = (i+2)+(i+3)=2i+5; total 5i+6
    // sum i=0..199 = 5*19900 + 6*200 = 100700
    if (acc != 100700) { return 999; }
    return __rc_underflow_count();
}`

const genReusePtrAliasedSrc = `struct Holder { id: i32, items: i32[] }
function main(): i32 {
    let a: Holder = Holder { id: 1, items: [7, 8] };
    let keep: Holder = a;             // alias -> rc 2, reuse declines
    let s: i32 = a.id;
    let b: Holder = Holder { id: s + 1, items: [3, 4] };
    if (keep.id != 1) { return 1; }
    if (keep.items[0] != 7) { return 2; }
    if (b.id != 2) { return 3; }
    if (b.items[1] != 4) { return 4; }
    return __rc_underflow_count();
}`

const genReuseCrossTypeChurnSrc = `struct Point { x: i32, y: i32 }
struct Pair { a: i32, b: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 300) {
        let p: Point = Point { x: i, y: i + 1 };
        let s: i32 = p.x + p.y;          // p's last use
        let q: Pair = Pair { a: s, b: i };   // reuses p's box (same class)
        acc = acc + q.a + q.b;
        i = i + 1;
    }
    // s = 2i+1, q.a=2i+1, q.b=i -> q.a+q.b = 3i+1; sum i=0..299 = 3*44850 + 300 = 134850
    if (acc != 134850) { return 999; }
    return __rc_underflow_count();
}`

const genReuseCrossTypePtrSrc = `struct Holder { id: i32, items: i32[] }
struct Bag { tag: i32, data: i32[] }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let a: Holder = Holder { id: i, items: [i, i + 1] };
        let s: i32 = a.id + a.items[0] + a.items[1];   // a's last use
        let b: Bag = Bag { tag: s, data: [i + 2, i + 3] };   // reuses a's box
        acc = acc + b.tag + b.data[0] + b.data[1];
        i = i + 1;
    }
    // s = i + i + (i+1) = 3i+1; b.tag=3i+1, b.data sum = 2i+5; total 5i+6
    // sum i=0..199 = 5*19900 + 6*200 = 100700
    if (acc != 100700) { return 999; }
    return __rc_underflow_count();
}`

const genReuseTupleChurnSrc = `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 300) {
        let a: (i32, i32) = (i, i + 1);
        let s: i32 = a.0 + a.1;          // a's last use
        let b: (i32, i32) = (s + 1, i);   // reuses a's box
        acc = acc + b.0 + b.1;
        i = i + 1;
    }
    // s = 2i+1, b.0=2i+2, b.1=i -> b.0+b.1 = 3i+2; sum i=0..299 = 3*44850 + 600 = 135150
    if (acc != 135150) { return 999; }
    return __rc_underflow_count();
}`

const genReuseTuplePtrSrc = `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let a: (i32, i32[]) = (i, [i, i + 1]);
        let s: i32 = a.0 + a.1[0] + a.1[1];   // a's last use
        let b: (i32, i32[]) = (s, [i + 2, i + 3]);   // reuses a's box
        acc = acc + b.0 + b.1[0] + b.1[1];
        i = i + 1;
    }
    // s = i + i + (i+1) = 3i+1; b.0=3i+1, b.1 sum = 2i+5; total 5i+6
    // sum i=0..199 = 5*19900 + 6*200 = 100700
    if (acc != 100700) { return 999; }
    return __rc_underflow_count();
}`

const genReuseEnumChurnSrc = `enum Wrapper { Wrap(i32[]) }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let a: Wrapper = Wrap([i, i + 1]);
        let s: i32 = match (a) { Wrap(xs) => xs[0] + xs[1] };   // a's last use
        let b: Wrapper = Wrap([s, i]);                          // reuses a's box
        acc = acc + match (b) { Wrap(xs) => xs[0] + xs[1] };
        i = i + 1;
    }
    // s = i + (i+1) = 2i+1; b = [2i+1, i] -> sum 3i+1; total i=0..199 = 3*19900 + 200 = 59900
    if (acc != 59900) { return 999; }
    return __rc_underflow_count();
}`

const genReuseEnumCrossVariantSrc = `enum Bag { Keep(i32[]), Swap(i32[]) }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let a: Bag = Keep([i, i + 1]);
        let s: i32 = match (a) { Keep(xs) => xs[0], Swap(xs) => xs[1] };   // a dead
        let b: Bag = Swap([s, i + 2]);                                     // reuses a's box
        acc = acc + match (b) { Keep(xs) => xs[0], Swap(xs) => xs[1] };
        i = i + 1;
    }
    // s = i (Keep.xs[0]); b = Swap([i, i+2]) -> match picks xs[1] = i+2; total i=0..199 = sum(i+2) = 19900 + 400 = 20300
    if (acc != 20300) { return 999; }
    return __rc_underflow_count();
}`

const genReuseCrossBlockScalarSrc = `struct Point { x: i32, y: i32 }
function run(go_: boolean): i32 {
    let a: Point = Point { x: 10, y: 20 };
    let s: i32 = a.x + a.y;          // a's last use, before the if
    let acc: i32 = 0;
    if (go_) {
        let b: Point = Point { x: s + 1, y: 5 };   // reuses a's box on this path
        acc = b.x + b.y;
    }
    return acc;
}
function main(): i32 {
    let t: i32 = run(true);    // s=30; b={31,5} -> 36
    let f: i32 = run(false);   // a exit-swept, acc=0
    if (t != 36) { return 1; }
    if (f != 0) { return 2; }
    return __rc_underflow_count();
}`

const genReuseCrossBlockPtrSrc = `struct Holder { id: i32, items: i32[] }
function runp(go_: boolean): i32 {
    let a: Holder = Holder { id: 1, items: [7, 8] };
    let s: i32 = a.id + a.items[0] + a.items[1];   // a's last use
    let acc: i32 = 0;
    if (go_) {
        let b: Holder = Holder { id: s, items: [3, 4] };   // reuses a's box; a's [7,8] freed here
        acc = b.id + b.items[0] + b.items[1];
    }
    return acc;
}
function main(): i32 {
    let t: i32 = runp(true);   // s=16; b={16,[3,4]} -> 23
    let f: i32 = runp(false);  // a (with [7,8]) exit-swept
    if (t != 23) { return 1; }
    if (f != 0) { return 2; }
    return __rc_underflow_count();
}`

const genReuseCrossBlockLoopSrc = `struct Holder { id: i32, items: i32[] }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let a: Holder = Holder { id: i, items: [i, i + 1] };
        let s: i32 = a.id + a.items[0] + a.items[1];   // a's last use in the loop body
        if (i % 2 == 0) {
            let b: Holder = Holder { id: s, items: [i + 2, i + 3] };   // reuses a's box
            acc = acc + b.id + b.items[0] + b.items[1];
        }
        i = i + 1;
    }
    // even i in [0,198]: s = i + i + (i+1) = 3i+1; b.id=3i+1, b.items=(i+2)+(i+3)=2i+5; per = 5i+6
    // i=2j, j=0..99: 5*2j+6 = 10j+6; sum = 10*(99*100/2) + 6*100 = 49500 + 600 = 50100
    if (acc != 50100) { return 999; }
    return __rc_underflow_count();
}`

var genReuseCases = []struct{ name, src string }{
	{"crossblock_scalar", genReuseCrossBlockScalarSrc},
	{"crossblock_ptr", genReuseCrossBlockPtrSrc},
	{"crossblock_loop", genReuseCrossBlockLoopSrc},
	{"churn", genReuseChurnSrc},
	{"aliased", genReuseAliasedSrc},
	{"ptr_churn", genReusePtrChurnSrc},
	{"ptr_aliased", genReusePtrAliasedSrc},
	{"crosstype_churn", genReuseCrossTypeChurnSrc},
	{"crosstype_ptr", genReuseCrossTypePtrSrc},
	{"tuple_churn", genReuseTupleChurnSrc},
	{"tuple_ptr", genReuseTuplePtrSrc},
	{"enum_churn", genReuseEnumChurnSrc},
	{"enum_cross_variant", genReuseEnumCrossVariantSrc},
}

func TestX86_64GeneralReuse(t *testing.T) {
	for _, c := range genReuseCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}

func TestArm64GeneralReuse(t *testing.T) {
	for _, c := range genReuseCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}
