package e2e

import (
	"testing"
)

const ownSelfAppendSrc = `
function build_i32(own p: i32[], n: i32): i32[] {
    let i: i32 = 0;
    while (i < n) { p = p.append(i * 2 + 1); i = i + 1; }
    return p;
}
function build_str(own p: string[], n: i32): string[] {
    let i: i32 = 0;
    while (i < n) { p = p.append("x"); i = i + 1; }
    return p;
}
function main(): i32 {
    let k: i32 = 0;
    while (k < 200) {
        let a: i32[] = build_i32([], 120);
        if (a.len() != 120) { return 10; }
        if (a[0] != 1) { return 11; }
        if (a[119] != 239) { return 12; }      // integrity across grows
        let s: string[] = build_str(["seed"], 60);
        if (s.len() != 61) { return 20; }
        k = k + 1;
    }
    return __rc_underflow_count();              // 0 == no over-release / double-free
}`

func TestX86_64OwnSelfAppend(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, ownSelfAppendSrc); code != 0 {
		t.Errorf("own-param self-append: got %d, want 0", code)
	}
}

func TestArm64OwnSelfAppend(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, ownSelfAppendSrc); code != 0 {
		t.Errorf("own-param self-append: got %d, want 0", code)
	}
}

func TestWASMOwnSelfAppend(t *testing.T) {
	if got := runWasm(t, ownSelfAppendSrc); got != 0 {
		t.Errorf("own-param self-append: got %d, want 0", got)
	}
}
