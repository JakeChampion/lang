package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// destinationInferenceSrc calls generics whose type parameters no argument
// spells, so only the destination pins them (#12094, #12093): a `let`
// annotation, an assignment's target and a `return`, through a free
// function, a method, and a bounded generic.
const destinationInferenceSrc = `import "core/cmp";
import "std/i32";
import "std/i64";

struct Holder { n: i32 }

function (h: Holder) fresh[T](): T[] {
  return [];
}

function empty[T](): T[] {
  return [];
}

function zero[T: cmp.Default](): T {
  return T.default();
}

function back(): i64[] {
  return empty();
}

function main(): i32 {
  let xs: i32[] = empty();
  xs = xs.append(3);
  print(xs.len().to_string());
  let ys: string[] = ["a", "b"];
  ys = Holder { n: 1 }.fresh();
  print(ys.len().to_string());
  let zs: i64[] = back();
  print(zs.len().to_string());
  let z: i64 = zero();
  print(z.to_string());
  let s: string = "x";
  s = zero();
  print("[" + s + "]");
  return 0;
}
`

const destinationInferenceWant = `1
0
0
0
[]
`

func TestInterpDestinationInference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(destinationInferenceSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != destinationInferenceWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, destinationInferenceWant, errOut)
	}
}

func TestX86_64DestinationInference(t *testing.T) {
	out, code := compileAndRunX86_64(t, destinationInferenceSrc)
	if code != 0 || out != destinationInferenceWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, destinationInferenceWant)
	}
}

func TestArm64DestinationInference(t *testing.T) {
	out, code := compileAndRunArm64(t, destinationInferenceSrc)
	if code != 0 || out != destinationInferenceWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, destinationInferenceWant)
	}
}

func TestWasmDestinationInference(t *testing.T) {
	if out := compileAndRunWasmCapture(t, destinationInferenceSrc); out != destinationInferenceWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, destinationInferenceWant)
	}
}
