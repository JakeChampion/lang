package checker

import (
	"strings"
	"testing"
)

func TestEntryValuesDoNotCaptureImportedVariants(t *testing.T) {
	for _, decl := range []string{
		`function Hit(x: i32): i32 { return x; }`,
		`function Hit[T](x: T): T { return x; }`,
		`const Hit: i32 = 2;`,
	} {
		t.Run(decl, func(t *testing.T) {
			err, _ := checkFiles(t, map[string]string{
				"a.fern": `pub enum A { Hit, Miss }
pub enum B { Data(i32) }
pub function f(): A { return Hit; }
pub function g(): B { return Data(7); }`,
				"main.fern": "import \"./a\";\n" + decl + `
function Data(x: string): string { return x; }
function main(): i32 { let x: a.A = a.f(); let y: a.B = a.g(); return 0; }`,
			}, "main.fern")
			if err != nil {
				t.Fatalf("an imported module cannot see the entry's values: %v", err)
			}
		})
	}
}

func TestImportedModuleCannotCallEntryFunction(t *testing.T) {
	for _, decl := range []string{
		`function hidden(x: i32): i32 { return x; }`,
		`function hidden[T](x: T): T { return x; }`,
	} {
		t.Run(decl, func(t *testing.T) {
			err, _ := checkFiles(t, map[string]string{
				"a.fern": `pub function f(): i32 { return hidden(2); }`,
				"main.fern": "import \"./a\";\n" + decl + `
function main(): i32 { return hidden(3); }`,
			}, "main.fern")
			if err == nil || !strings.Contains(err.Error(), `cannot find "hidden" in this scope`) {
				t.Fatalf("want an undefined name in a.fern, got %v", err)
			}
		})
	}
}

func TestEntryFunctionDoesNotCaptureStdlibVariant(t *testing.T) {
	err, _ := checkFiles(t, map[string]string{
		"main.fern": `import "std/dns";
function Malformed(s: string): i32 { return 0; }
function main(): i32 { return Malformed("entry"); }`,
	}, "main.fern")
	if err != nil {
		t.Fatal(err)
	}
}

func TestBareVariantResolvesWithinTheReferringModule(t *testing.T) {
	t.Run("independent modules do not ambiguate each other", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"a.fern": "enum Kind { Text, Binary }\n" +
				"pub function ka(): i32 { let k: Kind = Text; return 0; }\n",
			"b.fern": "enum Kind { Text, Blob }\n" +
				"pub function kb(): i32 { let k: Kind = Text; return 0; }\n",
			"main.fern": "import \"./a\";\nimport \"./b\";\n" +
				"function main(): i32 { return a.ka() + b.kb(); }\n",
		}, "main.fern")
		if err != nil {
			t.Errorf("neither module can see the other's Kind, so neither bare Text is ambiguous:\n%v", err)
		}
	})

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

	t.Run("a module that sees both must qualify", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"other.fern": "pub enum Kind { Text, Blob }\n",
			"lib.fern": "import \"./other\";\nenum Kind { Text, Binary }\n" +
				"pub function pick(): i32 { let k: Kind = Text; return 0; }\n",
			"main.fern": "import \"./lib\";\nfunction main(): i32 { return lib.pick(); }\n",
		}, "main.fern")
		if err == nil {
			t.Fatal("lib imports other, so its bare Text is genuinely ambiguous")
		}
		if !strings.Contains(err.Error(), "declared in multiple enums") {
			t.Errorf("want the ambiguity diagnostic, got:\n%v", err)
		}
	})

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

	t.Run("an imported enum's variant is still nameable", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"lib.fern": "pub enum Kind { Text, Binary }\n",
			"main.fern": "import \"./lib\";\n" +
				"function main(): i32 { let k: lib.Kind = Text; return 0; }\n",
		}, "main.fern")
		if err != nil {
			t.Errorf("lib is in main's closure, so bare Text resolves:\n%v", err)
		}
	})

	t.Run("a re-exported enum's variant is still nameable", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"shapes.fern": "pub enum Kind { Text, Binary }\n",
			"facade.fern": "pub use \"./shapes\".{Kind};\n",
			"main.fern": "import \"./facade\";\n" +
				"function main(): i32 { let k: facade.Kind = Text; return 0; }\n",
		}, "main.fern")
		if err != nil {
			t.Errorf("facade re-exports Kind, so bare Text resolves in main:\n%v", err)
		}
	})

	t.Run("an unreachable module's variant is not nameable", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"other.fern": "pub enum Kind { Text, Binary }\n",
			"lib.fern":   "pub function bad(): i32 { let x = Text; return 0; }\n",
			"main.fern": "import \"./lib\";\nimport \"./other\";\n" +
				"function main(): i32 { return lib.bad(); }\n",
		}, "main.fern")
		if err == nil {
			t.Fatal("lib does not import other, so Text names nothing there")
		}
		if !strings.Contains(err.Error(), `cannot find "Text" in this scope`) {
			t.Errorf("want an undefined-identifier diagnostic, got:\n%v", err)
		}
	})
}

func TestTwoStdlibModulesDoNotAmbiguateEachOther(t *testing.T) {
	err, _ := checkFiles(t, map[string]string{
		"main.fern": "import \"std/pmap\";\nimport \"std/pvec\";\n" +
			"function main(): void { }\n",
	}, "main.fern")
	if err != nil {
		t.Fatalf("std/pmap and std/pvec do not import each other, so neither "+
			"module's bare Empty is ambiguous where it stands:\n%v", err)
	}
}
