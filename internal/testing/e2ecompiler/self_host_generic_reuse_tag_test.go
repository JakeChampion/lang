package e2ecompiler

import (
	"path/filepath"
	"testing"
)

var genericReuseTagCases = []struct {
	name, src, want string
}{
	{"result-payload", `import "std/i32";
struct G[T] { v: T, n: i32 }
function (g: G[T]) get(): T { return g.v; }
enum GO[T] { GNil, GHas(T, i32) }
function (o: GO[T]) count(): i32 { match (o) { GHas(_, k) => { return k; }, GNil => { return 0; } } }
function main(): i32 {
    let x: Result[i32, i32] = Err(620);
    let a: G[Result[i32, i32]] = G { v: x, n: 908 };
    let o: GO[Result[i32, i32]] = GHas(a.get(), 5);
    print("count=" + o.count().to_string());
    return 0;
}
`, "count=5"},
	{"map-payload-after-get", `import "std/i32";
import "core/map";
struct G[T] { v: T, n: i32 }
function (g: G[T]) get(): T { return g.v; }
enum GO[T] { GNil, GHas(T, i32) }
function (o: GO[T]) or(d: T): T { match (o) { GHas(x, _) => { return x; }, GNil => { return d; } } }
function (o: GO[T]) count(): i32 { match (o) { GHas(_, k) => { return k; }, GNil => { return 0; } } }
function main(): i32 {
    let x: Map[i32, i32] = Map { 1: 2, 3: 4 };
    let a: G[Map[i32, i32]] = G { v: x, n: 7 };
    let y: Map[i32, i32] = a.get();
    let o: GO[Map[i32, i32]] = GHas(y, 9);
    print("count=" + o.count().to_string() + " len=" + o.or(x).len().to_string());
    return 0;
}
`, "count=9 len=2"},
}

func TestSelfHostGenericReuseTag(t *testing.T) {
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
	for _, c := range genericReuseTagCases {
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
