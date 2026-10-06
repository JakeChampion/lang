package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// arrEnumReclaimCases pin the #5474 `MyEnum[]` array-of-enums reclaim (#4353 item 4) —
// the last array-of-X element kind with no element walk at any site. `string[]` (#5471),
// `(…)[]` (ARRTUP) and `(<struct-with-array>)[]` (ARRSTRUCT) all reclaim their elements;
// an enum array was left on a buffer-only dec, so the outer buffer was freed and every
// element enum box — plus any rc payload it carried — leaked per iteration.
//
// Measured on the pre-fix compiler with FERN_LEAKCHECK=1, 200 iterations of the churn
// below (allocs/frees/live_bytes), against controls that must be bounded:
//
//	i32[]           201 / 200 / 24      bounded (control)
//	string[]       1401 / 1400 / 24     bounded (control)
//	(i32,string)[] 1801 / 1800 / 24     bounded (control)
//	E[]            2001 /  600 / 35224  LEAK -> 2001 / 2000 / 24 after
//	E[] all-unit    801 /  200 / 19224  LEAK ->  801 /  800 / 24 after
//
// The all-unit row is the one that localises the gap: 200 iterations x (3 element boxes
// + 1 buffer) allocated, exactly 200 freed — so the outer buffer was already being
// reclaimed and it was purely the element walk that was missing.
//
// The new "ARRENUM:" class credits a fresh array of fresh enum constructions and
// releases it with the same counted element walk its siblings use, but drops each
// element through the RUNTIME VARIANT DISPATCH (emit_enum_variant_drops) rather than a
// type-directed drop. That primitive frees the element box ITSELF and zeroes the slot,
// so — unlike ARRTUP/ARRSTRUCT — the walk emits NO trailing __fern_rc_dec per element;
// adding one double-frees every element.
//
// The element enum name is in the CREDIT ("ARRENUM:<local>#<Enum>") because an `E[]`
// slot records its element type in neither arrarr_elem (populated only for `T[][]`, and
// only for four scalar tags) nor struct_type.
//
// The negative cases extract an element — a match over `xs[0]`, a bound element, a
// returned array — so freeing that element while a binding aliases its payload is a
// double free. Each must compute the exact value with the underflow detector at zero.
//
// Every case must also leave a balanced census.
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
	// UNQUALIFIED ctor spelling (`A(..)` rather than `E.A(..)`) reaches the same credit —
	// fresh_rcpayload_enum_init admits both, and a class that only saw one would silently
	// leak half the real call sites.
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
	src, err := os.ReadFile("../../compiler/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

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
