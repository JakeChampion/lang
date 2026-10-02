package e2e

import "testing"

// A local handed to an `own` parameter is moved there when it is stored to
// again after straight-line statements that do not name it (#11093). threaded
// hands two buffers through a helper in a loop: xs is stored back on the next
// line, ys only after xs's store. gap reads an unrelated local between the
// move and the store. Every move must free each value exactly once.
const ownStoreAfterGapSrc = `
struct Pair { a: i64[], b: i64[] }
@noinline function step(own xs: i64[], own ys: i64[], k: i64): Pair {
  return Pair { a: xs.append(k), b: ys.append(k * 2) };
}
@noinline function take(own xs: i64[]): i32 { return xs.len() as i32; }
function threaded(n: i32): i32 {
  var xs: i64[] = [];
  var ys: i64[] = [1];
  var i: i32 = 0;
  while (i < n) {
    var p: Pair = step(xs, ys, i as i64);
    xs = p.a;
    ys = p.b;
    i = i + 1;
  }
  return xs.len() as i32 + ys.len() as i32;
}
function gap(): i32 {
  var xs: i64[] = [1, 2];
  var r: i32 = take(xs);
  var m: i32 = r + 1;
  xs = [3];
  return m + xs.len() as i32;
}
function main(): i32 {
  return threaded(5) + gap();
}
`

func TestOwnStoreAfterGapX86_64Sanitized(t *testing.T) {
	checkSanitizedBalanced(t, ownStoreAfterGapSrc, 15, runSanitizeX86_64)
}

func TestOwnStoreAfterGapArm64Sanitized(t *testing.T) {
	checkSanitizedBalanced(t, ownStoreAfterGapSrc, 15, runSanitizeArm64)
}

func TestOwnStoreAfterGapWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, ownStoreAfterGapSrc); got != 15 {
		t.Fatalf("wasm exited %d, want 15", got)
	}
}
