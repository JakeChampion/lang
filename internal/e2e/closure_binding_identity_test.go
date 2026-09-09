package e2e

import "testing"

func TestClosureBindingIdentityDifferential(t *testing.T) {
	cases := []struct{ name, src string }{
		{"later-local", `function value(): i32 {
var n = 7; var call = (): i32 => {
var inner = (): i32 => n; var n = 99; return inner(); }; return call(); }`},
		{"local-function", `function value(): i32 {
var n = 7; if (true) { function inner(): i32 { return n; }
var n = 99; return inner(); } return 99; }`},
		{"escaping-array", `function make(xs: i32[]): () => i32[] {
return (): i32[] => xs; }
function value(): i32 { var xs = [1]; var f = make(xs); xs = xs.with(0, 9);
var original = f(); return original[0] * 10 + xs[0]; }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertNumProgramAgrees(t, `import "std/i32";`+tc.src+`
function main(): i32 { print(value().to_string()); return 0; }`)
		})
	}
}
