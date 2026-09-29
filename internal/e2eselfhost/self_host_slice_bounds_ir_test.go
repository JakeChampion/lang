package e2eselfhost

import (
	"strings"
	"testing"
)

// TestSelfHostSliceBoundsIR pins the slice-CONSTRUCTION bounds contract
// (#5419, docs/ARRAY-BOUNDS.md "Slice construction") on x86-64: `a[lo:hi]`
// with hi > len, lo > hi, or lo < 0 ABORTS with exit 134 through
// __fern_oob_abort instead of copying (arrays) or viewing (strings) past the
// source. `lo == hi == len` stays legal (empty slice at the boundary).
func TestSelfHostSliceBoundsIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range selfHostSliceBoundsCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := cli.emit(t, "x86-64-linux", tc.src)
			if !strings.Contains(asm, ".Lssa_") {
				t.Fatalf("%s: did not lower through the IR (no .Lssa_ labels)", tc.name)
			}
			if tc.want == 134 && !strings.Contains(asm, "__fern_oob_abort") {
				t.Fatalf("%s: no __fern_oob_abort in emitted asm — bounds check missing", tc.name)
			}
			if code, _ := cli.runX86(t, asm); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostSliceBoundsIRArm64 is the arm64 counterpart, under qemu.
func TestSelfHostSliceBoundsIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range selfHostSliceBoundsCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := cli.emit(t, "arm64-linux", tc.src)
			if tc.want == 134 && !strings.Contains(asm, "__fern_oob_abort") {
				t.Fatalf("%s: no __fern_oob_abort in emitted arm64 asm — bounds check missing", tc.name)
			}
			if code, _ := runArm64(t, gcc, qemu, asm); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

func selfHostSliceBoundsCases() []struct {
	name string
	src  string
	want int
} {
	return []struct {
		name string
		src  string
		want int
	}{
		{"arr-high-past-end",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; var s: [i32] = a[0:4]; return s.len(); }`, 134},
		{"arr-high-far-past-end",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; var s: [i32] = a[1:100]; return s.len(); }`, 134},
		{"arr-reversed",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; var s: [i32] = a[2:1]; return s.len(); }`, 134},
		{"arr-negative-low",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; var lo: i32 = 0 - 1; var s: [i32] = a[lo:2]; return s.len(); }`, 134},
		{"str-high-past-end",
			`function main(): i32 { var s: string = "abc"; var t: str = slice_unchecked(s, 0, 9); return t.len(); }`, 134},
		{"str-reversed",
			`function main(): i32 { var s: string = "abc"; var t: str = slice_unchecked(s, 2, 1); return t.len(); }`, 134},
		// In-range: boundary forms stay legal. a[1:3] sums 2+3 = 5,
		// a[3:3] is empty, s[1:3] = "bc" has len 2 → exit 5+0+2 = 7.
		{"in-range-ok",
			`function main(): i32 { var a: i32[] = [1, 2, 3]; var w: [i32] = a[1:3]; var t: i32 = 0; var i: i32 = 0; while (i < w.len()) { t = t + w[i]; i = i + 1; } var e: [i32] = a[3:3]; var s: string = "abc"; return t + e.len() + (slice_unchecked(s, 1, 3)).len(); }`, 7},
	}
}
