package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

const genericEnumAssocSrc = `import "core/cmp";

@derive(cmp.Default, cmp.Display)
enum E[A] { V(A), W }

@derive(cmp.Default, cmp.Display)
enum Two[A, B] { P(A, B), Q }

function mk[T: cmp.Default](): T {
  return T.default();
}

function fresh(): E[string] {
  return E.default();
}

function main(): i32 {
  let a: E[i32] = E.default();
  print(a.to_string());
  let b: E[i32] = mk[E[i32]]();
  print(b.to_string());
  print(fresh().to_string());
  let c: E[boolean] = mk[E[boolean]]();
  print(c.to_string());
  let d: Two[i32, string] = Two.default();
  print(d.to_string());
  print(mk[Two[boolean, i32]]().to_string());
  return 0;
}
`

const genericEnumAssocWant = `V(0)
V(0)
V()
V(false)
P(0, )
P(false, 0)
`

func TestInterpGenericEnumAssoc(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(genericEnumAssocSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != genericEnumAssocWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, genericEnumAssocWant, errOut)
	}
}

func TestX86_64GenericEnumAssoc(t *testing.T) {
	out, code := compileAndRunX86_64(t, genericEnumAssocSrc)
	if code != 0 || out != genericEnumAssocWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, genericEnumAssocWant)
	}
}

func TestArm64GenericEnumAssoc(t *testing.T) {
	out, code := compileAndRunArm64(t, genericEnumAssocSrc)
	if code != 0 || out != genericEnumAssocWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, genericEnumAssocWant)
	}
}

func TestWasmGenericEnumAssoc(t *testing.T) {
	if out := compileAndRunWasmCapture(t, genericEnumAssocSrc); out != genericEnumAssocWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, genericEnumAssocWant)
	}
}
