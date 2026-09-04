package sourcelint

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// checker.fern's `bind_stmt` is `check_stmt`'s SCOPE half without the
// inference (#8181): twelve diagnostic walkers thread a Scope through a body
// and discard the type, and running the whole type checker to obtain that
// Scope was 14.41% of a `checker.fern` compile. The saving rests on one fact —
// only `ast.StmtVar` (and `ast.StmtDefer`, which delegates to the statement it
// wraps) can hand back a scope other than the one it was given. Every other
// statement form runs full type inference to conclude that the scope is
// unchanged.
//
// The failure mode that fact invites is drift: a scope-binding form added to
// check_stmt and not to bind_stmt breaks name resolution in all twelve passes,
// and the symptom is a spurious or missing diagnostic somewhere else entirely.
// The behavioural gate on that is TestSelfHostBindStmtScopeParity
// (internal/e2eselfhost), which runs both steppers over the same statements
// and compares the bindings. It can only compare forms its corpus contains,
// which a NEW binding form by definition does not — so this lint reads the
// two functions instead and pins the set of arms that may rebind at all, in
// both, from the source.
const bindStmtArmsPath = "../../examples/self_host/checker.fern"

// scopeBindingForms is the pinned set. A change here is not a lint bug: it
// means the checker learned a new way for a statement to bind a name, and
// both steppers plus the parity corpus have to learn it too.
var scopeBindingForms = []string{"StmtDefer", "StmtVar"}

func parseSelfHostChecker(t *testing.T) *ast.Program {
	t.Helper()
	p, err := filepath.Abs(bindStmtArmsPath)
	if err != nil {
		t.Fatalf("abs %s: %v", bindStmtArmsPath, err)
	}
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	prog, err := parser.Parse(string(src))
	if err != nil {
		t.Fatalf("parse %s: %v", p, err)
	}
	return prog
}

// stmtDispatch returns the `match (st) { … }` a stepper dispatches on.
func stmtDispatch(t *testing.T, prog *ast.Program, fn string) *ast.Match {
	t.Helper()
	for _, fd := range prog.Funcs {
		if fd.Name != fn || fd.Body == nil {
			continue
		}
		for _, st := range fd.Body.Stmts {
			if m, ok := st.(*ast.Match); ok {
				if id, ok := m.Tag.(*ast.Ident); ok && id.Name == "st" {
					return m
				}
			}
		}
		t.Fatalf("%s no longer dispatches on `match (st)`; this lint reads that match to pin which statement forms may bind a name", fn)
	}
	t.Fatalf("%s is gone from checker.fern", fn)
	return nil
}

// isScopeParam reports whether e is the bare `s` a stepper was handed — the
// one shape that means "this arm did not touch the scope".
func isScopeParam(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "s"
}

// checkStmtBindingArms reads check_stmt: an arm rebinds when a CheckResult it
// returns carries a `scope:` that is not the bare `s`, or when it delegates to
// check_stmt itself (the StmtDefer arm, which hands back whatever the wrapped
// statement binds).
func checkStmtBindingArms(t *testing.T, m *ast.Match) []string {
	t.Helper()
	var out []string
	for _, arm := range m.Arms {
		if arm.IsWildcard || arm.Body == nil {
			continue
		}
		returns, rebinds, opaque := 0, false, false
		for _, st := range arm.Body.Stmts {
			ast.Walk(st, func(x ast.Node) bool {
				r, ok := x.(*ast.Return)
				if !ok {
					return true
				}
				returns++
				switch v := r.Value.(type) {
				case *ast.StructLit:
					if v.TypeName != "CheckResult" {
						opaque = true
						return true
					}
					for _, f := range v.Fields {
						if f.Name == "scope" && !isScopeParam(f.Value) {
							rebinds = true
						}
					}
				case *ast.Call:
					if id, ok := v.Callee.(*ast.Ident); ok && id.Name == "check_stmt" {
						rebinds = true
						return true
					}
					opaque = true
				default:
					opaque = true
				}
				return true
			})
		}
		if returns == 0 || opaque {
			t.Errorf("check_stmt's %s arm returns something this lint cannot read (%d return(s), opaque=%v): it wants a `CheckResult { scope: … }` literal or a delegating check_stmt call.\n"+
				"    Teach it the new shape — leaving it blind would let a binding form appear in check_stmt and not in bind_stmt, which is the drift it exists to catch.", arm.VariantName, returns, opaque)
			continue
		}
		if rebinds {
			out = append(out, arm.VariantName)
		}
	}
	sort.Strings(out)
	return out
}

