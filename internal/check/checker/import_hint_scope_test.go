package checker

import (
	"strings"
	"testing"
)

func TestImportHintYieldsToTheMethodListOnceImported(t *testing.T) {
	for _, tc := range []struct {
		name       string
		src        string
		wantListed string
	}{
		{"string", `import "std/string";
function f(t: string): i32 { return t.bogusmethod(); }
function main(): i32 { return f("abc"); }`, "camel_case"},
		{"i32", `import "std/i32";
function f(x: i32): i32 { return x.bogusmethod(); }
function main(): i32 { return f(3); }`, "abs"},
		{"str", `import "std/string";
function f(t: string): i32 { return slice_unchecked(t, 0, 3).bogusmethod(); }
function main(): i32 { return f("abcdef"); }`, "as_bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkModuleSource(t, tc.src)
			if err == nil {
				t.Fatal("expected an unresolved-method error")
			}
			if strings.Contains(err.Error(), "add `import") {
				t.Fatalf("the import is already there; the hint sends the reader nowhere:\n%s", err.Error())
			}
			if !strings.Contains(err.Error(), tc.wantListed) {
				t.Fatalf("wanted the method list to name %q, got:\n%s", tc.wantListed, err.Error())
			}
		})
	}
}

func TestImportHintStillFiresWithoutTheImport(t *testing.T) {
	for _, tc := range []struct {
		name, src, wantMod string
	}{
		{"string", `function f(t: string): i32 { return t.trim().len(); }
function main(): i32 { return f("abc"); }`, "std/string"},
		// Formatting is a builtin; other numeric methods retain their module
		// boundary and still suggest the module that supplies them.
		{"i32", `function f(x: i32): string { return x.to_string_radix(16); }
function main(): i32 { return f(3).len(); }`, "std/i32"},
		{"transitive only", `import "std/array";
function f(t: string): i32 { return t.to_json().len(); }
function main(): i32 { return f("abc"); }`, "std/json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkModuleSource(t, tc.src)
			if err == nil {
				t.Fatal("expected an unresolved-method error")
			}
			want := "add `import \"" + tc.wantMod + "\"`"
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("wanted the hint to say %q, got:\n%s", want, err.Error())
			}
		})
	}
}

func TestBuiltinUnionMethodNamesItsModule(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{"option", `function g(): Option[i32] { return None; }
function main(): i32 { return g().unwrap_or(0); }`, "no method \"unwrap_or\" on Option[i32] — add `import \"std/option\"`"},
		{"result", `function g(): Result[i32, string] { return Ok(1); }
function main(): i32 { return g().unwrap_or(0); }`, "no method \"unwrap_or\" on Result[i32, string] — add `import \"std/result\"`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkModuleSource(t, tc.src)
			if err == nil {
				t.Fatal("expected E043, got no error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}
