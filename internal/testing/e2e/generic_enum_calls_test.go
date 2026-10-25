package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

const genericEnumCallsSrc = `import "core/cmp";
import "std/i32";
import "std/json";

function tag[T](x: T): string {
  return "tag";
}

function shown[T: cmp.Display](x: T): string {
  return "<" + x.to_string() + ">";
}

enum Box[A] { Full(A), Empty }

trait Show {
  function show(self: Self): string;
}

impl[A: cmp.Display] Show for Box[A] {
  function show(self: Self): string {
    match (self) {
      Full(a) => {
        return tag(a) + shown(a);
      },
      Empty => {
        return "empty";
      }
    }
    return "";
  }
}

@derive(json.Json, json.FromJson)
enum Either[L, R] { Left(L), Right(R), Both { l: L, r: R } }

@derive(json.FromJson)
enum Bag[T] { Items(T[]), Nothing }

function bag(text: string): string {
  let r: Result[Bag[i32], json.JsonError] = Bag.from_json(text);
  match (r) {
    Ok(b) => {
      match (b) {
        Items(xs) => {
          return "items " + xs.len().to_string();
        },
        Nothing => {
          return "nothing";
        }
      }
    },
    Err(why) => {
      return "err " + why.to_string();
    }
  }
  return "";
}

function round(text: string): string {
  let r: Result[Either[i32, string], json.JsonError] = Either.from_json(text);
  match (r) {
    Ok(e) => {
      return "ok " + e.to_json();
    },
    Err(why) => {
      return "err " + why.to_string();
    }
  }
  return "";
}

function main(): i32 {
  let a: Box[i32] = Full(7);
  print(a.show());
  let b: Box[string] = Full("s");
  print(b.show());
  let c: Box[boolean] = Empty;
  print(c.show());
  print(round("{\"Left\":3}"));
  print(round("{\"Right\":\"x\"}"));
  print(round("{\"Both\":{\"l\":1,\"r\":\"p\"}}"));
  print(round("{\"Left\":\"no\"}"));
  print(round("{\"Both\":{\"l\":1}}"));
  let back: Either[i32, string] = Both(5, "z");
  print(round(back.to_json()));
  print(bag("{\"Items\":[1,2,3]}"));
  print(bag("\"Nothing\""));
  print(bag("{\"Items\":[1,\"x\"]}"));
  return 0;
}
`

const genericEnumCallsWant = `tag<7>
tag<s>
empty
ok {"Left":3}
ok {"Right":"x"}
ok {"Both":{"l":1,"r":"p"}}
err variant "Left": expected an integer, got a string
err variant "Both": missing field "r"
ok {"Both":{"l":5,"r":"z"}}
items 3
nothing
err variant "Items": [1]: expected an integer, got a string
`

func TestInterpGenericEnumCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(genericEnumCallsSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != genericEnumCallsWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, genericEnumCallsWant, errOut)
	}
}

func TestX86_64GenericEnumCalls(t *testing.T) {
	out, code := compileAndRunX86_64(t, genericEnumCallsSrc)
	if code != 0 || out != genericEnumCallsWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, genericEnumCallsWant)
	}
}

func TestArm64GenericEnumCalls(t *testing.T) {
	out, code := compileAndRunArm64(t, genericEnumCallsSrc)
	if code != 0 || out != genericEnumCallsWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, genericEnumCallsWant)
	}
}

func TestWasmGenericEnumCalls(t *testing.T) {
	if out := compileAndRunWasmCapture(t, genericEnumCallsSrc); out != genericEnumCallsWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, genericEnumCallsWant)
	}
}
