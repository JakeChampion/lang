package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// lastArmCoversRemainder checks src and reports the stamp on the last arm
// of the first enum match statement it finds.
func lastArmCoversRemainder(t *testing.T, src string) bool {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	var got *ast.MatchArm
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if m, ok := n.(*ast.Match); ok && got == nil && len(m.Arms) > 0 {
			got = m.Arms[len(m.Arms)-1]
		}
		return true
	})
	if got == nil {
		t.Fatal("no match statement found")
	}
	return got.CoversRemainder
}

const listDecl = "enum List { Cons(i32, List), Nil }\n"

// armCoversRemainder reports the stamp on the arm at index i.
func armCoversRemainder(t *testing.T, src string, i int) bool {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	var m *ast.Match
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if x, ok := n.(*ast.Match); ok && m == nil && len(x.Arms) > 0 {
			m = x
		}
		return true
	})
	if m == nil {
		t.Fatal("no match statement found")
	}
	return m.Arms[i].CoversRemainder
}

// The shape #7848 is about: two variants, both matched, no guard, no
// payload sub-pattern. Nothing but Nil can reach the last arm.
func TestCoversRemainderOnAnExhaustiveVariantMatch(t *testing.T) {
	if !lastArmCoversRemainder(t, listDecl+`
function f(xs: List): i32 {
  match (xs) { Cons(h, t) => { return h; }, Nil => { return 0; } }
}`) {
		t.Error("the last arm of an exhaustive two-variant match covers the remainder")
	}
}

// Three variants, all covered: the stamp is not special to two.
func TestCoversRemainderOnThreeVariants(t *testing.T) {
	if !lastArmCoversRemainder(t, "enum C { R, G, B }\n"+`
function f(c: C): i32 {
  match (c) { R => { return 0; }, G => { return 1; }, B => { return 2; } }
}`) {
		t.Error("the last arm of a three-variant exhaustive match covers the remainder")
	}
}

// A wildcard last arm is not an enum-variant arm. It already carries no
// tag test, so there is nothing for the stamp to say.
func TestCoversRemainderLeavesAWildcardLastArmAlone(t *testing.T) {
	if lastArmCoversRemainder(t, listDecl+`
function f(xs: List): i32 {
  match (xs) { Cons(h, t) => { return h; }, _ => { return 0; } }
}`) {
		t.Error("a wildcard arm was stamped; the stamp is for variant arms")
	}
}

// Only the LAST arm is ever stamped. A guarded arm mid-chain is not, and
// the unguarded arm after it still is — a guard can fail, so the arm
// carrying it does not match everything that reaches it.
func TestCoversRemainderStampsOnlyTheLastArm(t *testing.T) {
	src := listDecl + `
function f(xs: List): i32 {
  match (xs) {
    Nil => { return 0; },
    Cons(h, t) when h > 0 => { return h; },
    Cons(h, t) => { return 1; },
  }
}`
	if armCoversRemainder(t, src, 0) {
		t.Error("a non-last arm was stamped")
	}
	if armCoversRemainder(t, src, 1) {
		t.Error("the guarded arm was stamped")
	}
	if !armCoversRemainder(t, src, 2) {
		t.Error("the unguarded last arm covers the remainder")
	}
}

// A refutable payload before the irrefutable arm for the same variant:
// the refutable one is not stamped, and the last arm still is, because
// what reaches it is only Nil.
func TestCoversRemainderWithARefutablePayloadEarlier(t *testing.T) {
	src := listDecl + `
function f(xs: List): i32 {
  match (xs) {
    Cons(1, t) => { return 1; },
    Cons(h, t) => { return h; },
    Nil => { return 0; },
  }
}`
	if armCoversRemainder(t, src, 0) {
		t.Error("an arm with a refutable payload was stamped")
	}
	if !armCoversRemainder(t, src, 2) {
		t.Error("the last arm covers the remainder")
	}
}

// The deliberate narrowing. This match IS exhaustive — the checker
// proves V covered by the GROUP of its two refutable arms
// (patsCoverOneField) — but the stamp does not use that rule, because a
// group's members each test a sub-pattern and "what reaches this arm"
// stops being a statement about tags alone. It keeps its tag test.
func TestCoversRemainderDoesNotUseTheGroupCoverageRule(t *testing.T) {
	src := "enum Inner { A, B }\nenum Outer { V(Inner), W }\n" + `
function f(o: Outer): i32 {
  match (o) {
    V(A()) => { return 1; },
    V(B()) => { return 2; },
    W => { return 0; },
  }
}`
	if lastArmCoversRemainder(t, src) {
		t.Error("the stamp used the group coverage rule; it is deliberately narrower")
	}
}

// The checker REJECTS the programs the stamp's other guards defend
// against, so those guards are unreachable rather than untested. Pinned
// so a later relaxation of the checker does not silently make the stamp
// wrong: a wildcard before another arm, and a duplicate variant arm,
// must stay errors.
func TestCoversRemainderGuardsAreDefendedByTheChecker(t *testing.T) {
	if got := checkSrc(t, listDecl+`
function f(xs: List): i32 {
  match (xs) { _ => { return 0; }, Nil => { return 1; } }
}`); got == "" {
		t.Error("a wildcard before another arm is accepted; the stamp assumes it is not")
	}
	if got := checkSrc(t, listDecl+`
function f(xs: List): i32 {
  match (xs) {
    Cons(h, t) => { return h; },
    Nil => { return 0; },
    Nil => { return 1; },
  }
}`); got == "" {
		t.Error("a duplicate variant arm is accepted; the stamp assumes it is not")
	}
}
