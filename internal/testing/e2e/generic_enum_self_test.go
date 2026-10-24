package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

const genericEnumSelfSrc = `import "core/cmp";
import "std/i32";

@derive(cmp.Default, cmp.Display)
enum E[A] { V(A), W }

trait Reset {
  function reset(self: Self): Self;
}

impl[A] Reset for E[A] {
  function reset(self: Self): Self {
    return W;
  }
}

trait Make {
  function make(n: i32): Result[Self, string];
}

impl[A: cmp.Default] Make for E[A] {
  function make(n: i32): Result[Self, string] {
    if (n < 0) {
      return Err("negative");
    }
    if (n == 0) {
      return Ok(W);
    }
    return Ok(V(A.default()));
  }
}

function made[T: Make](n: i32): Result[T, string] {
  return T.make(n);
}

function wrapped(): Result[E[i32], string] {
  return Ok(W);
}

function show(r: Result[E[i32], string]): string {
  match (r) {
    Ok(e) => {
      return "ok " + e.to_string();
    },
    Err(why) => {
      return "err " + why;
    }
  }
  return "";
}

function main(): i32 {
  let a: E[i32] = V(3);
  match (a.reset()) {
    V(n) => {
      print("v " + n.to_string());
    },
    W => {
      print("w");
    }
  }
  print(show(wrapped()));
  let d: Result[E[i32], string] = E.make(1);
  print(show(d));
  print(show(made[E[i32]](0)));
  print(show(made[E[i32]](0 - 1)));
  let s: Result[E[string], string] = E.make(2);
  match (s) {
    Ok(e) => {
      print("string " + e.to_string());
    },
    Err(why) => {
      print(why);
    }
  }
  return 0;
}
`

const genericEnumSelfWant = `w
ok W
ok V(0)
ok W
err negative
string V()
`

const genericEnumNameClashSrc = `import "core/cmp";
import "std/i32";

struct Holder[T] { v: T }

function make[T: cmp.Default](): T {
  return T.default();
}

enum E[A] { V(A), W }

trait Size {
  function size(self: Self): i32;
}

impl[A] Size for E[A] {
  function size(self: Self): i32 {
    let n: i32 = make();
    match (self) {
      V(_) => {
        return n + 1;
      },
      W => {
        return n;
      }
    }
    return n;
  }
}

trait Make {
  function make(n: i32): Self;
}

impl[A: cmp.Default] Make for E[A] {
  function make(n: i32): Self {
    if (n == 0) {
      return W;
    }
    return V(A.default());
  }
}

function main(): i32 {
  let h: Holder[i32] = Holder { v: 2 };
  let e: E[i32] = E.make(1);
  print((e.size() + h.v).to_string());
  return 0;
}
`

func TestGenericEnumNameClash(t *testing.T) {
	const want = "3\n"
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(genericEnumNameClashSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path); code != 0 || out != want {
		t.Fatalf("interp exit %d, printed %q, want %q\nstderr: %s", code, out, want, errOut)
	}
	if out, code := compileAndRunX86_64(t, genericEnumNameClashSrc); code != 0 || out != want {
		t.Fatalf("x86-64 exit %d, printed %q, want %q", code, out, want)
	}
	if out := compileAndRunWasmCapture(t, genericEnumNameClashSrc); out != want {
		t.Fatalf("wasm printed %q, want %q", out, want)
	}
}

func TestInterpGenericEnumSelf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(genericEnumSelfSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != genericEnumSelfWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, genericEnumSelfWant, errOut)
	}
}

func TestX86_64GenericEnumSelf(t *testing.T) {
	out, code := compileAndRunX86_64(t, genericEnumSelfSrc)
	if code != 0 || out != genericEnumSelfWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, genericEnumSelfWant)
	}
}

func TestArm64GenericEnumSelf(t *testing.T) {
	out, code := compileAndRunArm64(t, genericEnumSelfSrc)
	if code != 0 || out != genericEnumSelfWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, genericEnumSelfWant)
	}
}

func TestWasmGenericEnumSelf(t *testing.T) {
	if out := compileAndRunWasmCapture(t, genericEnumSelfSrc); out != genericEnumSelfWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, genericEnumSelfWant)
	}
}
