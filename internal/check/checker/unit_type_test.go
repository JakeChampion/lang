package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestUnitValueChecks(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"void type argument, unit payload",
			`function f(): Result[void, IoError] { return Ok(()); }
			 function main(): i32 { return 0; }`},
		{"? propagates through a unit Result",
			`function f(): Result[void, IoError] { return Ok(()); }
			 function g(): Result[void, IoError] { f()?; return Ok(()); }
			 function main(): i32 { return 0; }`},
		{"unit in an Option payload",
			`function f(): Option[void] { return Some(()); }
			 function main(): i32 { return 0; }`},
		{"bound to a variable",
			`function main(): i32 { let u = (); return 0; }`},
		{"unit binding as a payload",
			`function main(): i32 { let u = (); let o: Option[void] = Some(u); return 0; }`},
		{"matched unit as a payload",
			`function f(o: Option[void]): Result[void, i32] {
                 match (o) { Some(u) => { return Ok(u); }, None => { return Err(1); } }
               }
               function main(): i32 { return 0; }`},
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

func TestVoidCallIsNotAValue(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"void call as Ok payload",
			`function nothing(): void { return; }
			 function f(): Result[void, IoError] { return Ok(nothing()); }
			 function main(): i32 { return 0; }`},
		{"void call as Some payload",
			`function nothing(): void { return; }
			 function f(): Option[void] { return Some(nothing()); }
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
