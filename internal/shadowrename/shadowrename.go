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
		r := newRenamer()
		r.pushFrame()
		for _, p := range fn.Params {
			r.bindFresh(p.Name)
		}
		r.walkBlock(fn.Body)
		r.popFrame()
	}
}

type renamer struct {
	stack   []map[string]string
	counter int
}

func newRenamer() *renamer { return &renamer{} }

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
	return name
}

// bindShadow handles a Var/binding declaration. If the name
// already binds in any enclosing scope, the new declaration
// gets a unique form `name$N`. Otherwise the binding is kept
// as-is. Returns the resolved name to store on the AST node.
func (r *renamer) bindShadow(name string) string {
	if _, shadowed := r.lookup(name); shadowed {
		r.counter++
		out := name + "$" + strconv.Itoa(r.counter)
		r.stack[len(r.stack)-1][name] = out
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
	switch n := s.(type) {
	case *ast.Block:
		r.walkBlock(n)
	case *ast.If:
		r.walkExpr(n.Cond)
		r.walkStmt(n.Then)
		if n.Else != nil {
			r.walkStmt(n.Else)
		}
	case *ast.IfLet:
		r.walkExpr(n.Source)
		// Bindings land in Then's scope, not Else's.
		r.pushFrame()
		for i, name := range n.Bindings {
			n.Bindings[i] = r.bindShadow(name)
		}
		// `Then` may be a Block; the block's own pushFrame is
		// fine because lookups walk up, so the bindings stay
		// visible.
		r.walkStmt(n.Then)
		r.popFrame()
		if n.Else != nil {
			r.walkStmt(n.Else)
		}
	case *ast.LetElse:
		r.walkExpr(n.Source)
		// Else runs in the *outer* scope (before the bindings
		// are introduced) — match the user's reading.
		if n.Else != nil {
			r.walkBlock(n.Else)
		}
		// Bindings become visible after the let-else stmt in
		// the surrounding block. We piggy-back on the
		// surrounding block's scope by binding into the
		// current frame.
		for i, name := range n.Bindings {
			n.Bindings[i] = r.bindShadow(name)
		}
	case *ast.While:
		r.walkExpr(n.Cond)
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
		}
	case *ast.Return:
		if n.Value != nil {
			r.walkExpr(n.Value)
		}
	case *ast.Defer:
		if n.Expr != nil {
			r.walkExpr(n.Expr)
		}
	case *ast.Arena:
		r.walkBlock(n.Body)
	case *ast.Match:
		r.walkExpr(n.Tag)
		for _, arm := range n.Arms {
			r.walkMatchArm(arm)
		}
	case *ast.Switch:
		r.walkExpr(n.Tag)
		for _, sc := range n.Cases {
			r.walkBlock(sc.Body)
		}
		if n.Default != nil {
			r.walkBlock(n.Default)
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
		r.pushFrame()
		for _, p := range n.Params {
			r.bindFresh(p.Name)
		}
		r.walkBlock(n.Body)
		r.popFrame()
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
	switch n := e.(type) {
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
	case *ast.StructLit:
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
	}
}
