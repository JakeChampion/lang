package e2eselfhost

import "testing"

// A caller releases a returned tuple's array child whatever the callee's body
// spells (#9004). The AST lowering decided that from the callee's syntax, so
// `return t` of a local tuple stranded the array a tuple literal released;
// the typed lowering reads the callee's return contract and balances both.
func TestSelfHostReturnedLocalTupleReleasesItsChild(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct{ name, body string }{
		{"local", "var xs: i32[] = [n];\n    var t: (i32, i32[]) = (n, xs);\n    var c: i32 = t.1[0];\n    return t;"},
		{"literal", "return (n, [n, n + 1]);"},
	} {
		src := writeEnumMapSrc(t, "returned_tuple_"+tc.name, `function pair(n: i32): (i32, i32[]) {
    `+tc.body+`
}
function main(): i32 {
    var p: (i32, i32[]) = pair(3);
    return p.1[0];
}
`)
		t.Run(tc.name, func(t *testing.T) {
			bin := cli.x86Binary(t, src, "FERN_LEAKCHECK=1", "FERN_SEM_IR=1")
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			if exit != 3 {
				t.Fatalf("exit = %d, want 3\n%s", exit, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
