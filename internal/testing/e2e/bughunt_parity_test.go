package e2e

import "testing"

func assertBackendsAgreeWithInterp(t *testing.T, name, src string) {
	t.Helper()
	want := runInterpByte(t, src)
	t.Run(name+"/x86_64", func(t *testing.T) {
		_, code := compileAndRunX86_64(t, src)
		if code != want {
			t.Errorf("x86_64 exit=%d, interp=%d\nsrc:\n%s", code, want, src)
		}
	})
	t.Run(name+"/arm64", func(t *testing.T) {
		_, code := compileAndRunArm64(t, src)
		if code != want {
			t.Errorf("arm64 exit=%d, interp=%d\nsrc:\n%s", code, want, src)
		}
	})
	t.Run(name+"/wasmbin", func(t *testing.T) {
		got := compileAndRunWasmbinMain(t, src)
		if got != want {
			t.Errorf("wasmbin result=%d, interp=%d\nsrc:\n%s", got, want, src)
		}
	})
}

func TestBugHunt_SignedShiftParity(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"arith_shr_neg", `function main(): i32 { if (((0 - 7) >> 1) == (0 - 4)) { return 7; } return 1; }`},
		{"arith_shr_allones", `function main(): i32 { if (((0 - 1) >> 5) == (0 - 1)) { return 7; } return 1; }`},
		{"shr_count_mask", `function main(): i32 { let s: i32 = 33; if (((0 - 8) >> s) == (0 - 4)) { return 7; } return 1; }`},
		{"shl_count_mask", `function main(): i32 { let s: i32 = 33; if ((1 << s) == 2) { return 7; } return 1; }`},
	}
	for _, c := range cases {
		assertBackendsAgreeWithInterp(t, c.name, c.src)
	}
}

func TestBugHunt_FloatLiteralDefaultsToF64(t *testing.T) {
	src := `function main(): i32 {
    let x = 16777217.0;
    return (x - 16777216.0) as i32;
}`
	if got := runInterpByte(t, src); got != 1 {
		t.Fatalf("interp: untyped float literal lost precision: got %d, want 1 (f64)", got)
	}
	assertBackendsAgreeWithInterp(t, "float_default_f64", src)
}

func TestBugHunt_FloatBinaryToIntCast(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"sub", `function main(): i32 { return (7.9 - 0.0) as i32; }`},
		{"add", `function main(): i32 { return (3.25 + 4.5) as i32; }`},
		{"mul", `function main(): i32 { return (2.5 * 3.0) as i32; }`},
		{"div", `function main(): i32 { return (40.0 / 5.0) as i32; }`},
	}
	for _, c := range cases {
		assertBackendsAgreeWithInterp(t, c.name, c.src)
	}
}

func TestBugHunt_FloatNaNComparisonParity(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"ne_true", `function main(): i32 { let z: f64 = 0.0; let n: f64 = z / z; if (n != n) { return 7; } return 1; }`},
		{"eq_false", `function main(): i32 { let z: f64 = 0.0; let n: f64 = z / z; if (n == n) { return 1; } return 7; }`},
		{"lt_false", `function main(): i32 { let z: f64 = 0.0; let n: f64 = z / z; if (n < 1.0) { return 1; } return 7; }`},
		{"le_false", `function main(): i32 { let z: f64 = 0.0; let n: f64 = z / z; if (n <= n) { return 1; } return 7; }`},
		{"gt_false", `function main(): i32 { let z: f64 = 0.0; let n: f64 = z / z; if (n > 1.0) { return 1; } return 7; }`},
		{"ge_false", `function main(): i32 { let z: f64 = 0.0; let n: f64 = z / z; if (n >= 1.0) { return 1; } return 7; }`},
		{"f32_ne_true", `function main(): i32 { let z: f32 = 0.0; let n: f32 = z / z; if (n != n) { return 7; } return 1; }`},
		{"f32_eq_false", `function main(): i32 { let z: f32 = 0.0; let n: f32 = z / z; if (n == n) { return 1; } return 7; }`},
		{"ordered_lt", `function main(): i32 { let a: f64 = 1.5; let b: f64 = 2.5; if (a < b) { return 7; } return 1; }`},
		{"ordered_eq", `function main(): i32 { let a: f64 = 2.5; if (a == a) { return 7; } return 1; }`},
	}
	for _, c := range cases {
		assertBackendsAgreeWithInterp(t, c.name, c.src)
	}
}

func TestBugHunt_FloatToIntSaturation(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"i32_pos_ovf", `function main(): i32 { let x: f64 = 1e30; if ((x as i32) == 2147483647) { return 7; } return 1; }`},
		{"i32_neg_ovf", `function main(): i32 { let x: f64 = 0.0 - 1e30; if ((x as i32) == 0 - 2147483647 - 1) { return 7; } return 1; }`},
		{"i32_nan", `function main(): i32 { let x: f64 = 0.0 / 0.0; if ((x as i32) == 0) { return 7; } return 1; }`},
		{"i32_in_range", `function main(): i32 { let x: f64 = 0.0 - 42.9; if ((x as i32) == 0 - 42) { return 7; } return 1; }`},
		{"i64_pos_ovf", `function main(): i32 { let x: f64 = 1e30; if ((x as i64) == 9223372036854775807) { return 7; } return 1; }`},
		{"i64_neg_ovf", `function main(): i32 { let x: f64 = 0.0 - 1e30; if ((x as i64) == 0 - 9223372036854775807 - 1) { return 7; } return 1; }`},
		{"i64_nan", `function main(): i32 { let x: f64 = 0.0 / 0.0; let r: i64 = x as i64; if (r == 0) { return 7; } return 1; }`},
		{"f32_i32_ovf", `function main(): i32 { let x: f32 = 1e30; if ((x as i32) == 2147483647) { return 7; } return 1; }`},
	}
	for _, c := range cases {
		assertBackendsAgreeWithInterp(t, c.name, c.src)
	}
}

func TestBugHunt_FloatToStringLargeMagnitude(t *testing.T) {
	src := `import "std/f64";
function main(): i32 {
    print((1e20).to_string());
    print((0.0 - 3.0e20).to_string());
    return 0;
}`
	const want = "100000000000000000000\n-300000000000000000000"
	t.Run("x86_64", func(t *testing.T) {
		out, _ := compileAndRunX86_64(t, src)
		if got := normalizeOut(out); got != want {
			t.Errorf("x86_64 stdout = %q, want %q", got, want)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		out, _ := compileAndRunArm64(t, src)
		if got := normalizeOut(out); got != want {
			t.Errorf("arm64 stdout = %q, want %q", got, want)
		}
	})
}

func normalizeOut(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
