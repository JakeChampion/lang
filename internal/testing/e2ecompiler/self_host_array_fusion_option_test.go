package e2ecompiler

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The allocation window includes the whole call, while construction of the
// input and the independent scalar oracle stay outside it.
func scalarOptionSource() string {
	var source strings.Builder
	source.WriteString("import \"std/array\";\n")
	for i, ty := range []string{"i32", "i64", "u8", "f32", "f64"} {
		fmt.Fprintf(&source, `
@noinline function scalar_%d(xs: i32[]): i64 {
  let out = xs.map((x: i32): %s => (x + 1) as %s)
    .reduce((a: %s, b: %s): %s => a + b);
  match (out) { Some(v) => { return v as i64; }, None => { return -99i64; } }
}
`, i, ty, ty, ty, ty, ty)
	}
	source.WriteString(`
@noinline function flags(xs: i32[]): i64 {
  let out = xs.map((x: i32): boolean => x % 2 == 0)
    .reduce((a: boolean, b: boolean): boolean => a != b);
  match (out) {
    Some(v) => { if (v) { return 1i64; } return 0i64; },
    None => { return -99i64; }
  }
}
@noinline function selected(xs: i32[]): i64 {
  let out = xs.filter((x: i32): boolean => x % 2 != 0)
    .map((x: i32): i64 => x as i64)
    .reduce((a: i64, b: i64): i64 => a * 2i64 - b);
  match (out) { Some(v) => { return v; }, None => { return -99i64; } }
}
@noinline function rejected(xs: i32[]): i64 {
  let out = xs.filter((x: i32): boolean => x < 0).reduce((a: i32, b: i32): i32 => a + b);
  match (out) { Some(v) => { return v as i64; }, None => { return -99i64; } }
}
// A copy, a branch phi and a loop phi all join the same private component.
// Repeated tests/reads must use the same scalar payload without taking it.
@noinline function carried(xs: i32[], pick: boolean): i64 {
  let out = xs.map((x: i32): i64 => x as i64 + 1i64).reduce((a: i64, b: i64): i64 => a + b);
  let saved = out;
  if (pick) { out = None; }
  let i: i32 = 0;
  while (i < 5) {
    match (out) { Some(v) => { out = Some(v + 1i64); }, None => { out = Some(10i64); } }
    i = i + 1;
  }
  let total = 0i64;
  match (saved) { Some(v) => { total = v; }, None => {} }
  match (out) { Some(v) => { total = total + v; }, None => { return -999i64; } }
  match (out) { Some(v) => { total = total + v; }, None => { return -998i64; } }
  return total;
}
@noinline function escaping(xs: i32[]): Option[i64] {
  return xs.map((x: i32): i64 => x as i64 + 1i64).reduce((a: i64, b: i64): i64 => a + b);
}
@noinline function take_box(out: Option[i64]): i64 {
  match (out) { Some(v) => { return v; }, None => { return -99i64; } }
}
@noinline function called(xs: i32[]): i64 {
  let out = xs.map((x: i32): i64 => x as i64 + 1i64).reduce((a: i64, b: i64): i64 => a + b);
  return take_box(out);
}
@noinline function external(xs: i32[], seed: Option[i64], use_seed: boolean): i64 {
  let out = xs.map((x: i32): i64 => x as i64 + 1i64).reduce((a: i64, b: i64): i64 => a + b);
  if (use_seed) { out = seed; }
  match (out) { Some(v) => { return v; }, None => { return -99i64; } }
}
function check(xs: i32[], count: boolean): i32 {
  let n = xs.len();
  let sum = (n as i64 * (n as i64 + 1i64)) / 2i64;
  let want = sum;
  if (n == 0) { want = -99i64; }
  let mark = 0i64;
  let got = 0i64;
`)
	for i := range 5 {
		fmt.Fprintf(&source, `
  mark = __heap_alloc_count(); got = scalar_%d(xs);
  if (count && __heap_alloc_count() != mark) { return %d; }
  if (got != want) { return %d; }
`, i, 10+i, 20+i)
	}
	source.WriteString(`
  mark = __heap_alloc_count(); got = flags(xs);
  if (count && __heap_alloc_count() != mark) { return 30; }
  let flag_want = ((n + 1) / 2) % 2 as i64;
  if (n == 0) { flag_want = -99i64; }
  if (got != flag_want) { return 31; }
  let picked = -99i64;
  let i: i32 = 1;
  while (i < n) { if (i == 1) { picked = 1i64; } else { picked = picked * 2i64 - i as i64; } i = i + 2; }
  mark = __heap_alloc_count(); got = selected(xs);
  if (count && __heap_alloc_count() != mark) { return 32; }
  if (got != picked) { return 33; }
  mark = __heap_alloc_count(); got = rejected(xs);
  if (count && __heap_alloc_count() != mark || got != -99i64) { return 34; }
  for pick in [false, true] {
    let next = sum + 5i64;
    if (pick || n == 0) { next = 14i64; }
    mark = __heap_alloc_count(); got = carried(xs, pick);
    if (count && __heap_alloc_count() != mark) { return 35; }
    if (got != sum + next * 2i64) { return 36; }
  }
  let boxes = 0i64;
  if (n > 0) { boxes = 1i64; }
  mark = __heap_alloc_count(); let box = escaping(xs);
  if (count && __heap_alloc_count() - mark != boxes || take_box(box) != want) { return 40; }
  mark = __heap_alloc_count(); got = called(xs);
  if (count && __heap_alloc_count() - mark != boxes || got != want) { return 41; }
  let seed = Some(71i64);
  mark = __heap_alloc_count(); got = external(xs, seed, false);
  if (count && __heap_alloc_count() - mark != boxes || got != want) { return 42; }
  if (external(xs, seed, true) != 71i64 || take_box(seed) != 71i64) { return 43; }
  i = 0;
  while (i < n) { if (xs[i] != i) { return 44; } i = i + 1; }
  return 0;
}
function main(): i32 {
  let xs: i32[] = [];
  let n: i32 = 0;
  while (n <= 17) {
    let result = check(xs, true);
    if (result != 0) { return result; }
    xs = xs.append(n); n = n + 1;
  }
  if (__rc_underflow_count() != 0) { return 90; }
  return 0;
}
`)
	return source.String()
}

func TestSelfHostArrayFusionScalarOptions(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			for _, disabled := range []string{"0", "1"} {
				t.Run("disabled="+disabled, func(t *testing.T) {
					src := scalarOptionSource()
					if disabled == "1" {
						src = strings.Replace(src, "check(xs, true)", "check(xs, false)", 1)
					}
					bin := e2eharness.CompileSelfHostSource(t, target, src,
						[]string{"FERN_NO_ARRAY_FUSION=" + disabled, "FERN_LEAKCHECK=1"})
					out, err := runScaleTarget(t, target, bin).CombinedOutput()
					if err != nil {
						t.Fatalf("scalar options: %v\n%s", err, out)
					}
					var allocs, frees, live int64
					if _, err := fmtSscan(leakSummaryLine(string(out)), &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
						t.Fatalf("scalar option ownership: %v\n%s", err, out)
					}
				})
			}
		})
	}
}
