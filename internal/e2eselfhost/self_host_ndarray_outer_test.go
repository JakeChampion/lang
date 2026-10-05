package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The callback records its position so changing traversal order cannot pass
// merely by producing the same unordered set of products. Strided cases make
// both packed guards necessary; extent-one reversals exercise packed handles
// whose visible strides are not canonical.
const ndarrayOuterSrc = `import "std/ndarray";
import "std/i64";
function check(a: ndarray.NdArray[f64], b: ndarray.NdArray[f64]): boolean {
  let xs: f64[] = a.to_flat();
  let ys: f64[] = b.to_flat();
  let calls: i32 = 0;
  let out: ndarray.NdArray[f64] = a.outer(b, (x: f64, y: f64): f64 => {
    calls = calls + 1;
    return x * 1000.0 + y + calls as f64;
  });
  if (calls != xs.len() * ys.len() || !out.is_packed() || out.rank() != a.rank() + b.rank()) { return false; }
  let axis: i32 = 0;
  while (axis < a.rank()) {
    if (out.shape()[axis] != a.shape()[axis]) { return false; }
    axis = axis + 1;
  }
  axis = 0;
  while (axis < b.rank()) {
    if (out.shape()[axis + a.rank()] != b.shape()[axis]) { return false; }
    axis = axis + 1;
  }
  let data: f64[] = out.to_flat();
  let after_a: f64[] = a.to_flat();
  let after_b: f64[] = b.to_flat();
  let i: i32 = 0;
  while (i < xs.len()) {
    if (xs[i] != after_a[i]) { return false; }
    let j: i32 = 0;
    while (j < ys.len()) {
      if (ys[j] != after_b[j] || data[i * ys.len() + j] != xs[i] * 1000.0 + ys[j] + (i * ys.len() + j + 1) as f64) { return false; }
      j = j + 1;
    }
    i = i + 1;
  }
  return true;
}
function main(): i32 {
  let a: ndarray.NdArray[f64] = ndarray.from_flat([1.0, 2.0, 3.0, 4.0, 5.0, 6.0], [2, 3]);
  let b: ndarray.NdArray[f64] = ndarray.from_flat([7.0, 8.0, 9.0, 10.0], [2, 2]);
  if (!check(a, b)) { return 1; }
  if (!check(a.transpose(), b) || !check(a, b.transpose())) { return 2; }
  if (!check(a.reverse(1), b) || !check(a, b.reverse(0))) { return 3; }
  let row: ndarray.NdArray[f64] = ndarray.from_flat([11.0, 12.0, 13.0], [1, 3]);
  if (!check(row.broadcast_to([2, 3]), b) || !check(a, row.broadcast_to([2, 3]))) { return 4; }
  if (!row.reverse(0).is_packed() || !check(row.reverse(0), b)) { return 5; }
  if (!check(a, row.reverse(0)) || !check(a.broadcast_to([1, 2, 3]), row)) { return 6; }
  let scalar: ndarray.NdArray[f64] = ndarray.from_flat([14.0], []);
  if (!check(scalar, b) || !check(a, scalar) || !check(scalar, scalar)) { return 7; }
  let empty: ndarray.NdArray[f64] = ndarray.from_flat([], [2, 0, 3]);
  if (!check(empty, b) || !check(a, empty) || !check(empty, empty)) { return 8; }
  let canonical: ndarray.NdArray[f64] = row.reverse(0).outer(scalar, (x: f64, y: f64): f64 => x + y);
  if (canonical.strides()[0] != 3 || canonical.strides()[1] != 1 || row.reverse(0).strides()[0] != -3) { return 9; }
  let ints: ndarray.NdArray[i64] = ndarray.from_flat([1i64, 2i64], [2]);
  let names: ndarray.NdArray[string] = ndarray.from_flat(["a", "b", "c"], [3]);
  let labels: ndarray.NdArray[string] = ints.outer(names, (x: i64, y: string): string => x.to_string() + y);
  let flat: string[] = labels.to_flat();
  if (flat.len() != 6 || flat[0] != "1a" || flat[1] != "1b" || flat[2] != "1c" || flat[3] != "2a" || flat[4] != "2b" || flat[5] != "2c") { return 10; }
  if (names.get([1]) != "b" || ints.get([1]) != 2i64 || a.get([1, 2]) != 6.0 || b.get([1, 1]) != 10.0) { return 11; }
  return 0;
}
`

func TestSelfHostNdarrayPackedOuter(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			if out, code := runSelfHostFusionProgram(t, target, ndarrayOuterSrc); code != 0 {
				t.Fatalf("packed outer: exit %d\n%s", code, out)
			}
		})
	}
	t.Run("interpreters", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(src, []byte(ndarrayOuterSrc), 0o644); err != nil {
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
