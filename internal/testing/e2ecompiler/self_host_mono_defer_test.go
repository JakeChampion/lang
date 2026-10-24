package e2ecompiler

import (
	"path/filepath"
	"testing"
)

var monoDeferCases = []struct {
	name, src, want string
}{
	{"bounded-generic-call", `import "std/i32";
trait Tr { function m(self: Self, x: i32): i32; function d(self: Self): i32 { return self.m(2) * 10; } }
impl Tr for i32 { function m(self: Self, x: i32): i32 { return self - x; } }
struct S { a: i32 }
impl Tr for S { function m(self: Self, x: i32): i32 { return self.a + x; } }
function tg[T: Tr](a: T, b: T, x: i32): i32 { return a.m(x) + b.d(); }
function h(n: i32): i32 {
    defer { print("h=" + tg(S { a: n }, S { a: 1 }, 4).to_string()); }
    return n;
}
function main(): i32 {
    defer { print("main=" + tg(9, 5, 3).to_string()); }
    let r: i32 = h(7);
    return r - 7;
}
`, "h=41\nmain=36"},
	{"generic-struct-literal", `import "std/i32";
struct Box[T] { v: T }
function (b: Box[T]) get(): T { return b.v; }
function main(): i32 {
    defer {
        let b: Box[string] = Box { v: "abc" };
        let c: Box[i32] = Box { v: 4 };
        print("n=" + (b.get().len() + c.get()).to_string());
    }
    return 0;
}
`, "n=7"},
	{"generic-enum-match", `import "std/i32";
enum Opt[T] { Nil, Has(T) }
function main(): i32 {
    defer {
        let o: Opt[string] = Has("xyz");
        match (o) {
            Has(s) => { print("len=" + s.len().to_string()); },
            Nil => { print("nil"); }
        }
    }
    return 0;
}
`, "len=3"},
}

func TestSelfHostMonoDefer(t *testing.T) {
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
	for _, c := range monoDeferCases {
		t.Run(c.name, func(t *testing.T) {
			if got := interpStdout(t, interpBin, c.src); got != c.want {
				t.Fatalf("interpreter = %q, want %q", got, c.want)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stdout, stderr := routedMapRun(t, selfHostBin, stdlibRoot, c.src, target, "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1")
					if stdout != c.want {
						t.Fatalf("stdout = %q, want %q\n%s", stdout, c.want, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
		})
	}
}
