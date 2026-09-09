package e2e

import "testing"

// A recursive generic visitor must preserve the original lexical scope while
// accumulating bindings in its children. Appending to empty supplies spare
// capacity: a full literal would force allocation and mask unsafe buffer reuse.
const callbackSnapshotProg = `
struct Binding { id: i32, name: string, symbol: string, line: i32, col: i32 }
struct Scope { bindings: Binding[], visible: i32[], reserved: string[] }
function bind(s: Scope): Scope {
    var id = s.bindings.len();
    var b = Binding { id: id, name: "n", symbol: "test", line: 0, col: 0 };
    return Scope { ...s, bindings: s.bindings.append(b), visible: s.visible.append(id) };
}
function restore(inner: Scope, outer: Scope): Scope {
    return Scope { ...inner, visible: outer.visible };
}
function map[T](n: i32, s: T, cb: (i32, T) => (i32, T, boolean)): (i32, T, boolean) {
    var (node, tail, handled) = cb(n, s);
    if (handled) { return (node, tail, true); }
    return (n, tail, false);
}
function body(ns: i32[], s: Scope): (i32[], Scope) {
    var out: i32[] = [];
    var cur = s;
    for n in ns {
        var (node, tail, handled) = map(n, cur, visit);
        out = out.append(node);
        cur = tail;
    }
    return (out, cur);
}
function visit(n: i32, s: Scope): (i32, Scope, boolean) {
    if (n == 0) {
        var (cond, cs, ch) = map(2, s, visit);
        var (left, ls) = body([1], cs);
        var (right, rs) = body([1], restore(ls, cs));
        return (n, restore(rs, s), true);
    }
    if (n == 2) { return (n, s, true); }
    var before = s;
    var (node, tail, handled) = map(2, before, visit);
    tail = bind(tail);
    return (n, tail, true);
}
function main(): i32 {
    var s = bind(Scope { bindings: [], visible: [], reserved: [] });
    var (node, result, handled) = map(0, s, visit);
    if (result.visible.len() != 1) { return 1; }
    if (result.visible[0] != 0) { return 2; }
    if (result.bindings.len() != 3) { return 3; }
    if (result.bindings[2].id != 2) { return 4; }
    return 42;
}`

func TestCallbackSnapshotInterp(t *testing.T) {
	if got := runInterpExit(t, callbackSnapshotProg); got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
}

func TestCallbackSnapshotX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, callbackSnapshotProg); got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
}

func TestCallbackSnapshotArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, callbackSnapshotProg); got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
}

func TestCallbackSnapshotWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, callbackSnapshotProg); got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
}
