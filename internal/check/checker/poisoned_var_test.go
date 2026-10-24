package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
)

func diagCount(err error) int {
	if err == nil {
		return 0
	}
	if es, ok := err.(diag.Errors); ok {
		return len(es)
	}
	return 1
}

func TestFailedVarInitPoisonsTheBinding(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "undefined initialiser, read once",
			src: `function main(): i32 {
    let b = nosuch();
    return b;
}`,
			want: `cannot find "nosuch" in this scope`,
		},
		{
			name: "read in every shape a value fits",
			src: `struct S { n: i32 }
function take(x: i32): i32 { return x; }
function main(): i32 {
    let b = nosuch();
    let s = S { n: b };
    let t = take(b);
    let u = b as i64;
    print(f"{b}\n");
    if (b) { return 1; }
    while (b) { }
    return b;
}`,
			want: `cannot find "nosuch" in this scope`,
		},
		{
			name: "poison transfers to the bindings that read it",
			src: `function main(): i32 {
    let b = nosuch();
    let c = b + 1;
    let d = c * 2;
    return d;
}`,
			want: `cannot find "nosuch" in this scope`,
		},
		{
			name: "captured by a local function and a lambda",
			src: `function main(): i32 {
    let b = nosuch();
    function inner(): i32 { return b; }
    let f = (x: i32) => x + b;
    return inner();
}`,
			want: `cannot find "nosuch" in this scope`,
		},
		{
			name: "un-annotated empty array",
			src: `function main(): i32 {
    let xs = [];
    return xs.len();
}`,
			want: "empty array literal needs a type annotation",
		},
		{
			name: "array literal whose elements failed to check",
			src: `function main(): i32 {
    let xs = [nosuch(), 1];
    return xs.len();
}`,
			want: `cannot find "nosuch" in this scope`,
		},
		{
			name: "failed arithmetic, then assigned and passed",
			src: `function f(s: string): i32 { return s.len(); }
function main(): i32 {
    let b = 1 + "x";
    let c: string = b;
    return f(b);
}`,
			want: `operator "+" requires an integer type`,
		},
		{
			name: "failed arithmetic, then compared and multiplied",
			src: `function main(): i32 {
    let b = 2 * "x";
    let c: i32 = b * 2;
    if (b > 3) { return c; }
    return b;
}`,
			want: `operator "*" requires an integer type`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, tc.src)
			if err == nil {
				t.Fatalf("expected a diagnostic for:\n%s", tc.src)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want a diagnostic containing %q, got: %v", tc.want, err)
			}
			if n := diagCount(err); n != 1 {
				t.Errorf("want exactly 1 diagnostic, got %d:\n%v", n, err)
			}
		})
	}
}

func TestFailedDestructurePoisonsEveryName(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "tuple pattern, undefined initialiser",
			src: `function main(): i32 {
    let (a, b) = nosuch();
    return a + b;
}`,
			want: `cannot find "nosuch" in this scope`,
		},
		{
			name: "tuple pattern, init is not a tuple",
			src: `function main(): i32 {
    let (a, b) = 5;
    return a + b;
}`,
			want: "tuple destructure needs a tuple expression",
		},
		{
			name: "tuple pattern, arity mismatch",
			src: `function pair(): (i32, i32) { return (1, 2); }
function main(): i32 {
    let (a, b, c) = pair();
    return a + b + c;
}`,
			want: "tuple has 2 elements, but 3 names given",
		},
		{
			name: "struct pattern, undefined initialiser",
			src: `struct S { n: i32 }
function main(): i32 {
    let S { n } = nosuch();
    return n;
}`,
			want: `cannot find "nosuch" in this scope`,
		},
		{
			name: "struct pattern, unknown field",
			src: `struct S { n: i32 }
function mk(): S { return S { n: 1 }; }
function main(): i32 {
    let S { q } = mk();
    return q;
}`,
			want: `struct S has no field "q"`,
		},
		{
			name: "struct pattern names another struct",
			src: `struct S { n: i32 }
struct T { n: i32 }
function mk(): S { return S { n: 1 }; }
function main(): i32 {
    let T { n } = mk();
    return n;
}`,
			want: "struct destructure pattern names T, but the expression is S",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, tc.src)
			if err == nil {
				t.Fatalf("expected a diagnostic for:\n%s", tc.src)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want a diagnostic containing %q, got: %v", tc.want, err)
			}
			if n := diagCount(err); n != 1 {
				t.Errorf("want exactly 1 diagnostic, got %d:\n%v", n, err)
			}
		})
	}
}

func TestPoisonDoesNotSwallowIndependentErrors(t *testing.T) {
	err := checkSource(t, `function main(): i32 {
    let b = nosuch();
    let d = alsonosuch();
    let e: i32 = "hi";
    return 0;
}`)
	if err == nil {
		t.Fatal("expected diagnostics")
	}
	if n := diagCount(err); n != 3 {
		t.Fatalf("want 3 diagnostics, got %d:\n%v", n, err)
	}
	for _, w := range []string{`"nosuch"`, `"alsonosuch"`, "expected i32, got string"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("missing %q, got: %v", w, err)
		}
	}
}

func TestPoisonedBindingStillCollidesOnRedeclaration(t *testing.T) {
	err := checkSource(t, `function main(): i32 {
    let b = nosuch();
    let b: i32 = 1;
    return b;
}`)
	if err == nil {
		t.Fatal("expected diagnostics")
	}
	if !strings.Contains(err.Error(), `variable "b" already declared in this scope`) {
		t.Errorf("want E013 for the redeclaration, got: %v", err)
	}
}
