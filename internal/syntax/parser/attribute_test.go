package parser

import (
	"errors"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

func TestAttributePlacement(t *testing.T) {
	const (
		structOrEnum = "only applies to a `struct` or `enum` declaration"
		enumOnly     = "@try only applies to an `enum` declaration"
		inlineMsg    = "@inline / @noinline only applies to a function declaration"
	)
	for _, tc := range []struct{ src, pos, msg string }{
		{"@value\nfunction f(): void {}", "2:1", "@value only applies to a `struct` declaration"},
		{"@value\nenum E { Empty }", "2:1", "@value only applies to a `struct` declaration"},
		{"@secret\nfunction f(): void {}", "2:1", "@secret only applies to a `struct` declaration"},
		{"@secret\nenum E { Empty }", "2:1", "@secret only applies to a `struct` declaration"},
		{"@derive(Eq)\nconst X: i32 = 1;", "2:1", "@derive " + structOrEnum},
		{"@must_consume\nconst X: i32 = 1;", "2:1", "@must_consume " + structOrEnum},
		{"@try\nconst X: i32 = 1;", "2:1", enumOnly},
		{"@must_consume\nresource R;", "2:1", "@must_consume " + structOrEnum},
		{"@try\nresource R;", "2:1", enumOnly},
		{"@inline\nresource R;", "2:1", inlineMsg},
		{"@derive(Eq)\nresource R;", "2:1", "@derive " + structOrEnum},
		{"@export(\"a:b/c\", \"d\")\nresource R;", "2:1", "@export only applies to a function declaration"},
		{"@provide(\"log\")\nstruct S { a: i32 }", "2:1", "@provide only applies to a function declaration"},
		{"@norc\nstruct S { a: i32 }", "2:1", "@norc only applies to a function declaration"},
		{"@derive(Eq)\npub use \"./helper\".{ answer };", "2:5", "@derive " + structOrEnum},
		{"@try\nstruct S { a: i32 }\nfunction main(): void {}", "2:1", enumOnly},
		{"@try\npub function f(): void {}", "2:5", enumOnly},
		{"@inline\nstruct S { a: i32 }", "2:1", inlineMsg},
		{"@value\n@try\nstruct S { a: i32 }", "3:1", enumOnly},
		{"@secret\n@secret\nstruct S { a: i32 }", "2:1", "@secret appears twice on one declaration"},
		{"@derive(Eq)\n@derive(Hash)\nstruct S { a: i32 }", "2:1", "@derive appears twice on one declaration"},
		{"@value\n@must_consume\nstruct S { a: i32 }", "2:1", "@value and @must_consume cannot apply to one declaration"},
		{"@inline\n@noinline\nfunction f(): void {}", "2:1", "@inline and @noinline cannot apply to one declaration"},
		{"@import(\"a:b/c\", \"f\")\n@export(\"a:b/c\", \"g\")\nfunction f(): void;", "2:1", "@import and @export cannot apply to one declaration"},
		{"@norc\n@early\nfunction f(): void {}", "2:1", "@norc and @early cannot apply to one declaration"},
		{"@derive(cmp.Eq)\n@must_consume\nstruct S { a: i32 }", "2:1", "@must_consume and @derive(cmp.Eq) cannot apply to one declaration, because the derived method reads a value that it does not consume"},
		{"@must_consume\n@derive(cmp.Default, cmp.Display)\nenum E { A }", "2:1", "@must_consume and @derive(cmp.Display) cannot apply to one declaration, because the derived method reads a value that it does not consume"},
		{"@derive(Eq)", "2:1", "@derive " + structOrEnum},
		{"pub @derive(Eq) struct S { a: i32 }", "1:1", "`pub` must be followed by `function`, `struct`, `enum`, `type`, `trait`, `const`, `opaque`, `fip`, `fbip`, or `async`"},
		{"pub(foo) function f(): void {}", "1:1", "`pub(...)` only supports `pub(package)`"},
		{"@frob\nstruct S { a: i32 }", "1:1", "unknown attribute @frob (only @derive, @import, @export, @jni, @callback, @provide, @norc, @interrupt, @early, @secondary, @must_consume, @value, @secret, @try, @inline, and @noinline are supported)"},
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
@provide("log")
function uart(data: usize, len: i32): void {}
@norc
function tick(n: i32): i32 { return n; }
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
	if len(prog.Funcs) != 4 || prog.Funcs[0].ImportWITName != "f" || prog.Funcs[1].ExportWITName != "g" || prog.Funcs[2].Provides != "log" || prog.Funcs[1].Provides != "" || !prog.Funcs[3].NoRC || prog.Funcs[2].NoRC {
		t.Errorf("functions not bound: %+v", prog.Funcs)
	}
}

func TestValueAttributeDeclaration(t *testing.T) {
	prog, err := Parse("@value\npub opaque struct Key { bytes: u8[] }\nstruct Plain { n: i32 }\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Structs) != 2 || !prog.Structs[0].ValueType || !prog.Structs[0].Public || !prog.Structs[0].Opaque || prog.Structs[1].ValueType {
		t.Fatalf("value declaration lost its metadata: %+v", prog.Structs)
	}
}

func TestSecretAttributeDeclaration(t *testing.T) {
	prog, err := Parse("@secret\npub struct Key { bytes: u8[] }\nstruct Plain { n: i32 }\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Structs) != 2 || !prog.Structs[0].Secret || !prog.Structs[0].Public || prog.Structs[1].Secret {
		t.Fatalf("secret declaration lost its metadata: %+v", prog.Structs)
	}
}

func TestStackedAttributes(t *testing.T) {
	prog, err := Parse(`@value
@secret
pub struct Key { bytes: u8[] }
@derive(cmp.Default)
@must_consume
struct B { x: i32 }
@must_consume
@try
enum C { Good(i32), Bad(string) }
@noinline
@export("a:b/c", "g")
function g(): void {}
@provide("log")
@norc
function uart(data: usize, len: i32): void {}
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Structs) != 2 || !prog.Structs[0].ValueType || !prog.Structs[0].Secret || !prog.Structs[0].Public {
		t.Errorf("@value @secret struct lost an attribute: %+v", prog.Structs)
	}
	if len(prog.Structs) == 2 && (len(prog.Structs[1].Derives) != 1 || !prog.Structs[1].MustConsume) {
		t.Errorf("@derive @must_consume struct lost an attribute: %+v", prog.Structs[1])
	}
	if len(prog.Enums) != 1 || !prog.Enums[0].MustConsume || !prog.Enums[0].Try {
		t.Errorf("@must_consume @try enum lost an attribute: %+v", prog.Enums)
	}
	if len(prog.Funcs) != 2 || prog.Funcs[0].ExportWITName != "g" || prog.Funcs[0].InlineHint != ast.InlineHintNever || prog.Funcs[1].Provides != "log" || !prog.Funcs[1].NoRC {
		t.Errorf("stacked function attributes lost one: %+v", prog.Funcs)
	}
}
