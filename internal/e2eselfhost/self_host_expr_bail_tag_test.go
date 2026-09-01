package e2eselfhost

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestExprBailTagNamesEveryExprVariant keeps `irlower.expr_bail_tag`'s `_` arm
// meaning exactly one thing: the discriminant is not a valid tag at all, so the
// node has been freed and its storage reused.
//
// That distinction is load-bearing rather than tidy. #7948 is a use-after-free
// on the wasm-hosted compiler which surfaces as `did not lower: unknown
// expression`, and diagnosing it cost a bisection precisely because three
// legitimate `ast.Expr` members (ExprMapLit, ExprFString, ExprUnknown) shared
// that arm and printed the same string. A corrupt tag and an unhandled variant
// must never be indistinguishable again.
//
// Static on purpose: none of the three is reachable from a well-formed program
// — the parser desugars map literals and f-strings before the compile path sees
// them (`ast.fern:170-186`), and asmcore reports an ExprUnknown as P001/P002
// before lowering runs (`ast.fern:164-168`) — so there is no program to feed a
// driver that would exercise them. The property that CAN regress is a new
// variant joining ast.Expr and silently falling into `_`, and that is what this
// asserts.
func TestExprBailTagNamesEveryExprVariant(t *testing.T) {
	astSrc, err := os.ReadFile("../../examples/self_host/ast.fern")
	if err != nil {
		t.Fatalf("read ast.fern: %v", err)
	}
	irlowerSrc, err := os.ReadFile("../../examples/self_host/irlower.fern")
	if err != nil {
		t.Fatalf("read irlower.fern: %v", err)
	}

	members := exprUnionMembers(t, string(astSrc))
	// Guard against regex drift silently emptying the check, the way
	// docs_embeds_test.go and examples_test.go do: the union has 17 members
	// today and is not plausibly going to shrink below a dozen.
	if len(members) < 12 {
		t.Fatalf("found only %d ast.Expr members (%v) — the union regex has drifted", len(members), members)
	}

	body := bailTagBody(t, string(irlowerSrc))
	var missing []string
	for _, m := range members {
		if !strings.Contains(body, "ast."+m+"(") {
			missing = append(missing, m)
		}
	}
	if len(missing) != 0 {
		t.Errorf("expr_bail_tag does not name %d of the %d ast.Expr members: %v\n"+
			"Each unnamed member falls into the `_` arm and prints as a corrupt node tag, "+
			"which is what made #7948 hard to diagnose. Add an arm naming it.",
			len(missing), len(members), missing)
	}
}

// exprUnionMembers pulls the member names out of `pub type Expr = A | B | …;`,
// which spans several lines in ast.fern.
func exprUnionMembers(t *testing.T, src string) []string {
	t.Helper()
	decl := regexp.MustCompile(`(?s)pub type Expr\s*=\s*(.*?);`).FindStringSubmatch(src)
	if decl == nil {
		t.Fatal("no `pub type Expr = …;` declaration in ast.fern")
	}
	var out []string
	for _, part := range strings.Split(decl[1], "|") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// bailTagBody returns the source of expr_bail_tag, from its signature to the
// close of its match. Bounded by the next top-level `function` so a later
// function's arms cannot satisfy the check.
func bailTagBody(t *testing.T, src string) string {
	t.Helper()
	start := strings.Index(src, "function expr_bail_tag(")
	if start < 0 {
		t.Fatal("no expr_bail_tag in irlower.fern")
	}
	rest := src[start+len("function expr_bail_tag("):]
	if end := strings.Index(rest, "\nfunction "); end >= 0 {
		return rest[:end]
	}
	return rest
}
