package e2e

import (
	"testing"
)

const ownTransferFreshSrc = `function consume(own xs: i32[]): i32 { return xs[0] + xs[1]; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 300) {
        acc = acc + consume([i, i + 1]);   // [i,i+1] transferred to consume
        i = i + 1;
    }
    // consume = i + (i+1) = 2i+1; sum i=0..299 = 2*44850 + 300 = 90000
    if (acc != 90000) { return 999; }
    return __rc_underflow_count();
}`

const ownTransferChainSrc = `function consume(own xs: i32[]): i32 { return xs[0] + xs[1]; }
function relay(own ys: i32[]): i32 { return consume(ys); }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 300) {
        acc = acc + relay([i, i + 1]);
        i = i + 1;
    }
    if (acc != 90000) { return 999; }
    return __rc_underflow_count();
}`

var ownTransferCases = []struct{ name, src string }{
	{"fresh", ownTransferFreshSrc},
	{"chain", ownTransferChainSrc},
}

func TestX86_64OwnTransfer(t *testing.T) {
	for _, c := range ownTransferCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}

func TestArm64OwnTransfer(t *testing.T) {
	for _, c := range ownTransferCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}

func TestWASMOwnTransfer(t *testing.T) {
	for _, c := range ownTransferCases {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("%s: got %d, want 0", c.name, got)
			}
		})
	}
	bumpSrc := func(n string) string {
		return `function consume(own xs: i32[]): i32 { return xs[0]; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) {
        let unused: i32 = consume([i, i + 1, i + 2, i + 3]);
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
	}
	small := runWasm(t, bumpSrc("5000"))
	large := runWasm(t, bumpSrc("50000"))
	if small != large {
		t.Errorf("own-transfer should free the arg each turn (bounded): N=5000 -> %d, N=50000 -> %d", small, large)
	}
}
