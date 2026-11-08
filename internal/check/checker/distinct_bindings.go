package checker

import (
	"strconv"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// distinctBindings gives a new name to each binding in fn that repeats the
// name of an earlier binding, and to each read of it. The last-use analysis
// keys its facts by name, so two values with one name mix. The returned
// function restores every name.
func distinctBindings(fn *ast.FuncDecl) func() {
	r := &bindingRenamer{seen: map[string]int{}}
	r.function(fn.Params, fn.Body, false)
	return func() {
		for i := len(r.undo) - 1; i >= 0; i-- {
			*r.undo[i].slot = r.undo[i].name
		}
	}
}

type bindingRenamer struct {
	scopes []map[string]string
	seen   map[string]int
	undo   []renamedSlot
}

type renamedSlot struct {
	slot *string
	name string
}

func (r *bindingRenamer) set(slot *string, name string) {
	if *slot != name {
		r.undo = append(r.undo, renamedSlot{slot, *slot})
		*slot = name
	}
}

func (r *bindingRenamer) push() { r.scopes = append(r.scopes, map[string]string{}) }

func (r *bindingRenamer) pop() { r.scopes = r.scopes[:len(r.scopes)-1] }

// bind declares the names in slots in the innermost scope. Slots with one
// name are one binding: an arm can hold its binder in more than one field.
func (r *bindingRenamer) bind(slots ...*string) {
	fresh := map[string]string{}
	for _, slot := range slots {
		name := *slot
		if name == "" || name == "_" {
			continue
		}
		to, had := fresh[name]
		if !had {
			to = name
			if n := r.seen[name]; n > 0 {
				to = name + "#" + strconv.Itoa(n)
			}
			r.seen[name]++
			fresh[name] = to
			r.scopes[len(r.scopes)-1][name] = to
		}
		r.set(slot, to)
	}
}

func (r *bindingRenamer) lookup(name string) (string, bool) {
	for i := len(r.scopes) - 1; i >= 0; i-- {
		if to, ok := r.scopes[i][name]; ok {
			return to, true
		}
	}
	return "", false
}

// function walks the parameters and the body of a function or a lambda. A
// lambda sees the locals around it. A nested function does not.
func (r *bindingRenamer) function(params []ast.Param, body *ast.Block, closure bool) {
	outer := r.scopes
	if !closure {
		r.scopes = nil
	}
	r.push()
	slots := make([]*string, len(params))
	for i := range params {
		slots[i] = &params[i].Name
	}
	r.bind(slots...)
	r.walk(body)
	r.scopes = outer
}

func (r *bindingRenamer) walk(n ast.Node) {
	if n != nil {
		ast.Walk(n, r.visit)
	}
}

func (r *bindingRenamer) stmts(list []ast.Stmt) {
	for _, st := range list {
		r.walk(st)
	}
}

func (r *bindingRenamer) visit(n ast.Node) bool {
	switch x := n.(type) {
	case *ast.Ident:
		if to, ok := r.lookup(x.Name); ok {
			r.set(&x.Name, to)
		}
	case *ast.Block:
		r.push()
		r.stmts(x.Stmts)
		r.pop()
	case *ast.BlockExpr:
		r.push()
		r.stmts(x.Stmts)
		if x.Tail != nil {
			r.walk(x.Tail)
		}
		r.pop()
	case *ast.Var:
		if x.Init != nil {
			r.walk(x.Init)
		}
		r.bind(&x.Name)
	case *ast.Destructure:
		r.destructure(x)
	case *ast.For:
		r.push()
		if x.Init != nil {
			r.walk(x.Init)
		}
		if x.Cond != nil {
			r.walk(x.Cond)
		}
		if x.Step != nil {
			r.walk(x.Step)
		}
		r.walk(x.Body)
		r.pop()
	case *ast.ForEach:
		r.walk(x.Iter)
		if x.RangeHigh != nil {
			r.walk(x.RangeHigh)
		}
		r.push()
		r.bind(&x.Var)
		if x.Pattern != nil {
			r.destructure(x.Pattern)
		}
		r.walk(x.Body)
		r.pop()
	case *ast.Match:
		r.walk(x.Tag)
		for _, a := range x.Arms {
			r.push()
			r.bind(armSlots(a.Bindings, &a.AtBinding, &a.ScalarBinding, a.TupleElems, a.Payloads)...)
			if a.Guard != nil {
				r.walk(a.Guard)
			}
			if a.Body != nil {
				r.walk(a.Body)
			}
			r.pop()
		}
	case *ast.MatchExpr:
		r.walk(x.Tag)
		for _, a := range x.Arms {
			r.push()
			r.bind(armSlots(a.Bindings, &a.AtBinding, &a.ScalarBinding, a.TupleElems, a.Payloads)...)
			if a.Guard != nil {
				r.walk(a.Guard)
			}
			r.walk(a.Body)
			r.pop()
		}
	case *ast.Lambda:
		r.function(x.Params, x.Body, true)
	case *ast.FuncDecl:
		r.function(x.Params, x.Body, false)
	default:
		return true
	}
	return false
}

// destructure binds the names of a let pattern after its initialiser. A
// nested pattern reads the temporary of the pattern around it.
func (r *bindingRenamer) destructure(d *ast.Destructure) {
	r.walk(d.Init)
	slots := []*string{&d.AtName, &d.TempName}
	for i := range d.Names {
		slots = append(slots, &d.Names[i])
	}
	r.bind(slots...)
	for _, sub := range d.Nested {
		if sub != nil {
			r.destructure(sub)
		}
	}
}

// armSlots gives the fields that hold the binders of a match arm, in the
// order of ast.MatchArm.Binders.
func armSlots(bindings []string, at, scalar *string, elems []ast.TuplePatElem, payloads []*ast.TuplePatElem) []*string {
	var out []*string
	for i := range bindings {
		out = append(out, &bindings[i])
	}
	out = append(out, at)
	for i := range elems {
		out = elemSlots(out, &elems[i])
	}
	for _, p := range payloads {
		if p != nil {
			out = elemSlots(out, p)
		}
	}
	return append(out, scalar)
}

func elemSlots(acc []*string, e *ast.TuplePatElem) []*string {
	acc = append(acc, &e.Name, &e.AtBinding)
	for i := range e.VariantBindings {
		acc = append(acc, &e.VariantBindings[i])
	}
	for i := range e.Nested {
		acc = elemSlots(acc, &e.Nested[i])
	}
	for _, vp := range e.VariantPayloads {
		if vp != nil {
			acc = elemSlots(acc, vp)
		}
	}
	return acc
}
