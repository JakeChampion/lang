package e2e

import (
	"strings"
	"testing"
)

// A `return m.insert(..)` whose cow ran in place hands the caller m's own
// handle. The caller binds a call result as counted, so with no retain at
// the return two bindings drop one buffer: the callee's borrowed param and
// the caller's `a` (#8276, SIGSEGV on arm64 and a trap on wasm through the
// string-key column walk), or the callee's owned local and the caller's
// binding. The return now retains the handle when it is unchanged.

// The issue's program: a callee returning its borrowed Map param's insert.
const returnedBorrowedMapInsertSrc = `import "core/map";
function add(m: Map[string, i32]): Map[string, i32] {
    var s: string = "z";
    return m.insert(s + "-key-long-nine", 90);
}
function mk(): i32 {
    var stem: string = "a";
    var a: Map[string, i32] = map_new(4);
    a = a.insert(stem + "-key-long-seven", 70);
    var b: Map[string, i32] = add(a);
    return a.len() + b.len();
}
function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 10) { t = t + mk(); k = k + 1; }
    return t - 30;
}`

// The same return over a local the callee owns: the exit sweep releases the
// local, so the retain is what the caller's binding lives on.
const returnedOwnedMapInsertSrc = `import "core/map";
function mkm(): Map[string, i32] {
    var s: string = "z";
    var m2: Map[string, i32] = map_new(4);
    m2 = m2.insert(s + "-key-long-one", 1);
    return m2.insert(s + "-key-long-nine", 90);
}
function mk(): i32 {
    var b: Map[string, i32] = mkm();
    return b.len();
}
function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 10) { t = t + mk(); k = k + 1; }
    return t - 10;
}`

// One return retains through the insert, the other is the bare param
// (move-on-return); both hand back a counted handle.
const returnedMapInsertBranchSrc = `import "core/map";
function add(m: Map[string, i32]): Map[string, i32] {
    var s: string = "z";
    if (m.len() > 0) { return m.insert(s + "-key-long-nine", 90); }
    return m;
}
function mk(): i32 {
    var stem: string = "a";
    var a: Map[string, i32] = map_new(4);
    a = a.insert(stem + "-key-long-seven", 70);
    var b: Map[string, i32] = add(a);
    return a.len() + b.len();
}
function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 10) { t = t + mk(); k = k + 1; }
    return t - 30;
}`

var returnedMapInsertCases = []struct {
	name string
	src  string
}{
	{"borrowed_param", returnedBorrowedMapInsertSrc},
	{"owned_local", returnedOwnedMapInsertSrc},
	{"branch_with_bare_return", returnedMapInsertBranchSrc},
}

const returnedMapInsertWant = 10

func TestInterpReturnedMapInsertHandsBackACount(t *testing.T) {
	for _, tc := range returnedMapInsertCases {
		if got := runInterpByte(t, tc.src); got != returnedMapInsertWant {
			t.Errorf("%s: interp exit = %d, want %d", tc.name, got, returnedMapInsertWant)
		}
	}
}

func checkReturnedMapInsertCensus(t *testing.T, name, stderr string, exit int) {
	t.Helper()
	if exit != returnedMapInsertWant {
		t.Fatalf("%s: exit %d, want %d — a returned in-place map insert must hand the caller a counted handle; stderr: %s", name, exit, returnedMapInsertWant, stderr)
	}
	a, f, live := parseLeakCheckLine(t, stderr)
	if a != f || live != 0 {
		t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", name, a, f, live)
	}
}

func TestX86_64ReturnedMapInsertHandsBackACount(t *testing.T) {
	for _, tc := range returnedMapInsertCases {
		_, stderr, exit := runLeakCheckX86_64(t, tc.src)
		checkReturnedMapInsertCensus(t, tc.name, stderr, exit)
	}
}

func TestArm64ReturnedMapInsertHandsBackACount(t *testing.T) {
	for _, tc := range returnedMapInsertCases {
		_, stderr, exit := runLeakCheckArm64(t, tc.src)
		checkReturnedMapInsertCensus(t, tc.name, stderr, exit)
	}
}

// The wasm census component prints main's result instead of exiting with it.
func TestWASMReturnedMapInsertHandsBackACount(t *testing.T) {
	for _, tc := range returnedMapInsertCases {
		stdout, stderr, exit := runLeakCheckWasm(t, tc.src, false)
		if exit != 0 || !strings.Contains(stdout, "10") {
			t.Fatalf("%s: exit %d stdout %q, want exit 0 printing 10 — a returned in-place map insert must hand the caller a counted handle; stderr: %s", tc.name, exit, stdout, stderr)
		}
		a, f, live := parseWasmLeakCheckLine(t, stderr)
		if a != f || live != 0 {
			t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", tc.name, a, f, live)
		}
	}
}
