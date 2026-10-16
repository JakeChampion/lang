package checker

import "testing"

func TestBoundDefaultMethodTypeArguments(t *testing.T) {
	for _, tc := range []struct{ name, body, codes string }{
		{"inferred", `let x: Option[string] = c.wrap("ok"); return 0;`, ""},
		{"written", `let x: Option[string] = c.wrap[string]("ok"); return 0;`, ""},
		{"argument", `let x = c.wrap[string](2); return 0;`, "E038"},
		{"arity", `let x = c.wrap[string, i32](2); return 0;`, "E040,E038"},
		{"result", `let x: string = c.wrap(2); return 0;`, "E003"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, `trait Cfg { function wrap[U](self: Self, x: U): Option[U] { return Some(x); } }
function f[P: Cfg](c: P): i32 { `+tc.body+` }
function main(): i32 { return 0; }`)
			if got := errCodes(err); got != tc.codes {
				t.Fatalf("codes %q, want %q: %v", got, tc.codes, err)
			}
		})
	}
	// The method's inference must not make the enclosing receiver concrete.
	err := checkSource(t, `trait Cfg { function keep[U](self: Self, x: U): Self { return self; } }
function f[P: Cfg](c: P): string { return c.keep(2); }
function main(): i32 { return 0; }`)
	if got := errCodes(err); got != "E002" {
		t.Fatalf("enclosing type parameter was rebound: %q: %v", got, err)
	}
}
