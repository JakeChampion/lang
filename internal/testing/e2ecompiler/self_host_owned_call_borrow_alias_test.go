package e2ecompiler

import (
	"fmt"
	"testing"
)

func TestSelfHostOwnedCallKeepsBorrowedAlias(t *testing.T) {
	const prefix = `@noinline function array(n: i32): i32[] {
    let a = __alloc_i32(n);
    let i: i32 = 0;
    while (i < n) { a = a.with(i, i + 1); i = i + 1; }
    return a;
}
@noinline function copy(own a: i32[], src: i32[]): i32[] {
    let i: i32 = 0;
    while (i < 3) { a = a.with(i + 1, src[i]); i = i + 1; }
    return a;
}
@noinline function other(own a: i32[], src: i32[]): i32[] { return copy(a, src); }
@noinline function choose(n: i32): (own i32[], i32[]) => i32[] {
    if (n == 0) { return copy; }
    return other;
}
`
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, indirect := range []bool{false, true} {
			for _, alias := range []bool{false, true} {
				name := fmt.Sprintf("%s/indirect=%t/alias=%t", target, indirect, alias)
				t.Run(name, func(t *testing.T) {
					source := prefix + "function main(): i32 { let a = array(4);\n"
					if alias {
						source += "let src = a;\n"
					} else {
						source += "let src = array(4);\n"
					}
					call := "copy(a, src)"
					if indirect {
						source += "let f = choose(0);\n"
						call = "f(a, src)"
					}
					wantCopies := 0
					if alias {
						wantCopies = 1
					}
					source += fmt.Sprintf(`let before = __heap_alloc_count();
    let out = %s;
    if (__heap_alloc_count() - before != %di64) { return 2; }
    if (out[0] != 1 || out[1] != 1 || out[2] != 2 || out[3] != 3) { return 1; }
    return 0;
}`, call, wantCopies)
					stderr, code := cli.exitOf(t, source, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("owned/borrowed alias: exit %d, want 0\n%s", code, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
		}
	}
}
