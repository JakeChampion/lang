package parser

import "testing"

// Trailing commas are legal in EVERY comma-separated element list, and the
// rule is enforced in one place (`moreElems`) rather than re-decided per
// list. Before that helper, five of the eleven list loops spelled the
// separator as a bare `accept(",")` and rejected a trailing comma while the
// other six accepted one — so `P { x: 1, }` compiled and `[1, ]` did not,
// with a diagnostic that pointed at the closing bracket rather than at the
// comma.
//
// Worse, the two compilers disagreed about WHICH half was which:
// `function f(a: i32,)` parsed under the self-host compiler and was rejected
// natively. The self-host mirror lives in `examples/self_host/parser.fern`
// (`(p: Par) more_elems`); this table and the self-host leg must stay in
// step, because a difference here decides a program's legality by which
// compiler reads it.
//
// A case per list position. Adding a new comma-separated list to the
// grammar means adding a row here.
func TestTrailingCommaAcceptedInEveryListPosition(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"array literal", `function main(): i32 { var xs: i32[] = [1, 2,]; return xs.len(); }`},
		{"nested array literal", `function main(): i32 { var xs: i32[][] = [[1,], [2, 3,],]; return xs.len(); }`},
		{"call arguments", `function f(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return f(1, 2,); }`},
		{"named call arguments", `function f(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return f(a = 1, b = 2,); }`},
		{"function parameters", `function f(a: i32, b: i32,): i32 { return a + b; }
function main(): i32 { return f(1, 2); }`},
		{"lambda parameters", `function main(): i32 {
    var f: (i32, i32) => i32 = (a: i32, b: i32,) => a + b;
    return f(1, 2,);
}`},
		{"function-keyword lambda parameters", `function main(): i32 {
    var f: (i32) => i32 = function (a: i32,): i32 { return a; };
    return f(1,);
}`},
		{"type parameters", `function id[T,](x: T): T { return x; }
function main(): i32 { return id(1); }`},
		{"generic type arguments", `enum E[A, B] { X(A), Y(B) }
function main(): i32 { var p: E[i32, i32,] = X(1); return 0; }`},
		{"call type arguments", `function id[T](x: T): T { return x; }
function main(): i32 { return id[i32,](7,); }`},
		{"struct literal", `struct P { x: i32, y: i32 }
function main(): i32 { var p: P = P { x: 1, y: 2, }; return p.x; }`},
		{"struct declaration", `struct P { x: i32, y: i32, }
function main(): i32 { var p: P = P { x: 1, y: 2 }; return p.x; }`},
		{"enum declaration", `enum E { A(i32), B, }
function main(): i32 { match (A(1)) { A(n) => { return n; }, B => { return 0; }, } }`},
		{"match arms", `enum E { A(i32), B }
function main(): i32 { match (A(1)) { A(n) => { return n; }, B => { return 0; }, } }`},
		{"tuple literal", `function main(): i32 { var t: (i32, i32) = (1, 2,); return t.0; }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.src); err != nil {
				t.Fatalf("trailing comma rejected in %s: %v", tc.name, err)
			}
		})
	}
}

// The empty forms must not be collateral damage: `moreElems` is only reached
// after an element has been parsed, so `[]` / `f()` / `()` still take the
// zero-element path.
func TestEmptyListsStillParse(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"empty array", `function main(): i32 { var xs: i32[] = []; return xs.len(); }`},
		{"empty call", `function f(): i32 { return 3; }
function main(): i32 { return f(); }`},
		{"empty params", `function f(): i32 { return 3; }
function main(): i32 { return f(); }`},
		{"unit literal", `function main(): i32 { var u: void = (); return 0; }`},
		{"empty struct literal", `struct P { }
function main(): i32 { var p: P = P { }; return 0; }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.src); err != nil {
				t.Fatalf("empty list rejected in %s: %v", tc.name, err)
			}
		})
	}
}

// A trailing comma is one comma after an ELEMENT. A leading comma, a doubled
// comma, or a comma alone is still a parse error — accepting a trailing one
// must not degrade into skipping commas entirely.
func TestStrayCommasStillRejected(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"lone comma in array", `function main(): i32 { var xs: i32[] = [,]; return xs.len(); }`},
		{"doubled comma in array", `function main(): i32 { var xs: i32[] = [1,,]; return xs.len(); }`},
		{"leading comma in array", `function main(): i32 { var xs: i32[] = [,1]; return xs.len(); }`},
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
