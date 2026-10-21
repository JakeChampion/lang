package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Associated constants (docs/ASSOCIATED-TYPES.md) read on a concrete type,
// through a bound with an explicit instantiation, as `Self` inside an impl,
// on a primitive, and from a trait default, on every engine. The sum is
// 2 + 1 + 4 + 3 + 20 + 1 + 2 = 33, printed; main exits 0 when it holds.
const assocConstSrc = `import "std/i32";
trait Lanes {
    const LANES: i32;
    const NAME: string = "lanes";
    function width(self: Self): i32;
}
struct V2 { a: f64, b: f64 }
impl Lanes for V2 {
    const LANES: i32 = 2;
    function width(self: Self): i32 { return Self::LANES * 10; }
}
impl Lanes for f64 {
    const LANES: i32 = 1;
    function width(self: Self): i32 { return Self::LANES; }
}
function chunk[T: Lanes](n: i32): i32 { return n / T::LANES; }
function main(): i32 {
    let v: V2 = V2 { a: 1.0, b: 2.0 };
    let LANES: i32 = 100;
    let x: i32 = V2::LANES + f64::LANES + chunk[V2](8) + chunk[f64](3) + v.width() + (1.5).width() + V2.LANES;
    print(V2::NAME);
    print(x.to_string());
    return x - 33;
}
`

func TestAssocConstInterp(t *testing.T) {
	if out := interpStdout(t, assocConstSrc); !strings.Contains(out, "lanes\n33") {
		t.Errorf("stdout = %q, want the name and the sum", out)
	}
}

func TestAssocConstSelfHostInterp(t *testing.T) {
	bin := buildFernCLI(t)
	src := filepath.Join(t.TempDir(), "assoc_const.fern")
	if err := os.WriteFile(src, []byte(assocConstSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "-interp", src).CombinedOutput()
	if code := exitCodeOf(err); code != 0 {
		t.Errorf("exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(string(out), "lanes\n33") {
		t.Errorf("stdout = %q, want the name and the sum", out)
	}
}

func TestAssocConstX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "assoc_const.fern")
	if err := os.WriteFile(src, []byte(assocConstSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "assoc_const.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}
	o, err := runX86Bin(qemu, out).CombinedOutput()
	if code := exitCodeOf(err); code != 0 {
		t.Errorf("exit = %d, want 0\n%s", code, o)
	}
	if !strings.Contains(string(o), "lanes\n33") {
		t.Errorf("stdout = %q, want the name and the sum", o)
	}
}

func TestAssocConstWasm(t *testing.T) {
	out := compileAndRunWasmCapture(t, assocConstSrc)
	if !strings.Contains(out, "lanes\n33") {
		t.Errorf("stdout = %q, want the name and the sum", out)
	}
}
