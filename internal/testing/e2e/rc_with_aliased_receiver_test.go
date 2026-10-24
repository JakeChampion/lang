package e2e

import (
	"testing"
)

const withAliasedLocalSrc = `function mk(n: i32): i32[] {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < n) { a = a.append(i * 10); i = i + 1; }
    return a;
}
function round(): i32 {
    let a: i32[] = mk(6);
    let b: i32[] = a;
    a = a.with(0, 99);
    let junk: i32[] = mk(6);
    if (junk.len() != 6) { return 1; }
    if (b[0] != 0) { return 2; }
    if (a[0] != 99) { return 3; }
    if (b[3] != 30 || a[3] != 30) { return 4; }
    return 0;
}
function main(): i32 {
    let i: i32 = 0;
    while (i < 200) {
        let r: i32 = round();
        if (r != 0) { return r; }
        i = i + 1;
    }
    return __rc_underflow_count();
}`

const withBorrowedParamSrc = `function mk(n: i32): i32[] {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < n) { a = a.append(i * 10); i = i + 1; }
    return a;
}
function bump(xs: i32[]): i32[] {
    xs = xs.with(0, 99);
    return xs;
}
function round(): i32 {
    let a: i32[] = mk(6);
    let b: i32[] = bump(a);
    let junk: i32[] = mk(6);
    if (junk.len() != 6) { return 1; }
    if (a[0] != 0) { return 2; }
    if (b[0] != 99) { return 3; }
    return 0;
}
function main(): i32 {
    let i: i32 = 0;
    while (i < 200) {
        let r: i32 = round();
        if (r != 0) { return r; }
        i = i + 1;
    }
    return __rc_underflow_count();
}`

const withAliasedStringElemSrc = `function mk(): string[] {
    let a: string[] = [];
    a = a.append("v0");
    a = a.append("v1");
    a = a.append("v2");
    a = a.append("v3");
    a = a.append("v4");
    return a;
}
function round(): i32 {
    let a: string[] = mk();
    let b: string[] = a;
    a = a.with(0, "changed");
    let junk: string[] = mk();
    if (junk.len() != 5) { return 1; }
    if (b[0] != "v0") { return 2; }
    if (a[0] != "changed") { return 3; }
    if (b[4] != "v4" || a[4] != "v4") { return 4; }
    return 0;
}
function main(): i32 {
    let i: i32 = 0;
    while (i < 200) {
        let r: i32 = round();
        if (r != 0) { return r; }
        i = i + 1;
    }
    return __rc_underflow_count();
}`

const withAliasedFieldSrc = `struct P { xs: i32[], n: i32 }
function mk(n: i32): i32[] {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < n) { a = a.append(i * 10); i = i + 1; }
    return a;
}
function round(): i32 {
    let p: P = P { xs: mk(6), n: 1 };
    let q: P = p;
    p = P { ...p, xs: p.xs.with(0, 99) };
    let junk: i32[] = mk(6);
    if (junk.len() != 6) { return 1; }
    if (q.xs[0] != 0) { return 2; }
    if (p.xs[0] != 99) { return 3; }
    if (q.n != 1 || p.n != 1) { return 4; }
    return 0;
}
function main(): i32 {
    let i: i32 = 0;
    while (i < 200) {
        let r: i32 = round();
        if (r != 0) { return r; }
        i = i + 1;
    }
    return __rc_underflow_count();
}`

func withAliasedCases() []struct {
	name string
	src  string
} {
	return []struct {
		name string
		src  string
	}{
		{"local alias", withAliasedLocalSrc},
		{"borrowed param", withBorrowedParamSrc},
		{"string elements", withAliasedStringElemSrc},
		{"struct field", withAliasedFieldSrc},
	}
}

func TestX86_64WithAliasedReceiverCopies(t *testing.T) {
	for _, c := range withAliasedCases() {
		out, code := compileAndRunX86_64FreeOn(t, c.src)
		if code != 0 {
			t.Errorf("x86-64 %s: code=%d — 2/3/4 mean the write landed in a buffer a "+
				"second reference still reads, >4 an over-release\n%s", c.name, code, out)
		}
	}
}

func TestArm64WithAliasedReceiverCopies(t *testing.T) {
	for _, c := range withAliasedCases() {
		out, code := compileAndRunArm64FreeOn(t, c.src)
		if code != 0 {
			t.Errorf("arm64 %s: code=%d — 2/3/4 mean the write landed in a buffer a "+
				"second reference still reads, >4 an over-release\n%s", c.name, code, out)
		}
	}
}

func TestWASMWithAliasedReceiverCopies(t *testing.T) {
	for _, c := range withAliasedCases() {
		if got := runWasm(t, c.src); got != 0 {
			t.Errorf("wasm %s: code=%d — 2/3/4 mean the write landed in a buffer a "+
				"second reference still reads, >4 an over-release", c.name, got)
		}
	}
}
