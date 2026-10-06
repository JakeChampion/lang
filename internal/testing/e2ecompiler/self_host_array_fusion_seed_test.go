package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Peeling must guard the first read, seed from the mapped first element, keep
// captured callbacks alive on both paths, and preserve scalar floating order.
func TestSelfHostArrayFusionSeededReductionBits(t *testing.T) {
	const src = `import "std/array";
function fused(xs: f64[], k: f64): Option[f64] {
  return xs.map((x: f64): f64 => x * k)
    .map((x: f64): f64 => x - 0.0)
    .reduce((a: f64, b: f64): f64 => a - b);
}
@noinline function mapped(x: f64, k: f64): f64 { return x * k - 0.0; }
@noinline function subtract(a: f64, b: f64): f64 { return a - b; }
function scalar(xs: f64[], k: f64): Option[f64] {
  if (xs.len() == 0) { return None; }
  let acc: f64 = mapped(xs[0], k);
  let i: i32 = 1;
  while (i < xs.len()) { acc = subtract(acc, mapped(xs[i], k)); i = i + 1; }
  return Some(acc);
}
function same(a: Option[f64], b: Option[f64]): boolean {
  match (a) {
    Some(x) => { match (b) {
      Some(y) => {
        // Mapping performs arithmetic even for a singleton (FS-04).
        if (y != y) { return x != x; }
        return f64_bits(x) == f64_bits(y);
      },
      None => { return false; }
    } },
    None => { match (b) { Some(_) => { return false; }, None => { return true; } } }
  }
}
function main(): i32 {
  let bits: i64[] = [0i64, 0i64 - 9223372036854775807i64 - 1i64,
    9218868437227405312i64, 0i64 - 4503599627370496i64,
    9221120237041090625i64, 9221120237041090626i64,
    9218868437227405313i64, 1i64, 0i64 - 9223372036854775807i64,
    4846369599423283200i64, 4607182418800017408i64,
    0i64 - 4377002437431492608i64, 4611686018427387904i64];
  let offset: i32 = 0;
  while (offset < bits.len()) {
    let xs: f64[] = [];
    let n: i32 = 0;
    while (n <= 17) {
      if (!same(fused(xs, 1.0), scalar(xs, 1.0))) { return 1; }
      if (!same(fused(xs, 3.0), scalar(xs, 3.0))) { return 2; }
      xs = xs.append(f64_from_bits(bits[(offset + n) % bits.len()]));
      n = n + 1;
    }
    offset = offset + 1;
  }
  if (__rc_underflow_count() != 0) { return 3; }
  return 0;
}`
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, target, src, []string{"FERN_LEAKCHECK=1"})
			out, err := runScaleTarget(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("seeded reduction: %v\n%s", err, out)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(leakSummaryLine(string(out)), &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("seeded reduction ownership: %v\n%s", err, out)
			}
		})
	}
}
