package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// The first kernel an std/ndarray shape lowers to (#9735): `a.map(x => x * k)`
// over a PACKED handle becomes `from_flat(__fern_scale_f64(a.data, k),
// a.shape)`, gated on #9734's layout analysis.
//
// What this holds is the gate, not the speed. A packed receiver's `data` IS
// the reading order, so the kernel reads the elements `map` would have
// walked; a STRIDED one's is not, and the same rewrite over a transpose
// produces different numbers rather than slower ones. Case 30 is that
// case, and it is the reason the layout has to be proved rather than
// assumed: scaling a transpose's storage in order and keeping its shape
// reads `[[10,20],[30,40]]` where the answer is `[[10,30],[20,40]]`.
const ndarrayScaleSrc = `import "std/ndarray";

function main(): i32 {
  // Packed, captured factor: the kernel path.
  var k: f64 = 2.5;
  var a: ndarray.NdArray[f64] = ndarray.from_flat([1.0, 2.0, 3.0, 4.0, 5.0, 6.0], [2, 3]);
  var m: ndarray.NdArray[f64] = a.map((x: f64): f64 => x * k);
  if (m.shape()[0] != 2 || m.shape()[1] != 3) { return 10; }
  if (m.get([0, 0]) != 2.5) { return 11; }
  if (m.get([1, 2]) != 15.0) { return 12; }
  if (!m.is_packed()) { return 13; }

  // Packed, literal factor.
  var b: ndarray.NdArray[f64] = ndarray.from_flat([1.0, 2.0], [2]);
  var c: ndarray.NdArray[f64] = b.map((x: f64): f64 => x * 3.0);
  if (c.get([0]) != 3.0 || c.get([1]) != 6.0) { return 20; }

  // STRIDED receiver. The transpose of [[1,2],[3,4]] reads [[1,3],[2,4]],
  // so scaling by 10 reads [[10,30],[20,40]]. Taking the kernel here would
  // scale the storage in order and keep the shape, reading [[10,20],[30,40]]
  // — so these two indices are what a wrong gate fails on.
  var t: ndarray.NdArray[f64] = ndarray.from_flat([1.0, 2.0, 3.0, 4.0], [2, 2]).transpose();
  var s: ndarray.NdArray[f64] = t.map((x: f64): f64 => x * 10.0);
  if (s.get([0, 1]) != 30.0) { return 30; }
  if (s.get([1, 0]) != 20.0) { return 31; }

  // A handle from a helper is packed too, through #9950's return summary,
  // so the kernel reaches the spelling a program actually uses.
  var h: ndarray.NdArray[f64] = grid().map((x: f64): f64 => x * 2.0);
  if (h.get([0, 1]) != 4.0) { return 40; }
  if (h.get([1, 1]) != 12.0) { return 41; }

  // Every result read once more at the end, so nothing above is released
  // early and measured as free.
  if (m.get([0, 1]) != 5.0 || c.get([1]) != 6.0 || s.get([0, 0]) != 10.0) { return 50; }
  return 0;
}

function grid(): ndarray.NdArray[f64] {
  return ndarray.from_flat([1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0], [2, 4]);
}
`

func TestX86_64NdarrayScaleKernelMatchesTheScalarWalk(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, ndarrayScaleSrc); code != 0 {
		t.Errorf("ndarray scale on x86-64: got %d, want 0", code)
	}
}

func TestArm64NdarrayScaleKernelMatchesTheScalarWalk(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, ndarrayScaleSrc); code != 0 {
		t.Errorf("ndarray scale on arm64: got %d, want 0", code)
	}
}

func TestWASMNdarrayScaleKernelMatchesTheScalarWalk(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, ndarrayScaleSrc); got != 0 {
		t.Errorf("ndarray scale on wasm: got %d, want 0", got)
	}
}

// The interpreter never sees the rewrite — it is an IR pass — so this leg is
// the oracle the three compiled ones are differenced against.
func TestInterpNdarrayScaleValuesCorrect(t *testing.T) {
	if got := runInterpExit(t, ndarrayScaleSrc); got != 0 {
		t.Errorf("ndarray scale on interp: got %d, want 0", got)
	}
}
