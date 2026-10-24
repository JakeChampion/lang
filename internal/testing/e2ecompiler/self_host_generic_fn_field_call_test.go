package e2ecompiler

import (
	"path/filepath"
	"testing"
)

const genericFnFieldCallSrc = `import "std/i32";
struct P[A, B] { a: A, b: B }
struct G[T] { v: T, n: i32 }
function (g: G[T]) get(): T { return g.v; }
function main(): i32 {
    let f: (i32) => i32 = ((q: i32) => q + 1);
    let p: P[(i32) => i32, i32] = P { a: f, b: 7 };
    let g: G[P[(i32) => i32, i32]] = G { v: p, n: 3 };
    let pp: P[(i32) => i32, i32] = g.get();
    print("r=" + (p.a(p.b) + pp.a(pp.b) + g.n).to_string());
    return 0;
}
`

func TestSelfHostGenericFnFieldCall(t *testing.T) {
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
	if got := interpStdout(t, interpBin, genericFnFieldCallSrc); got != "r=19" {
		t.Fatalf("interpreter = %q, want %q", got, "r=19")
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stdout, stderr := routedMapRun(t, selfHostBin, stdlibRoot, genericFnFieldCallSrc, target, "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1")
			if stdout != "r=19" {
				t.Fatalf("stdout = %q, want %q\n%s", stdout, "r=19", stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
