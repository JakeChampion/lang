package e2ecompiler

import "testing"

const strArrayLiteralSrc = `function main(): i32 {
    let owned: string = "ab" + "cd";
    let s: str = slice_unchecked(owned, 1, 3);
    let xs: str[] = ["x", s];
    let ys: str[] = [s, "yyy"];
    return xs.len() * 10 + xs[0].len() + xs[1].len() * 2 + ys[1].len() * 20;
}
`

func TestSelfHostStrArrayLiteral(t *testing.T) {
	const want = 85
	if got := interpExit(t, buildLangBinForInterp(t), strArrayLiteralSrc); got != want {
		t.Fatalf("interpreter exit %d, want %d", got, want)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, exit := cli.exitOf(t, strArrayLiteralSrc, target, "FERN_LEAKCHECK=1")
			if exit != want {
				t.Fatalf("exit = %d, want %d\n%s", exit, want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
