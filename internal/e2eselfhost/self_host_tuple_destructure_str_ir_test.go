package e2eselfhost

import "testing"

// tupleDestructureStrIRCases pin tuple destructuring (`var (a, b) = E` /
// `let (a, b) = E`) where at least one element is a POINTER-shaped value
// (`string`) on the self-host IR path (x86-64 + wasm). The existing
// tuple-destructure pin (self_host_tuple_destructure_ir_test) deliberately stays
// scalar `(i32, i32)` ("the confirmed-lowering shape"); these cases extend the
// coverage to mixed scalar+pointer and all-pointer tuples, plus a 3-element
// tuple — exercising the `tuple_get` element reads at pointer width for the
// string slots (and the i32 slots in the same tuple). All already lower, so no
// compiler change — an observability pin against a regression to the AST
// fallback.
//
// Each case is oracle-checked against the interpreter; results stay <= 120
// (the wasm exit-code clamp, #2908).
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
	// scalar + string element: 5 + len("hi") = 7.
	{"scalar-then-str", `var (a, s) = mk2(); return a + s.len();`, 7},
	// string-first tuple: len("abc") + 9 = 12.
	{"str-then-scalar", `var (s, n) = mkStrFirst(); return s.len() + n;`, 12},
	// three-element mixed tuple: 1 + len("xy") + 2 = 5.
	{"three-mixed", `var (a, s, b) = mk3(); return a + s.len() + b;`, 5},
	// both elements pointer-shaped: len("ab") + len("cde") = 5.
	{"two-strings", `var (p, q) = mk2str(); return p.len() + q.len();`, 5},
	// the `let` binder form with a string element: 5 + len("hi") = 7.
	{"let-scalar-str", `let (a, s) = mk2(); return a + s.len();`, 7},
	// bind only the string element (the i32 slot is still read past).
	{"str-only-use", `var (a, s) = mk2(); return s.len();`, 2},
}

func tupleDestructureStrIRSrc(mainBody string) string {
	return tupleDestructureStrIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostTupleDestructureStrIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
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
