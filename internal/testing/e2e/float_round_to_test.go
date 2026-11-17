package e2e

import "testing"

const floatRoundToProg = `
import "std/f32";
import "std/f64";
function approx(a: f64, b: f64): boolean { let d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.0001; }
function main(): i32 {
    if (!approx((3.14159).round_to(2), 3.14)) { return 1; }
    if (!approx((3.14159).round_to(0), 3.0)) { return 2; }
    if (!approx((2.5).round_to(0), 3.0)) { return 3; }               // half away from zero
    if (!approx((3.14159).round_to(4), 3.1416)) { return 4; }
    if (!approx((0.0 - 2.567).round_to(1), 0.0 - 2.6)) { return 5; } // negative value
    if (!approx((1234.0).round_to(0 - 2), 1200.0)) { return 6; }     // negative digits (hundreds)
    if (!approx((1250.0).round_to(0 - 2), 1300.0)) { return 7; }     // half away, hundreds
    if (!approx((5.0).round_to(3), 5.0)) { return 8; }               // whole number unchanged
    if (!approx((0.0).round_to(2), 0.0)) { return 9; }
    // f32 mirror
    if (!approx((3.14159 as f32).round_to(2) as f64, 3.14)) { return 10; }
    if (!approx((2.71828 as f32).round_to(3) as f64, 2.718)) { return 11; }
    return 42;
}
`

func TestFloatRoundToInterp(t *testing.T) {
	if got := runInterpExit(t, floatRoundToProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestFloatRoundToX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, floatRoundToProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestFloatRoundToWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, floatRoundToProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestFloatRoundToArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, floatRoundToProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

const floatRoundToSignedZeroProg = `
import "std/f32";
import "std/f64";
@noinline function round64(x: f64, digits: i32): f64 { return x.round_to(digits); }
@noinline function round32(x: f32, digits: i32): f32 { return x.round_to(digits); }
function main(): i32 {
  let digits: i32[] = [-2147483648, -2147483647, -1074, -400, -310, -309, -308, 2147483647];
  let values64: i64[] = [0, 1, 4503599627370495, 4503599627370496, 4598175219545276416, 4607182418800017408, 9094988921128908188, 9218868437227405311];
  let values32: i32[] = [0, 1, 8388607, 8388608, 1048576000, 1065353216, 1900671690, 2139095039];
  let special64: i64[] = [9218868437227405312, -4503599627370496, 9221120237041090561, -2251799813685247];
  let special32: i32[] = [2139095040, -8388608, 2143289345, -4194303];
  let checks = 0;
  for side in 0..2 {
    let sign64: i64 = if (side == 0) { 0 } else { -9223372036854775807 - 1 };
    let sign32: i32 = if (side == 0) { 0 } else { -2147483647 - 1 };
    for i in 0..values64.len() {
      let bits = values64[i] | sign64;
      for n in digits {
        let want = if (n == 2147483647) { bits } else if (n == -308 && i == 7) { 9218868437227405312 | sign64 } else { sign64 };
        if (f64_bits(round64(f64_from_bits(bits), n)) != want) { return 1; }
        checks += 1;
      }
    }
    for i in 0..values32.len() {
      let bits = values32[i] | sign32;
      for n in digits {
        let want = if (n == 2147483647) { bits } else { sign32 };
        if (f32_bits(round32(f32_from_bits(bits), n)) != want) { return 2; }
        checks += 1;
      }
    }
  }
  for bits in special64 {
    let x = f64_from_bits(bits);
    for n in digits {
      let y = round64(x, n);
      if (x.is_nan()) {
        if (!y.is_nan()) { return 3; }
      } else if (f64_bits(y) != bits) { return 4; }
      checks += 1;
    }
  }
  for bits in special32 {
    let x = f32_from_bits(bits);
    for n in digits {
      let y = round32(x, n);
      if (x.is_nan()) {
        if (!y.is_nan()) { return 5; }
      } else if (f32_bits(y) != bits) { return 6; }
      checks += 1;
    }
  }
  if (checks != 320) { return 7; }
  if (f64_bits(round64(-0.25, 0)) != -9223372036854775807 - 1) { return 8; }
  if (f64_bits(round64(0.25, 0)) != 0) { return 9; }
  if (round64(-0.5, 0) != -1.0 || round64(0.5, 0) != 1.0) { return 10; }
  if (f32_bits(round32(-0.25 as f32, 0)) != -2147483647 - 1) { return 11; }
  if (f32_bits(round32(0.25 as f32, 0)) != 0) { return 12; }
  if (round32(-0.5 as f32, 0) != -1.0 as f32 || round32(0.5 as f32, 0) != 1.0 as f32) { return 13; }
  if (__rc_underflow_count() != 0) { return 14; }
  print("328 round_to bit controls pass");
  return 42;
}
`

func TestFloatRoundToSignedZero(t *testing.T) {
	t.Run("go", func(t *testing.T) {
		if got := runInterpExit(t, floatRoundToSignedZeroProg); got != 42 {
			t.Fatalf("Go got %d, want 42", got)
		}
	})
	t.Run("x86-64", func(t *testing.T) {
		if _, got := compileAndRunX86_64(t, floatRoundToSignedZeroProg); got != 42 {
			t.Fatalf("x86-64 got %d, want 42", got)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		if _, got := compileAndRunArm64(t, floatRoundToSignedZeroProg); got != 42 {
			t.Fatalf("ARM64 got %d, want 42", got)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if got := compileAndRunWasmbinMain(t, floatRoundToSignedZeroProg); got != 42 {
			t.Fatalf("Wasm got %d, want 42", got)
		}
	})
}
