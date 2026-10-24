package e2ecompiler

import (
	"path/filepath"
	"testing"
)

var monoGuardCases = []struct {
	name, src, want string
}{
	{"bounded-generic-call", `trait Tr { function m(self: Self, x: i32): i32; }
impl Tr for i32 { function m(self: Self, x: i32): i32 { return self - x; } }
function tg[T: Tr](a: T, x: i32): i32 { return a.m(x); }
function pick(o: Option[i32]): string {
    match (o) {
        Some(x) when (tg(x, 3) > 0) => { return "big"; },
        Some(_) => { return "small"; },
        None => { return "none"; }
    }
}
function main(): i32 {
    print(pick(Some(5)) + " " + pick(Some(2)) + " " + pick(None));
    return 0;
}
`, "big small none"},
	{"generic-struct-method", `struct Box[T] { v: T }
function (b: Box[T]) get(): T { return b.v; }
function main(): i32 {
    let o: Option[i32] = Some(3);
    match (o) {
        Some(n) when (Box { v: [n, n + 1] }.get()[1] == 4) => { print("four"); },
        Some(_) => { print("other"); },
        None => { print("none"); }
    }
    return 0;
}
`, "four"},
}

func TestSelfHostMonoGuard(t *testing.T) {
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
	for _, c := range monoGuardCases {
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
