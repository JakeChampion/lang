package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
// STATIC-CONSTANT path (#6149): an all-scalar-literal aggregate is placed in
// data, which allocates nothing and makes the reuse it would have fed moot —
// correct, and strictly better, but it would leave this test measuring zero
// against zero. A donor that genuinely allocates is what the reuse contract
// below is about. (The constant/reuse interaction itself is pinned by
// TestSelfHostConstAggregateIRX86_64's `reuse-shape-all-constant`.)
const crossTypeReuseDeadDonor = `struct Point { x: i32, y: i32 } struct Pair { a: i32, b: i32 } function main(): i32 { var k = 1; var p = Point { x: 3 * k, y: 4 }; var s = p.x + p.y; var q = Pair { a: s, b: 9 }; return q.a + q.b; }`
const crossTypeReuseLiveDonor = `struct Point { x: i32, y: i32 } struct Pair { a: i32, b: i32 } function main(): i32 { var k = 1; var p = Point { x: 3 * k, y: 4 }; var q = Pair { a: 5 * k, b: 9 }; return q.a + q.b + p.x + p.y; }`

// Array-field cross-type pair: a dead Holder{id,items} reused for Bag{tag,data}
// (identical [i32, i32[]] layout). Dead donor → the struct box is reused (3
// __fern_arr_box: donor box + donor items array + recipient data array); with the
// donor read after the recipient, no reuse fires (4: both struct boxes + both
// arrays). The delta proves the array-field cross-type reuse lowers in place.
const crossTypeReuseArrDeadDonor = `struct Holder { id: i32, items: i32[] } struct Bag { tag: i32, data: i32[] } function main(): i32 { var h = Holder { id: 1, items: [1, 2] }; var s = h.id + h.items[0]; var b = Bag { tag: s, data: [3, 4] }; return b.tag + b.data[0]; }`
const crossTypeReuseArrLiveDonor = `struct Holder { id: i32, items: i32[] } struct Bag { tag: i32, data: i32[] } function main(): i32 { var h = Holder { id: 1, items: [1, 2] }; var b = Bag { tag: 5, data: [3, 4] }; return b.tag + b.data[0] + h.id + h.items[1]; }`

