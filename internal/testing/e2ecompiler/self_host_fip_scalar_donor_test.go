package e2ecompiler

import (
	"fmt"
	"testing"
)

func TestSelfHostFipScalarDonor(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct{ name, decl, call string }{
		{"free-fbip", "@noinline fbip function step(own row: Row, n: i32): Row", "step(row, i)"},
		{"method-fip", "@noinline fip function (own row: Row) step(n: i32): Row", "row.step(i)"},
	} {
		src := fmt.Sprintf(`struct Row { a: i32, b: i32, c: i32 }
@noinline function make(n: i32): Row { return Row { a: n, b: n, c: n }; }
@noinline function scalar_sum(own row: Row): i32 { return row.a + row.b + row.c; }
%s {
 row = Row { ...row, a: n };
 row = Row { ...row, b: n + 1 };
 return Row { ...row, c: n + 2 };
}
function main(): i32 {
 let row = make(0);
 let before = __heap_alloc_count();let i = 0;
 while (i < 1000) {
  row = %s;
  if (row.a != i || row.b != i + 1 || row.c != i + 2) { return 91; }
  if (scalar_sum(Row { a: i, b: i + 1, c: i + 2 }) != i * 3 + 3) { return 94; }
  i = i + 1;
 }
 let count = __heap_alloc_count() - before;
 let held: Row[] = [row];let last = held[0];
 if (last.a != 999 || last.b != 1000 || last.c != 1001) { return 92; }
 if (count != 0i64) { return 93; }
 return 0;
}
`, tc.decl, tc.call)
		t.Run(tc.name, func(t *testing.T) {
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, code := cli.exitOf(t, src, target, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("exit %d, want 0 (91/92/94: wrong fields, 93: loop allocated)\n%s", code, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
		})
	}
}
