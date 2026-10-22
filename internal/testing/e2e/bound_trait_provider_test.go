package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// A method called on a value of a bounded type variable is the bound's method
// in every instance (#12277). When two traits each provide `scale` for P, or
// a user trait and std/num both provide `add` for i32, only the first claimant
// keeps the plain symbol; a generic bounded by the other trait must reach the
// renamed provider (`P.B.scale`, `i32.__entry.add`). Each line reaches both
// providers, through a free generic, a supertrait, a lambda, a generic
// struct's and a generic enum's bounded impl. The last line is the bound's
// comparison, `>` meaning cmp's order with NaN above every number, through a
// bounded impl and through a match payload of the variable's type.
const boundTraitProviderSrc = `import "core/cmp" as cmp;
import "std/f64";
import "std/num";

struct P { x: i32 }

trait A { function scale(self: Self): i32; }
trait B { function scale(self: Self): i32; }
trait C: B { function tag(self: Self): i32; }

impl A for P { function scale(self: Self): i32 { return self.x; } }
impl B for P { function scale(self: Self): i32 { return 100; } }
impl C for P { function tag(self: Self): i32 { return 1000; } }

trait MyAdd { function add(self: Self, o: Self): Self; }
impl MyAdd for i32 { function add(self: Self, o: Self): Self { return self * 10 + o; } }

function via_a[T: A](v: T): i32 { return v.scale(); }
function via_b[T: B](v: T): i32 { return v.scale(); }
function via_c[T: C](v: T): i32 { return v.scale() + v.tag(); }
function via_b_lambda[T: B](vs: T[]): i32 {
  let f = (w: T): i32 => w.scale();
  return f(vs[0]) + vs[1].scale();
}

function mine[T: MyAdd](a: T, b: T): T { return a.add(b); }
function theirs[T: num.Add](a: T, b: T): T { return a.add(b); }

trait Show { function show(self: Self): i32; }

struct Q[T] { v: T }
impl[T: B] Show for Q[T] { function show(self: Self): i32 { return self.v.scale(); } }

enum E[T] { One(T), Zero }
impl[T: B] Show for E[T] {
  function show(self: Self): i32 {
    match (self) {
      One(v) => { return v.scale(); },
      Zero => { return 0; }
    }
  }
}

struct Pair[T] { a: T, b: T }
impl[T: cmp.Ord] Show for Pair[T] {
  function show(self: Self): i32 {
    if (self.a > self.b) {
      return 1;
    }
    return 0;
  }
}

function above_some[T: cmp.Ord](o: Option[T], p: Option[T]): i32 {
  match (o) {
    Some(a) => {
      match (p) {
        Some(c) => {
          if (a > c) {
            return 1;
          }
          return 0;
        },
        None => { return 0 - 1; }
      }
    },
    None => { return 0 - 1; }
  }
}

function main(): i32 {
  let v: P = P { x: 5 };
  print(via_a(v).to_string() + " " + via_b(v).to_string() + " " + via_c(v).to_string() + " " + via_b_lambda([v, v]).to_string());
  print(mine(3, 4).to_string() + " " + theirs(3, 4).to_string());
  let q: Q[P] = Q { v: v };
  let e: E[P] = E.One(v);
  print(q.show().to_string() + " " + e.show().to_string());
  let nan: f64 = 0.0 / 0.0;
  let pr: Pair[f64] = Pair { a: nan, b: 1.0 };
  print(pr.show().to_string() + " " + above_some(Some(nan), Some(1.0)).to_string());
  return 0;
}
`

const boundTraitProviderWant = `5 100 1100 200
34 7
100 100
1 1
`

func TestInterpBoundTraitProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(boundTraitProviderSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != boundTraitProviderWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, boundTraitProviderWant, errOut)
	}
}

func TestX86_64BoundTraitProvider(t *testing.T) {
	out, code := compileAndRunX86_64(t, boundTraitProviderSrc)
	if code != 0 || out != boundTraitProviderWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, boundTraitProviderWant)
	}
}

func TestArm64BoundTraitProvider(t *testing.T) {
	out, code := compileAndRunArm64(t, boundTraitProviderSrc)
	if code != 0 || out != boundTraitProviderWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, boundTraitProviderWant)
	}
}

