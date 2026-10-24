package parser

import "testing"

func TestParseAsyncModifier(t *testing.T) {
	prog, err := Parse(`async function compute(): i32 { return 42; }
pub async function run(): i32 { return 1; }
function plain(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string]struct{ async, pub bool }{
		"compute": {true, false},
		"run":     {true, true},
		"plain":   {false, false},
	}
	for _, fn := range prog.Funcs {
		w, ok := want[fn.Name]
		if !ok {
			continue
		}
		if fn.Async != w.async {
			t.Errorf("%s: Async = %v, want %v", fn.Name, fn.Async, w.async)
		}
		if fn.Public != w.pub {
			t.Errorf("%s: Public = %v, want %v", fn.Name, fn.Public, w.pub)
		}
	}
}

func TestAsyncUsableAsIdentifier(t *testing.T) {
	if _, err := Parse(`function f(): i32 { let async: i32 = 3; return async + 1; }`); err != nil {
		t.Errorf("`async` as a local name should parse: %v", err)
	}
}
