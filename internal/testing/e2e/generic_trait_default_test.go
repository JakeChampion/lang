package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const genericTraitDefaultSrc = `import "std/i32";
import "std/i64";

pub trait Holder[T] {
  function get(self: Self): T;
  function get_or(self: Self, d: T): T {
    let v: T = self.get();
    return v;
  }
  function both(self: Self, other: T): (T, T) {
    return (self.get(), other);
  }
}

pub struct IntBox { v: i32 }

impl Holder[i32] for IntBox {
  function get(self: IntBox): i32 { return self.v; }
}

pub struct Wrap[S] { v: S }

impl[S] Holder[S] for Wrap[S] {
  function get(self: Wrap[S]): S { return self.v; }
}

pub struct Pair[A, B] { a: A, b: B }

impl[A, B] Holder[B] for Pair[A, B] {
  function get(self: Pair[A, B]): B { return self.b; }
}

function main(): i32 {
  let b: IntBox = IntBox { v: 7 };
  let w: Wrap[i64] = Wrap { v: 9 as i64 };
  let p: Pair[i32, string] = Pair { a: 1, b: "pair" };
  print(b.get_or(0).to_string());
  print(w.get_or(0 as i64).to_string());
  print(p.get_or("none"));
  let t: (string, string) = p.both("other");
  print(t.0 + "," + t.1);
  return 0;
}
`

const genericTraitDefaultWant = "7\n9\npair\npair,other\n"

func TestInterpGenericTraitDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(genericTraitDefaultSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != genericTraitDefaultWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, genericTraitDefaultWant, errOut)
	}
}

func TestX86_64GenericTraitDefault(t *testing.T) {
	out, code := compileAndRunX86_64(t, genericTraitDefaultSrc)
	if code != 0 || out != genericTraitDefaultWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, genericTraitDefaultWant)
	}
}

func TestArm64GenericTraitDefault(t *testing.T) {
	out, code := compileAndRunArm64(t, genericTraitDefaultSrc)
	if code != 0 || out != genericTraitDefaultWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, genericTraitDefaultWant)
	}
}

func TestWasmGenericTraitDefault(t *testing.T) {
	if out := compileAndRunWasmCapture(t, genericTraitDefaultSrc); out != genericTraitDefaultWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, genericTraitDefaultWant)
	}
}

const traitDefaultOwnParamSrc = `import "std/i32";

pub trait Iter[T] {
  function next(self: Self): Option[(T, Self)];
  function map[U](self: Self, f: (T) => U): MapIt[Self, T, U] {
    return MapIt { it: self, f: f };
  }
}

pub struct ArrIt[T] { xs: T[], idx: i32 }

impl[T] Iter[T] for ArrIt[T] {
  function next(self: ArrIt[T]): Option[(T, ArrIt[T])] {
    if (self.idx >= self.xs.len()) { return None; }
    return Some((self.xs[self.idx], ArrIt { xs: self.xs, idx: self.idx + 1 }));
  }
}

pub struct MapIt[I, T, U] { it: I, f: (T) => U }

impl[I: Iter[T], T, U] Iter[U] for MapIt[I, T, U] {
  function next(self: MapIt[I, T, U]): Option[(U, MapIt[I, T, U])] {
    match (self.it.next()) {
      Some(t) => { return Some((self.f(t.0), MapIt { it: t.1, f: self.f })); },
      None => { return None; }
    }
  }
}

function main(): i32 {
  let it: ArrIt[i64] = ArrIt { xs: [1 as i64, 2 as i64, 3 as i64], idx: 0 };
  let m = it.map((x: i64): i64 => x * (10 as i64)).map((y: i64): boolean => y > (15 as i64));
  let cur = m;
  let n: i32 = 0;
  let go: boolean = true;
  while (go) {
    match (cur.next()) {
      Some(t) => { if (t.0) { n = n + 1; } cur = t.1; },
      None => { go = false; }
    }
  }
  print(n.to_string());
  return 0;
}
`

const traitDefaultOwnParamWant = "2\n"

func TestInterpTraitDefaultOwnParam(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(traitDefaultOwnParamSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != traitDefaultOwnParamWant {
		t.Fatalf("exit %d, printed %q, want %q\nstderr: %s", code, out, traitDefaultOwnParamWant, errOut)
	}
}

func TestX86_64TraitDefaultOwnParam(t *testing.T) {
	out, code := compileAndRunX86_64(t, traitDefaultOwnParamSrc)
	if code != 0 || out != traitDefaultOwnParamWant {
		t.Fatalf("exit %d, printed %q, want %q", code, out, traitDefaultOwnParamWant)
	}
}

func TestArm64TraitDefaultOwnParam(t *testing.T) {
	out, code := compileAndRunArm64(t, traitDefaultOwnParamSrc)
	if code != 0 || out != traitDefaultOwnParamWant {
		t.Fatalf("exit %d, printed %q, want %q", code, out, traitDefaultOwnParamWant)
	}
}

func TestWasmTraitDefaultOwnParam(t *testing.T) {
	if out := compileAndRunWasmCapture(t, traitDefaultOwnParamSrc); out != traitDefaultOwnParamWant {
		t.Fatalf("printed %q, want %q", out, traitDefaultOwnParamWant)
	}
}

func TestSelfHostTraitRequiredMethodOwnParamsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	src := "trait Iter[T] { function map[U](self: Self, f: (T) => U): U; }\nfunction main(): i32 { return 0; }\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(buildLangBinForInterp(t), "-target", "x86-64-linux", "-o", filepath.Join(t.TempDir(), "a.out"), path).CombinedOutput()
	if err == nil || !strings.Contains(string(out), `trait method "map" declares its own type parameters, so it must have a default body`) {
		t.Fatalf("compile: %v\n%s", err, out)
	}
}
