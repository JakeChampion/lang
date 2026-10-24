package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

const crossTypeReuseDeadDonor = `struct Point { x: i32, y: i32 } struct Pair { a: i32, b: i32 } function main(): i32 { let k = 1; let p = Point { x: 3 * k, y: 4 }; let s = p.x + p.y; let q = Pair { a: s, b: 9 }; return q.a + q.b; }`
const crossTypeReuseLiveDonor = `struct Point { x: i32, y: i32 } struct Pair { a: i32, b: i32 } function main(): i32 { let k = 1; let p = Point { x: 3 * k, y: 4 }; let q = Pair { a: 5 * k, b: 9 }; return q.a + q.b + p.x + p.y; }`

const crossTypeReuseArrDeadDonor = `struct Holder { id: i32, items: i32[] } struct Bag { tag: i32, data: i32[] } function main(): i32 { let k = 1; let h = Holder { id: 1 * k, items: [1, 2] }; let s = h.id + h.items[0]; let b = Bag { tag: s, data: [3, 4] }; return b.tag + b.data[0]; }`
const crossTypeReuseArrLiveDonor = `struct Holder { id: i32, items: i32[] } struct Bag { tag: i32, data: i32[] } function main(): i32 { let k = 1; let h = Holder { id: 1 * k, items: [1, 2] }; let b = Bag { tag: 5 * k, data: [3, 4] }; return b.tag + b.data[0] + h.id + h.items[1]; }`

var crossTypeReuseIRCases = []struct {
	name string
	main string
	want int
}{
	{"point-to-pair", `struct Point { x: i32, y: i32 } struct Pair { a: i32, b: i32 } function main(): i32 { let p = Point { x: 3, y: 4 }; let s = p.x + p.y; let q = Pair { a: s, b: 9 }; if (q.a + q.b != 16) { return 90; } return __rc_underflow_count(); }`, 0},
	{"cross-reuse-then-alloc-probe", `struct Point { x: i32, y: i32 } struct Pair { a: i32, b: i32 } function main(): i32 { let p = Point { x: 1, y: 2 }; let u = p.x + p.y; let q = Pair { a: 10, b: 20 }; let fresh = [100, 200, 300]; if (q.a + q.b + fresh[0] + fresh[1] + fresh[2] != 630) { return 91; } if (u != 3) { return 92; } return __rc_underflow_count(); }`, 0},
	{"mixed-width-cross", `struct A { p: i64, q: i32 } struct B { r: i64, s: i32 } function main(): i32 { let a = A { p: 5, q: 1 }; let d = (a.p as i32) + a.q; let b = B { r: 40, s: 2 }; let av: i64 = b.r; if ((av as i32) + b.s != 42) { return 90; } if (d != 6) { return 92; } return __rc_underflow_count(); }`, 0},
	{"different-count-no-reuse", `struct Point { x: i32, y: i32 } struct Trip { a: i32, b: i32, c: i32 } function main(): i32 { let p = Point { x: 3, y: 4 }; let u = p.x + p.y; let t = Trip { a: 10, b: 20, c: 30 }; if (t.a + t.b + t.c != 60) { return 90; } if (u != 7) { return 92; } return __rc_underflow_count(); }`, 0},
	{"array-field-cross", `struct Holder { id: i32, items: i32[] } struct Bag { tag: i32, data: i32[] } function main(): i32 { let h = Holder { id: 1, items: [10, 20] }; let u = h.id + h.items[0]; let b = Bag { tag: 2, data: [30, 40] }; if (b.tag + b.data[0] + b.data[1] + u != 83) { return 90; } return __rc_underflow_count(); }`, 0},
	{"array-field-cross-then-alloc-probe", `struct Holder { id: i32, items: i32[] } struct Bag { tag: i32, data: i32[] } function main(): i32 { let h = Holder { id: 1, items: [10, 20] }; let u = h.id + h.items[0]; let b = Bag { tag: 2, data: [10, 20] }; let fresh = [7, 8, 9]; if (b.data[0] + b.data[1] + fresh[0] + fresh[1] + fresh[2] + u + b.tag != 67) { return 91; } return __rc_underflow_count(); }`, 0},
}

func crossTypeReuseIRSrc(mainBody string) string {
	return mainBody + "\n"
}

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
	if got := countAllocs(crossTypeReuseArrDeadDonor); got != 1 {
		t.Errorf("dead array-field cross-type donor: got %d arr_box, want 1 (reuse should fire)", got)
	}
	if got := countAllocs(crossTypeReuseArrLiveDonor); got != 2 {
		t.Errorf("live array-field cross-type donor: got %d arr_box, want 2 (reuse must NOT fire)", got)
	}
}
