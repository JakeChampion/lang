package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

const genericJsonArraySrc = `import "std/json";

@derive(json.Json, json.FromJson)
enum Tagged[R] { Many(R[]), One(R), Both { first: R, rest: R[] }, Nil }

@derive(json.Json)
struct Bag[R] { xs: R[] }

function round(text: string): string {
  let r: Result[Tagged[string], json.JsonError] = Tagged.from_json(text);
  match (r) {
    Ok(t) => {
      return t.to_json();
    },
    Err(why) => {
      return "err " + why.to_string();
    }
  }
  return "";
}

function main(): i32 {
  let a: Tagged[string] = Many(["a", "b"]);
  print(a.to_json());
  let b: Tagged[i32] = Both(1, [2, 3]);
  print(b.to_json());
  let c: Tagged[i32] = Nil;
  print(c.to_json());
  let bag: Bag[i32] = Bag { xs: [4, 5] };
  print(bag.to_json());
  print(round(a.to_json()));
  print(round("{\"Both\":{\"first\":\"x\",\"rest\":[]}}"));
  print(round("{\"Many\":[1]}"));
  return 0;
}
`

const genericJsonArrayWant = `{"Many":["a","b"]}
{"Both":{"first":1,"rest":[2,3]}}
"Nil"
{"xs":[4,5]}
{"Many":["a","b"]}
{"Both":{"first":"x","rest":[]}}
err variant "Many": [0]: expected a string, got a number
`

func TestInterpGenericJsonArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(genericJsonArraySrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != genericJsonArrayWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, genericJsonArrayWant, errOut)
	}
}

func TestX86_64GenericJsonArray(t *testing.T) {
	out, code := compileAndRunX86_64(t, genericJsonArraySrc)
	if code != 0 || out != genericJsonArrayWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, genericJsonArrayWant)
	}
}

func TestArm64GenericJsonArray(t *testing.T) {
	out, code := compileAndRunArm64(t, genericJsonArraySrc)
	if code != 0 || out != genericJsonArrayWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, genericJsonArrayWant)
	}
}

func TestWasmGenericJsonArray(t *testing.T) {
	if out := compileAndRunWasmCapture(t, genericJsonArraySrc); out != genericJsonArrayWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, genericJsonArrayWant)
	}
}
