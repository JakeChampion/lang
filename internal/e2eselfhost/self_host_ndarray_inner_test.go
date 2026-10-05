package e2eselfhost

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// Public reading-order copies form an independent address oracle. Effects
// pin row/column/contraction order and the mul-then-add sequence at every step.
const ndarrayInnerSrc = `import "std/ndarray";
import "std/i64";
function check(a: ndarray.NdArray[f64], b: ndarray.NdArray[f64]): boolean {
  let xs = a.to_flat();
  let ys = b.to_flat();
  let extent = a.shape()[a.rank() - 1];
  let rows: i32 = 1;
  let columns: i32 = 1;
  let d: i32 = 0;
  while (d + 1 < a.rank()) { rows = rows * a.shape()[d]; d = d + 1; }
  d = 1;
  while (d < b.rank()) { columns = columns * b.shape()[d]; d = d + 1; }
  let calls: i32 = 0;
  let wrong: boolean = false;
  let out = a.inner(b, 0.25, (x: f64, y: f64): f64 => {
    calls = calls + 1;
    if (calls % 2 != 1) { wrong = true; }
    return x * 1000.0 + y + calls as f64;
  }, (acc: f64, product: f64): f64 => {
    calls = calls + 1;
    if (calls % 2 != 0) { wrong = true; }
    return acc * 0.5 + product + calls as f64;
  });
  if (wrong || calls != rows * columns * extent * 2 || !out.is_packed() || out.rank() != a.rank() + b.rank() - 2) { return false; }
  d = 0;
  while (d + 1 < a.rank()) {
    if (out.shape()[d] != a.shape()[d]) { return false; }
    d = d + 1;
  }
  d = 1;
  while (d < b.rank()) {
    if (out.shape()[a.rank() - 2 + d] != b.shape()[d]) { return false; }
    d = d + 1;
  }
  let values = out.to_flat();
  let i: i32 = 0;
  while (i < rows) {
    let j: i32 = 0;
    while (j < columns) {
      let acc: f64 = 0.25;
      let k: i32 = 0;
      while (k < extent) {
        let step: i32 = 2 * ((i * columns + j) * extent + k);
        let product = xs[i * extent + k] * 1000.0 + ys[k * columns + j] + (step + 1) as f64;
        acc = acc * 0.5 + product + (step + 2) as f64;
        k = k + 1;
      }
      if (f64_bits(values[i * columns + j]) != f64_bits(acc)) { return false; }
      j = j + 1;
    }
    i = i + 1;
  }
  let after_a = a.to_flat();
  let after_b = b.to_flat();
  i = 0;
  while (i < xs.len()) { if (xs[i] != after_a[i]) { return false; } i = i + 1; }
  i = 0;
  while (i < ys.len()) { if (ys[i] != after_b[i]) { return false; } i = i + 1; }
  return true;
}
function main(): i32 {
  let a = ndarray.from_flat([1.0, 2.0, 3.0, 4.0, 5.0, 6.0], [2, 3]);
  let b = ndarray.from_flat([7.0, 8.0, 9.0, 10.0, 11.0, 12.0], [3, 2]);
  if (!check(a, b) || !check(a.reshape([1, 2, 3]), b.reshape([3, 1, 2]))) { return 1; }
  if (!check(a, a.transpose()) || !check(b.transpose(), b)) { return 2; }
  if (!check(a.reverse(1), b) || !check(a, b.reverse(0))) { return 3; }
  if (!check(a.reshape([1, 2, 3]).reverse(0), b) || !check(a, b.reshape([3, 1, 2]).reverse(1))) { return 4; }
  let vector = ndarray.from_flat([2.0, 3.0, 4.0], [3]);
  if (!check(a, vector) || !check(vector, b) || !check(vector, vector)) { return 5; }
  let square = ndarray.from_flat([1.0, 2.0, 3.0, 4.0], [2, 2]);
  if (!check(square, square)) { return 6; }
  let one = ndarray.from_flat([2.0], [1, 1]);
  if (!check(one.reverse(1), one.reverse(0))) { return 7; }
  let row = ndarray.from_flat([2.0, 3.0, 4.0], [1, 3]);
  if (!check(row.broadcast_to([2, 3]), b) || !check(a, row.reshape([3, 1]).broadcast_to([3, 2]))) { return 8; }
  let empty_left = ndarray.from_flat([] as f64[], [2, 0]);
  let empty_right = ndarray.from_flat([] as f64[], [0, 3]);
  if (!check(empty_left, empty_right)) { return 9; }
  if (!check(ndarray.from_flat([] as f64[], [0, 3]), b) || !check(a, ndarray.from_flat([] as f64[], [3, 0]))) { return 10; }
  // No output exists, so large unused products must not introduce new errors.
  let huge = ndarray.from_flat([] as f64[], [0, 2147483647, 2147483647, 0]);
  if (huge.inner(empty_right, 0.25, (x: f64, y: f64): f64 => x * y, (x: f64, y: f64): f64 => x + y).len() != 0) { return 11; }
  let ints = ndarray.from_flat([1i64, 2i64], [2]);
  let names = ints.inner(ints, "start", (x: i64, y: i64): string => (x * y).to_string(), (x: string, y: string): string => x + ":" + y);
  if (names.rank() != 0 || names.get([]) != "start:1:4" || ints.get([1]) != 2i64) { return 12; }
  return 0;
}`

