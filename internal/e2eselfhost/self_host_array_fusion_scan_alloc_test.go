package e2eselfhost

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// Count just the pipeline call, excluding input construction and verification.
// Cross byte-capacity boundaries and exercise each physical element layout.
const arrayFusionScanAllocationSrc = `import "std/array";
@noinline function narrow(xs: i32[]): i32[] {
  return xs.map((x: i32): i32 => x + 1).scan(0, (a: i32, x: i32): i32 => a + x);
}
@noinline function wide(xs: i32[]): i64[] {
  return xs.map((x: i32): i64 => x as i64 + 1i64).scan(0i64, (a: i64, x: i64): i64 => a + x);
}
@noinline function real(xs: i32[]): f64[] {
  return xs.map((x: i32): f64 => x as f64 + 1.0).scan(0.0, (a: f64, x: f64): f64 => a + x);
}
@noinline function real32(xs: i32[]): f32[] {
  return xs.map((x: i32): f32 => x as f32 + 1.0).scan(0.0 as f32, (a: f32, x: f32): f32 => a + x);
}
@noinline function byte_scan(xs: i32[]): u8[] {
  return xs.map((x: i32): u8 => x as u8).scan(0 as u8, (a: u8, x: u8): u8 => a ^ x);
}
@noinline function flags(xs: i32[]): boolean[] {
  return xs.map((x: i32): boolean => x % 2 == 0).scan(false, (a: boolean, x: boolean): boolean => a != x);
}
@noinline function selected(xs: i32[]): i32[] {
  return xs.filter((x: i32): boolean => x % 2 == 0).scan(0, (a: i32, x: i32): i32 => a + x);
}
@noinline function rejected(xs: i32[]): i32[] {
  return xs.filter((x: i32): boolean => x < 0).scan(0, (a: i32, x: i32): i32 => a + x);
}
function check(xs: i32[]): i32 {
  let mark = __heap_alloc_count();
  let a = narrow(xs);
  if (__heap_alloc_count() - mark != 1i64) { return 10; }
  mark = __heap_alloc_count();
  let b = wide(xs);
  if (__heap_alloc_count() - mark != 1i64) { return 11; }
  mark = __heap_alloc_count();
  let c = real(xs);
  if (__heap_alloc_count() - mark != 1i64) { return 12; }
  mark = __heap_alloc_count();
  let cf = real32(xs);
  if (__heap_alloc_count() - mark != 1i64) { return 23; }
  mark = __heap_alloc_count();
  let d = byte_scan(xs);
  if (__heap_alloc_count() - mark != 1i64) { return 13; }
  mark = __heap_alloc_count();
  let e = flags(xs);
  if (__heap_alloc_count() - mark != 1i64) { return 14; }
  mark = __heap_alloc_count();
  let none = rejected(xs);
  if (__heap_alloc_count() - mark != 1i64 || none.len() != 0) { return 15; }
  mark = __heap_alloc_count();
  let some = selected(xs);
  if (__heap_alloc_count() - mark != 1i64) { return 16; }
  if (a.len() != xs.len() || b.len() != xs.len() || c.len() != xs.len()
    || cf.len() != xs.len() || d.len() != xs.len() || e.len() != xs.len()) { return 17; }
  let total: i32 = 0;
  let byte_total: u8 = 0 as u8;
  let flag: boolean = false;
  let select_total: i32 = 0;
  let j: i32 = 0;
  let i: i32 = 0;
  while (i < xs.len()) {
    if (xs[i] != i) { return 18; }
    total = total + i + 1;
    byte_total = byte_total ^ (i as u8);
    flag = flag != (i % 2 == 0);
    if (a[i] != total || b[i] != total as i64 || c[i] != total as f64
      || cf[i] != total as f32 || d[i] != byte_total || e[i] != flag) { return 19; }
    if (i % 2 == 0) {
      select_total = select_total + i;
      if (j >= some.len() || some[j] != select_total) { return 20; }
      j = j + 1;
    }
    i = i + 1;
  }
  if (some.len() != j) { return 21; }
  return 0;
}
function main(): i32 {
  for n in [0, 1, 2, 7, 8, 9, 16, 17, 33, 257] {
    let xs: i32[] = [];
    let i: i32 = 0;
    while (i < n) { xs = xs.append(i); i = i + 1; }
    let result = check(xs);
    if (result != 0) { return result; }
  }
  if (__rc_underflow_count() != 0) { return 22; }
  return 0;
}`

func TestSelfHostArrayFusionScanAllocation(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, target, arrayFusionScanAllocationSrc, []string{"FERN_LEAKCHECK=1"})
			out, err := runScaleTarget(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("scan capacity and layout: %v\n%s", err, out)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(leakSummaryLine(string(out)), &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("scan allocation ownership: %v\n%s", err, out)
			}
		})
	}
}
