package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

const fnParamForwardingSrc = `import "std/i32";
import "std/option";

enum Box[T] { Full(T), Empty }

function wrap[T](f: () => T): Box[T] {
  return Full(f());
}

function pick[T](f: () => T, show: (T) => string): string {
  match (wrap(f)) {
    Full(v) => { return show(v); },
    Empty => { return "empty"; }
  }
  return "unreached";
}

function tag[U](x: U, show: (U) => string): string {
  return "<" + pick(() => x, show) + ">";
}

function main(): i32 {
  let f: () => i32 = (): i32 => 40;
  print(pick(f, (v: i32): string => v.to_string()));
  let g: () => Option[string] = (): Option[string] => Some("nested");
  print(pick(g, (v: Option[string]): string => v.unwrap_or("none")));
  print(tag(7, (v: i32): string => v.to_string()));
  print(tag("s", (v: string): string => v));
  return 0;
}
`

const fnParamForwardingWant = "40\nnested\n<7>\n<s>\n"

func TestInterpFnParamForwarding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(fnParamForwardingSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != fnParamForwardingWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, fnParamForwardingWant, errOut)
	}
}

func TestX86_64FnParamForwarding(t *testing.T) {
	out, code := compileAndRunX86_64(t, fnParamForwardingSrc)
	if code != 0 || out != fnParamForwardingWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, fnParamForwardingWant)
	}
}

func TestArm64FnParamForwarding(t *testing.T) {
	out, code := compileAndRunArm64(t, fnParamForwardingSrc)
	if code != 0 || out != fnParamForwardingWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, fnParamForwardingWant)
	}
}

func TestWasmFnParamForwarding(t *testing.T) {
	if out := compileAndRunWasmCapture(t, fnParamForwardingSrc); out != fnParamForwardingWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, fnParamForwardingWant)
	}
}
