package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// crossTypeReuseFiresProg pairs — a DEAD all-scalar donor of a different type
// than the recipient (reuse SHOULD fire → 1 struct-box alloc) vs the same shape
// with the donor read AFTER the recipient (reuse must NOT fire → 2 allocs). The
// asm-level alloc-count delta is the direct proof the cross-type reuse actually
// lowers in place (a runtime value check alone can't distinguish reuse from a
// fresh alloc). The self-host x86 struct box is allocated with `call
// __fern_arr_box`, one per live construction.
// `* k` (k == 1, so every value is unchanged) keeps these literals off the
// STATIC-CONSTANT path (#6149): an aggregate of constants is placed in data,
// which allocates nothing and makes the reuse it would have fed moot —
// correct, and strictly better, but it would leave this test measuring zero
// against zero. A donor that genuinely allocates is what the reuse contract
// below is about. (The constant/reuse interaction itself is pinned by
// TestSelfHostConstAggregateIRX86_64's `reuse-shape-all-constant`.)
const crossTypeReuseDeadDonor = `struct Point { x: i32, y: i32 } struct Pair { a: i32, b: i32 } function main(): i32 { let k = 1; let p = Point { x: 3 * k, y: 4 }; let s = p.x + p.y; let q = Pair { a: s, b: 9 }; return q.a + q.b; }`
const crossTypeReuseLiveDonor = `struct Point { x: i32, y: i32 } struct Pair { a: i32, b: i32 } function main(): i32 { let k = 1; let p = Point { x: 3 * k, y: 4 }; let q = Pair { a: 5 * k, b: 9 }; return q.a + q.b + p.x + p.y; }`

// Array-field cross-type pair: a dead Holder{id,items} reused for Bag{tag,data}
// (identical [i32, i32[]] layout). The array literals are static constants, so
// only the struct boxes reach __fern_arr_box: one when the dead donor's box is
// reused, two when the donor is read after the recipient. `* k` keeps each
// struct off the static path as above. The delta proves the array-field
// cross-type reuse lowers in place.
const crossTypeReuseArrDeadDonor = `struct Holder { id: i32, items: i32[] } struct Bag { tag: i32, data: i32[] } function main(): i32 { let k = 1; let h = Holder { id: 1 * k, items: [1, 2] }; let s = h.id + h.items[0]; let b = Bag { tag: s, data: [3, 4] }; return b.tag + b.data[0]; }`
const crossTypeReuseArrLiveDonor = `struct Holder { id: i32, items: i32[] } struct Bag { tag: i32, data: i32[] } function main(): i32 { let k = 1; let h = Holder { id: 1 * k, items: [1, 2] }; let b = Bag { tag: 5 * k, data: [3, 4] }; return b.tag + b.data[0] + h.id + h.items[1]; }`

