package e2e

import (
	"testing"
)

const ownStringParamSrc = `import "std/i64";
function grow(own a: string, s: string): string { a = a + s; return a; }
function rounds(n: i32): i32 {
    let acc: string = "";
    let i: i32 = 0;
    while (i < n) { acc = grow(acc, "12345678"); i = i + 1; }
    return acc.len();
}
function main(): i32 {
    if (rounds(64) != 512) { return 97; }          // warm-up: first block of each size class
    let b0: i64 = __heap_bump_bytes();
    if (rounds(200) != 1600) { return 97; }
    let b1: i64 = __heap_bump_bytes();
    if (rounds(800) != 6400) { return 97; }        // 4x the appends
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (b1 - b0 > 8 * 1600 || b2 - b1 > 8 * 6400) { return 98; }    // see the margin above
    return 0;
}`

func TestX86_64OwnStringParamBalanced(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, ownStringParamSrc); code != 0 {
		t.Errorf("own string param: got %d, want 0 (97=length, 98=bytes outgrow the string, 99=over-release)", code)
	}
}

func TestArm64OwnStringParamBalanced(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, ownStringParamSrc); code != 0 {
		t.Errorf("own string param: got %d, want 0 (97=length, 98=bytes outgrow the string, 99=over-release)", code)
	}
}

func TestWASMOwnStringParamBalanced(t *testing.T) {
	if got := runWasm(t, ownStringParamSrc); got != 0 {
		t.Errorf("own string param: got %d, want 0 (97=length, 98=bytes outgrow the string, 99=over-release)", got)
	}
}
