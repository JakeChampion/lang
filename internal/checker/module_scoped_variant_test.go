package checker

import (
	"strings"
	"testing"
)

// A bare variant name resolves among the enums the REFERRING module can
// name, not every enum in the program (#6951).
//
// modload mangles each module's types and functions to `<mod>__name`, but
// leaves variant names bare, so before this the single flat variant table
// let any loaded module's `enum Kind { Text }` make every other module's
// bare `Text` ambiguous. Two libraries that never import each other could
// not be used together, and declaring `enum Opt { Some, None }` was enough
// to report E036 against stdlib source the author cannot edit.
//
// Every case here is multi-module on purpose: single-file programs have one
// module, so they cannot distinguish the rules.
func TestBareVariantResolvesWithinTheReferringModule(t *testing.T) {
	// Two libraries with the same variant name that never see each other.
	// Neither reference is ambiguous where it stands.
	t.Run("independent modules do not ambiguate each other", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"a.fern": "enum Kind { Text, Binary }\n" +
				"pub function ka(): i32 { var k: Kind = Text; return 0; }\n",
			"b.fern": "enum Kind { Text, Blob }\n" +
				"pub function kb(): i32 { var k: Kind = Text; return 0; }\n",
			"main.fern": "import \"./a\";\nimport \"./b\";\n" +
				"function main(): i32 { return a.ka() + b.kb(); }\n",
		}, "main.fern")
		if err != nil {
			t.Errorf("neither module can see the other's Kind, so neither bare Text is ambiguous:\n%v", err)
		}
	})

	// The issue's own repro: declaring the enums is enough, nothing in the
	// user's file references them, and the diagnostics landed on core/int.
	t.Run("a user enum does not ambiguate stdlib source", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"main.fern": "import \"core/int\";\n" +
				"enum Opt { Some(i32), None }\n" +
				"enum Three { None, Aa(i32), Bb(i32) }\n" +
				"function main(): i32 { return 0; }\n",
		}, "main.fern")
		if err != nil {
			t.Errorf("core/int's own bare None / Some are unambiguous inside core/int:\n%v", err)
		}
	})

	// A private enum is not exported and still reached every other module
	// through the shared variant table.
	t.Run("a private enum does not ambiguate stdlib source", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"lib.fern": "enum Secret { Some(i32), None }\n" +
				"pub function q(): i32 { return 0; }\n",
			"main.fern": "import \"./lib\";\nimport \"core/int\";\n" +
				"function main(): i32 { return lib.q(); }\n",
		}, "main.fern")
		if err != nil {
			t.Errorf("lib's unexported Secret must not reach core/int:\n%v", err)
		}
	})

	// The narrowing is to what the module can NAME, not to what it declares:
	// a module that imports the other sees both and must qualify.
	t.Run("a module that sees both must qualify", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"other.fern": "pub enum Kind { Text, Blob }\n",
			"lib.fern": "import \"./other\";\nenum Kind { Text, Binary }\n" +
				"pub function pick(): i32 { var k: Kind = Text; return 0; }\n",
			"main.fern": "import \"./lib\";\nfunction main(): i32 { return lib.pick(); }\n",
		}, "main.fern")
		if err == nil {
			t.Fatal("lib imports other, so its bare Text is genuinely ambiguous")
		}
		if !strings.Contains(err.Error(), "declared in multiple enums") {
			t.Errorf("want the ambiguity diagnostic, got:\n%v", err)
		}
	})

	// Built-in enums carry no SourceModule, so they stay candidates in every
	// module — shadowing Option's variants is still ambiguous where declared.
	t.Run("builtin enums stay visible everywhere", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"lib.fern": "enum O { Some(i32), None }\n" +
				"pub function get(): O { return None; }\n",
			"main.fern": "import \"./lib\";\nfunction main(): i32 { return 0; }\n",
		}, "main.fern")
		if err == nil {
			t.Fatal("Option's None is a candidate inside lib too, so bare None is ambiguous")
		}
		if !strings.Contains(err.Error(), "Option") {
			t.Errorf("the candidate list should name Option, got:\n%v", err)
		}
	})

	// The filter must not narrow away the ordinary cross-module case: an
	// imported enum's variants stay nameable bare.
	t.Run("an imported enum's variant is still nameable", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"lib.fern": "pub enum Kind { Text, Binary }\n",
			"main.fern": "import \"./lib\";\n" +
				"function main(): i32 { var k: lib.Kind = Text; return 0; }\n",
		}, "main.fern")
		if err != nil {
			t.Errorf("lib is in main's closure, so bare Text resolves:\n%v", err)
		}
	})

	// Same, through a `pub use` facade — the re-export is what puts shapes
	// in the consumer's closure.
	t.Run("a re-exported enum's variant is still nameable", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"shapes.fern": "pub enum Kind { Text, Binary }\n",
			"facade.fern": "pub use \"./shapes\".{Kind};\n",
			"main.fern": "import \"./facade\";\n" +
				"function main(): i32 { var k: facade.Kind = Text; return 0; }\n",
		}, "main.fern")
		if err != nil {
			t.Errorf("facade re-exports Kind, so bare Text resolves in main:\n%v", err)
		}
	})

	// The other edge of the same rule: a module cannot name a variant of an
	// enum it has no route to, which previously resolved silently.
	t.Run("an unreachable module's variant is not nameable", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"other.fern": "pub enum Kind { Text, Binary }\n",
			"lib.fern":   "pub function bad(): i32 { var x = Text; return 0; }\n",
			"main.fern": "import \"./lib\";\nimport \"./other\";\n" +
				"function main(): i32 { return lib.bad(); }\n",
		}, "main.fern")
		if err == nil {
			t.Fatal("lib does not import other, so Text names nothing there")
		}
		if !strings.Contains(err.Error(), `undefined identifier "Text"`) {
			t.Errorf("want an undefined-identifier diagnostic, got:\n%v", err)
		}
	})
}
