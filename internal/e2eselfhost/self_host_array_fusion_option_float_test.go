package e2eselfhost

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// Copying a float into/out of an Option performs no arithmetic: even NaN
// payloads must retain their exact bits. This is stricter than an arithmetic
// NaN oracle and catches a zero seed leaking through the absent phi arm.
const scalarOptionFloatSrc = `import "std/array";
@noinline function first(xs: f64[]): i64 {
  let out = xs.map((x: f64): f64 => x).reduce((a: f64, b: f64): f64 => a);
  match (out) { Some(v) => { return f64_bits(v); }, None => { return 123i64; } }
}
@noinline function last(xs: f64[]): i64 {
  let out = xs.filter((x: f64): boolean => true).reduce((a: f64, b: f64): f64 => b);
  match (out) { None => { return 123i64; }, Some(v) => { return f64_bits(v); } }
}
function main(): i32 {
  let bits: i64[] = [0i64, 0i64 - 9223372036854775807i64 - 1i64,
    9218868437227405312i64, 0i64 - 4503599627370496i64,
    9221120237041090625i64, 9221120237041090626i64,
    9218868437227405313i64, 1i64, 0i64 - 9223372036854775807i64,
    4607182418800017408i64];
  let offset: i32 = 0;
  while (offset < bits.len()) {
    let xs: f64[] = [];
    let n: i32 = 0;
    while (n <= 17) {
      let a = 123i64;
      let b = 123i64;
      if (n > 0) { a = bits[offset]; b = bits[(offset + n - 1) % bits.len()]; }
      let mark = __heap_alloc_count();
      let first_bits = first(xs);
      let last_bits = last(xs);
      if (__heap_alloc_count() != mark) { return 1; }
      if (first_bits != a || last_bits != b) { return 2; }
      let i: i32 = 0;
      while (i < n) {
        if (f64_bits(xs[i]) != bits[(offset + i) % bits.len()]) { return 3; }
        i = i + 1;
      }
      xs = xs.append(f64_from_bits(bits[(offset + n) % bits.len()]));
      n = n + 1;
    }
    offset = offset + 1;
  }
  if (__rc_underflow_count() != 0) { return 4; }
  return 0;
}`

func TestSelfHostArrayFusionScalarOptionFloatBits(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, target, scalarOptionFloatSrc, []string{"FERN_LEAKCHECK=1"})
			out, err := runScaleTarget(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("scalar option float bits: %v\n%s", err, out)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(leakSummaryLine(string(out)), &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("scalar option float ownership: %v\n%s", err, out)
			}
		})
	}
}
