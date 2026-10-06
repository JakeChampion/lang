package e2e

import "testing"

func TestClosureBindingIdentityDifferential(t *testing.T) {
	cases := []struct{ name, src string }{
		{"later-local", `function value(): i32 {
let n = 7; let call = (): i32 => {
let inner = (): i32 => n; let n = 99; return inner(); }; return call(); }`},
		{"local-function", `function value(): i32 {
let n = 7; if (true) { function inner(): i32 { return n; }
let n = 99; return inner(); } return 99; }`},
		{"escaping-array", `function make(xs: i32[]): () => i32[] {
return (): i32[] => xs; }
function value(): i32 { let xs = [1]; let f = make(xs); xs = xs.with(0, 9);
let original = f(); return original[0] * 10 + xs[0]; }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertNumProgramAgrees(t, `import "std/i32";`+tc.src+`
function main(): i32 { print(value().to_string()); return 0; }`)
		})
	}
}
