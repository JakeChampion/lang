package e2ecompiler

import "testing"

// patternHeadCases are two pattern heads native parses: an `if let` whose body
// is a statement rather than a block, as a plain `if`'s may be, and a
// `let … else` whose pattern names its variant through its enum. Every answer
// comes from the interpreter.
var patternHeadCases = []struct {
	name string
	src  string
}{
	{"if_let_braceless_then", `enum Box { Full(i32), Empty }
function main(): i32 {
  let b: Box = Full(8);
  if let Full(v) = b return v;
  return 99;
}`},
	{"let_else_qualified_variant", `enum Color { Red(i32), Blue }
function f(c: Color): i32 {
  let Color.Red(n) = c else { return 0; };
  return n;
}
function main(): i32 { return f(Red(6)); }`},
}

func TestSelfHostPatternHeadsX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range patternHeadCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src+"\n")
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostPatternHeadsWasm(t *testing.T) {
	cli := newStrictCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range patternHeadCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src+"\n")
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
