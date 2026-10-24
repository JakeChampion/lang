package e2ecompiler

import "testing"

const tupleDestructureStrIRPrelude = `function mk2(): (i32, string) { return (5, "hi"); }
function mkStrFirst(): (string, i32) { return ("abc", 9); }
function mk3(): (i32, string, i32) { return (1, "xy", 2); }
function mk2str(): (string, string) { return ("ab", "cde"); }
`

var tupleDestructureStrIRCases = []struct {
	name string
	main string
	want int
}{
	{"scalar-then-str", `let (a, s) = mk2(); return a + s.len();`, 7},
	{"str-then-scalar", `let (s, n) = mkStrFirst(); return s.len() + n;`, 12},
	{"three-mixed", `let (a, s, b) = mk3(); return a + s.len() + b;`, 5},
	{"two-strings", `let (p, q) = mk2str(); return p.len() + q.len();`, 5},
	{"let-scalar-str", `let (a, s) = mk2(); return a + s.len();`, 7},
	{"str-only-use", `let (a, s) = mk2(); return s.len();`, 2},
}

func tupleDestructureStrIRSrc(mainBody string) string {
	return tupleDestructureStrIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostTupleDestructureStrIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range tupleDestructureStrIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tupleDestructureStrIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
