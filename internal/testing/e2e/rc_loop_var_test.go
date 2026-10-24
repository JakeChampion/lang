package e2e

import (
	"testing"
)

const loopVarArrayReuseSrc = `function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 300) {
        let row: i32[] = [i, i * 2, i * 3];
        total = total + (row[0] + row[1] + row[2]) - (i * 6);
        i = i + 1;
    }
    return total + __rc_underflow_count();
}`

const loopVarStructReuseSrc = `struct Pt { x: i32, y: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 300) {
        let p: Pt = Pt { x: i, y: i + 1 };
        acc = acc + (p.y - p.x) - 1;
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`

const loopVarEnumReuseSrc = `enum Box { Val(i32[]), Empty }
function head(b: Box): i32 {
    match (b) {
        Val(xs) => { return xs[0]; },
        Empty => { return 0; }
    }
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 300) {
        let b: Box = Val([i, i + 7]);
        acc = acc + head(b) - i;
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`

const loopVarStringReuseSrc = `function suffix(n: i32): string {
    if (n % 2 == 0) { return "even"; }
    return "odd";
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 300) {
        let s: string = "v-" + suffix(i);
        acc = acc + s.len() - 2 - suffix(i).len();
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`

var loopVarReuseCases = []struct {
	name string
	src  string
}{
	{"array", loopVarArrayReuseSrc},
	{"struct", loopVarStructReuseSrc},
	{"enum", loopVarEnumReuseSrc},
	{"string", loopVarStringReuseSrc},
}

func TestX86_64LoopVarReclaim(t *testing.T) {
	for _, c := range loopVarReuseCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s loop-body let free+reuse: got %d, want 0 (drift / over-release on the per-iteration dec)", c.name, code)
			}
		})
	}
}

func TestArm64LoopVarReclaim(t *testing.T) {
	for _, c := range loopVarReuseCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s loop-body let free+reuse: got %d, want 0 (drift / over-release)", c.name, code)
			}
		})
	}
}

func TestWASMLoopVarReclaim(t *testing.T) {
	for _, c := range loopVarReuseCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("%s loop-body let free+reuse: got %d, want 0 (drift / over-release)", c.name, got)
			}
		})
	}
}
