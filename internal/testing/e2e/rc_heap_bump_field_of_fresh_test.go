package e2e

import (
	"testing"
)

func fieldOfFreshStructBump(n string) string {
	return `struct P { x: i32, data: i32[] }
function mk(v: i32): P { return P { x: v, data: [v, v + 1, v + 2] }; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { let v: i32 = mk(i).x; acc = acc + v; i = i + 1; }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func fieldOfFreshTupleBump(n string) string {
	return `function mk(v: i32): (i32, i32[]) { return (v, [v, v + 1]); }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { let v: i32 = mk(i).0; acc = acc + v; i = i + 1; }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const fieldOfFreshAliasedSafe = `struct P { x: i32, y: i32 }
@noinline function pass(p: P): P { return p; }
function main(): i32 {
    let s: P = P { x: 10, y: 20 };
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) { let v: i32 = pass(s).x; acc = acc + v + s.y; i = i + 1; }
    if (acc != 6000) { return 99; }
    return __rc_underflow_count();
}`

const fieldOfFreshPtrField = `struct P { x: i32, data: i32[] }
function mk(v: i32): P { return P { x: v, data: [v, v + 1, v + 2] }; }
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) { let d: i32[] = mk(i).data; acc = acc + d[0]; i = i + 1; }
    if (acc != 19900) { return 99; }
    return __rc_underflow_count();
}`

func checkFieldOfFresh(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	if _, code := run(t, fieldOfFreshAliasedSafe); code != 0 {
		t.Errorf("aliased-container safety: code=%d (99=value, >0=over-release/UAF)", code)
	}
	if _, code := run(t, fieldOfFreshPtrField); code != 0 {
		t.Errorf("pointer-field value/over-release: code=%d", code)
	}
}

func TestX86_64FieldOfFreshReclaim(t *testing.T) {
	checkFieldOfFresh(t, compileAndRunX86_64FreeOn)
}

func TestArm64FieldOfFreshReclaim(t *testing.T) {
	checkFieldOfFresh(t, compileAndRunArm64FreeOn)
}

func TestWASMFieldOfFreshReclaim(t *testing.T) {
	for _, c := range []struct {
		name string
		src  func(string) string
	}{
		{"struct", fieldOfFreshStructBump},
		{"tuple", fieldOfFreshTupleBump},
	} {
		small := runWasm(t, c.src("5000"))
		large := runWasm(t, c.src("50000"))
		if small != large {
			t.Errorf("%s field-of-fresh bump should be bounded: N=5000 -> %d, N=50000 -> %d", c.name, small, large)
		}
		if small == 0 {
			t.Errorf("%s: expected a non-zero bounded high-water, got 0", c.name)
		}
	}
	checkFieldOfFresh(t, func(t *testing.T, src string) (string, int) {
		return "", runWasm(t, src)
	})
}
