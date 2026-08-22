// Package shadowrename rewrites shadowed variable names so each
// declaration site (and every reference to it) carries a name
// that's unique within its enclosing function.
//
// The IR builder uses a flat `locals[string]int32` map keyed by
// AST name; nested-block shadowing collapsed two distinct
// declarations onto the same slot. Rather than thread a scope
// stack through every IR-level lookup, we run a quick AST pass
// that gives each shadowed declaration a fresh `name$N` form
// and rewrites every Ident reference that resolves to it to
// match. After this pass, every Var/Param name in the function
// body is globally unique within the function so the
// downstream layers' name-based dispatch stays correct.
//
// The pass runs after the checker (so `info.Locals[fn]` already
// has every Var node) but before closureconv / IR build.
// `info.Locals` is keyed by Var node pointer, so renaming
// `v.Name` in place doesn't disturb its slot-allocation order.
package shadowrename

import (
	"fmt"
	"strconv"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// Rename walks every function in `prog` and renames shadowed
// local variables (Var declarations, Destructure names, match /
// if-let payload bindings, for-init Var). Param names stay
// fixed; user code that names two params the same in different
// scopes never happens because params share one scope.
func Rename(prog *ast.Program, info *checker.Info) {
	for _, fn := range prog.Funcs {
		r := newRenamer(info)
		r.enterBody(fn)
		r.pushFrame()
		for _, p := range fn.Params {
			r.bindFresh(p.Name)
		}
		r.walkBlock(fn.Body)
		r.popFrame()
	}
}

type renamer struct {
	stack []map[string]string
	// declared holds every name bound anywhere in this function so far,
	// including scopes already popped. `stack` alone only sees ENCLOSING
	// scopes, so two declarations in DISJOINT SIBLING scopes — a match
	// payload binding in one arm and a `var` of the same name in another —
	// both kept the bare name and collapsed onto one slot in the IR
	// builder's flat locals map. That is a real miscompile whenever the two
	// have different types: the name-keyed type lookups
	// (isArrayTypeOfLocal / localArrayType / …) answer with whichever
	// declaration they find first, so one arm's binding is released with
	// the other's drop plan. In the self-host compiler
	// irlower.alias_names_in_stmt is exactly this shape — a
	// `parser.StmtAssign(a)` payload binding beside a `var a: string[]` in
	// the StmtIf/StmtMatch arms — and it over-released once per assignment
	// statement in every program the compiler saw.
	declared map[string]bool
	// locals is info.Locals for the body being walked. A tuple / struct
	// DESTRUCTURE binds through a plain []string on the Destructure node,
	// and the checker registers each name as a SYNTHETIC *ast.Var that
	// lives only in this slice — so renaming the Destructure's string alone
	// leaves the slot registered under the old name and the IR build dies
	// with `destructure name %q has no slot (compiler bug)`. Every other
	// binding form the pass touches carries its name on a node the IR reads
	// directly, which is why only this one needs the extra hop.
	//
	// It is per BODY, not per top-level function: enterBody swaps it when
	// the walk descends into a lambda or a nested function, whose locals the
	// checker registered against that body's own decl.
	locals  []*ast.Var
	info    *checker.Info
	counter int
}

func newRenamer(info *checker.Info) *renamer {
	return &renamer{declared: map[string]bool{}, info: info}
}

// enterBody points `locals` at the list the checker registered for fn's body
// and returns the previous one, for the caller to restore on the way out. A
// lambda's body-locals are keyed by its synthetic FuncDecl (checkExpr builds
// one per Lambda) and a nested function's by its own node, so a rename inside
// either has to look there — searching the ENCLOSING function's list finds
// nothing, leaves the synthetic Var under the old name, and the IR build
// fails on a name it cannot resolve to a slot.
func (r *renamer) enterBody(fn *ast.FuncDecl) []*ast.Var {
	prev := r.locals
	if r.info != nil && fn != nil {
		r.locals = r.info.Locals[fn]
	}
	return prev
}

// renameSyntheticLocal points the checker's synthetic *ast.Var for a
// destructure name at the renamed form. Matched on (position, old name):
// the synthetic Var carries the Destructure's own position, so this is
// exact even with several destructures in one function.
//
// A miss is fatal here rather than quiet. Returning silently is what turned
// a scoping oversight into a refusal to compile three passes later — twice:
// once for the statement destructure (the sibling-scope rename, pinned by
// rcCorpus/shadowed_tuple_destructure_keeps_its_slot) and once for the
// parameter pattern inside a lambda body, whose locals the checker registers
// against the lambda's synthetic decl. Both surfaced as
// `ir: destructure name %q has no slot (compiler bug)`, naming a pass that
// had done nothing wrong. A binding form that registers its synthetic Var
// somewhere this pass does not look now fails in the pass that got it wrong,
// with the name in hand.
func (r *renamer) renameSyntheticLocal(pos ast.Position, from, to string) {
	if from == to {
		return
	}
	for _, v := range r.locals {
		if v.Name == from && v.P == pos {
			v.Name = to
			return
		}
	}
	panic(fmt.Sprintf("shadowrename: no synthetic local %q at %d:%d to rename to %q "+
		"(the checker registers destructure binders against the decl whose body declares "+
		"them; this walk is looking at the wrong one)", from, pos.Line, pos.Col, to))
}

func (r *renamer) pushFrame() { r.stack = append(r.stack, map[string]string{}) }
func (r *renamer) popFrame()  { r.stack = r.stack[:len(r.stack)-1] }

// lookup walks the scope stack innermost-out and returns the
// current resolved name (the renamed form, or the original if
// no rename happened). The boolean signals presence.
func (r *renamer) lookup(name string) (string, bool) {
	for i := len(r.stack) - 1; i >= 0; i-- {
		if v, ok := r.stack[i][name]; ok {
			return v, true
		}
	}
	return "", false
}

// bindFresh binds `name` to itself in the current scope (no
// rename — used for params and the first decl of a name).
func (r *renamer) bindFresh(name string) string {
	r.stack[len(r.stack)-1][name] = name
	r.declared[name] = true
	return name
}

// bindShadow handles a Var/binding declaration. If the name is
// already bound anywhere in this function — an enclosing scope
// (true shadowing) or a sibling scope already closed (see the
// `declared` field) — the new declaration gets a unique form
// `name$N`. Otherwise the binding is kept as-is. Returns the
// resolved name to store on the AST node.
func (r *renamer) bindShadow(name string) string {
	_, shadowed := r.lookup(name)
	if shadowed || r.declared[name] {
		r.counter++
		out := name + "$" + strconv.Itoa(r.counter)
		r.stack[len(r.stack)-1][name] = out
		r.declared[out] = true
		return out
	}
	return r.bindFresh(name)
}

func (r *renamer) walkBlock(b *ast.Block) {
	if b == nil {
		return
	}
	r.pushFrame()
	for _, st := range b.Stmts {
		r.walkStmt(st)
	}
	r.popFrame()
}

func (r *renamer) walkStmt(s ast.Stmt) {
	if s == nil {
		return
	}
	switch n := s.(type) {
	case *ast.Block:
		r.walkBlock(n)
	case *ast.If:
		r.walkExpr(n.Cond)
		r.walkStmt(n.Then)
		if n.Else != nil {
			r.walkStmt(n.Else)
		}
	case *ast.While:
		r.walkExpr(n.Cond)
		r.walkStmt(n.Body)
	case *ast.Loop:
		r.walkStmt(n.Body)
	case *ast.For:
		// Init's Var lands in the for's own scope; same for
		// any other init form. Body, Cond, Step all share that
		// scope.
		r.pushFrame()
		if n.Init != nil {
			r.walkStmt(n.Init)
		}
		r.walkExpr(n.Cond)
		if n.Step != nil {
			r.walkStmt(n.Step)
		}
		r.walkStmt(n.Body)
		r.popFrame()
	case *ast.Var:
		// RHS sees the *outer* scope, so walk it before the
		// shadow-bind. `var x = x + 1` reads outer x for the
		// rhs and binds a fresh inner x for the lhs.
		if n.Init != nil {
			r.walkExpr(n.Init)
		}
		n.Name = r.bindShadow(n.Name)
	case *ast.Destructure:
		if n.Init != nil {
			r.walkExpr(n.Init)
		}
		for i, name := range n.Names {
			n.Names[i] = r.bindShadow(name)
			r.renameSyntheticLocal(n.P, name, n.Names[i])
		}
		// After this level's binders are bound: a nested level's Init reads
		// one of them, so walking it now resolves through the new name.
		for i := range n.Nested {
			if n.Nested[i] != nil {
				r.walkStmt(n.Nested[i])
			}
		}
	case *ast.Return:
		if n.Value != nil {
			r.walkExpr(n.Value)
		}
	case *ast.Defer:
		if n.Expr != nil {
			r.walkExpr(n.Expr)
		}
	case *ast.Match:
		r.walkExpr(n.Tag)
		for _, arm := range n.Arms {
			r.walkMatchArm(arm)
		}
	case *ast.ExprStmt:
		if n.Expr != nil {
			r.walkExpr(n.Expr)
		}
	case *ast.FuncDecl:
		// Nested function: walks under the CURRENT renamer so
		// the body sees the parent's renames. References to a
		// captured outer-scope name resolve to the renamed form
		// (e.g. `return n;` where `n` shadowed an outer param
		// `n` rewrites to `return n$1;`). Each nested function
		// pushes a fresh frame for its own params + body so its
		// private bindings stay scoped. The checker-built
		// `n.Captures` list was named with the pre-rename forms,
		// so rewrite each capture's name to the current
		// resolution too — the closureconv pass / IR emit need
		// the captures' names to match the body's references.
		for i, cap := range n.Captures {
			if resolved, ok := r.lookup(cap.Name); ok {
				n.Captures[i].Name = resolved
			}
		}
		outer := r.enterBody(n)
		r.pushFrame()
		for _, p := range n.Params {
			r.bindFresh(p.Name)
		}
		r.walkBlock(n.Body)
		r.popFrame()
		r.locals = outer
	case *ast.ForEach:
		// A for-in binds its element name for the body, and is walked
		// here for completeness only: the parser lowers the plain form
		// and the checker lowers the destructuring and lazy-stream ones,
		// all before this pass runs.
		r.pushFrame()
		r.walkExpr(n.Iter)
		r.walkExpr(n.RangeHigh)
		if n.Pattern != nil {
			r.walkStmt(n.Pattern)
		} else {
			n.Var = r.bindShadow(n.Var)
		}
		r.walkStmt(n.Body)
		r.popFrame()
	case *ast.Break, *ast.Continue:
		// leaves — a label is not a value binding
	default:
		panic(unhandled("walkStmt", s))
	}
}

func (r *renamer) walkMatchArm(arm *ast.MatchArm) {
	if arm == nil {
		return
	}
	r.pushFrame()
	for i, name := range arm.Bindings {
		arm.Bindings[i] = r.bindShadow(name)
	}
	if arm.Guard != nil {
		r.walkExpr(arm.Guard)
	}
	if arm.Body != nil {
		r.walkBlock(arm.Body)
	}
	r.popFrame()
}

func (r *renamer) walkExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.NumberLit, *ast.FloatLit, *ast.BoolLit, *ast.UnitLit,
		*ast.StringLit, *ast.CharLit, *ast.CaptureRef:
		// leaves — no name to resolve
	case *ast.Ident:
		if resolved, ok := r.lookup(n.Name); ok {
			n.Name = resolved
		}
	case *ast.Binary:
		r.walkExpr(n.Left)
		r.walkExpr(n.Right)
	case *ast.Unary:
		r.walkExpr(n.Operand)
	case *ast.CastExpr:
		r.walkExpr(n.Inner)
	case *ast.DowncastExpr:
		r.walkExpr(n.Inner)
	case *ast.SliceExpr:
		r.walkExpr(n.Source)
		if n.Low != nil {
			r.walkExpr(n.Low)
		}
		if n.High != nil {
			r.walkExpr(n.High)
		}
	case *ast.Call:
		r.walkExpr(n.Callee)
		for _, a := range n.Args {
			r.walkExpr(a)
		}
	case *ast.Index:
		r.walkExpr(n.Array)
		r.walkExpr(n.Idx)
	case *ast.ArrayLit:
		for _, el := range n.Elems {
			r.walkExpr(el)
		}
	case *ast.Assign:
		r.walkExpr(n.Target)
		r.walkExpr(n.Value)
	case *ast.IfExpr:
		r.walkExpr(n.Cond)
		r.walkExpr(n.Then)
		r.walkExpr(n.Else)
	case *ast.TryOp:
		r.walkExpr(n.Inner)
	case *ast.MatchExpr:
		r.walkExpr(n.Tag)
		for _, arm := range n.Arms {
			r.pushFrame()
			for i, name := range arm.Bindings {
				arm.Bindings[i] = r.bindShadow(name)
			}
			if arm.Guard != nil {
				r.walkExpr(arm.Guard)
			}
			if arm.Body != nil {
				r.walkExpr(arm.Body)
			}
			r.popFrame()
		}
	case *ast.BlockExpr:
		// Block-expression branch: a fresh frame so locals bound by the
		// statements are visible to the tail but don't leak past `}`.
		r.pushFrame()
		for _, st := range n.Stmts {
			r.walkStmt(st)
		}
		if n.Tail != nil {
			r.walkExpr(n.Tail)
		}
		r.popFrame()
	case *ast.StructLit:
		// Base is the spread source of a struct-update literal
		// (`Foo { ...base, field: v }`). It must be walked too, or a
		// shadowed `base` Ident resolves to the wrong (outer) slot and
		// the program miscompiles. See docs/ADVERSARIAL-REVIEW-2026-06.md (F1).
		if n.Base != nil {
			r.walkExpr(n.Base)
		}
		for _, f := range n.Fields {
			r.walkExpr(f.Value)
		}
	case *ast.MapLit:
		for _, ent := range n.Entries {
			r.walkExpr(ent.Key)
			r.walkExpr(ent.Value)
		}
	case *ast.TupleLit:
		for _, el := range n.Elems {
			r.walkExpr(el)
		}
	case *ast.FieldAccess:
		r.walkExpr(n.Target)
	case *ast.FString:
		for _, p := range n.Parts {
			if p.Expr != nil {
				r.walkExpr(p.Expr)
			}
		}
		if n.Desugared != nil {
			r.walkExpr(n.Desugared)
		}
	case *ast.EnumLit:
		for _, a := range n.Args {
			r.walkExpr(a)
		}
	case *ast.MakeClosure:
		for _, c := range n.Captures {
			r.walkExpr(c)
		}
	case *ast.Lambda:
		// Same treatment as the nested *ast.FuncDecl case in walkStmt,
		// for the anonymous spelling: the body reads the enclosing
		// scope, so a reference to a shadowed local has to resolve to
		// the renamed form, and the checker built n.Captures from the
		// pre-rename names. Without this the body's Ident kept the bare
		// name and the IR's flat locals map bound it to the OUTER
		// declaration's slot — the compiled backends returned the outer
		// value where the interpreter, which scopes independently,
		// returned the inner one.
		for i, cap := range n.Captures {
			if resolved, ok := r.lookup(cap.Name); ok {
				n.Captures[i].Name = resolved
			}
		}
		outer := r.enterBody(n.Synthetic)
		r.pushFrame()
		for _, p := range n.Params {
			r.bindFresh(p.Name)
		}
		r.walkBlock(n.Body)
		r.popFrame()
		r.locals = outer
	default:
		panic(unhandled("walkExpr", e))
	}
}

// unhandled reports a node kind neither switch names. The pass renames every
// reference to a shadowed declaration, so a kind that falls through does not
// fail loudly: the reference keeps the outer name and binds to the outer slot
// at IR build. ast.NodeKinds drives both switches in the tests so a new kind
// arrives here instead.
func unhandled(where string, n ast.Node) string {
	return fmt.Sprintf("shadowrename: %s: unhandled node kind %T (add a case for it)", where, n)
}
