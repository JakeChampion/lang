package checker

import (
	"strings"
	"testing"
)

// E043's "if it comes from …, add `import …`" hint answers the likeliest cause
// of an unrecognised method on a scalar or string receiver: the module holding
// it was never imported. It fired unconditionally, so a plain TYPO in a file
// that already has the import was told to add it again — unfollowable in the
// same way the u8 hint's wrong module name was (TestScalarModuleHintIsFollowable),
// since doing what it says changes nothing and the identical error repeats.
//
// With the import present the receiver's whole method surface is known, so the
// message now falls through to listing it, which is what a typo actually needs.
func TestImportHintYieldsToTheMethodListOnceImported(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		// A method the receiver really does carry, which the list must name.
		wantListed string
	}{
		{"string", `import "std/string";
function f(t: string): i32 { return t.bogusmethod(); }
function main(): i32 { return f("abc"); }`, "camel_case"},
		{"i32", `import "std/i32";
function f(x: i32): i32 { return x.bogusmethod(); }
function main(): i32 { return f(3); }`, "abs"},
		// A `str` receiver reaches the hint through the same StrType case that
		// gives it the import advice in the first place, so it has to yield
		// here too or that fix would have moved the dead end rather than
		// removed it.
		{"str", `import "std/string";
function f(t: string): i32 { return t[0:3].bogusmethod(); }
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

// The negative half, and the one that matters: a mis-keyed lookup collapses to
// "never already imported", which is the OLD behaviour and would sail through a
// positive-only test. The spellings the hint is written in (`std/string`) and
// the keys DirectImports is built from (`stdlib://std/string.fern`) are
// different vocabularies — stdlib.ModuleKey is what joins them.
func TestImportHintStillFiresWithoutTheImport(t *testing.T) {
	for _, tc := range []struct {
		name, src, wantMod string
	}{
		{"string", `function f(t: string): i32 { return t.bogusmethod(); }
function main(): i32 { return f("abc"); }`, "std/string"},
		// `to_string`, not an invented name: with no import at all a scalar
		// carries no method namespace, so a nonsense spelling is refused
		// earlier as a field access on a non-struct and never reaches this
		// message. A real stdlib method is what gets there.
		{"i32", `function f(x: i32): string { return x.to_string(); }
function main(): i32 { return f(3).len(); }`, "std/i32"},
		// Reaching a module TRANSITIVELY does not put its methods in scope, so
		// the closure must not be what suppresses the hint: std/array imports
		// std/string, and a program importing only std/array still needs to be
		// told about std/string. This is why the lookup reads DirectImports.
		{"transitive only", `import "std/array";
function f(t: string): i32 { return t.bogusmethod(); }
function main(): i32 { return f("abc"); }`, "std/string"},
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
