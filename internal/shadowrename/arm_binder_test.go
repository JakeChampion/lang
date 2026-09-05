package shadowrename_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Every binder position of a match arm has to be renamed, not just the
// payload list. The pass walked `arm.Bindings` alone, so a tuple element, a
// payload SUB-PATTERN and the `@` whole-value name all kept the outer
// declaration's name and shared its slot in the IR builder's flat locals map
// — the arm's borrowed value overwrote the outer local, which then read back
// as the element (#8607). The compiled backends answered with the arm's
// value where the interpreter, which scopes independently, answered with the
// outer one.

// armBinderNamed reports the resolved name of the arm binder at the position
// `pick` selects, for the single match in fn.
func armBinderNames(fn *ast.FuncDecl) []string {
	var out []string
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch m := n.(type) {
		case *ast.Match:
			for _, arm := range m.Arms {
				out = append(out, ast.ArmBinderNames(arm)...)
			}
		case *ast.MatchExpr:
			for _, arm := range m.Arms {
				out = append(out, ast.ArmExprBinderNames(arm)...)
			}
		}
		return true
	})
	return out
}

func funcNamed(t *testing.T, prog *ast.Program, name string) *ast.FuncDecl {
	t.Helper()
	for _, f := range prog.Funcs {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no func %q", name)
	return nil
}

// assertRenamedAwayFrom checks that no arm binder in fn kept `name`, which
// the function also declares as a local.
func assertRenamedAwayFrom(t *testing.T, prog *ast.Program, fnName, name string) {
	t.Helper()
	fn := funcNamed(t, prog, fnName)
	for _, got := range armBinderNames(fn) {
		if got == name {
			t.Errorf("%s: arm binder kept %q, the name a local in the same function declares — "+
				"the two share one IR slot and the binder's borrowed value overwrites the local",
				fnName, name)
		}
	}
}

func TestRenameTupleElementBinderShadowingLocal(t *testing.T) {
	prog := runRename(t, `function tuple_binder(): i32 {
	var s: string = "outer";
	var t: (string, i32) = ("elem", 4);
	match (t) {
		(s, n) => { return n; }
	}
}
function main(): i32 { return tuple_binder(); }`)
	assertRenamedAwayFrom(t, prog, "tuple_binder", "s")
}

func TestRenameNestedTupleElementBinderShadowingLocal(t *testing.T) {
	prog := runRename(t, `function nested_binder(): i32 {
	var s: string = "outer";
	var t: (i32, (string, i32)) = (1, ("elem", 4));
	match (t) {
		(a, (s, n)) => { return a + n; }
	}
}
function main(): i32 { return nested_binder(); }`)
	assertRenamedAwayFrom(t, prog, "nested_binder", "s")
}

func TestRenamePayloadSubPatternBinderShadowingLocal(t *testing.T) {
	prog := runRename(t, `enum Inner { Some1(string), None1 }
enum Outer2 { Holds(Inner), Empty }
function payload_sub(): i32 {
	var s: string = "outer";
	var o: Outer2 = Holds(Some1("elem"));
	match (o) {
		Holds(Some1(s)) => { return s.len(); },
		Holds(None1()) => { return 0; },
		Empty => { return 0; }
	}
}
function main(): i32 { return payload_sub(); }`)
	assertRenamedAwayFrom(t, prog, "payload_sub", "s")
}

func TestRenameAtBindingShadowingLocal(t *testing.T) {
	prog := runRename(t, `enum Inner { Some1(string), None1 }
function at_binding(): i32 {
	var s: string = "outer";
	var i: Inner = Some1("elem");
	match (i) {
		s @ Some1(v) => { return v.len(); },
		None1 => { return 0; }
	}
}
function main(): i32 { return at_binding(); }`)
	assertRenamedAwayFrom(t, prog, "at_binding", "s")
}

// The expression form carries the same pattern fields and had the same gap.
func TestRenameMatchExprTupleBinderShadowingLocal(t *testing.T) {
	prog := runRename(t, `function expr_binder(): i32 {
	var s: string = "outer";
	var t: (string, i32) = ("elem", 4);
	var got: i32 = match (t) {
		(s, n) => n
	};
	return got + s.len();
}
function main(): i32 { return expr_binder(); }`)
	assertRenamedAwayFrom(t, prog, "expr_binder", "s")
}

// Control: the payload binder the pass has always renamed. A regression that
// takes out both halves is a different bug from one that takes out only the
// positions this file added.
func TestRenamePayloadBinderShadowingLocalStillRenames(t *testing.T) {
	prog := runRename(t, `enum Inner { Some1(string), None1 }
function payload(): i32 {
	var s: string = "outer";
	var i: Inner = Some1("elem");
	match (i) {
		Some1(s) => { return s.len(); },
		None1 => { return 0; }
	}
}
function main(): i32 { return payload(); }`)
	assertRenamedAwayFrom(t, prog, "payload", "s")
}

// An arm binder that shadows NOTHING keeps the name the programmer wrote:
// renaming unconditionally would churn every binder in every program and
// make the pass's output unreadable, and the IR's exit sweep resolves sibling
// arms' bindings through one shared name on purpose.
func TestRenameLeavesUnshadowedTupleBinderAlone(t *testing.T) {
	prog := runRename(t, `function untouched(): i32 {
	var t: (i32, i32) = (3, 4);
	match (t) {
		(a, b) => { return a + b; }
	}
}
function main(): i32 { return untouched(); }`)
	names := armBinderNames(funcNamed(t, prog, "untouched"))
	for _, n := range names {
		if n != "a" && n != "b" {
			t.Errorf("unshadowed tuple binder renamed to %q", n)
		}
	}
	if len(names) != 2 {
		t.Errorf("expected the two tuple binders, got %v", names)
	}
}
