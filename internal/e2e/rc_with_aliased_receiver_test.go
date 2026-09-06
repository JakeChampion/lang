package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #8530 lowered `a.with(i, v)`'s mutate-or-copy decision into the IR, so the
// uniquely-owned accumulator writes in place with no call. An in-place store
// where the buffer is NOT uniquely owned is a silent wrong answer, which is
// far worse than the slowness the change is about — so the aliased case is
// pinned here, on every backend, in each of the shapes that reach a different
// arm of the lowering.
//
// The `mk` calls between the write and the read exist for the same reason
// they do in the view-local tests: they recycle any block the write freed
// through the freelist, so a wrong answer lands on reused memory rather than
// on stale-but-intact bytes.

// A second live LOCAL name. The receiver's textually-last use is the `.with`,
// which is exactly where a static last-use test says the reference may be
// taken over — the runtime count is what says otherwise.
const withAliasedLocalSrc = `function mk(n: i32): i32[] {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < n) { a = a.append(i * 10); i = i + 1; }
    return a;
}
function round(): i32 {
    var a: i32[] = mk(6);
    var b: i32[] = a;
    a = a.with(0, 99);
    var junk: i32[] = mk(6);
    if (junk.len() != 6) { return 1; }
    if (b[0] != 0) { return 2; }
    if (a[0] != 99) { return 3; }
    if (b[3] != 30 || a[3] != 30) { return 4; }
    return 0;
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 200) {
        var r: i32 = round();
        if (r != 0) { return r; }
        i = i + 1;
    }
    return __rc_underflow_count();
}`

// A BORROWED parameter: the caller still owns the buffer, so the callee's
// write must copy even though the param's own last use is the `.with` and it
// is reassigned to itself.
const withBorrowedParamSrc = `function mk(n: i32): i32[] {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < n) { a = a.append(i * 10); i = i + 1; }
    return a;
}
function bump(xs: i32[]): i32[] {
    xs = xs.with(0, 99);
    return xs;
}
function round(): i32 {
    var a: i32[] = mk(6);
    var b: i32[] = bump(a);
    var junk: i32[] = mk(6);
    if (junk.len() != 6) { return 1; }
    if (a[0] != 0) { return 2; }
    if (b[0] != 99) { return 3; }
    return 0;
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 200) {
        var r: i32 = round();
        if (r != 0) { return r; }
        i = i + 1;
    }
    return __rc_underflow_count();
}`

// A string-element array takes the element-retaining CoW helper rather than
// the scalar one, so it is a separate arm of the same decision.
const withAliasedStringElemSrc = `function mk(): string[] {
    var a: string[] = [];
    a = a.append("v0");
    a = a.append("v1");
    a = a.append("v2");
    a = a.append("v3");
    a = a.append("v4");
    return a;
}
function round(): i32 {
    var a: string[] = mk();
    var b: string[] = a;
    a = a.with(0, "changed");
    var junk: string[] = mk();
    if (junk.len() != 5) { return 1; }
    if (b[0] != "v0") { return 2; }
    if (a[0] != "changed") { return 3; }
    if (b[4] != "v4" || a[4] != "v4") { return 4; }
    return 0;
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 200) {
        var r: i32 = round();
        if (r != 0) { return r; }
        i = i + 1;
    }
    return __rc_underflow_count();
}`

// A struct field the update replaces: `p = P { ...p, xs: p.xs.with(…) }`
// through the constructor-reuse path, with a second name on the struct so
// both the box reuse and the buffer write have to decline.
const withAliasedFieldSrc = `struct P { xs: i32[], n: i32 }
function mk(n: i32): i32[] {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < n) { a = a.append(i * 10); i = i + 1; }
    return a;
}
function round(): i32 {
    var p: P = P { xs: mk(6), n: 1 };
    var q: P = p;
    p = P { ...p, xs: p.xs.with(0, 99) };
    var junk: i32[] = mk(6);
    if (junk.len() != 6) { return 1; }
    if (q.xs[0] != 0) { return 2; }
    if (p.xs[0] != 99) { return 3; }
    if (q.n != 1 || p.n != 1) { return 4; }
    return 0;
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 200) {
        var r: i32 = round();
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
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	for _, c := range withAliasedCases() {
		if got := runWasm(t, c.src); got != 0 {
			t.Errorf("wasm %s: code=%d — 2/3/4 mean the write landed in a buffer a "+
				"second reference still reads, >4 an over-release", c.name, got)
		}
	}
}
