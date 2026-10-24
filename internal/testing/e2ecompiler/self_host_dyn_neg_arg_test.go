package e2ecompiler

import (
	"path/filepath"
	"testing"
)

const dynNegArgSrc = `import "std/i32";
trait Tr { function m(self: Self, x: i32): i32; function d(self: Self): i32 { return 3; } }
impl Tr for i32 { function m(self: Self, x: i32): i32 { return self - x; } }
function dg(d: dyn Tr, x: i32): i32 { return d.m(x) * 10 + d.d(); }
function main(): i32 {
    let a: i32 = dg(-1, 1);
    let b: i32 = dg(40 - 665, 122);
    let v: dyn Tr = -7;
    let ds: dyn Tr[] = [-2, 5];
    print(a.to_string() + " " + b.to_string() + " " + v.m(1).to_string() + " " + ds[0].m(0).to_string());
    return 0;
}
`

func TestSelfHostDynNegArg(t *testing.T) {
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
	want := interpStdout(t, interpBin, dynNegArgSrc)
	if want != "-17 -7467 -8 -2" {
		t.Fatalf("interpreter = %q", want)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stdout, stderr := routedMapRun(t, selfHostBin, stdlibRoot, dynNegArgSrc, target, "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1")
			if stdout != want {
				t.Fatalf("stdout = %q, want %q\n%s", stdout, want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
