package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

const deadPhiAfterThreadingSrc = `import "std/i32";
function id[T](x: T): T { return x; }
function pick(v0: Result[i32, i32]): i32 {
  return (if (false) {
    (match (v0) { Ok(a1) => (match (v0) { Ok(a2) => 685i32, Err(e2) => a1 }), Err(e1) => e1 })
  } else {
    (match (v0) { Ok(a3) => 840i32, Err(e3) => e3 - 900 })
  });
}
function main(): i32 {
  let v0: Result[i32, i32] = id(Err(951i32));
  print(pick(v0).to_string());
  print(pick(id(Ok(5i32))).to_string());
  let v1: Result[i32, i32] = id((Err(951i32)));
  return ((if (false) { ((681i32 % 982i32) <<| (match (v1) { Ok(o1) => (match (v1) { Ok(o2) => 685i32, Err(r2) => o1 }), Err(r1) => r1 })) } else { (match (v1) { Ok(o3) => 840i32, Err(r3) => r3 }) }) & 255i32) - 183;
}
`

const deadPhiAfterThreadingWant = "51\n840\n"

func TestInterpDeadPhiAfterThreading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(deadPhiAfterThreadingSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != deadPhiAfterThreadingWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s\nstderr: %s", code, out, deadPhiAfterThreadingWant, errOut)
	}
}

func TestX86_64DeadPhiAfterThreading(t *testing.T) {
	out, code := compileAndRunX86_64(t, deadPhiAfterThreadingSrc)
	if code != 0 || out != deadPhiAfterThreadingWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, deadPhiAfterThreadingWant)
	}
}

func TestArm64DeadPhiAfterThreading(t *testing.T) {
	out, code := compileAndRunArm64(t, deadPhiAfterThreadingSrc)
	if code != 0 || out != deadPhiAfterThreadingWant {
		t.Fatalf("exit %d, printed\n%s\nwant\n%s", code, out, deadPhiAfterThreadingWant)
	}
}

func TestWasmDeadPhiAfterThreading(t *testing.T) {
	if out := compileAndRunWasmCapture(t, deadPhiAfterThreadingSrc); out != deadPhiAfterThreadingWant {
		t.Fatalf("printed\n%s\nwant\n%s", out, deadPhiAfterThreadingWant)
	}
}
