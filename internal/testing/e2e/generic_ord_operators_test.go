package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// The comparison operators on a value of a bounded type variable mean the
// bound's method in every instance (#12034): `<` on `T: Ord` is `a.cmp(b) < 0`
// and `==` on `T: Eq` is `a.eq(b)`. For an f64 instance that is cmp's total
// order, NaN above every number, and eq's NaN equal to itself, not the IEEE
// operators. The core/cmp, core/iter and std/array helpers write their
// comparisons with the operators, so they are held to the same order. NaN
// prints as "nan": 0.0 / 0.0 is a negative NaN on x86-64 and a positive one on
// arm64.
const genericOrdOperatorsSrc = `import "core/cmp" as cmp;
import "core/iter" as iter;
import "std/array";
import "std/f64";
import "std/i64";

function above[T: cmp.Ord](a: T, b: T): boolean { return a > b; }
function same[T: cmp.Eq](a: T, b: T): boolean { return a == b; }
function differ[T: cmp.Eq](a: T, b: T): boolean { return a != b; }

struct Box[T] { v: T }
function (bx: Box[T]) wider[U: cmp.Ord](a: U, c: U): boolean { return a >= c; }

function flag(x: boolean): string { if (x) { return "T"; } return "F"; }
function bits(x: f64): string { if (x != x) { return "nan"; } return f64_bits(x).to_string(); }

function main(): i32 {
  let nan: f64 = 0.0 / 0.0;
  let xs: f64[] = [1.0, nan, -2.0, 3.5];
  let bx: Box[i32] = Box { v: 0 };
  print(flag(above(nan, 1.0)) + flag(above(1.0, nan)) + flag(above(3, 2)) + flag(above("b", "a")));
  print(flag(same(nan, nan)) + flag(differ(nan, nan)) + flag(same(2, 2)) + flag(bx.wider(nan, 1.0)));
  print(bits(cmp.min(nan, 1.0)) + " " + bits(cmp.max(nan, 1.0)) + " " + bits(cmp.clamp(nan, 0.0, 2.0)));
  print(flag(cmp.lt(1.0, nan)) + flag(cmp.lte(nan, nan)) + flag(cmp.gt(nan, 1.0)) + flag(cmp.gte(1.0, nan)));
  match (cmp.max_of(xs)) { Some(v) => { print(bits(v)); }, None => { print("none"); } }
  match (iter.max(iter.of(xs))) { Some(v) => { print(bits(v)); }, None => { print("none"); } }
  match (iter.min(iter.of(xs))) { Some(v) => { print(bits(v)); }, None => { print("none"); } }
  let rm: f64[] = array.running_max(xs);
  print(bits(rm[3]));
  let ints: i32[] = [4, 9, -1, 7];
  match (iter.max(iter.of(ints))) { Some(v) => { print(v.to_string()); }, None => { print("none"); } }
  return 0;
}
`

const genericOrdOperatorsWant = `TFTT
TFTT
4607182418800017408 nan 4611686018427387904
TTTF
nan
nan
-4611686018427387904
nan
9
`

func TestInterpGenericOrdOperators(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(genericOrdOperatorsSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != genericOrdOperatorsWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, genericOrdOperatorsWant, errOut)
	}
}

func TestX86_64GenericOrdOperators(t *testing.T) {
	out, code := compileAndRunX86_64(t, genericOrdOperatorsSrc)
	if code != 0 || out != genericOrdOperatorsWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, genericOrdOperatorsWant)
	}
}

func TestArm64GenericOrdOperators(t *testing.T) {
	out, code := compileAndRunArm64(t, genericOrdOperatorsSrc)
	if code != 0 || out != genericOrdOperatorsWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, genericOrdOperatorsWant)
	}
}

func TestWasmGenericOrdOperators(t *testing.T) {
	if out := compileAndRunWasmCapture(t, genericOrdOperatorsSrc); out != genericOrdOperatorsWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, genericOrdOperatorsWant)
	}
}