func TestWasmBoundTraitProvider(t *testing.T) {
	if out := compileAndRunWasmCapture(t, boundTraitProviderSrc); out != boundTraitProviderWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, boundTraitProviderWant)
	}
}

// An associated function called through a bounded type variable, `T.zero()`,
// is the bound's associated function in every instance (#12281), as a method
// call is above: A and B each provide `zero` and `make` for P, a user trait and
// std/num each provide `zero` for i32. Each line reaches both providers,
// through a free generic, a supertrait, a lambda, a parameter named like the
// variable, and a generic struct's and enum's bounded impl.
const boundTraitProviderAssocSrc = `import "std/i32";
import "std/num";

struct P { x: i32 }

trait A { function zero(): Self; function make(n: i32): Self; function val(self: Self): i32; }
trait B { function zero(): Self; function make(n: i32): Self; function val(self: Self): i32; }
trait C: B { function tag(self: Self): i32; }

impl A for P {
  function zero(): Self { return P { x: 1 }; }
  function make(n: i32): Self { return P { x: n }; }
  function val(self: Self): i32 { return self.x; }
}
impl B for P {
  function zero(): Self { return P { x: 100 }; }
  function make(n: i32): Self { return P { x: n * 100 }; }
  function val(self: Self): i32 { return self.x + 5; }
}
impl C for P { function tag(self: Self): i32 { return self.x + 1000; } }

trait MyZero { function zero(): Self; }
impl MyZero for i32 { function zero(): Self { return 7; } }

function via_a[T: A](): T { return T.zero(); }
function via_b[T: B](): T { return T.zero(); }
function via_c[T: C](): i32 { let v: T = T.make(2); return v.tag(); }
function via_b_lambda[T: B](): T {
  let f = (n: i32): T => T.make(n);
  return f(3);
}
function shadowed[T: B](T: T): i32 { return T.val(); }

function mine[T: MyZero](): T { return T.zero(); }
function theirs[T: num.Zero](): T { return T.zero(); }

trait Mk { function mk(): Self; }

struct Q[T] { v: T }
impl[T: B] Mk for Q[T] { function mk(): Self { return Q { v: T.make(4) }; } }

enum E[T] { One(T), Zero }
impl[T: B] Mk for E[T] { function mk(): Self { return E.One(T.zero()); } }

function main(): i32 {
  let a: P = via_a[P]();
  let b: P = via_b[P]();
  print(a.x.to_string() + " " + b.x.to_string() + " " + via_c[P]().to_string() + " " + via_b_lambda[P]().x.to_string() + " " + shadowed(P { x: 1 }).to_string());
  let m: i32 = mine[i32]();
  let t: i32 = theirs[i32]();
  print(m.to_string() + " " + t.to_string());
  let q: Q[P] = Q.mk();
  let e: E[P] = E.mk();
  match (e) {
    One(p) => { print(q.v.x.to_string() + " " + p.x.to_string()); },
    Zero => { print("zero"); }
  }
  return 0;
}
`

const boundTraitProviderAssocWant = `1 100 1200 300 6
7 0
400 100
`

func TestInterpBoundTraitProviderAssoc(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(boundTraitProviderAssocSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != boundTraitProviderAssocWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, boundTraitProviderAssocWant, errOut)
	}
}

func TestX86_64BoundTraitProviderAssoc(t *testing.T) {
	out, code := compileAndRunX86_64(t, boundTraitProviderAssocSrc)
	if code != 0 || out != boundTraitProviderAssocWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, boundTraitProviderAssocWant)
	}
}

func TestArm64BoundTraitProviderAssoc(t *testing.T) {
	out, code := compileAndRunArm64(t, boundTraitProviderAssocSrc)
	if code != 0 || out != boundTraitProviderAssocWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, boundTraitProviderAssocWant)
	}
}

func TestWasmBoundTraitProviderAssoc(t *testing.T) {
	if out := compileAndRunWasmCapture(t, boundTraitProviderAssocSrc); out != boundTraitProviderAssocWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, boundTraitProviderAssocWant)
	}
}