// crossTypeReuseIRCases exercise cross-TYPE FBIP reuse (structs_reuse_compatible):
// a dead struct donor whose box is reused in place by a LATER construction of a
// DIFFERENT struct type with the same box class (identical per-position field
// widths + kinds — scalar↔scalar or leak-safe-array↔leak-safe-array). Native does
// this (general_reuse same-box-class incl. pointer fields), so the self-host
// must not require the donor and recipient to be the SAME type. Each case
// embeds a value check (returns 90/91 on mismatch)
// and then returns __rc_underflow() — so want=0 means both the reused value is
// correct AND no over-release occurred. A mis-sized reuse (wrong box class) would
// corrupt the freelist and surface as a wrong value or non-zero underflow,
// especially the "-probe" cases that allocate again after the reuse.
var crossTypeReuseIRCases = []struct {
	name string
	main string
	want int
}{
	// Dead Point{i32,i32} reused for Pair{i32,i32} — native's canonical
	// same-box-class cross-type case. 3+4 read, then 7+9 = 16.
	{"point-to-pair", `struct Point { x: i32, y: i32 } struct Pair { a: i32, b: i32 } function main(): i32 { var p = Point { x: 3, y: 4 }; var s = p.x + p.y; var q = Pair { a: s, b: 9 }; if (q.a + q.b != 16) { return 90; } return __rc_underflow(); }`, 0},
	// Reuse then a FRESH array alloc: if the reuse mis-sized Point's box, the
	// recycled block would poison the freelist and the array would read back
	// wrong. 10+20 + 100+200+300 = 630.
	{"cross-reuse-then-alloc-probe", `struct Point { x: i32, y: i32 } struct Pair { a: i32, b: i32 } function main(): i32 { var p = Point { x: 1, y: 2 }; var u = p.x + p.y; var q = Pair { a: 10, b: 20 }; var fresh = [100, 200, 300]; if (q.a + q.b + fresh[0] + fresh[1] + fresh[2] != 630) { return 91; } if (u != 3) { return 92; } return __rc_underflow(); }`, 0},
	// Mixed widths (i64 + i32) — donor and recipient share the width sequence
	// [8,4], so the box class matches. av=40, q.b=2 -> 42.
	{"mixed-width-cross", `struct A { p: i64, q: i32 } struct B { r: i64, s: i32 } function main(): i32 { var a = A { p: 5, q: 1 }; var d = (a.p as i32) + a.q; var b = B { r: 40, s: 2 }; var av: i64 = b.r; if ((av as i32) + b.s != 42) { return 90; } if (d != 6) { return 92; } return __rc_underflow(); }`, 0},
	// Different field COUNT must NOT cross-reuse (guard rejects), but the program
	// stays correct: Trip allocates fresh. 3+4 read, 10+20+30 = 60.
	{"different-count-no-reuse", `struct Point { x: i32, y: i32 } struct Trip { a: i32, b: i32, c: i32 } function main(): i32 { var p = Point { x: 3, y: 4 }; var u = p.x + p.y; var t = Trip { a: 10, b: 20, c: 30 }; if (t.a + t.b + t.c != 60) { return 90; } if (u != 7) { return 92; } return __rc_underflow(); }`, 0},
	// ARRAY-FIELD cross-type: dead Holder{id:i32, items:i32[]} reused for
	// Bag{tag:i32, data:i32[]} (identical [i32, i32[]] layout). The reuse rc-dec's
	// the donor's OLD items array before writing the recipient's fresh data array;
	// the rc-underflow detector confirms exactly-once release. 2+30+40+11 = 83.
	{"array-field-cross", `struct Holder { id: i32, items: i32[] } struct Bag { tag: i32, data: i32[] } function main(): i32 { var h = Holder { id: 1, items: [10, 20] }; var u = h.id + h.items[0]; var b = Bag { tag: 2, data: [30, 40] }; if (b.tag + b.data[0] + b.data[1] + u != 83) { return 90; } return __rc_underflow(); }`, 0},
	// ARRAY-FIELD reuse then a FRESH array: the donor's freed items buffer must not
	// dangle into the recipient's data or the later fresh array. data=[10,20],
	// fresh=[7,8,9]: 10+20 + 7+8+9 + u(11) + tag(2) = 67.
	{"array-field-cross-then-alloc-probe", `struct Holder { id: i32, items: i32[] } struct Bag { tag: i32, data: i32[] } function main(): i32 { var h = Holder { id: 1, items: [10, 20] }; var u = h.id + h.items[0]; var b = Bag { tag: 2, data: [10, 20] }; var fresh = [7, 8, 9]; if (b.data[0] + b.data[1] + fresh[0] + fresh[1] + fresh[2] + u + b.tag != 67) { return 91; } return __rc_underflow(); }`, 0},
}

func crossTypeReuseIRSrc(mainBody string) string {
	return mainBody + "\n"
}

// TestSelfHostCrossTypeReuseIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, pinned to the "ir" path.
func TestSelfHostCrossTypeReuseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range crossTypeReuseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(crossTypeReuseIRSrc(tc.main))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
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
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

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
	// Array-field cross-type: dead donor reuses the box (3 arr_box: donor box +
	// donor items + recipient data); live donor allocates both boxes (4).
	if got := countAllocs(crossTypeReuseArrDeadDonor); got != 3 {
		t.Errorf("dead array-field cross-type donor: got %d arr_box, want 3 (reuse should fire)", got)
	}
	if got := countAllocs(crossTypeReuseArrLiveDonor); got != 4 {
		t.Errorf("live array-field cross-type donor: got %d arr_box, want 4 (reuse must NOT fire)", got)
	}
}

// TestSelfHostCrossTypeReuseIRWasm runs the same cases through the wasm IR backend
// — the cross-type reuse emit is pure scalar struct_set/struct_get (no raw ops),
// so it must lower cleanly on wasm too.
func TestSelfHostCrossTypeReuseIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host cross-type-reuse wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range crossTypeReuseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(crossTypeReuseIRSrc(tc.main))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "crosstypereuse_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("cross-type-reuse wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
