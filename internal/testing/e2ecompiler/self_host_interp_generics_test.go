package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The self-host interpreter (`fern -interp`, compiler/interp.fern) erases
// generics and binds a type parameter from what a call can see. std/simd's
// `Dual[T]` and `DoubleDouble[T]` reach the shapes that used to leave one
// unbound: an operator on a struct value (its impl's method), a parametric
// impl's associated function called on an applied type, a generic struct
// value whose type arguments a method reads back, a written type argument
// naming the caller's own parameter, and a call only its result types
// (`seed(2.0)`) pinned by its binding's annotation or by the parameter it
// fills. The Go interpreter runs the same programs through the test-runner
// gates; these hold the self-host one to the same answers.
func runSelfHostInterp(t *testing.T, name, src string) string {
	t.Helper()
	cli := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(cli, "-interp", path, stdlib).CombinedOutput()
	if err != nil {
		t.Fatalf("self-host interp failed: %v\n%s", err, out)
	}
	return string(out)
}

const selfHostStructOpsSrc = `import "std/num" as num;
import "std/f64";
import "std/i32";
trait Lanes { const LANES: i32; function splat(x: f64): Self; }
impl Lanes for f64 {
    const LANES: i32 = 1;
    function splat(x: f64): f64 { return x; }
}
struct P { v: f64 }
impl num.Mul for P { function mul(self: P, o: P): P { return P { v: self.v * o.v }; } }
impl num.Neg for P { function neg(self: P): P { return P { v: -self.v }; } }
struct G[T] { v: T }
impl[T: Lanes + num.Num] num.Add for G[T] {
    function add(self: G[T], o: G[T]): G[T] { return G { v: self.v + o.v }; }
}
impl[T: Lanes + num.Num] Lanes for G[T] {
    const LANES: i32 = T.LANES + 1;
    function splat(x: f64): G[T] { return G { v: T.splat(x) }; }
}
function lanes_of[T: Lanes](x: T): i32 { return T.LANES; }
function wrap[T: Lanes](x: f64): G[T] { return G { v: T.splat(x) }; }
function twice[T: Lanes + num.Num](g: G[T], h: G[T]): G[T] { return g + h; }
function main(): i32 {
    let p: P = P { v: 3.0 };
    let q: P = -(p * p);
    let g: G[f64] = G { v: 3.0 };
    let h: G[f64] = g + g;
    let s: G[f64] = G.splat(2.5);
    let w: G[f64] = wrap(1.5);
    let u: G[f64] = twice(w, wrap(2.0));
    print(q.v.to_string());
    print(h.v.to_string());
    print(s.v.to_string());
    print(lanes_of(g).to_string());
    print(u.v.to_string());
    return 0;
}
`

func TestSelfHostInterpStructOps(t *testing.T) {
	out := runSelfHostInterp(t, "struct_ops.fern", selfHostStructOpsSrc)
	if got := strings.TrimSpace(out); got != "-9\n6\n2.5\n2\n3.5" {
		t.Errorf("stdout = %q, want -9, 6, 2.5, 2 and 3.5 on their own lines", out)
	}
}

// Associated constants read through a bound with an explicit instantiation
// (`chunk[V2](8)`), as `Self` and on a primitive (the e2e package's
// assocConstSrc, which the Go interpreter, x86-64 and wasm also run).
const selfHostAssocConstSrc = `import "std/i32";
trait Lanes {
    const LANES: i32;
    const NAME: string = "lanes";
    function width(self: Self): i32;
}
struct V2 { a: f64, b: f64 }
impl Lanes for V2 {
    const LANES: i32 = 2;
    function width(self: Self): i32 { return Self.LANES * 10; }
}
impl Lanes for f64 {
    const LANES: i32 = 1;
    function width(self: Self): i32 { return Self.LANES; }
}
function chunk[T: Lanes](n: i32): i32 { return n / T.LANES; }
function main(): i32 {
    let v: V2 = V2 { a: 1.0, b: 2.0 };
    let x: i32 = V2.LANES + f64.LANES + chunk[V2](8) + chunk[f64](3) + v.width() + (1.5).width() + V2.LANES;
    print(V2.NAME);
    print(x.to_string());
    return x - 33;
}
`

func TestSelfHostInterpAssocConsts(t *testing.T) {
	out := runSelfHostInterp(t, "assoc_const.fern", selfHostAssocConstSrc)
	if !strings.Contains(out, "lanes\n33") {
		t.Errorf("stdout = %q, want the name and the sum", out)
	}
}

func TestSelfHostInterpSimdSuite(t *testing.T) {
	path, err := filepath.Abs("../../../tests/stdlib/simd_test.fern")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := runSelfHostInterp(t, "simd_test.fern", string(src))
	for _, w := range []string{"1..23", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\n%s", w, out)
		}
	}
}
