package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A once body takes a capture out of an environment that has one
// reference, so a write to it needs no copy. When the environment is
// shared, the capture gets a new reference and a write copies it.
const onceTakeSrc = `@noinline function bump(own xs: i32[], i: i32): i32[] {
  let at = i % xs.len();
  return xs.with(at, xs[at] + 1);
}
@noinline function step(own xs: i32[], n: i32): once () => i32[] {
  return once (): i32[] => bump(xs, n);
}
function unique_steps(): i32 {
  let xs: i32[] = [];
  for i in 0..64 {
    xs = xs.append(0);
  }
  let mark = __heap_alloc_count();
  for n in 0..128 {
    let k = step(xs, n);
    xs = k();
  }
  if (__heap_alloc_count() - mark != 128i64) { return 10; }
  for i in 0..64 {
    if (xs[i] != 2) { return 11; }
  }
  return 0;
}
function shared_steps(): i32 {
  let k = step([5, 6], 0);
  let k2 = k;
  let mark = __heap_alloc_count();
  let a = k();
  let b = k2();
  if (__heap_alloc_count() - mark != 1i64) { return 20; }
  if (a[0] != 6 || b[0] != 6 || a[1] != 6 || b[1] != 6) { return 21; }
  return 0;
}
function main(): i32 {
  let u = unique_steps();
  if (u != 0) { return u; }
  let s = shared_steps();
  if (s != 0) { return s; }
  if (__rc_underflow_count() != 0) { return 30; }
  return 0;
}`

func TestSelfHostOnceTakesUniqueEnvironment(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, target, onceTakeSrc, []string{"FERN_LEAKCHECK=1"})
			out, err := runScaleTarget(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("once take: %v\n%s", err, out)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(leakSummaryLine(string(out)), &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("once take ownership: %v\n%s", err, out)
			}
		})
	}
}