func TestSelfHostNdarrayPackedInner(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			if out, code := runSelfHostFusionProgram(t, target, ndarrayInnerSrc); code != 0 {
				t.Fatalf("packed inner: exit %d\n%s", code, out)
			}
		})
	}
	t.Run("interpreters", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(src, []byte(ndarrayInnerSrc), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, compiler := range []string{buildLangBinForInterp(t), e2eharness.SelfHostCLI(t)} {
			cmd := exec.Command(compiler, "-interp", src, e2eharness.SelfHostStdlibRoot(t))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", compiler, err, out)
			}
		}
	})
}

func TestSelfHostNdarrayInnerBenchmark(t *testing.T) {
	src := filepath.Join(repoRootFromTest(t), "examples", "array_pipeline", "ndarray_inner.fern")
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostFile(t, target, src, nil)
			for _, mode := range []string{"0", "1", "2"} {
				out, err := runScaleTarget(t, target, bin, "3", "5", "7", "2", mode).CombinedOutput()
				if err != nil {
					t.Fatalf("inner benchmark mode=%s: %v\n%s", mode, err, out)
				}
				var report struct {
					Rows    int `json:"rows"`
					Extent  int `json:"extent"`
					Columns int `json:"columns"`
					Rounds  int `json:"rounds"`
				}
				if err := json.Unmarshal(out, &report); err != nil || report.Rows != 3 || report.Extent != 5 || report.Columns != 7 || report.Rounds != 2 {
					t.Fatalf("incorrect benchmark report: %v\n%s", err, out)
				}
			}
		})
	}
}

const ndarrayInnerBitsSrc = `import "std/ndarray";
@noinline function value(i: i32): f64 {
  let bits: i64[] = [0i64, 0i64 - 9223372036854775807i64 - 1i64,
    9218868437227405312i64, 0i64 - 4503599627370496i64,
    9221120237041090625i64, 9221120237041090626i64,
    9218868437227405313i64, 1i64, 0i64 - 9223372036854775807i64,
    4846369599423283200i64, 4607182418800017408i64,
    0i64 - 4377002437431492608i64, 4611686018427387904i64];
  return f64_from_bits(bits[i % bits.len()]);
}
@noinline function mul(x: f64, y: f64): f64 { return x * y; }
@noinline function add(x: f64, y: f64): f64 { return x + y; }
function build(n: i32, offset: i32): f64[] {
  let xs: f64[] = [];
  let i: i32 = 0;
  while (i < n) { xs = xs.append(value(i + offset)); i = i + 1; }
  return xs;
}
function check(xs: f64[], ys: f64[], rows: i32, extent: i32, columns: i32, init: f64): boolean {
  let a = ndarray.from_flat(xs, [rows, extent]);
  let b = ndarray.from_flat(ys, [extent, columns]);
  let out = a.inner(b, init, (x: f64, y: f64): f64 => x * y, (x: f64, y: f64): f64 => x + y);
  let data = out.to_flat();
  if (!out.is_packed() || out.shape()[0] != rows || out.shape()[1] != columns || data.len() != rows * columns) { return false; }
  let i: i32 = 0;
  while (i < rows) {
    let j: i32 = 0;
    while (j < columns) {
      let acc = init;
      let k: i32 = 0;
      while (k < extent) { acc = add(acc, mul(xs[i * extent + k], ys[k * columns + j])); k = k + 1; }
      if (f64_bits(data[i * columns + j]) != f64_bits(acc)) { return false; }
      j = j + 1;
    }
    i = i + 1;
  }
  return true;
}
function main(): i32 {
  let n: i32 = 0;
  while (n < 18) {
    let xs = build(2 * n, 0);
    let ys = build(n * 5, 2);
    if (!check(xs, ys, 2, n, 5, value(n))) { return 1; }
    let i: i32 = 0;
    while (i < xs.len()) { if (f64_bits(xs[i]) != f64_bits(value(i))) { return 2; } i = i + 1; }
    i = 0;
    while (i < ys.len()) { if (f64_bits(ys[i]) != f64_bits(value(i + 2))) { return 3; } i = i + 1; }
    n = n + 1;
  }
  // A reassociated sum can retain the one that index-order evaluation loses.
  if (!check([10000000000000000.0, 1.0, -10000000000000000.0, 2.0], [1.0, 1.0, 1.0, 1.0], 1, 4, 1, 0.0)) { return 4; }
  let shared = build(4, 3);
  if (!check(shared, shared, 2, 2, 2, -0.0)) { return 5; }
  if (__rc_underflow_count() != 0) { return 6; }
  return 0;
}`

func TestSelfHostNdarrayInnerBits(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, target, ndarrayInnerBitsSrc, []string{"FERN_LEAKCHECK=1"})
			out, err := runScaleTarget(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("inner exact bits: %v\n%s", err, out)
			}
			var allocs, frees, live int64
			line := leakSummaryLine(string(out))
			if _, err := fmtSscan(line, &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("inner ownership: %v\n%s", err, out)
			}
		})
	}
}
