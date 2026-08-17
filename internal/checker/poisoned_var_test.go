package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/diag"
)

// diagCount returns how many separate diagnostics err carries.
func diagCount(err error) int {
	if err == nil {
		return 0
	}
	if es, ok := err.(diag.Errors); ok {
		return len(es)
	}
	return 1
}

// A `var` whose initialiser fails to check keeps its name in scope with
// an erroneous type, so the one real error is the only one reported
// (#5317). Dropping the binding — what the recovery path used to do —
// turned a single typo into an E001 per later use, which is how a
// three-line function reported four errors.
//
// The self-hosted checker already binds on this path (`check_stmt`'s
// StmtVar arm binds `t` whatever it is, unknown included); this is the
// native side catching up.
func TestFailedVarInitPoisonsTheBinding(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "undefined initialiser, read once",
			src: `function main(): i32 {
    var b = nosuch();
    return b;
}`,
			want: `undefined identifier "nosuch"`,
		},
		{
			name: "read in every shape a value fits",
			src: `struct S { n: i32 }
function take(x: i32): i32 { return x; }
function main(): i32 {
    var b = nosuch();
    var s = S { n: b };
    var t = take(b);
    var u = b as i64;
    print(f"{b}\n");
    if (b) { return 1; }
    while (b) { }
    return b;
}`,
			want: `undefined identifier "nosuch"`,
		},
		{
			name: "poison transfers to the bindings that read it",
			src: `function main(): i32 {
    var b = nosuch();
    var c = b + 1;
    var d = c * 2;
    return d;
}`,
			want: `undefined identifier "nosuch"`,
		},
		{
			// A capture records the name's type in the enclosing
			// function's capture sink, so the poisoned type travels the
			// captureChain — the one path where a nil type reaches code
			// that never sees a checkExpr result.
			name: "captured by a local function and a lambda",
			src: `function main(): i32 {
    var b = nosuch();
    function inner(): i32 { return b; }
    var f = (x: i32) => x + b;
    return inner();
}`,
			want: `undefined identifier "nosuch"`,
		},
		{
			name: "un-annotated empty array",
			src: `function main(): i32 {
    var xs = [];
    return xs.len();
}`,
			want: "empty array literal needs a type annotation",
		},
		{
			name: "array literal whose elements failed to check",
			src: `function main(): i32 {
    var xs = [nosuch(), 1];
    return xs.len();
}`,
			want: `undefined identifier "nosuch"`,
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

// A destructure binds several names at once, so dropping the group on a
// failed pattern multiplies the follow-on noise by the pattern's width.
// Every path that gives up on `let (a, b) = …` / `let S { f } = …`
// poisons the names it would have bound.
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
			want: `undefined identifier "nosuch"`,
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
			want: `undefined identifier "nosuch"`,
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

// Poisoning silences the *follow-on* reports, not independent ones: two
// bad initialisers are still two errors, and a later mistake with no
// connection to either is still reported.
func TestPoisonDoesNotSwallowIndependentErrors(t *testing.T) {
	err := checkSource(t, `function main(): i32 {
    var b = nosuch();
    var d = alsonosuch();
    var e: i32 = "hi";
    return 0;
}`)
	if err == nil {
		t.Fatal("expected diagnostics")
	}
	if n := diagCount(err); n != 3 {
		t.Fatalf("want 3 diagnostics, got %d:\n%v", n, err)
	}
	for _, w := range []string{`"nosuch"`, `"alsonosuch"`, "cannot assign string to variable of type i32"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("missing %q, got: %v", w, err)
		}
	}
}

// A poisoned name is a declared name: redeclaring it in the same scope
// is still E013.
func TestPoisonedBindingStillCollidesOnRedeclaration(t *testing.T) {
	err := checkSource(t, `function main(): i32 {
    var b = nosuch();
    var b: i32 = 1;
    return b;
}`)
	if err == nil {
		t.Fatal("expected diagnostics")
	}
	if !strings.Contains(err.Error(), `variable "b" already declared in this scope`) {
		t.Errorf("want E013 for the redeclaration, got: %v", err)
	}
}
