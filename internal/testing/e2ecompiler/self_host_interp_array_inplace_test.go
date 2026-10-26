package e2ecompiler

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

// `x = x.with(i, v)` and `x = x.append(v)` release x's slot first, so the
// write is in place only when no other value holds the array. Each case
// keeps a second holder or checks a value the write must not change.
var interpArrayInPlaceCases = []struct {
	name string
	src  string
}{
	{"alias-keeps-old-value", "function main(): i32 {\n  let a: i32[] = [1, 2, 3];\n  let b: i32[] = a;\n  a = a.with(0, 9);\n  a = a.append(4);\n  if (b[0] != 1 || b.len() != 3) { return 1; }\n  if (a[0] != 9 || a.len() != 4) { return 2; }\n  return 7;\n}\n"},
	{"argument-reads-target", "function main(): i32 {\n  let a: i32[] = [4, 5, 6];\n  a = a.with(0, a[2] + a.len());\n  a = a.append(a[0]);\n  if (a[0] != 9 || a[3] != 9) { return 1; }\n  return 7;\n}\n"},
	{"caller-array-unchanged", "function bump(xs: i32[]): i32[] {\n  xs = xs.with(0, 99);\n  return xs;\n}\nfunction main(): i32 {\n  let a: i32[] = [1, 2];\n  let b: i32[] = bump(a);\n  if (a[0] != 1 || b[0] != 99) { return 1; }\n  return 7;\n}\n"},
	{"struct-field-holder", "struct Box { xs: i32[] }\nfunction main(): i32 {\n  let a: i32[] = [1, 2];\n  let box: Box = Box { xs: a };\n  a = a.with(1, 8);\n  if (box.xs[1] != 2 || a[1] != 8) { return 1; }\n  return 7;\n}\n"},
	{"closure-capture", "function main(): i32 {\n  let a: i32[] = [1, 2];\n  let first = (): i32 => { return a[0]; };\n  let b: i32[] = a;\n  b = b.with(0, 5);\n  if (first() != 1 || b[0] != 5) { return 1; }\n  return 7;\n}\n"},
	{"byte-elements", "function main(): i32 {\n  let a: u8[] = [250, 251];\n  a = a.with(0, 255 as u8);\n  a = a.append(3 as u8);\n  let sum: i32 = 0;\n  for b in a { sum = sum + b as i32; }\n  if (sum != 509) { return 1; }\n  return 7;\n}\n"},
	{"chain-alias", "function main(): i32 {\n  let a: i32[] = [1, 2];\n  let b: i32[] = a;\n  a = a.with(0, 5).append(a[1]).with(1, a[0]);\n  if (b[0] != 1 || b.len() != 2) { return 1; }\n  if (a[0] != 5 || a[1] != 1 || a[2] != 2) { return 2; }\n  return 7;\n}\n"},
	{"loop-fill", "function main(): i32 {\n  let a: i32[] = [];\n  for i in 0..100 { a = a.append(0); }\n  let keep: i32[] = a;\n  for i in 0..100 { a = a.with(i, i * 2); }\n  if (keep[99] != 0 || a[99] != 198 || a.len() != 100) { return 1; }\n  return 7;\n}\n"},
}

func TestSelfHostInterpArrayInPlace(t *testing.T) {
	native := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/interp_run.fern")

	var driverBin string
	var runner []string
	if native {
		driverBin = buildSelfHostBinArm64Darwin(t, dir, "drivers/interp_run.fern", "interp_run")
	} else {
		gcc, r := x86_64Tooling(t)
		runner = r
		driverBin = buildSelfHostBin(t, gcc, dir, "drivers/interp_run.fern", "interp_run")
	}
	interpBin := buildLangBinForInterp(t)

	for _, tc := range interpArrayInPlaceCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if want != 7 {
				t.Fatalf("native interp oracle exited %d, want 7 — the case itself is wrong, not the self-host engine", want)
			}
			if got := runDriverExit(t, runner, driverBin, []byte(tc.src)); got != want {
				t.Errorf("self-host interp exited %d, want %d (native interp oracle)", got, want)
			}
		})
	}

	// A quadratic fill takes sixteen times as long for four times the
	// elements. The bound of eight leaves room for timing noise.
	fill := func(n int) time.Duration {
		src := fmt.Sprintf("function main(): i32 {\n  let a: u8[] = [];\n  for i in 0..%d { a = a.append(0 as u8); }\n  for i in 0..%d { a = a.with(i, (i %% 251) as u8); }\n  if (a[%d] != (%d %% 251) as u8) { return 1; }\n  return 7;\n}\n", n, n, n-1, n-1)
		start := time.Now()
		if got := runDriverExit(t, runner, driverBin, []byte(src)); got != 7 {
			t.Fatalf("fill of %d elements exited %d, want 7", n, got)
		}
		return time.Since(start)
	}
	small, large := fill(20000), fill(80000)
	if large > 8*small {
		t.Errorf("a fill of 80000 elements took %v and 20000 took %v: the writes copy the array", large, small)
	}
}
