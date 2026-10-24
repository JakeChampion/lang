package e2e

import (
	"testing"
)

const pdValuesSrc = `function main(): i32 {
    let a: i32[] = [10, 20, 30]; let sa: i32 = a[0] + a[2];
    let b: i32[] = [1, 2, 3]; let sb: i32 = b[1];
    let c: i32[] = [100, 200]; let sc: i32 = c[0] + c[1];
    if (sa != 40) { return 901; }
    if (sb != 2) { return 902; }
    if (sc != 300) { return 903; }
    return __rc_underflow_count();
}`

const pdAliasSrc = `struct Holder { items: i32[], n: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let a: i32[] = [i, i + 1, i + 2];
        let h: Holder = Holder{ items: a, n: 0 };
        let sa: i32 = a[0];
        let junk: i32[] = [7, 7, 7];
        acc = acc + sa + h.items[2] + junk[0];
        i = i + 1;
    }
    // sa=i, h.items[2]=i+2, junk=7 -> 2i+9; i=0..199 -> 39800 + 1800 = 41600
    if (acc != 41600) { return 999; }
    return __rc_underflow_count();
}`

const pdArgReturnSrc = `function biggy(xs: i32[]): i32[] {
    let w: i32 = 0;
    let j: i32 = 0;
    while (j < 3) { w = w + xs[j]; j = j + 1; }
    if (w < -999999) { return xs; }
    return xs;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let a: i32[] = [i, i + 1, i + 2];
        let b: i32[] = biggy(a);
        let junk: i32[] = [7, 7, 7];
        acc = acc + b[0] + b[2] + junk[0];
        i = i + 1;
    }
    // b[0]=i, b[2]=i+2, junk=7 -> 2i+9; i=0..199 -> 41600
    if (acc != 41600) { return 999; }
    return __rc_underflow_count();
}`

const rcArrValuesSrc = `struct P { x: i32, y: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let ps: P[] = [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }];
        let keep: P = ps[0];
        let junk: i32[] = [7, 7, 7];
        acc = acc + ps[1].x + keep.y + junk[0];
        i = i + 1;
    }
    // ps[1].x=i+2, keep.y=i+1, junk=7 -> 2i+10; i=0..199 -> 39800+2000 = 41800
    if (acc != 41800) { return 999; }
    return __rc_underflow_count();
}`

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
