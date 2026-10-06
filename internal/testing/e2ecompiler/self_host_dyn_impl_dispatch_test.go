package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A `dyn Trait` method call dispatches over the impls of the trait, not over
// every method that shares the name. std/num's `impl Add for i32` gives i32 an
// `add` of its own, and the user's `impl Adder for i32` is interposed beside
// it, so a dispatch keyed on the name called num's `self + n` and printed
// r=42 on the native targets and failed wasm validation (#11122).

// dynImplDispatchPrimSrc is the issue's program.
const dynImplDispatchPrimSrc = `import "std/i32";
trait Adder {
    function add(self: Self, n: i32): i32;
}
impl Adder for i32 {
    function add(self: Self, n: i32): i32 { return self * n; }
}
function run(a: dyn Adder, n: i32): i32 { return a.add(n); }
function main(): i32 {
    let x: i32 = 10;
    print("r=" + run(x, 32).to_string());
    return 0;
}
`

// dynImplDispatchMixedSrc: two implementors, and two types with a same-named
// `add` and no impl of Adder, one of them returning i64.
const dynImplDispatchMixedSrc = `import "std/i32";
import "std/i64";
trait Adder {
    function add(self: Self, n: i32): i32;
}
struct Acc { base: i32 }
struct Other { v: i32 }
struct Wide { v: i64 }
impl Adder for i32 {
    function add(self: Self, n: i32): i32 { return self * n; }
}
impl Adder for Acc {
    function add(self: Self, n: i32): i32 { return self.base - n; }
}
function (o: Other) add(n: i32): i32 { return o.v + n + 1000; }
function (w: Wide) add(n: i32): i64 { return w.v + (n as i64); }
function main(): i32 {
    let xs: dyn Adder[] = [10, Acc { base: 100 }];
    let out: string = "";
    for x in xs {
        out = out + x.add(7).to_string() + ",";
    }
    let o: Other = Other { v: 5 };
    let w: Wide = Wide { v: 6 };
    print(out + o.add(1).to_string() + "," + w.add(2).to_string());
    return 0;
}
`

// dynImplDispatchSuperSrc: `add` and `sub` are reached through Num2's
// supertraits, whose impls are written for Add2 and Sub2. The dyn names them
// too: a supertrait method on a bare `dyn Num2` is E021 (#10524).
const dynImplDispatchSuperSrc = `import "std/i32";
trait Add2 {
    function add(self: Self, n: i32): i32;
}
trait Sub2 {
    function sub(self: Self, n: i32): i32;
}
trait Num2: Add2 + Sub2 {}
impl Add2 for i32 {
    function add(self: Self, n: i32): i32 { return self * n; }
}
impl Sub2 for i32 {
    function sub(self: Self, n: i32): i32 { return self - n - 100; }
}
impl Num2 for i32 {}
function run(d: dyn Num2 + Add2 + Sub2): i32 { return d.add(3) + d.sub(1); }
function main(): i32 {
    let x: i32 = 10;
    print("s=" + run(x).to_string());
    return 0;
}
`

func TestSelfHostDynDispatchImplsOnly(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	selfHostBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	cases := []struct {
		name, src, want string
	}{
		{"prim_impl_beside_std_add", dynImplDispatchPrimSrc, "r=320"},
		{"impls_and_unrelated_add", dynImplDispatchMixedSrc, "70,93,1006,8"},
		{"supertrait_methods", dynImplDispatchSuperSrc, "s=-61"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := interpStdout(t, interpBin, c.src); got != c.want {
				t.Fatalf("interpreter = %q, want %q", got, c.want)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stdout, stderr := routedMapRun(t, selfHostBin, stdlibRoot, c.src, target, "FERN_LEAKCHECK=1")
					if stdout != c.want {
						t.Fatalf("stdout = %q, want %q\n%s", stdout, c.want, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
		})
	}
}

func interpStdout(t *testing.T, interpBin, src string) string {
	t.Helper()
	in := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(interpBin, "-interp", in).Output()
	if err != nil {
		t.Fatalf("interpret: %v", err)
	}
	return strings.TrimSpace(string(out))
}
