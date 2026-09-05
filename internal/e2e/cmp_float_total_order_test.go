package e2e

import "testing"

// core/cmp's float `Ord` is IEEE 754 totalOrder and its float `Eq` is bit
// equality (#8588), so every `[T: Ord]` / `[T: Eq]` generic — the sorts, the
// std/test relational and uniqueness asserts — sees one place for each NaN
// and signed zero instead of a `cmp` of 0 that let `assert_le(nan, 1.0)` pass
// vacuously. The NaNs are bit patterns because an arithmetic NaN's sign is
// target-dependent. Each check adds a power of two; every backend must reach
// 63 (kept below the 126 WASI can express).
func TestNativeCmpFloatTotalOrder(t *testing.T) {
	src := `import "core/cmp" as cmp;
function main(): i32 {
    var r: i32 = 0;
    var pnan: f64 = f64_from_bits(9221120237041090560 as i64);
    var nnan: f64 = f64_from_bits(0 - 2251799813685248 as i64);
    var nzero: f64 = f64_from_bits((0 - 9223372036854775807 as i64) - 1 as i64);
    var inf: f64 = 1.0 / 0.0;
    var zero: f64 = 0.0;
    var xs: f64[] = [3.0, pnan, zero, 0.0 - inf, 1.0, inf, nnan, nzero];
    var want: f64[] = [nnan, 0.0 - inf, nzero, zero, 1.0, 3.0, inf, pnan];
    var s: f64[] = cmp.sort(xs);
    var inverted: f64[] = [3.0, pnan, 1.0];
    if (cmp.eq_arrays(s, want) && cmp.is_sorted(s) && !cmp.is_sorted(inverted)) { r = r + 1; }
    if (nzero.cmp(zero) == 0 - 1 && !nzero.eq(zero)) { r = r + 2; }
    var nans: f64[] = [pnan, pnan, nnan];
    if (pnan.eq(pnan) && !pnan.eq(nnan) && pnan.cmp(nnan) == 1 && cmp.distinct(nans).len() == 2) { r = r + 4; }
    var pnan32: f32 = f32_from_bits(2143289344);
    var nzero32: f32 = f32_from_bits((0 - 2147483647) - 1);
    var one32: f32 = 1.0 as f32;
    var zero32: f32 = 0.0 as f32;
    var xs32: f32[] = [one32, pnan32, zero32, nzero32];
    var want32: f32[] = [nzero32, zero32, one32, pnan32];
    if (cmp.eq_arrays(cmp.sort(xs32), want32)) { r = r + 8; }
    if (pnan32.eq(pnan32) && !nzero32.eq(zero32)) { r = r + 16; }
    var hi: f64 = cmp.max(inf, pnan);
    var lo: f64 = cmp.min(0.0 - inf, nnan);
    if (hi.eq(pnan) && lo.eq(nnan)) { r = r + 32; }
    return r;
}
`
	const want = 63
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != want {
		t.Errorf("interp = %d, want %d", code, want)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != want {
		t.Errorf("x86-64 = %d, want %d", code, want)
	}
	if _, code := runFixtureArm64(t, p, ""); code != want {
		t.Errorf("arm64 = %d, want %d", code, want)
	}
	if code := runWasm(t, src); code != want {
		t.Errorf("wasm = %d, want %d", code, want)
	}
}
