package parser

import "testing"

func TestTrailingCommaAcceptedInEveryListPosition(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"array literal", `function main(): i32 { let xs: i32[] = [1, 2,]; return xs.len(); }`},
		{"nested array literal", `function main(): i32 { let xs: i32[][] = [[1,], [2, 3,],]; return xs.len(); }`},
		{"call arguments", `function f(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return f(1, 2,); }`},
		{"named call arguments", `function f(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return f(a = 1, b = 2,); }`},
		{"function parameters", `function f(a: i32, b: i32,): i32 { return a + b; }
function main(): i32 { return f(1, 2); }`},
		{"lambda parameters", `function main(): i32 {
    let f: (i32, i32) => i32 = (a: i32, b: i32,) => a + b;
    return f(1, 2,);
}`},
		{"function-keyword lambda parameters", `function main(): i32 {
    let f: (i32) => i32 = (a: i32,): i32 => { return a; };
    return f(1,);
}`},
		{"type parameters", `function id[T,](x: T): T { return x; }
function main(): i32 { return id(1); }`},
		{"struct type parameters", `struct Pair[A, B,] { a: A, b: B }
function main(): i32 { let p: Pair[i32, i32] = Pair { a: 1, b: 2 }; return p.a; }`},
		{"enum type parameters", `enum E[A, B,] { X(A), Y(B) }
function main(): i32 { return 0; }`},
		{"variant payload", `enum E { X(i32, i32,), Y }
function main(): i32 { return 0; }`},
		{"generic type arguments", `enum E[A, B] { X(A), Y(B) }
function main(): i32 { let p: E[i32, i32,] = X(1); return 0; }`},
		{"call type arguments", `function id[T](x: T): T { return x; }
function main(): i32 { return id[i32,](7,); }`},
		{"struct literal", `struct P { x: i32, y: i32 }
function main(): i32 { let p: P = P { x: 1, y: 2, }; return p.x; }`},
		{"struct declaration", `struct P { x: i32, y: i32, }
function main(): i32 { let p: P = P { x: 1, y: 2 }; return p.x; }`},
		{"enum declaration", `enum E { A(i32), B, }
function main(): i32 { match (A(1)) { A(n) => { return n; }, B => { return 0; }, } }`},
		{"match arms", `enum E { A(i32), B }
function main(): i32 { match (A(1)) { A(n) => { return n; }, B => { return 0; }, } }`},
		{"tuple literal", `function main(): i32 { let t: (i32, i32) = (1, 2,); return t.0; }`},
		{"map foreach binder", `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = Map { "a": 1 };
    let n: i32 = 0;
    for (k, v,) in m { n = n + v; }
    return n;
}`},
		{"tuple parameter", `function f((a, b,): (i32, i32)): i32 { return a + b; }
function main(): i32 { return f((1, 2)); }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.src); err != nil {
				t.Fatalf("trailing comma rejected in %s: %v", tc.name, err)
			}
		})
	}
}

func TestCStyleForStillParses(t *testing.T) {
	cases := []string{
		`function main(): i32 { let t: i32 = 0; for (let i: i32 = 0; i < 3; i = i + 1) { t = t + i; } return t; }`,
		`function main(): i32 { let i: i32 = 0; let t: i32 = 0; for (; i < 3; i = i + 1) { t = t + i; } return t; }`,
		`function main(): i32 { let i: i32 = 0; for (let j: i32 = 0; i < 3; ) { i = i + 1; } return i; }`,
	}
	for _, src := range cases {
		if _, err := Parse(src); err != nil {
			t.Fatalf("C-style for rejected: %v\nsrc: %s", err, src)
		}
	}
}

func TestEmptyListsStillParse(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"empty array", `function main(): i32 { let xs: i32[] = []; return xs.len(); }`},
		{"empty call", `function f(): i32 { return 3; }
function main(): i32 { return f(); }`},
		{"empty params", `function f(): i32 { return 3; }
function main(): i32 { return f(); }`},
		{"unit literal", `function main(): i32 { let u: void = (); return 0; }`},
		{"empty struct literal", `struct P { }
function main(): i32 { let p: P = P { }; return 0; }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.src); err != nil {
				t.Fatalf("empty list rejected in %s: %v", tc.name, err)
			}
		})
	}
}

func TestStrayCommasStillRejected(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"lone comma in array", `function main(): i32 { let xs: i32[] = [,]; return xs.len(); }`},
		{"doubled comma in array", `function main(): i32 { let xs: i32[] = [1,,]; return xs.len(); }`},
		{"leading comma in array", `function main(): i32 { let xs: i32[] = [,1]; return xs.len(); }`},
		{"lone comma in call", `function f(a: i32): i32 { return a; }
function main(): i32 { return f(,); }`},
		{"doubled comma in call", `function f(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return f(1,,2); }`},
		{"doubled comma in params", `function f(a: i32,,b: i32): i32 { return a + b; }
function main(): i32 { return f(1, 2); }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.src); err == nil {
				t.Fatalf("stray comma accepted in %s — a trailing comma is one comma AFTER an element", tc.name)
			}
		})
	}
}
