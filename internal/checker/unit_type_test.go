package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/parser"
)

// The unit value `()` is what makes `Result[void, E]` constructible, and
// with it the whole errors-as-values story works for operations that have
// nothing to hand back: `?` propagates, `Ok(())` is the success case, and
// the caller matches the same two arms as any other Result.
func TestUnitValueChecks(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"unit type argument, unit payload",
			`function f(): Result[(), IoError] { return Ok(()); }
			 function main(): i32 { return 0; }`},
		{"void spelling of the same type",
			`function f(): Result[void, IoError] { return Ok(()); }
			 function main(): i32 { return 0; }`},
		{"? propagates through a unit Result",
			`function f(): Result[(), IoError] { return Ok(()); }
			 function g(): Result[(), IoError] { f()?; return Ok(()); }
			 function main(): i32 { return 0; }`},
		{"unit in an Option payload",
			`function f(): Option[()] { return Some(()); }
			 function main(): i32 { return 0; }`},
		{"bound to a variable",
			`function main(): i32 { var u = (); return 0; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := Check(prog); err != nil {
				t.Errorf("checker rejected a valid program: %v", err)
			}
		})
	}
}

// E072 — a void-returning CALL is not a value. This is the hole the unit
// literal closes: `Ok(f())` for a void `f` used to type-check and then
// diverge by backend (the interpreter and native invented a zero for the
// payload slot; wasm emitted a store with nothing on the stack and failed
// module verification at load).
func TestVoidCallIsNotAValue(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"void call as Ok payload",
			`function nothing(): void { return; }
			 function f(): Result[(), IoError] { return Ok(nothing()); }
			 function main(): i32 { return 0; }`},
		{"void call as Some payload",
			`function nothing(): void { return; }
			 function f(): Option[()] { return Some(nothing()); }
			 function main(): i32 { return 0; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = Check(prog)
			if err == nil {
				t.Fatalf("checked clean, want E072")
			}
			es, ok := err.(diag.Errors)
			if !ok {
				t.Fatalf("expected diag.Errors, got %T: %v", err, err)
			}
			ce, ok := es[0].(*Error)
			if !ok {
				t.Fatalf("expected *Error, got %T", es[0])
			}
			if ce.ErrCode != "E072" {
				t.Errorf("code = %q, want E072 (%v)", ce.ErrCode, err)
			}
		})
	}
}
