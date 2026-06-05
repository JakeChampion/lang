package parser

import "testing"

// `fip` is a contextual modifier (`fip function …` / `pub fip function …`) that
// stamps FuncDecl.Fip — the checker then verifies the allocation-free guarantee
// (E053). Because it is contextual (only consumed directly before `function`),
// `fip` stays usable as an ordinary identifier everywhere else.

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
