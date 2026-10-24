package parser

import (
	"errors"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
)

func TestAttributePlacement(t *testing.T) {
	const (
		structOrEnum = "only applies to a `struct` or `enum` declaration"
		enumOnly     = "@try only applies to an `enum` declaration"
		inlineMsg    = "@inline / @noinline only applies to a function declaration"
	)
	for _, tc := range []struct{ src, pos, msg string }{
		{"@derive(Eq)\nconst X: i32 = 1;", "2:1", "@derive " + structOrEnum},
		{"@must_consume\nconst X: i32 = 1;", "2:1", "@must_consume " + structOrEnum},
		{"@try\nconst X: i32 = 1;", "2:1", enumOnly},
		{"@must_consume\nresource R;", "2:1", "@must_consume " + structOrEnum},
		{"@try\nresource R;", "2:1", enumOnly},
		{"@inline\nresource R;", "2:1", inlineMsg},
		{"@derive(Eq)\nresource R;", "2:1", "@derive " + structOrEnum},
		{"@export(\"a:b/c\", \"d\")\nresource R;", "2:1", "@export only applies to a function declaration"},
		{"@derive(Eq)\npub use \"./helper\".{ answer };", "2:5", "@derive " + structOrEnum},
		{"@try\nstruct S { a: i32 }\nfunction main(): void {}", "2:1", enumOnly},
		{"@try\npub function f(): void {}", "2:5", enumOnly},
		{"@inline\nstruct S { a: i32 }", "2:1", inlineMsg},
		{"@derive(Eq)\n@must_consume\nstruct S { a: i32 }", "2:1", "@derive " + structOrEnum},
		{"@derive(Eq)", "2:1", "@derive " + structOrEnum},
		{"pub @derive(Eq) struct S { a: i32 }", "1:1", "`pub` must be followed by `function`, `struct`, `enum`, `type`, `trait`, `const`, `opaque`, `fip`, `fbip`, or `async`"},
		{"pub(foo) function f(): void {}", "1:1", "`pub(...)` only supports `pub(package)`"},
		{"@frob\nstruct S { a: i32 }", "1:1", "unknown attribute @frob (only @derive, @import, @export, @must_consume, @try, @inline, and @noinline are supported)"},
	} {
		_, err := Parse(tc.src + "\n")
		var batch diag.Errors
		if !errors.As(err, &batch) || len(batch) == 0 {
			t.Errorf("%q: want a parse error, got %v", tc.src, err)
			continue
		}
		var first *Error
		if !errors.As(batch[0], &first) {
			t.Errorf("%q: first error is %T, want *Error", tc.src, batch[0])
			continue
		}
		if first.Pos.String() != tc.pos || first.Msg != tc.msg || first.ErrCode != "P001" {
			t.Errorf("%q: got %s [%s] %q, want %s [P001] %q", tc.src, first.Pos, first.ErrCode, first.Msg, tc.pos, tc.msg)
		}
	}
}

func TestAttributeAppliesToItsDeclaration(t *testing.T) {
	prog, err := Parse(`@derive(Eq)
pub opaque struct A { x: i32 }
@must_consume
struct B { x: i32 }
@try
enum C { Good(i32), Bad(string) }
@must_consume
pub enum D { X, Y }
@import("a:b/c", "r")
resource R;
@import("a:b/c", "f")
function f(): void;
@export("a:b/c", "g")
function g(): void {}
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Structs) != 2 || len(prog.Structs[0].Derives) != 1 || !prog.Structs[0].Opaque || !prog.Structs[0].Public || !prog.Structs[1].MustConsume {
		t.Errorf("structs not stamped: %+v", prog.Structs)
	}
	if len(prog.Enums) != 2 || !prog.Enums[0].Try || !prog.Enums[1].MustConsume || !prog.Enums[1].Public {
		t.Errorf("enums not stamped: %+v", prog.Enums)
	}
	if len(prog.Resources) != 1 || prog.Resources[0].ImportWITName != "r" {
		t.Errorf("resource not bound: %+v", prog.Resources)
	}
	if len(prog.Funcs) != 2 || prog.Funcs[0].ImportWITName != "f" || prog.Funcs[1].ExportWITName != "g" {
		t.Errorf("functions not bound: %+v", prog.Funcs)
	}
}
