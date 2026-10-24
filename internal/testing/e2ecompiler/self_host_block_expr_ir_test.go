package e2ecompiler

import (
	"strings"
	"testing"
)

var blockExprIRCases = []struct {
	name string
	main string
}{
	{"if-then-block", `function f(e: i32): i32 { let x: i32 = if (e > 0) { let k = e + 1; k } else { 0 }; return x; }
function main(): i32 { return f(5); }`},
	{"if-else-block", `function f(e: i32): i32 { let x: i32 = if (e > 0) { e } else { let j = 3 + 4; j }; return x; }
function main(): i32 { return f(0); }`},
	{"if-then-multi-stmt", `function main(): i32 { let x: i32 = if (true) { let a = 2; let b = a * 5; a + b } else { 0 }; return x; }`},
	{"if-then-while", `function main(): i32 { let x: i32 = if (true) { let s = 0; let i = 1; while (i <= 3) { s = s + i; i = i + 1; } s } else { 0 }; return x; }`},
	{"match-arm-block", `enum E { A(i32), B } function f(e: E): i32 { let x: i32 = match (e) { A(n) => { let d = n + 1; d }, B => 9 }; return x; }
function main(): i32 { return f(A(20)) + f(B); }`},
	{"litmatch-arm-block", `function f(tag: i32): i32 { let x: i32 = match (tag) { 0 => { let s = 10 + 5; s }, _ => 99 }; return x; }
function main(): i32 { return f(0); }`},
	{"nested-if-tail", `function f(c: i32): i32 { let x: i32 = if (c > 0) { let a = c * 2; if (a > 5) { a } else { 1 } } else { 0 }; return x; }
function main(): i32 { return f(3); }`},
	{"else-if-chain", `function f(n: i32): i32 { let x: i32 = if (n > 10) { 30 } else if (n > 0) { 20 } else { 10 }; return x; }
function main(): i32 { return f(5); }`},
	{"single-expr-regress", `function f(e: i32): i32 { let x: i32 = if (e > 0) { e + 1 } else { 0 }; return x; }
function main(): i32 { return f(5); }`},
	{"if-then-string-tail", `function f(): i32 { let s: string = if (true) { let p = "hi"; p } else { "x" }; return s.len(); }
function main(): i32 { return f(); }`},
	{"value-position-var-rhs", `function main(): i32 { let n: i32 = { let k = 3; let m = 4; k * m }; return n; }`},
	{"value-position-call-arg", `function id(x: i32): i32 { return x; } function main(): i32 { return id({ let a = 40; a + 2 }); }`},
	{"value-position-single-expr", `function main(): i32 { let n: i32 = { 7 }; return n + 1; }`},
	{"value-position-string-tail", `function main(): i32 { let s: string = { let a = "foo"; let b = "bar"; a + b }; return s.len(); }`},
	{"cf-conditional-return-tail", `function f(e: i32): i32 { let x: i32 = { if (e < 0) { return 99; } e + 1 }; return x; }
function main(): i32 { return f(5); }`},
	{"cf-conditional-return-taken", `function f(e: i32): i32 { let x: i32 = { if (e < 0) { return 99; } e + 1 }; return x; }
function main(): i32 { return f(-1); }`},
	{"cf-break-in-block", `function main(): i32 {
	let s: i32 = 0; let i: i32 = 0;
	while (i < 10) { i = i + 1; let d: i32 = { if (i == 4) { break; } i }; s = s + d; }
	return s;
}`},
	{"nested-block-tail", `function f(e: i32): i32 { let x: i32 = if (e > 0) { let k = e; { let m = k * 2; m + 1 } } else { { 2 } }; return x; }
function main(): i32 { return f(3) + f(0 - 1); }`},
	{"nested-block-tail-arms", `enum E { A, B }
function pick(e: E): i32 { return match (e) { E.A => { { 10 } }, E.B => { let t = 2; { { t + 20 } } } }; }
function main(): i32 { let f = (n: i32): i32 => { { n + 1 } }; return pick(E.A) + pick(E.B) + f(4); }`},
	{"nested-block-tail-deep", "function main(): i32 { let x: i32 = if (true) " + strings.Repeat("{ ", 64) + "7" + strings.Repeat(" }", 64) + " else { 2 }; return x; }"},
	{"nested-block-statement", `function main(): i32 { let t: i32 = 1; let x: i32 = if (t > 0) { { let t = 9; } 5 + t } else { 0 }; return x; }`},
	{"cf-continue-in-block", `function main(): i32 {
	let s: i32 = 0; let i: i32 = 0;
	while (i < 6) { i = i + 1; let d: i32 = { if (i == 3) { continue; } i }; s = s + d; }
	return s;
}`},
}

func TestSelfHostBlockExprIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range blockExprIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
