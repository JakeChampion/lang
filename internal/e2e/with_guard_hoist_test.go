package e2e

import "testing"

// HoistUniquenessGuards moves the mutate-or-copy decision of an in-loop
// `b = b.with(i, v)` out of the loop. What must not change is what the
// program can see: a buffer another name still holds is copied before it
// is written, a loop that never writes never copies (the alias survives),
// and a loop that never runs leaves the value as it found it.
const withGuardHoistSrc = `
@noinline function fill_alias(n: i32): i32 {
    var a: u8[] = [1u8, 2u8, 3u8];
    var b: u8[] = a;
    var i: i32 = 0;
    while (i < n) { b = b.with(i, 7u8); i = i + 1; }
    if (a[0] as i32 != 1 || a[1] as i32 != 2 || a[2] as i32 != 3) { return 1; }
    if (n > 0 && (b[0] as i32 != 7 || b[n - 1] as i32 != 7)) { return 2; }
    if (n == 0 && b[1] as i32 != 2) { return 3; }
    return 0;
}
@noinline function never_writes(n: i32): i32 {
    var a: u8[] = [1u8, 2u8, 3u8];
    var c: u8[] = a;
    var i: i32 = 0;
    while (i < n) { if (i > 100) { c = c.with(i, 9u8); } i = i + 1; }
    if (c[0] as i32 != 1 || a[2] as i32 != 3) { return 4; }
    return 0;
}
@noinline function writes_once(n: i32): i32 {
    var a: u8[] = [1u8, 2u8, 3u8];
    var d: u8[] = a;
    var i: i32 = 0;
    while (i < n) { if (i == 1) { d = d.with(i, 9u8); } i = i + 1; }
    if (a[1] as i32 != 2 || d[1] as i32 != 9 || d[0] as i32 != 1 || d[2] as i32 != 3) { return 5; }
    return 0;
}
@noinline function fresh(n: i32): i32 {
    var e: u8[] = __alloc_u8(n);
    var i: i32 = 0;
    while (i < e.len()) { e = e.with(i, (e[i] as i32 + i + 1) as u8); i = i + 1; }
    if (e[n - 1] as i32 != n) { return 6; }
    return 0;
}
@noinline function nested(n: i32): i32 {
    var a: u8[] = [1u8, 2u8, 3u8, 4u8];
    var g: u8[] = a;
    var r: i32 = 0;
    while (r < n) {
        var i: i32 = 0;
        while (i < 4) { g = g.with(i, (g[i] as i32 + 1) as u8); i = i + 1; }
        r = r + 1;
    }
    if (a[0] as i32 != 1 || g[0] as i32 != 1 + n || g[3] as i32 != 4 + n) { return 7; }
    return 0;
}
function main(): i32 {
    var r: i32 = fill_alias(3);
    if (r != 0) { return r; }
    r = fill_alias(0);
    if (r != 0) { return 10 + r; }
    r = never_writes(3);
    if (r != 0) { return r; }
    r = writes_once(3);
    if (r != 0) { return r; }
    r = fresh(5);
    if (r != 0) { return r; }
    r = nested(3);
    if (r != 0) { return r; }
    return 0;
}
`

func TestX86_64WithGuardHoistKeepsValueSemantics(t *testing.T) {
	out, code := compileAndRunX86_64(t, withGuardHoistSrc)
	if code != 0 {
		t.Fatalf("exit = %d (a non-zero exit names the failing scenario)\n%s", code, out)
	}
}

func TestArm64WithGuardHoistKeepsValueSemantics(t *testing.T) {
	out, code := compileAndRunArm64(t, withGuardHoistSrc)
	if code != 0 {
		t.Fatalf("exit = %d (a non-zero exit names the failing scenario)\n%s", code, out)
	}
}

func TestWasmWithGuardHoistKeepsValueSemantics(t *testing.T) {
	if code := compileAndRunWasmbinMain(t, withGuardHoistSrc); code != 0 {
		t.Fatalf("exit = %d (a non-zero exit names the failing scenario)", code)
	}
}