// bindStmtBindingArms reads bind_stmt: an arm rebinds when it returns anything
// other than the bare `s`.
func bindStmtBindingArms(t *testing.T, m *ast.Match) []string {
	t.Helper()
	var out []string
	for _, arm := range m.Arms {
		if arm.IsWildcard || arm.Body == nil {
			continue
		}
		returns, rebinds := 0, false
		for _, st := range arm.Body.Stmts {
			ast.Walk(st, func(x ast.Node) bool {
				r, ok := x.(*ast.Return)
				if !ok {
					return true
				}
				returns++
				if r.Value != nil && !isScopeParam(r.Value) {
					rebinds = true
				}
				return true
			})
		}
		if returns == 0 {
			t.Errorf("bind_stmt's %s arm returns nothing, so this lint cannot see which scope it produces.\n"+
				"    An arm that falls through to the function's trailing `return s;` says the form does not bind — write that `return s;` in the arm, or drop the arm.", arm.VariantName)
			continue
		}
		if rebinds {
			out = append(out, arm.VariantName)
		}
	}
	sort.Strings(out)
	return out
}

func TestCheckStmtAndBindStmtBindTheSameStatementForms(t *testing.T) {
	prog := parseSelfHostChecker(t)
	check := checkStmtBindingArms(t, stmtDispatch(t, prog, "check_stmt"))
	bind := bindStmtBindingArms(t, stmtDispatch(t, prog, "bind_stmt"))

	if !equalForms(check, bind) {
		t.Errorf("check_stmt and bind_stmt disagree about which statement forms bind a name.\n"+
			"    check_stmt rebinds on: %s\n"+
			"    bind_stmt  rebinds on: %s\n"+
			"    They are the same walk for twelve diagnostic passes, which take their scope from bind_stmt and their diagnostics from elsewhere:\n"+
			"    a form that binds in one and not the other resolves a name in the type-check pass and loses it in every diagnostic pass,\n"+
			"    which surfaces as a spurious or missing diagnostic far from the statement that caused it. Mirror the arm, and add the form to\n"+
			"    internal/e2eselfhost/self_host_bind_stmt_parity_test.go's corpus so the two are compared on real statements as well.",
			strings.Join(check, ", "), strings.Join(bind, ", "))
	}
	if !equalForms(check, scopeBindingForms) {
		t.Errorf("the set of statement forms that bind a name has changed: %s (pinned: %s).\n"+
			"    bind_stmt's whole saving is that everything else returns its scope untouched, so a new member here is worth reading twice.\n"+
			"    If it is right, update scopeBindingForms and add the form to the parity corpus in internal/e2eselfhost/self_host_bind_stmt_parity_test.go.",
			strings.Join(check, ", "), strings.Join(scopeBindingForms, ", "))
	}
}

// The scope-only re-walks this replaced were 14.41% of a `checker.fern`
// compile. `check_stmt(st, cur).scope` is what they all spelled, and it is
// the shape a new walker reaches for first, so it stays gone by lint rather
// than by memory.
func TestNoScopeOnlyCheckStmtCalls(t *testing.T) {
	prog := parseSelfHostChecker(t)
	var found []string
	ast.WalkProgram(prog, func(x ast.Node) bool {
		fa, ok := x.(*ast.FieldAccess)
		if !ok || fa.Field != "scope" {
			return true
		}
		call, ok := fa.Target.(*ast.Call)
		if !ok {
			return true
		}
		if id, ok := call.Callee.(*ast.Ident); ok && id.Name == "check_stmt" {
			found = append(found, fmt.Sprintf("checker.fern:%d", fa.P.Line))
		}
		return true
	})
	if len(found) > 0 {
		t.Errorf("`check_stmt(...).scope` is back at %s.\n"+
			"    That runs the whole type checker over a statement to keep a Scope current and throws the inferred type away — 14.41%% of a `checker.fern` compile across twelve walkers (#8181).\n"+
			"    Call bind_stmt instead. A caller that genuinely wants both halves binds the CheckResult and reads both fields, which is what check_block does.",
			strings.Join(found, ", "))
	}
}

func equalForms(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
