package e2e

import (
	"testing"
)

const structUpdateSpreadSrc = `struct S { xs: i32[], n: i32, m: i32 }
function main(): i32 {
    let s: S = S { xs: [1, 2, 3], n: 0, m: 7 };
    let keep: S = s;
    s = S { ...s, n: 1 };
    s = S { ...s, n: s.n + 1 };
    if (keep.n != 0) { return 253; }
    return keep.m + keep.xs.len() + s.m + s.xs.len() + s.n;
}`

const structUpdateSpreadCliffSrc = `struct S { xs: i32[], n: i32 }
function push(own s: S, v: i32): S {
    s = S { ...s, xs: s.xs.append(v) };
    s = S { ...s, xs: s.xs.append(v + 1) };
    return s;
}
function main(): i32 {
    let s: S = S { xs: [], n: 0 };
    let i: i32 = 0;
    while (i < 10) { s = push(s, i); i = i + 1; }
    if (s.xs.len() != 20) { return 254; }
    return __arr_push_shared_count();
}`

func checkStructUpdateSpread(t *testing.T, backend string, gotValue, gotCliff int) {
	t.Helper()
	if gotValue == 253 {
		t.Fatalf("%s: the alias saw the update — reuse fired on a box with rc>1, which is a "+
			"write into another live value, not a missing field", backend)
	}
	if gotValue != 22 {
		t.Errorf("%s: struct-update spread = %d, want 22 — an alias reading 0 from a "+
			"field the literal never listed means the fresh-alloc branch dropped it",
			backend, gotValue)
	}
	if gotCliff == 254 {
		t.Fatalf("%s: the accumulator built the WRONG array; the cliff reading below it is meaningless", backend)
	}
	if gotCliff != 0 {
		t.Errorf("%s: record-update accumulator crossed the append cliff %d times, want 0 — "+
			"each crossing copies the whole buffer, so this is the quadratic shape", backend, gotCliff)
	}
}

func TestX86_64StructUpdateSpreadReuse(t *testing.T) {
	_, value := compileAndRunX86_64FreeOn(t, structUpdateSpreadSrc)
	_, cliff := compileAndRunX86_64FreeOn(t, structUpdateSpreadCliffSrc)
	checkStructUpdateSpread(t, "x86-64-linux", value, cliff)
}

func TestArm64StructUpdateSpreadReuse(t *testing.T) {
	_, value := compileAndRunArm64(t, structUpdateSpreadSrc)
	_, cliff := compileAndRunArm64(t, structUpdateSpreadCliffSrc)
	checkStructUpdateSpread(t, "arm64-linux", value, cliff)
}

func TestWASMStructUpdateSpreadReuse(t *testing.T) {
	checkStructUpdateSpread(t, "wasm32-wasi", runWasm(t, structUpdateSpreadSrc), runWasm(t, structUpdateSpreadCliffSrc))
}

func TestInterpStructUpdateSpreadReuse(t *testing.T) {
	checkStructUpdateSpread(t, "interp",
		runInterpExit(t, structUpdateSpreadSrc), runInterpExit(t, structUpdateSpreadCliffSrc))
}
