package parser

import "testing"

// `async` is a contextual modifier (`async function …` / `pub async function
// …`) that stamps FuncDecl.Async — the WASI Preview-3 component-model-async
// export surface (docs/WASI-PREVIEW3-ASYNC-PLAN.md). Because it is contextual
// (consumed only directly before `function`), `async` stays usable as an
// ordinary identifier everywhere else.

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
	// `async` is contextual: as a local / parameter name it must still parse.
	if _, err := Parse(`function f(): i32 { var async: i32 = 3; return async + 1; }`); err != nil {
		t.Errorf("`async` as a local name should parse: %v", err)
	}
}
