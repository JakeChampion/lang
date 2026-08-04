package shadowrename_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// The pass's contract is that after it runs, every Var / binding name in a
// function is unique WITHIN THE FUNCTION — the IR builder keys its locals by
// a flat `locals[string]int32` map, so two declarations sharing a name share
// a slot. Enclosing-scope shadowing was handled from the start; DISJOINT
// SIBLING scopes were not, and that is the harder half to notice because
// nothing about the source looks like shadowing.
//
// It is a miscompile, not a cosmetic collision: the name-keyed type lookups
// (isArrayTypeOfLocal / localArrayType / structOrEnumTypeOfLocal) answer with
// whichever declaration they reach first, so one branch's value gets the
// other's drop plan. The self-host compiler's irlower.alias_names_in_stmt is
// exactly this shape — a `parser.StmtAssign(a)` match payload binding beside
// a `var a: string[]` in the StmtIf / StmtMatch arms — and it over-released
// one refcount for every assignment statement in every program the compiler
// compiled, which the rc detector reports and nothing else did.

// TestRenameUnshadowedSiblingBlocksGetDistinctNames — two sibling blocks each declare
// `x`. Neither shadows the other (their scopes are disjoint), but they must
// still end up with distinct names.
func TestRenameUnshadowedSiblingBlocksGetDistinctNames(t *testing.T) {
	prog := runRename(t, `function f(): i32 {
		{
			var x: i32 = 1;
		}
		{
			var x: i32 = 2;
		}
		return 0;
	}`)
	names := collectVarNames(prog.Funcs[0].Body)
	if len(names) != 2 {
		t.Fatalf("expected 2 var decls, got %d (%v)", len(names), names)
	}
	if names[0] == names[1] {
		t.Errorf("sibling declarations of x both kept %q — they collapse onto one IR slot", names[0])
	}
}

// TestRenameSiblingMatchArmBindingAndLocalGetDistinctNames — the shape that
// miscompiled: a match payload binding named `a` in one arm and a
// differently-TYPED `var a` in another. The types are what make the collision
// a miscompile rather than merely confusing.
func TestRenameSiblingMatchArmBindingAndLocalGetDistinctNames(t *testing.T) {
	prog := runRename(t, `struct Asg { k: i32 }
enum St { SAssign(Asg), SIf(i32) }
function walk(st: St, acc: string[]): string[] {
	match (st) {
		SAssign(a) => { return acc; },
		SIf(n) => {
			var a: string[] = acc;
			return a;
		}
	}
	return acc;
}
function main(): i32 { return 0; }`)

	var fn *ast.FuncDecl
	for _, f := range prog.Funcs {
		if f.Name == "walk" {
			fn = f
		}
	}
	if fn == nil {
		t.Fatal("no func walk")
	}

	var binding string
	ast.Walk(fn.Body, func(n ast.Node) bool {
		m, ok := n.(*ast.Match)
		if !ok {
			return true
		}
		for _, arm := range m.Arms {
			for _, nm := range arm.Bindings {
				if nm != "" && nm != "_" {
					binding = nm
				}
			}
		}
		return true
	})
	if binding == "" {
		t.Fatal("no match payload binding found")
	}
	for _, nm := range collectVarNames(fn.Body) {
		if nm == binding {
			t.Errorf("match payload binding and sibling-arm local both named %q — "+
				"they share an IR slot, so one is released with the other's drop plan", nm)
		}
	}
}
