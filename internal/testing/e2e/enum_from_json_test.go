package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

const enumFromJsonSrc = `import "std/json" as json;
@derive(json.Json, json.FromJson)
enum Shape { Empty, Circle(f64), Pair(i32, i32), Rect { w: i32, h: i32 }, Tagged(string[]) }
@derive(json.FromJson)
enum Opt { Maybe(Option[i32]), Two(i32, Option[string]) }
function show(r: Result[Shape, string]): string {
  match (r) {
    Ok(s) => { return "ok " + s.to_json(); },
    Err(e) => { return "err " + e; }
  }
  return "";
}
function opt(r: Result[Opt, string]): string {
  match (r) {
    Ok(o) => {
      match (o) {
        Maybe(m) => { match (m) { Some(n) => { return "maybe " + n.to_string(); }, None => { return "maybe none"; } } },
        Two(a, b) => { match (b) { Some(t) => { return "two " + a.to_string() + " " + t; }, None => { return "two " + a.to_string() + " none"; } } }
      }
    },
    Err(e) => { return "err " + e; }
  }
  return "";
}
function main(): i32 {
  let all: Shape[] = [Empty, Circle(1.5), Pair(2, 3), Rect(4, 5), Tagged(["a", "b"])];
  for s in all {
    print(show(Shape.from_json(s.to_json())));
  }
  print(opt(Opt.from_json("{\"Maybe\":null}")));
  print(opt(Opt.from_json("{\"Maybe\":7}")));
  print(opt(Opt.from_json("{\"Two\":[1,null]}")));
  print(opt(Opt.from_json("{\"Two\":[1,\"s\"]}")));
  print(opt(Opt.from_json("{\"Two\":[1,2]}")));
  print(show(Shape.from_json("\"Nope\"")));
  print(show(Shape.from_json("{\"Pair\":[1]}")));
  print(show(Shape.from_json("{\"Pair\":[1,\"x\"]}")));
  print(show(Shape.from_json("{\"Rect\":{\"w\":1}}")));
  print(show(Shape.from_json("{\"Rect\":{\"w\":1,\"h\":true}}")));
  print(show(Shape.from_json("{\"Circle\":\"x\"}")));
  print(show(Shape.from_json("{\"Circle\":1,\"Empty\":2}")));
  print(show(Shape.from_json("3")));
  print(show(Shape.from_json("{\"Tagged\":[1]}")));
  return 0;
}
`

const enumFromJsonWant = `ok "Empty"
ok {"Circle":1.5}
ok {"Pair":[2,3]}
ok {"Rect":{"w":4,"h":5}}
ok {"Tagged":["a","b"]}
maybe none
maybe 7
two 1 none
two 1 s
err variant "Two": [1]: expected a string, got a number
err unknown variant "Nope"
err variant "Pair": expected an array of 2, got 1
err variant "Pair": [1]: expected an integer, got a string
err variant "Rect": missing field "h"
err variant "Rect": field "h": expected an integer, got a boolean
err variant "Circle": expected a number, got a string
err expected an object with one key, got 2
err expected a variant name or a one-key object, got a number
err variant "Tagged": [0]: expected a string, got a number
`

func TestInterpEnumFromJson(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(enumFromJsonSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != enumFromJsonWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, enumFromJsonWant, errOut)
	}
}

func TestX86_64EnumFromJson(t *testing.T) {
	out, code := compileAndRunX86_64(t, enumFromJsonSrc)
	if code != 0 || out != enumFromJsonWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, enumFromJsonWant)
	}
}

func TestArm64EnumFromJson(t *testing.T) {
	out, code := compileAndRunArm64(t, enumFromJsonSrc)
	if code != 0 || out != enumFromJsonWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, enumFromJsonWant)
	}
}

func TestWasmEnumFromJson(t *testing.T) {
	if out := compileAndRunWasmCapture(t, enumFromJsonSrc); out != enumFromJsonWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, enumFromJsonWant)
	}
}
