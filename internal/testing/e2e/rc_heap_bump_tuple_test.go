package e2e

import (
	"testing"
)

const tupleStrElemChurnSrc = `import "std/i32";
function wide(k: i32): string { return "a-value-well-past-the-inline-threshold-" + k.to_string(); }
function probe(k: i32): i32 {
    let t: (string, i32) = (wide(k), k);
    return t.0.len();
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + probe(i); i = i + 1; }
    return t;
}
function main(): i32 {
    let warm: i32 = churn(200);
    let before: i64 = __heap_bump_bytes();
    let again: i32 = churn(200);
    let per: i64 = (__heap_bump_bytes() - before) / 200;
    if (warm != again) { return 98; }
    if (warm <= 0) { return 97; }
    return (per as i32);
}`

func TestX86_64TupleStringElemReclaimed(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, tupleStrElemChurnSrc); code != 0 {
		t.Errorf("tuple string-element churn leaked %d bytes/round on x86-64, want 0", code)
	}
}

func TestArm64TupleStringElemReclaimed(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, tupleStrElemChurnSrc); code != 0 {
		t.Errorf("tuple string-element churn leaked %d bytes/round on arm64, want 0", code)
	}
}

func TestWASMTupleStringElemReclaimed(t *testing.T) {
	if got := runWasm(t, tupleStrElemChurnSrc); got != 0 {
		t.Errorf("tuple string-element churn leaked %d bytes/round on wasm, want 0", got)
	}
}
