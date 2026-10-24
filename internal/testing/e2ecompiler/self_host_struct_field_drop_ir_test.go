package e2ecompiler

import "testing"

func TestSelfHostStructFieldDrop(t *testing.T) {
	boxedProbes(t)
	const prog = `struct Bag { items: i32[], n: i32 }
@noinline function id(xs: i32[]): i32[] { return xs; }
function use_bag(): i32 {
    let b: Bag = Bag { items: id([1, 2, 3]), n: 3 };
    return b.items[0] + b.n;
}
function main(): i32 { return use_bag(); }
`
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, prog, target, "FERN_LEAKCHECK=1")
			if code != 4 {
				t.Fatalf("exit %d, want 4 (b.items[0] + b.n)\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
