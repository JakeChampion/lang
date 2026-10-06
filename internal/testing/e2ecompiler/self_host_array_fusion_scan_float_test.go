package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const arrayFusionScanFloatSrc = `import "std/array";
@noinline function product(a: f64, b: f64): f64 { return a * b; }
@noinline function subtract(a: f64, b: f64): f64 { return a - b; }
function check(xs: f64[], factor: f64, seed: f64): boolean {
  let out = xs.map((x: f64): f64 => x * factor)
    .scan(seed, (a: f64, x: f64): f64 => a - x);
  if (out.len() != xs.len()) { return false; }
  let acc = seed;
  let i: i32 = 0;
  while (i < xs.len()) {
    acc = subtract(acc, product(xs[i], factor));
    // Only arithmetic NaN payloads are unspecified by FS-04.
    if (acc != acc) {
      if (out[i] == out[i]) { return false; }
    } else if (f64_bits(out[i]) != f64_bits(acc)) { return false; }
    i = i + 1;
  }
  return true;
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
      if (!check(xs, 1.0, -0.0) || !check(xs, 3.0, f64_from_bits(bits[offset]))) { return 1; }
      let i: i32 = 0;
      while (i < n) {
        if (f64_bits(xs[i]) != bits[(offset + i) % bits.len()]) { return 2; }
        i = i + 1;
      }
      xs = xs.append(f64_from_bits(bits[(offset + n) % bits.len()]));
      n = n + 1;
    }
    offset = offset + 1;
  }
  if (!check([10000000000000000.0, 1.0, -10000000000000000.0, 2.0], 1.0, 0.0)) { return 3; }
  if (__rc_underflow_count() != 0) { return 4; }
  return 0;
}`

func TestSelfHostArrayFusionScanFloatOrder(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, target, arrayFusionScanFloatSrc, []string{"FERN_LEAKCHECK=1"})
			out, err := runScaleTarget(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("scan floating order: %v\n%s", err, out)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(leakSummaryLine(string(out)), &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("scan floating ownership: %v\n%s", err, out)
			}
		})
	}
}
