package parser

import (
	"strings"
	"testing"
)

// `fip` / `fbip` are contextual modifiers (`fip function …` / `pub fbip
// function …`, optionally graded `fip(2) function …`) that stamp
// FuncDecl.Fip / .Fbip / .FipAllowance — the checker then verifies the shape
// rules (E053) and the IR verifies the emitted allocation budget (E068).
// Because they are contextual (only consumed when the whole modifier shape
// directly precedes `function`), both stay usable as ordinary identifiers
// everywhere else.

func TestParseFipModifier(t *testing.T) {
	prog, err := Parse(`fip function f(own a: i32[]): i32[] { return a; }
pub fip function g(x: i32): i32 { return x; }
function h(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string]struct{ fip, pub bool }{
		"f": {true, false},
		"g": {true, true},
		"h": {false, false},
	}
	for _, fn := range prog.Funcs {
		w, ok := want[fn.Name]
		if !ok {
			continue
		}
		if fn.Fip != w.fip {
			t.Errorf("%s: Fip = %v, want %v", fn.Name, fn.Fip, w.fip)
		}
		if fn.Public != w.pub {
			t.Errorf("%s: Public = %v, want %v", fn.Name, fn.Public, w.pub)
		}
	}
}

func TestFipUsableAsIdentifier(t *testing.T) {
	// `fip` is contextual: as a local variable / parameter name it must still
	// parse fine (no keyword reservation).
	if _, err := Parse(`function f(): i32 { var fip: i32 = 3; return fip + 1; }`); err != nil {
		t.Errorf("`fip` as a local name should parse: %v", err)
	}
}

func TestParseFbipModifier(t *testing.T) {
	prog, err := Parse(`fbip function f(own a: i32[]): i32[] { return a; }
pub fbip function g(x: i32): i32 { return x; }
function h(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string]struct{ fbip, pub bool }{
		"f": {true, false},
		"g": {true, true},
		"h": {false, false},
	}
	for _, fn := range prog.Funcs {
		w, ok := want[fn.Name]
		if !ok {
			continue
		}
		if fn.Fbip != w.fbip {
			t.Errorf("%s: Fbip = %v, want %v", fn.Name, fn.Fbip, w.fbip)
		}
		if fn.Fip {
			t.Errorf("%s: Fip = true, want false (fbip is not fip)", fn.Name)
		}
		if fn.Public != w.pub {
			t.Errorf("%s: Public = %v, want %v", fn.Name, fn.Public, w.pub)
		}
	}
}

// Graded forms: `fip(n)` / `fbip(n)` parse the allowance into
// FuncDecl.FipAllowance; the bare forms stay allowance 0.
func TestParseGradedFipAllowance(t *testing.T) {
	prog, err := Parse(`fip(2) function f(x: i32): i32 { return x; }
pub fbip(1) function g(x: i32): i32 { return x; }
fip function h(x: i32): i32 { return x; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string]struct {
		fip, fbip bool
		allowance int
	}{
		"f": {true, false, 2},
		"g": {false, true, 1},
		"h": {true, false, 0},
	}
	for _, fn := range prog.Funcs {
		w, ok := want[fn.Name]
		if !ok {
			continue
		}
		if fn.Fip != w.fip || fn.Fbip != w.fbip || fn.FipAllowance != w.allowance {
			t.Errorf("%s: (Fip, Fbip, FipAllowance) = (%v, %v, %d), want (%v, %v, %d)",
				fn.Name, fn.Fip, fn.Fbip, fn.FipAllowance, w.fip, w.fbip, w.allowance)
		}
	}
}

// `fip` and `fbip` are mutually exclusive — combining them (either order,
// graded or not) is a parse error.
func TestParseFipFbipMutuallyExclusive(t *testing.T) {
	for _, src := range []string{
		`fip fbip function f(): i32 { return 0; }`,
		`fbip fip function f(): i32 { return 0; }`,
		`fip(1) fbip function f(): i32 { return 0; }`,
	} {
		_, err := Parse(src)
		if err == nil {
			t.Errorf("expected fip+fbip conflict error for %q, got none", src)
			continue
		}
		if !strings.Contains(err.Error(), "not both") {
			t.Errorf("expected the `fip` or `fbip`, not both error for %q, got: %v", src, err)
		}
	}
}

func TestFbipUsableAsIdentifier(t *testing.T) {
	// `fbip` is contextual: as a function / local name it must still parse
	// (no keyword reservation), including a CALL `fbip(2)` that looks like
	// the graded modifier shape but is not followed by `function`.
	if _, err := Parse(`function fbip(x: i32): i32 { return x + 1; }
function f(): i32 { var fbip: i32 = 3; return fbip + 1; }
function main(): i32 { return fbip(2); }`); err != nil {
		t.Errorf("`fbip` as an ordinary identifier should parse: %v", err)
	}
}
