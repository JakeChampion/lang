package e2eselfhost

import (
	"runtime"
	"testing"
)

// interpU64Cases are u64 programs run through the self-host interpreter's
// stdin driver. The interpreter carries a 64-bit value as two i32 halves and
// read them back SIGNED, with no unsigned 64-bit carrier at all, so before
// #6810 every compare, divide, remainder and `>>` on a u64 above 2^63 took the
// signed path — a wrong answer rather than a missing feature. std/float's
// Dragonbox formatter is written almost entirely in u64, which is where it was
// reported (`(1.5).to_string()` came back starting with `-`); those cases need
// the module loader and live in TestSelfHostInterpStdlibModload.
//
// Each case is oracle-checked against the native interpreter rather than
// against a hardcoded exit code, so what they pin is the language's behaviour.
var interpU64Cases = []struct {
	name string
	src  string
}{
	// Wrapping below zero puts the sign bit on, which is the whole boundary:
	// unsigned that value is u64::MAX, signed it is -1.
	{"unsigned-compare", "function main(): i32 {\n  var a: u64 = 1;\n  a = a - 2;\n  if (a > 100) { return 7; }\n  return 1;\n}\n"},
	{"unsigned-div-rem", "function main(): i32 {\n  var a: u64 = 1;\n  a = a - 2;\n  var q: u64 = a / 2;\n  var r: u64 = a % 10;\n  if (q == 9223372036854775807 && (r as i32) == 5) { return 7; }\n  return 1;\n}\n"},
	// `>>` on a u64 is LOGICAL: u64::MAX >> 1 is i64::MAX, not -1.
	{"logical-shift-right", "function main(): i32 {\n  var a: u64 = 1;\n  a = a - 2;\n  var s: u64 = a >> (1 as u64);\n  if (s == 9223372036854775807) { return 7; }\n  return 1;\n}\n"},
	// A u64 above 2^63 converts to the huge positive float, not a negative one.
	{"u64-to-f64", "function main(): i32 {\n  var a: u64 = 1;\n  a = a - 2;\n  var f: f64 = a as f64;\n  if (f > 1.8e19 && f < 1.9e19) { return 7; }\n  return 1;\n}\n"},
	// Saturating arithmetic clamps to [0, u64::MAX] rather than the signed
	// bounds: the sub floors at 0 where a signed clamp would reach i64::MIN.
	{"saturating", "function main(): i32 {\n  var big: u64 = 9223372036854775807;\n  big = big + 1;\n  var sat_add: u64 = big +| big;\n  var sat_sub: u64 = (0 as u64) -| (1 as u64);\n  if (sat_add == (0 as u64) - (1 as u64) && sat_sub == (0 as u64)) { return 7; }\n  return 1;\n}\n"},
	// The signedness has to enter at the CAST too, not only at a declared
	// binding: a compact 32-bit carrier has nowhere to put the tag, so
	// `(0 as u64) - (1 as u64)` has to widen to reach u64::MAX.
	{"cast-tags-inline", "function main(): i32 {\n  var d: u64 = (0 as u64) - (1 as u64);\n  if (d <= (1 as u64)) { return 1; }\n  var half: u64 = d / (2 as u64);\n  if (half != 9223372036854775807) { return 2; }\n  return 7;\n}\n"},
	// A u64 receiver dispatches on `u64`, not `i64` — which is what puts
	// std/u64's ~30 methods in reach of the engine.
	{"u64-receiver-method", "function (n: u64) twice(): u64 { return n * (2 as u64); }\nfunction main(): i32 {\n  var big: u64 = 9223372036854775807;\n  big = big + (1 as u64);\n  var t: u64 = big.twice();\n  if (t != (0 as u64)) { return 1; }\n  var small: u64 = 21;\n  if (small.twice() != (42 as u64)) { return 2; }\n  return 7;\n}\n"},

	// CONTROLS. The fix reads MORE values as unsigned, so what can break is a
	// value that should have stayed signed.
	{"i64-stays-signed", "function main(): i32 {\n  var x: i64 = 1;\n  x = x - 2;\n  if (x > 100) { return 1; }\n  if (x / 2 != 0) { return 2; }\n  if ((x >> 1) != 0 - 1) { return 3; }\n  return 7;\n}\n"},
	{"u32-and-i32-unchanged", "function main(): i32 {\n  var a: u32 = 1;\n  a = a - 2;\n  if (a <= 100) { return 1; }\n  if (a / 2 != 2147483647) { return 2; }\n  var b: i32 = 0 - 1;\n  if (b / 2 != 0) { return 3; }\n  if (b >= 100) { return 4; }\n  return 7;\n}\n"},
}

// TestSelfHostInterpU64 drives each case through a compiled `interp_run.fern`
// — the stdin driver, which resolves no imports, so every case here is
// self-contained.
//
// Host modes mirror TestSelfHostInterpEnumConstructors: on Apple Silicon the
// driver is built for arm64-darwin through the in-process Mach-O path, off it
// with the Go x86-64 backend.
func TestSelfHostInterpU64(t *testing.T) {
	native := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "interp_run.fern")

	var driverBin string
	var runner []string
	if native {
		driverBin = buildSelfHostBinArm64Darwin(t, dir, "interp_run.fern", "interp_run")
	} else {
		gcc, r := x86_64Tooling(t)
		runner = r
		driverBin = buildSelfHostBin(t, gcc, dir, "interp_run.fern", "interp_run")
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
