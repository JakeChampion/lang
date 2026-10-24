package e2ecompiler

import (
	"runtime"
	"testing"
)

var interpU64Cases = []struct {
	name string
	src  string
}{
	{"unsigned-compare", "function main(): i32 {\n  let a: u64 = 1;\n  a = a - 2;\n  if (a > 100) { return 7; }\n  return 1;\n}\n"},
	{"unsigned-div-rem", "function main(): i32 {\n  let a: u64 = 1;\n  a = a - 2;\n  let q: u64 = a / 2;\n  let r: u64 = a % 10;\n  if (q == 9223372036854775807 && (r as i32) == 5) { return 7; }\n  return 1;\n}\n"},
	{"logical-shift-right", "function main(): i32 {\n  let a: u64 = 1;\n  a = a - 2;\n  let s: u64 = a >> (1 as u64);\n  if (s == 9223372036854775807) { return 7; }\n  return 1;\n}\n"},
	{"u64-to-f64", "function main(): i32 {\n  let a: u64 = 1;\n  a = a - 2;\n  let f: f64 = a as f64;\n  if (f > 1.8e19 && f < 1.9e19) { return 7; }\n  return 1;\n}\n"},
	{"saturating", "function main(): i32 {\n  let big: u64 = 9223372036854775807;\n  big = big + 1;\n  let sat_add: u64 = big +| big;\n  let sat_sub: u64 = (0 as u64) -| (1 as u64);\n  if (sat_add == (0 as u64) - (1 as u64) && sat_sub == (0 as u64)) { return 7; }\n  return 1;\n}\n"},
	{"cast-tags-inline", "function main(): i32 {\n  let d: u64 = (0 as u64) - (1 as u64);\n  if (d <= (1 as u64)) { return 1; }\n  let half: u64 = d / (2 as u64);\n  if (half != 9223372036854775807) { return 2; }\n  return 7;\n}\n"},
	{"u64-receiver-method", "function (n: u64) twice(): u64 { return n * (2 as u64); }\nfunction main(): i32 {\n  let big: u64 = 9223372036854775807;\n  big = big + (1 as u64);\n  let t: u64 = big.twice();\n  if (t != (0 as u64)) { return 1; }\n  let small: u64 = 21;\n  if (small.twice() != (42 as u64)) { return 2; }\n  return 7;\n}\n"},

	{"suffixed-literal-in-array", "function main(): i32 {\n  let xs: u64[] = [5u64, 9223372036854775809u64];\n  if (xs[1] > xs[0]) { return 7; }\n  return 1;\n}\n"},
	{"i64-return-widens", "function pow2(n: i32): i64 {\n  if (n <= 0) { return 1; }\n  return pow2(n - 1) * 2;\n}\nfunction main(): i32 {\n  if (pow2(33) == 8589934592) { return 7; }\n  return 1;\n}\n"},

	{"i64-stays-signed", "function main(): i32 {\n  let x: i64 = 1;\n  x = x - 2;\n  if (x > 100) { return 1; }\n  if (x / 2 != 0) { return 2; }\n  if ((x >> 1) != 0 - 1) { return 3; }\n  return 7;\n}\n"},
	{"u32-and-i32-unchanged", "function main(): i32 {\n  let a: u32 = 1;\n  a = a - 2;\n  if (a <= 100) { return 1; }\n  if (a / 2 != 2147483647) { return 2; }\n  let b: i32 = 0 - 1;\n  if (b / 2 != 0) { return 3; }\n  if (b >= 100) { return 4; }\n  return 7;\n}\n"},
}

func TestSelfHostInterpU64(t *testing.T) {
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

	for _, tc := range interpU64Cases {
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
}
