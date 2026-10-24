package e2ecompiler

import "testing"

var namedArgsIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"trailing-named",
		`function f(a: i32, b: i32 = 2, c: i32 = 3): i32 { return a * 100 + b * 10 + c; } function main(): i32 { return f(7, c = 9) - 700; }`, 29},
	{"reorder-all-named",
		`function f(a: i32, b: i32, c: i32): i32 { return a * 100 + b * 10 + c; } function main(): i32 { return f(c = 3, a = 1, b = 2); }`, 123},
	{"skip-defaulted-middle",
		`function g(p: i32, h: i32 = 5, b: i32 = 7): i32 { return p + h * 10 + b; } function main(): i32 { return g(3, b = 9); }`, 62},
	{"override-default",
		`function g(p: i32, h: i32 = 5, b: i32 = 7): i32 { return p + h * 10 + b; } function main(): i32 { return g(1, h = 2); }`, 28},
	{"string-param-named",
		`function mk(prefix: string, n: i32 = 0): i32 { return prefix.len() + n; } function main(): i32 { return mk(n = 5, prefix = "abc"); }`, 8},
}

func TestSelfHostNamedArgsIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range namedArgsIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}

var shadowedCalleeCases = []struct {
	name     string
	src      string
	expected int
}{
	{"let-closure",
		`function f(a: i32, b: i32 = 0): i32 { return a + b; } function main(): i32 { let f = (n: i32) => n + 7; return f(0); }`, 7},
	{"scopes",
		`function f(a: i32, b: i32 = 5): i32 { return a + b; }
function g(f: (i32) => i32): i32 { return f(1); }
function main(): i32 {
  let x: i32 = f(1);
  if (x > 0) { let f = (n: i32) => n * 10; x = x + f(2); }
  for k in [1, 2] { x = x + f(k); }
  return x + f(3) + g((n: i32) => n + 30);
}`, 78},
	{"local-function",
		`function f(a: i32, b: i32 = 5): i32 { return a + b; } function main(): i32 { function f(n: i32): i32 { return n * 2; } return f(4); }`, 8},
	{"match-binder",
		`function f(a: i32, b: i32 = 5): i32 { return a + b; } function main(): i32 { let o: Option[(i32) => i32] = Some((n: i32) => n + 1); match (o) { Some(f) => { return f(1); }, None => { return 0; } } }`, 2},
}

func TestSelfHostShadowedCalleeGetsNoDefaults(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range shadowedCalleeCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.expected {
					t.Errorf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
			})
		}
	}
}
