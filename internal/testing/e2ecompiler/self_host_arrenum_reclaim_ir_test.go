package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// arrEnumReclaimCases pin the #5474 `MyEnum[]` array-of-enums reclaim (#4353 item 4):
// releasing an enum array frees every element enum box and any rc payload it
// carries, not just the outer buffer — as `string[]`, `(…)[]` and
// `(<struct-with-array>)[]` already do. Every case must leave a balanced census.
//
// The negative cases extract an element — a match over `xs[0]`, a bound element, a
// returned array — so freeing that element while a binding aliases its payload is a
// double free. Each must compute the exact value with the underflow detector at zero.
var arrEnumReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// Core churn: rebuilt per iteration, length-only use, rc payload present.
	{"arrenum-churn", `enum E { A(string), B }
function main(): i32 {
    let acc: i32 = 0;
    let pre: string = "ab";
    let i: i32 = 0;
    while (i < 200) {
        let xs: E[] = [E.A(pre + "x"), E.B, E.A(pre + "yy")];
        acc = (acc + xs.len()) % 251;
        i = i + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) {
        let ys: E[] = [E.A(pre + "z"), E.B];
        acc = (acc + ys.len()) % 251;
        j = j + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// ALL-UNIT variants: no payload anywhere, so this isolates the element BOX walk
	// from any payload drop. Leaked 600 boxes per 200 iterations pre-fix.
	{"arrenum-unit-only", `enum E { A(string), B }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let xs: E[] = [E.B, E.B, E.B]; acc = (acc + xs.len()) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let ys: E[] = [E.B, E.B, E.B]; acc = (acc + ys.len()) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// UNQUALIFIED ctor spelling (`A(..)` rather than `E.A(..)`) must reclaim the same as
	// the qualified one.
	{"arrenum-unqualified-ctor", `enum E { A(string), B }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    let pre: string = "ab";
    while (i < 200) { let xs: E[] = [A(pre + "x"), B]; acc = (acc + xs.len()) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let ys: E[] = [A(pre + "z"), B]; acc = (acc + ys.len()) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// MATCH-EXTRACT negative: a match over `xs[0]` binding the payload. The bound
	// string must read back exactly (2 + 1 = 3 per round over 50
	// rounds = 150, %251 = 150, %97 = 53) with the detector at zero.
	{"arrenum-match-extract-safe", `enum E { A(string), B }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let pre: string = "ab";
        let xs: E[] = [E.A(pre + "x"), E.B, E.A(pre + "yy")];
        match (xs[0]) {
            E.A(s) => { acc = (acc + s.len()) % 251; },
            E.B => { acc = (acc + 1) % 251; },
        }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 97;
}`, 53},
	// ELEMENT-BIND negative: `let e = xs[1]` binds an element box out of the array.
	{"arrenum-elem-bind-safe", `enum E { A(string), B }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let pre: string = "ab";
        let xs: E[] = [E.A(pre + "x"), E.B];
        let e: E = xs[1];
        match (e) { E.A(s) => { acc = (acc + s.len()) % 251; }, E.B => { acc = (acc + 3) % 251; }, }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 97;
}`, 53},
	// ESCAPE-VIA-RETURN negative: the array leaves the frame, so mk must not free it.
	{"arrenum-escape-fn-safe", `enum E { A(string), B }
function mk(pre: string): E[] { let xs: E[] = [E.A(pre + "x"), E.B]; return xs; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { let r: E[] = mk("ab"); acc = (acc + r.len()) % 251; i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 97;
}`, 3},
}

// TestSelfHostArrEnumReclaimIRX86_64 drives the cases through the self-hosted x86-64
// compiler (asm_run), heap-bump, census and underflow guarded.
func TestSelfHostArrEnumReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	t.Setenv("FERN_LEAKCHECK", "1")
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range arrEnumReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			stderr, code := hevRun(t, runner, bin)
			if code != tc.want {
				t.Errorf("%s = %d, want %d (98 = enum-array elements leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
			allocs, frees, live := parseLeakcheck(t, tc.name, stderr)
			if live != 0 || allocs != frees {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", tc.name, allocs, frees, live)
			}
		})
	}
}
