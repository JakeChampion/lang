package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// genericEnumSelfSrc has a generic enum's instance come only from context:
// a unit variant inside `Ok(...)` and a `match` on a method returning `Self`
// (#12089), and a bounded impl's associated function reached directly and
// through a bound (#12088).
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
