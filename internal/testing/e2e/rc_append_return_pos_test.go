package e2e

import (
	"testing"
)

func appendReturnPosBumpSrc(n string) string {
	return `function step(acc: i32[], v: i32): i32[] {
    if (v >= 0) { return acc.append(v); }
    return acc;
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let acc: i32[] = [];
    let i: i32 = 0;
    while (i < ` + n + `) { acc = step(acc, i); i = i + 1; }
    if (acc.len() != ` + n + `) { return 255; }
    return ((__heap_bump_bytes() as i32) - before) / 256;
}`
}

func TestX86_64AppendReturnPosBounded(t *testing.T) {
	_, n1 := compileAndRunX86_64FreeOn(t, appendReturnPosBumpSrc("60"))
	_, n2 := compileAndRunX86_64FreeOn(t, appendReturnPosBumpSrc("120"))
	assertSubQuadratic(t, "x86-64-linux", n1, n2)
}

func TestWASMAppendReturnPosBounded(t *testing.T) {
	n1 := runWasm(t, appendReturnPosBumpSrc("60"))
	n2 := runWasm(t, appendReturnPosBumpSrc("120"))
	assertSubQuadratic(t, "wasm32-wasi", n1, n2)
}
