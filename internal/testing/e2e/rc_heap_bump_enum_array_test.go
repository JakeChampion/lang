package e2e

import (
	"testing"
)

func enumArrBumpSrc(n string) string {
	return `enum Box { Val(string), Empty }
@noinline function ids(s: string): string { return s; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < ` + n + `) {
        let xs: Box[] = [Val(ids("hello there friend, ")), Val(ids("general kenobi!!!")), Empty];
        acc = acc + xs.len();
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const enumArrCheckBox = `enum Box { Val(string), Empty }
@noinline function ids(s: string): string { return s; }
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        let xs: Box[] = [Val(ids("hello there friend, ")), Val(ids("general kenobi!!!")), Empty];
        let a: i32 = match (xs[0]) { Val(s) => s.len(), Empty => 0 };
        let b: i32 = match (xs[1]) { Val(s) => s.len(), Empty => 0 };
        acc = acc + a + b;
        i = i + 1;
    }
    if (acc != 7400) { return 99; }
    return __rc_underflow_count();
}`

const enumArrCheckOption = `@noinline function ids(s: string): string { return s; }
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        let xs: Option[string][] = [Some(ids("hello there friend")), None, Some(ids("kenobi"))];
        let a: i32 = match (xs[0]) { Some(s) => s.len(), None => 0 };
        let b: i32 = match (xs[2]) { Some(s) => s.len(), None => 0 };
        acc = acc + a + b;
        i = i + 1;
    }
    if (acc != 4800) { return 99; }
    return __rc_underflow_count();
}`

const enumArrCheckNested = `enum Box { Val(string), Empty }
@noinline function ids(s: string): string { return s; }
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        let g: Box[][] = [[Val(ids("alpha")), Empty], [Val(ids("beta")), Val(ids("gamma"))]];
        let a: i32 = match (g[0][0]) { Val(s) => s.len(), Empty => 0 };
        let b: i32 = match (g[1][0]) { Val(s) => s.len(), Empty => 0 };
        let c: i32 = match (g[1][1]) { Val(s) => s.len(), Empty => 0 };
        acc = acc + a + b + c;
        i = i + 1;
    }
    if (acc != 2800) { return 99; }
    return __rc_underflow_count();
}`

func checkEnumArr(t *testing.T, run func(*testing.T, string) (string, int)) {
	t.Helper()
	for _, c := range []struct{ name, src string }{
		{"box", enumArrCheckBox},
		{"option", enumArrCheckOption},
		{"nested", enumArrCheckNested},
	} {
		if _, code := run(t, c.src); code != 0 {
			t.Errorf("%s: code=%d (99=value mismatch, >0=over-release)", c.name, code)
		}
	}
}

func TestX86_64EnumArrayReclaim(t *testing.T) {
	checkEnumArr(t, compileAndRunX86_64FreeOn)
}

func TestArm64EnumArrayReclaim(t *testing.T) {
	checkEnumArr(t, compileAndRunArm64FreeOn)
}

func TestWASMEnumArrayReclaim(t *testing.T) {
	small := runWasm(t, enumArrBumpSrc("50"))
	large := runWasm(t, enumArrBumpSrc("5000"))
	if small != large {
		t.Errorf("enum-array bump should be bounded (payload reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, enumArrCheckBox); got != 0 {
		t.Errorf("box: %d (99=value mismatch, >0=over-release)", got)
	}
	if got := runWasm(t, enumArrCheckOption); got != 0 {
		t.Errorf("option: %d", got)
	}
	if got := runWasm(t, enumArrCheckNested); got != 0 {
		t.Errorf("nested: %d", got)
	}
}
