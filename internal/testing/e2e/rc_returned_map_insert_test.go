package e2e

import (
	"strings"
	"testing"
)

const returnedBorrowedMapInsertSrc = `import "core/map";
function add(m: Map[string, i32]): Map[string, i32] {
    let s: string = "z";
    return m.insert(s + "-key-long-nine", 90);
}
function mk(): i32 {
    let stem: string = "a";
    let a: Map[string, i32] = map_new(4);
    a = a.insert(stem + "-key-long-seven", 70);
    let b: Map[string, i32] = add(a);
    return a.len() + b.len();
}
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 10) { t = t + mk(); k = k + 1; }
    return t - 20;
}`

const returnedOwnedMapInsertSrc = `import "core/map";
function mkm(): Map[string, i32] {
    let s: string = "z";
    let m2: Map[string, i32] = map_new(4);
    m2 = m2.insert(s + "-key-long-one", 1);
    return m2.insert(s + "-key-long-nine", 90);
}
function mk(): i32 {
    let b: Map[string, i32] = mkm();
    return b.len();
}
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 10) { t = t + mk(); k = k + 1; }
    return t - 10;
}`

const returnedMapInsertBranchSrc = `import "core/map";
function add(m: Map[string, i32]): Map[string, i32] {
    let s: string = "z";
    if (m.len() > 0) { return m.insert(s + "-key-long-nine", 90); }
    return m;
}
function mk(): i32 {
    let stem: string = "a";
    let a: Map[string, i32] = map_new(4);
    a = a.insert(stem + "-key-long-seven", 70);
    let b: Map[string, i32] = add(a);
    return a.len() + b.len();
}
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 10) { t = t + mk(); k = k + 1; }
    return t - 20;
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