// crossTypeReuseIRCases exercise cross-TYPE FBIP reuse: a dead struct donor
// whose box is reused in place by a LATER construction of a DIFFERENT struct
// type with the same box class (identical per-position field widths + kinds —
// scalar↔scalar or leak-safe-array↔leak-safe-array), so the donor and
// recipient need not be the SAME type. Each case embeds a value check (returns
// 90/91 on mismatch) and then returns __rc_underflow_count() — so want=0 means
// both the reused value is correct AND no over-release occurred. A mis-sized
// reuse (wrong box class) would corrupt the freelist and surface as a wrong
// value or non-zero underflow, especially the "-probe" cases that allocate
// again after the reuse.
var crossTypeReuseIRCases = []struct {
	name string
	main string
	want int
}{
	// Dead Point{i32,i32} reused for Pair{i32,i32} — native's canonical
	// same-box-class cross-type case. 3+4 read, then 7+9 = 16.
	{"point-to-pair", `struct Point { x: i32, y: i32 } struct Pair { a: i32, b: i32 } function main(): i32 { let p = Point { x: 3, y: 4 }; let s = p.x + p.y; let q = Pair { a: s, b: 9 }; if (q.a + q.b != 16) { return 90; } return __rc_underflow_count(); }`, 0},
	// Reuse then a FRESH array alloc: if the reuse mis-sized Point's box, the
	// recycled block would poison the freelist and the array would read back
	// wrong. 10+20 + 100+200+300 = 630.
	{"cross-reuse-then-alloc-probe", `struct Point { x: i32, y: i32 } struct Pair { a: i32, b: i32 } function main(): i32 { let p = Point { x: 1, y: 2 }; let u = p.x + p.y; let q = Pair { a: 10, b: 20 }; let fresh = [100, 200, 300]; if (q.a + q.b + fresh[0] + fresh[1] + fresh[2] != 630) { return 91; } if (u != 3) { return 92; } return __rc_underflow_count(); }`, 0},
	// Mixed widths (i64 + i32) — donor and recipient share the width sequence
	// [8,4], so the box class matches. av=40, q.b=2 -> 42.
	{"mixed-width-cross", `struct A { p: i64, q: i32 } struct B { r: i64, s: i32 } function main(): i32 { let a = A { p: 5, q: 1 }; let d = (a.p as i32) + a.q; let b = B { r: 40, s: 2 }; let av: i64 = b.r; if ((av as i32) + b.s != 42) { return 90; } if (d != 6) { return 92; } return __rc_underflow_count(); }`, 0},
	// Different field COUNT must NOT cross-reuse (guard rejects), but the program
	// stays correct: Trip allocates fresh. 3+4 read, 10+20+30 = 60.
	{"different-count-no-reuse", `struct Point { x: i32, y: i32 } struct Trip { a: i32, b: i32, c: i32 } function main(): i32 { let p = Point { x: 3, y: 4 }; let u = p.x + p.y; let t = Trip { a: 10, b: 20, c: 30 }; if (t.a + t.b + t.c != 60) { return 90; } if (u != 7) { return 92; } return __rc_underflow_count(); }`, 0},
	// ARRAY-FIELD cross-type: dead Holder{id:i32, items:i32[]} reused for
	// Bag{tag:i32, data:i32[]} (identical [i32, i32[]] layout). The reuse rc-dec's
	// the donor's OLD items array before writing the recipient's fresh data array;
	// the rc-underflow detector confirms exactly-once release. 2+30+40+11 = 83.
	{"array-field-cross", `struct Holder { id: i32, items: i32[] } struct Bag { tag: i32, data: i32[] } function main(): i32 { let h = Holder { id: 1, items: [10, 20] }; let u = h.id + h.items[0]; let b = Bag { tag: 2, data: [30, 40] }; if (b.tag + b.data[0] + b.data[1] + u != 83) { return 90; } return __rc_underflow_count(); }`, 0},
	// ARRAY-FIELD reuse then a FRESH array: the donor's freed items buffer must not
	// dangle into the recipient's data or the later fresh array. data=[10,20],
	// fresh=[7,8,9]: 10+20 + 7+8+9 + u(11) + tag(2) = 67.
	{"array-field-cross-then-alloc-probe", `struct Holder { id: i32, items: i32[] } struct Bag { tag: i32, data: i32[] } function main(): i32 { let h = Holder { id: 1, items: [10, 20] }; let u = h.id + h.items[0]; let b = Bag { tag: 2, data: [10, 20] }; let fresh = [7, 8, 9]; if (b.data[0] + b.data[1] + fresh[0] + fresh[1] + fresh[2] + u + b.tag != 67) { return 91; } return __rc_underflow_count(); }`, 0},
}

func crossTypeReuseIRSrc(mainBody string) string {
	return mainBody + "\n"
}

// TestSelfHostCrossTypeReuseIR runs each case through the self-host CLI on
// x86-64 and wasm. The cross-type reuse emit is plain scalar
// struct_set/struct_get, so it lowers on wasm as on x86-64.
func TestSelfHostCrossTypeReuseIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range crossTypeReuseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := crossTypeReuseIRSrc(tc.main)
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}

// TestSelfHostCrossTypeReuseFiresX86_64 proves the cross-type reuse actually
// lowers in place: a dead cross-type donor yields ONE struct-box alloc (its box
// is reused by the recipient), while the same program with the donor read after
// the recipient yields TWO (no reuse). Guards against the widening silently
// regressing to a no-op that stays correct only because a fresh alloc is also
// correct.
func TestSelfHostCrossTypeReuseFiresX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	countAllocs := func(prog string) int {
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		return countUserArrBoxAllocs(asm)
	}
	if got := countAllocs(crossTypeReuseDeadDonor); got != 1 {
		t.Errorf("dead cross-type donor: got %d struct-box allocs, want 1 (reuse should fire)", got)
	}
	if got := countAllocs(crossTypeReuseLiveDonor); got != 2 {
		t.Errorf("live cross-type donor: got %d struct-box allocs, want 2 (reuse must NOT fire)", got)
	}
	// Array-field cross-type: the dead donor's box is reused (1 arr_box); the
	// live donor keeps both boxes (2).
	if got := countAllocs(crossTypeReuseArrDeadDonor); got != 1 {
		t.Errorf("dead array-field cross-type donor: got %d arr_box, want 1 (reuse should fire)", got)
	}
	if got := countAllocs(crossTypeReuseArrLiveDonor); got != 2 {
		t.Errorf("live array-field cross-type donor: got %d arr_box, want 2 (reuse must NOT fire)", got)
	}
}
