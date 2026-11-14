package checker

import (
	"slices"
	"strings"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func StmtReferencesName(st ast.Node, name string) bool {
	found := false
	ast.Walk(st, func(n ast.Node) bool {
		if lm, ok := n.(*ast.Lambda); ok {
			found = found || lambdaFreeNames(lm)[name]
			return false
		}
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}

type IdentOrder struct {
	Idx    map[*ast.Ident]int
	last   map[string]int
	byName map[string][]*ast.Ident
	arms   map[*ast.Ident][]forkArm
}

// forkArm is one arm of a match or an if outside every loop. One run takes
// one arm, so an identifier in one arm is not after one in another arm.
type forkArm struct{ fork, arm int }

// IdentOrderOf numbers the identifiers of body in source order. The else arm
// of a let-else is the last arm of its match, but the source writes it first.
func IdentOrderOf(body ast.Node) IdentOrder {
	o := IdentOrder{Idx: map[*ast.Ident]int{}, last: map[string]int{}, byName: map[string][]*ast.Ident{}, arms: map[*ast.Ident][]forkArm{}}
	n := 0
	var visit func(node ast.Node) bool
	visit = func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.Ident:
			n++
			o.Idx[x] = n
			o.byName[x.Name] = append(o.byName[x.Name], x)
			if n > o.last[x.Name] {
				o.last[x.Name] = n
			}
		case *ast.Match:
			els := len(x.Arms) - 1
			if els < 1 || !letElseDivergentArm(x, els) {
				return true
			}
			ast.Walk(x.Tag, visit)
			for _, a := range append([]*ast.MatchArm{x.Arms[els]}, x.Arms[:els]...) {
				if a.Guard != nil {
					ast.Walk(a.Guard, visit)
				}
				if a.Body != nil {
					ast.Walk(a.Body, visit)
				}
			}
			return false
		}
		return true
	}
	ast.Walk(body, visit)
	if st, ok := body.(ast.Stmt); ok {
		forks := 0
		o.markForks(st, nil, &forks)
	}
	return o
}

// markForks records the arms of the forks that hold each identifier of st.
// A loop body can take each arm in turn, so it records no fork inside a loop.
func (o IdentOrder) markForks(st ast.Stmt, path []forkArm, forks *int) {
	in := func(fork, arm int) []forkArm {
		return append(slices.Clone(path), forkArm{fork, arm})
	}
	switch x := st.(type) {
	case *ast.Block:
		for _, s := range x.Stmts {
			o.markForks(s, path, forks)
		}
		return
	case *ast.If:
		fork := *forks
		*forks++
		o.markIdents(x.Cond, path)
		if x.Then != nil {
			o.markForks(x.Then, in(fork, 0), forks)
		}
		if x.Else != nil {
			o.markForks(x.Else, in(fork, 1), forks)
		}
		return
	case *ast.Match:
		fork := *forks
		*forks++
		o.markIdents(x.Tag, path)
		for i, a := range x.Arms {
			if a.Guard != nil {
				o.markIdents(a.Guard, in(fork, i))
			}
			if a.Body != nil {
				o.markForks(a.Body, in(fork, i), forks)
			}
		}
		return
	}
	o.markIdents(st, path)
}

func (o IdentOrder) markIdents(node ast.Node, path []forkArm) {
	if len(path) == 0 || node == nil {
		return
	}
	ast.Walk(node, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			o.arms[id] = path
		}
		return true
	})
}

func (o IdentOrder) IsLast(id *ast.Ident) bool {
	if o.Idx[id] == o.last[id.Name] {
		return true
	}
	for _, later := range o.byName[id.Name] {
		if o.Idx[later] > o.Idx[id] && !forksApart(o.arms[id], o.arms[later]) {
			return false
		}
	}
	return true
}

func forksApart(a, b []forkArm) bool {
	for _, x := range a {
		for _, y := range b {
			if x.fork == y.fork && x.arm != y.arm {
				return true
			}
		}
	}
	return false
}

func DeferOrLambdaNames(body ast.Node) map[string]bool {
	return escapingNames(body, true)
}

// LambdaNames is the subset of DeferOrLambdaNames that a lambda captures.
func LambdaNames(body ast.Node) map[string]bool {
	return escapingNames(body, false)
}

func lambdaFreeNames(lm *ast.Lambda) map[string]bool {
	names := map[string]bool{}
	w := &ast.ScopeWalker{}
	w.Visit = func(slot *ast.Expr, w *ast.ScopeWalker) bool {
		if id, ok := (*slot).(*ast.Ident); ok && !w.IsBound(id.Name) {
			names[id.Name] = true
		}
		return true
	}
	w.WalkFunc(&ast.FuncDecl{Params: lm.Params, Body: lm.Body})
	return names
}

func escapingNames(body ast.Node, withDefer bool) map[string]bool {
	esc := map[string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		var sub ast.Node
		switch d := n.(type) {
		case *ast.Defer:
			if !withDefer {
				return true
			}
			sub = d.Expr
		case *ast.Lambda:
			for name := range lambdaFreeNames(d) {
				esc[name] = true
			}
			return false
		default:
			return true
		}
		ast.Walk(sub, func(m ast.Node) bool {
			if id, isIdent := m.(*ast.Ident); isIdent {
				esc[id.Name] = true
			}
			return true
		})
		return true
	})
	for name := range endedCallbackLoans(body) {
		delete(esc, name)
	}
	return esc
}

func RenameRoots(body ast.Node) map[string]string {
	occurrences := map[string]int{}
	declCount := map[string]int{}
	ast.Walk(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			occurrences[x.Name]++
		case *ast.Var:
			declCount[x.Name]++
		}
		return true
	})
	direct := map[string]string{}
	ast.Walk(body, func(n ast.Node) bool {
		v, isVar := n.(*ast.Var)
		if !isVar || declCount[v.Name] != 1 {
			return true
		}
		if src, isID := v.Init.(*ast.Ident); isID && occurrences[src.Name] == 1 {
			direct[v.Name] = src.Name
		}
		return true
	})
	out := make(map[string]string, len(direct))
	for name := range direct {
		root := direct[name]
		seen := map[string]bool{name: true, root: true}
		for {
			next, chained := direct[root]
			if !chained || seen[next] {
				break
			}
			root, seen[next] = next, true
		}
		out[name] = root
	}
	return out
}

type ArgDeaths struct {
	Dies      map[*ast.Call]map[string]bool
	Repeating map[*ast.Call]bool
	Escaping  map[string]bool
	// Deferred holds the call of each defer statement whose argument dies
	// there: the deferred call runs at the function's exit, so it is the last
	// use of the names it consumes.
	Deferred map[*ast.Call]bool
	// FieldDies names the field reads that are the last use of their field
	// path, with no later use of the whole: `q.r` passed to an own parameter
	// moves the field out of the local or own parameter `q`.
	FieldDies map[*ast.Call]map[*ast.FieldAccess]bool
	// Moves holds the match scrutinees and the for-each iterators (the
	// lowered iterator binding's init) that are the last use of a local or
	// of one of its field paths: the match or the loop takes the payloads.
	Moves map[ast.Expr]bool
	// Bound names what a match arm or a for-each binds in the function.
	Bound map[string]bool
	// Unadmitted names the locals in Dies that are not a fresh local, an
	// unpack or an alias of one: they can be a read out of a borrowed
	// structure, so only a type that cannot hold a handle may move.
	Unadmitted  map[string]bool
	Occurrences map[string]int
}

func CallArgDeaths(fn *ast.FuncDecl, info *Info) ArgDeaths {
	return callArgDeaths(fn, info, nil, nil)
}

// callArgDeaths is CallArgDeaths with handleFree, which reports a type that
// cannot hold a handle. A payload an arm binds from a fresh value or an own
// parameter is a fresh local when its type is handle-free; without the
// predicate no such payload is.
func callArgDeaths(fn *ast.FuncDecl, info *Info, handleFree func(ast.Type) bool, scalar map[ast.Expr]bool) ArgDeaths {
	body := fn.Body
	bf := newBodyFrames(body)
	out := map[*ast.Call]map[string]bool{}
	occurrences := map[string]int{}
	ast.Walk(body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			occurrences[id.Name]++
		}
		return true
	})
	isParam := map[string]bool{}
	ownParam := map[string]bool{}
	frameOwns := map[string]bool{}
	for _, p := range fn.Params {
		isParam[p.Name] = true
		if p.Own {
			ownParam[p.Name] = true
			frameOwns[p.Name] = true
		}
	}
	ast.Walk(body, func(n ast.Node) bool {
		if v, isVar := n.(*ast.Var); isVar && !isParam[v.Name] {
			frameOwns[v.Name] = true
		}
		return true
	})
	// A name is a fresh local when each of its declarations is fresh.
	freshInitLocal := map[string]bool{}
	staleDecl := map[string]bool{}
	declCount := map[string]int{}
	assignedNames := map[string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.Assign:
			if target, named := v.Target.(*ast.Ident); named {
				assignedNames[target.Name] = true
			}
		case *ast.Var:
			declCount[v.Name]++
			if freshInit(info, v.Init) {
				freshInitLocal[v.Name] = true
			} else {
				staleDecl[v.Name] = true
			}
		case *ast.Destructure:
			fresh := v.AtName == "" && freshInit(info, v.Init)
			for i, name := range v.Names {
				declCount[name]++
				if fresh && (i >= len(v.Nested) || v.Nested[i] == nil) {
					freshInitLocal[name] = true
				} else {
					staleDecl[name] = true
				}
			}
		}
		return true
	})
	for name := range staleDecl {
		delete(freshInitLocal, name)
	}
	escaping := DeferOrLambdaNames(body)
	order := IdentOrderOf(body)
	// A name a match arm binds owns its payload when the scrutinee is owned;
	// the ownership walk decides that, and here such a name is admitted as a
	// field owner like a fresh local.
	bound := map[string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Match:
			for _, arm := range x.Arms {
				names, _ := arm.OwnBindings()
				for _, b := range names {
					bound[b] = true
				}
			}
		case *ast.MatchExpr:
			for _, arm := range x.Arms {
				names, _ := arm.OwnBindings()
				for _, b := range names {
					bound[b] = true
				}
			}
		}
		return true
	})
	// declNode is where a field owner is bound: its let, or the match that
	// binds it. A loop that holds a consuming call but not that node runs
	// the call again on a value the round did not bind afresh.
	declNode := map[string]ast.Node{}
	ast.Walk(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Var:
			if _, had := declNode[x.Name]; !had {
				declNode[x.Name] = x
			}
		case *ast.Destructure:
			for _, nm := range x.Names {
				if _, had := declNode[nm]; !had {
					declNode[nm] = x
				}
			}
		case *ast.Match:
			for _, arm := range x.Arms {
				names, _ := arm.OwnBindings()
				for _, b := range names {
					if _, had := declNode[b]; !had {
						declNode[b] = x
					}
				}
			}
		case *ast.MatchExpr:
			for _, arm := range x.Arms {
				names, _ := arm.OwnBindings()
				for _, b := range names {
					if _, had := declNode[b]; !had {
						declNode[b] = x
					}
				}
			}
		}
		return true
	})
	var fieldSelections map[string]int
	var fieldReads map[[2]string]int
	var replacingBases map[[2]string]int
	// A name that only matches on a fresh call bind: the match takes the
	// result, so the payload is owned.
	freshBound, staleBound := matchBinds(info, body)
	payloadOwned := func(name string) bool {
		return freshBound[name] && !staleBound[name] && !isParam[name] && declCount[name] == 0
	}
	rebinds := matchPayloadRebinds(info, body, occurrences, escaping)
	payloadOf := func(value *ast.Ident, target string) bool {
		return !isParam[value.Name] && declCount[value.Name] == 0 && rebinds[value] == target
	}
	// An unpack reads the one field of a fresh local, an own parameter or a
	// payload that a match took, that nothing else reads, from a holder that
	// each mention selects a field of, or is the base of a literal that
	// replaces that field.
	unpack := func(e ast.Expr) bool {
		fa, isField := e.(*ast.FieldAccess)
		if !isField {
			return false
		}
		hid, isID := fa.Target.(*ast.Ident)
		// An own parameter holds its fields as a fresh local does.
		if !isID || !(freshInitLocal[hid.Name] && declCount[hid.Name] == 1) && !(ownParam[hid.Name] && declCount[hid.Name] == 0) && !payloadOwned(hid.Name) {
			return false
		}
		if fieldSelections == nil {
			fieldSelections = map[string]int{}
			fieldReads = map[[2]string]int{}
			replacingBases = map[[2]string]int{}
			ast.Walk(body, func(m ast.Node) bool {
				if sl, ok := m.(*ast.StructLit); ok {
					if base, ok := sl.Base.(*ast.Ident); ok {
						for _, f := range sl.Fields {
							replacingBases[[2]string{base.Name, f.Name}]++
						}
					}
				}
				if field, ok := m.(*ast.FieldAccess); ok {
					if root, ok := field.Target.(*ast.Ident); ok {
						fieldSelections[root.Name]++
						fieldReads[[2]string{root.Name, field.Field}]++
					}
				}
				return true
			})
		}
		return fieldReads[[2]string{hid.Name, fa.Field}] == 1 && fieldSelections[hid.Name]+replacingBases[[2]string{hid.Name, fa.Field}] == occurrences[hid.Name]
	}
	// An assignment that stores a value another name can hold ends the
	// freshness of its target. An unpack can read a holder that an earlier
	// round pruned, so the pruning runs to a fixpoint.
	staleAssign := map[string]bool{}
	for changed := true; changed; {
		changed = false
		ast.Walk(body, func(n ast.Node) bool {
			a, ok := n.(*ast.Assign)
			if !ok {
				return true
			}
			t, ok := a.Target.(*ast.Ident)
			if !ok || staleAssign[t.Name] || assignedOwned(info, t.Name, a.Value, freshInitLocal, payloadOwned, payloadOf, unpack) {
				return true
			}
			staleAssign[t.Name] = true
			if freshInitLocal[t.Name] {
				delete(freshInitLocal, t.Name)
				changed = true
			}
			return true
		})
	}
	unpackInitLocal := map[string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		if v, isVar := n.(*ast.Var); isVar && declCount[v.Name] == 1 && unpack(v.Init) {
			unpackInitLocal[v.Name] = true
		}
		return true
	})
	aliasInitLocal := map[string]bool{}
	for name, root := range RenameRoots(body) {
		if ownParam[root] || freshInitLocal[root] || unpackInitLocal[root] {
			aliasInitLocal[name] = true
		}
	}
	for name := range ownedAliases(info, body, ownParam, declCount, order, freshInitLocal, staleAssign, payloadOwned) {
		aliasInitLocal[name] = true
	}
	fieldOwner := func(name string) bool {
		if isParam[name] {
			return ownParam[name] && declCount[name] == 0
		}
		return (freshInitLocal[name] || unpackInitLocal[name] || aliasInitLocal[name]) && declCount[name] == 1 || bound[name] && declCount[name] == 0
	}
	admitted := func(name string) bool {
		return isParam[name] || freshInitLocal[name] || unpackInitLocal[name] || aliasInitLocal[name]
	}
	lambdaCaptured := LambdaNames(body)
	fieldOf := map[*ast.Ident]string{}
	updates := UpdateBases(body)
	ast.Walk(body, func(n ast.Node) bool {
		if fa, ok := n.(*ast.FieldAccess); ok {
			if id, ok := fa.Target.(*ast.Ident); ok {
				fieldOf[id] = fa.Field
			}
		}
		return true
	})
	exclusiveOwners := map[string]bool{}
	for name := range occurrences {
		exclusiveOwners[name] = ownParam[name]
	}
	ast.Walk(body, func(node ast.Node) bool {
		if v, local := node.(*ast.Var); local && fieldOwner(v.Name) && declCount[v.Name] == 1 {
			if call, named := v.Init.(*ast.Call); named && info != nil && info.CallOwnedResult != nil && info.CallOwnedResult(call) {
				exclusiveOwners[v.Name] = true
			}
		}
		return true
	})
	var arrayCopies map[*ast.FieldAccess]bool
	if info != nil {
		arrayCopies = info.ArrayFieldCopies
	}
	exclusiveFields := exclusiveOwnedFields(body, exclusiveOwners, fieldOf, occurrences, scalar, arrayCopies)

	// A deferred action runs at the exit, so a path it mentions is live until
	// then: a bare mention of q holds every field, and `q.g` holds g alone.
	deferBare := map[string]bool{}
	deferField := map[[2]string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		d, ok := n.(*ast.Defer)
		if !ok {
			return true
		}
		ast.Walk(d.Expr, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok {
				if f, isField := fieldOf[id]; isField {
					deferField[[2]string{id.Name, f}] = true
				} else {
					deferBare[id.Name] = true
				}
			}
			return true
		})
		return true
	})
	// A for-each binds its variable afresh each round from the lowered
	// iterator. When the iterator took its array, each element is a fresh
	// local: the array is a fresh call, an own parameter, or a local or a
	// field path at its last use.
	iterOwned := map[string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		v, ok := n.(*ast.Var)
		if !ok || !strings.HasPrefix(v.Name, ast.ForEachIterPrefix) {
			return true
		}
		switch it := v.Init.(type) {
		case *ast.Ident:
			if (ownParam[it.Name] && declCount[it.Name] == 0 || admitted(it.Name) && !isParam[it.Name]) && !escaping[it.Name] && !liveAfter(info, bf, v, it, order) {
				iterOwned[v.Name] = true
			}
		case *ast.FieldAccess:
			q, ok := it.Target.(*ast.Ident)
			if ok && !lambdaCaptured[q.Name] && !deferBare[q.Name] && !deferField[[2]string{q.Name, it.Field}] && fieldOwner(q.Name) &&
				!loopRepeats(body, v, declNode[q.Name]) && fieldPathDead(info, bf, v, q, it.Field, order, fieldOf, updates) {
				iterOwned[v.Name] = true
			}
		default:
			if freshInit(info, v.Init) {
				iterOwned[v.Name] = true
			}
		}
		return true
	})
	borrowedAssigns := map[*ast.Var]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		if a, ok := n.(*ast.Assign); ok && !freshInit(info, a.Value) && !freshLocalReplacement(info, bf, a, a.Value, order) {
			if target, ok := a.Target.(*ast.Ident); ok {
				borrowedAssigns[bindingVar(bf, a, target.Name)] = true
			}
		}
		return true
	})
	ast.Walk(body, func(n ast.Node) bool {
		if v, ok := n.(*ast.Var); ok && iterOwned[forEachElementOf(v)] && declCount[v.Name] == 1 && !borrowedAssigns[v] {
			freshInitLocal[v.Name] = true
		}
		return true
	})
	moves := map[ast.Expr]bool{}
	moveSite := func(holder ast.Node, e ast.Expr) {
		switch t := e.(type) {
		case *ast.Ident:
			if escaping[t.Name] || !admitted(t.Name) || liveAfter(info, bf, holder, t, order) {
				return
			}
			moves[e] = true
		case *ast.FieldAccess:
			q, ok := t.Target.(*ast.Ident)
			if !ok || lambdaCaptured[q.Name] || deferBare[q.Name] || deferField[[2]string{q.Name, t.Field}] || !fieldOwner(q.Name) {
				return
			}
			if !loopRepeats(body, holder, declNode[q.Name]) && fieldPathDead(info, bf, holder, q, t.Field, order, fieldOf, updates) {
				moves[e] = true
			}
		}
	}
	// A match on a local or a field path at its last use takes its payloads,
	// so each name an arm binds is a fresh local. A match on a fresh value or
	// an own parameter takes them too, and a name it binds is a fresh local
	// when its type is handle-free; a payload that can hold a handle is an
	// owned name that moves once. One walk in source order decides a match
	// before the matches inside its arms, so a name an outer match binds is
	// admitted when an inner match on it is decided.
	type armBinds struct {
		names []string
		types []ast.Type
	}
	freshTag := func(tag ast.Expr) bool {
		if id, ok := tag.(*ast.Ident); ok {
			return ownParam[id.Name] && declCount[id.Name] == 0
		}
		return freshInit(info, tag) || originalOwnedFieldSource(fn, tag) != ""
	}
	ast.Walk(body, func(n ast.Node) bool {
		var tag ast.Expr
		var arms []armBinds
		switch x := n.(type) {
		case *ast.Match:
			moveSite(x, x.Tag)
			tag = x.Tag
			for _, arm := range x.Arms {
				names, types := arm.OwnBindings()
				arms = append(arms, armBinds{names, types})
			}
		case *ast.MatchExpr:
			moveSite(x, x.Tag)
			tag = x.Tag
			for _, arm := range x.Arms {
				names, types := arm.OwnBindings()
				arms = append(arms, armBinds{names, types})
			}
		case *ast.Var:
			if strings.HasPrefix(x.Name, ast.ForEachIterPrefix) {
				moveSite(x, x.Init)
			}
			return true
		case *ast.Return:
			if fn.ReturnOwn {
				for _, part := range argParts(&ast.Call{Args: []ast.Expr{x.Value}}) {
					moveSite(x, part)
				}
			}
			return true
		default:
			return true
		}
		taken := moves[tag]
		ownedField := originalOwnedFieldSource(fn, tag) != ""
		if id, ok := tag.(*ast.Ident); ok && isParam[id.Name] {
			taken = false
		}
		if !taken && (handleFree == nil || !freshTag(tag)) {
			return true
		}
		for _, arm := range arms {
			for i, b := range arm.names {
				if taken || (!ownedField || !assignedNames[b]) && i < len(arm.types) && arm.types[i] != nil && handleFree(arm.types[i]) {
					freshInitLocal[b] = true
				}
			}
		}
		return true
	})
	markOnce := func(scope ast.Expr, name string) {
		total := 0
		copied := copiedArgs(scope, name, scalar)
		ast.Walk(scope, func(m ast.Node) bool {
			if e, ok := m.(ast.Expr); ok && copied[e] {
				return false
			}
			if id, ok := m.(*ast.Ident); ok && id.Name == name {
				total++
			}
			return true
		})
		if total != 1 {
			return
		}
		var site *ast.Call
		ast.Walk(scope, func(m ast.Node) bool {
			c, isCall := m.(*ast.Call)
			if !isCall {
				return true
			}
			for _, a := range argParts(c) {
				if aid, ok := a.(*ast.Ident); ok && aid.Name == name {
					site = c
				}
			}
			return true
		})
		if site == nil {
			return
		}
		if out[site] == nil {
			out[site] = map[string]bool{}
		}
		out[site][name] = true
	}
	ast.Walk(body, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.Assign:
			t, ok := st.Target.(*ast.Ident)
			if !ok {
				return true
			}
			if sl, isLit := st.Value.(*ast.StructLit); isLit {
				markSupersededFields(out, sl, t.Name)
				return true
			}
			c, ok := st.Value.(*ast.Call)
			if !ok {
				return true
			}
			markOnce(c, t.Name)
		case *ast.Return:
			if sl, isLit := st.Value.(*ast.StructLit); isLit {
				if sl.Base != nil {
					if bid, ok := sl.Base.(*ast.Ident); ok && frameOwns[bid.Name] {
						markSupersededFields(out, sl, bid.Name)
					}
				}
			}
			if st.Value == nil {
				return true
			}
			ast.Walk(st.Value, func(m ast.Node) bool {
				switch m.(type) {
				case *ast.BlockExpr, *ast.IfExpr, *ast.MatchExpr, *ast.Lambda:
					return false
				}
				if c, isCall := m.(*ast.Call); isCall {
					for _, a := range argParts(c) {
						if aid, ok := a.(*ast.Ident); ok {
							markOnce(st.Value, aid.Name)
						}
					}
				}
				return true
			})
		}
		return true
	})
	ast.Walk(body, func(n ast.Node) bool {
		blk, isBlk := n.(*ast.Block)
		if !isBlk {
			return true
		}
		for i := 0; i+1 < len(blk.Stmts); i++ {
			var init ast.Expr
			switch st := blk.Stmts[i].(type) {
			case *ast.Var:
				init = st.Init
			case *ast.Destructure:
				init = st.Init
			default:
				continue
			}
			c, isCall := init.(*ast.Call)
			if !isCall {
				continue
			}
			for j := i + 1; j < len(blk.Stmts); j++ {
				t, asn := storeTarget(blk.Stmts[j])
				if t == "" || !StmtReferencesName(c, t) || !reboundAfter(blk.Stmts[i+1:j], t) {
					continue
				}
				if !StmtReferencesName(asn.Value, t) {
					markOnce(c, t)
				}
			}
		}
		return true
	})
	repeating := map[*ast.Call]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.While, *ast.Loop, *ast.For, *ast.ForEach, *ast.Lambda:
		default:
			return true
		}
		ast.Walk(n, func(m ast.Node) bool {
			if c, ok := m.(*ast.Call); ok {
				repeating[c] = true
			}
			return true
		})
		return true
	})
	ast.Walk(body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok || repeating[c] {
			return true
		}
		for _, a := range argParts(c) {
			aid, ok := a.(*ast.Ident)
			if !ok || !isParam[aid.Name] || occurrences[aid.Name] != 1 {
				continue
			}
			if out[c] == nil {
				out[c] = map[string]bool{}
			}
			out[c][aid.Name] = true
		}
		return true
	})
	inLambda := map[*ast.Call]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.Lambda); !ok {
			return true
		}
		ast.Walk(n, func(m ast.Node) bool {
			if c, ok := m.(*ast.Call); ok {
				inLambda[c] = true
			}
			return true
		})
		return true
	})
	ast.Walk(body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok || inLambda[c] {
			return true
		}
		for _, a := range argParts(c) {
			aid, ok := a.(*ast.Ident)
			if !ok || escaping[aid.Name] || !admitted(aid.Name) && declCount[aid.Name] == 0 {
				continue
			}
			if bf.heldByEnclosingCall(c, aid.Name) {
				continue
			}
			if liveAfterBinding(info, bf, c, aid, order, declNode[aid.Name]) {
				continue
			}
			markOnce(c, aid.Name)
		}
		return true
	})
	deferred := map[*ast.Call]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		d, ok := n.(*ast.Defer)
		if !ok {
			return true
		}
		c, isCall := d.Expr.(*ast.Call)
		if !isCall || repeating[c] {
			return true
		}
		for _, a := range argParts(c) {
			aid, ok := a.(*ast.Ident)
			if !ok || lambdaCaptured[aid.Name] || !admitted(aid.Name) || declCount[aid.Name] > 1 {
				continue
			}
			if !isParam[aid.Name] && !freshInitLocal[aid.Name] && !unpackInitLocal[aid.Name] {
				continue
			}
			markOnce(c, aid.Name)
			if out[c][aid.Name] {
				deferred[c] = true
			}
		}
		return true
	})
	fieldDies := map[*ast.Call]map[*ast.FieldAccess]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		d, ok := n.(*ast.Defer)
		if !ok {
			return true
		}
		c, isCall := d.Expr.(*ast.Call)
		if !isCall || repeating[c] {
			return true
		}
		for _, a := range argParts(c) {
			fa, ok := a.(*ast.FieldAccess)
			if !ok {
				continue
			}
			nested := fa != enclosingField(fa)
			fa = enclosingField(fa)
			q, ok := fa.Target.(*ast.Ident)
			if nested && (!ok || !exclusiveFields[[2]string{q.Name, fa.Field}]) {
				continue
			}
			if !ok || lambdaCaptured[q.Name] || !fieldOwner(q.Name) {
				continue
			}
			if mentionsBesideFields(c, q.Name, fa.Field) != 1 {
				continue
			}
			if fieldDies[c] == nil {
				fieldDies[c] = map[*ast.FieldAccess]bool{}
			}
			fieldDies[c][fa] = true
			deferred[c] = true
		}
		return true
	})
	ast.Walk(body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok || inLambda[c] || deferred[c] {
			return true
		}
		for _, a := range argParts(c) {
			fa, ok := a.(*ast.FieldAccess)
			if !ok {
				continue
			}
			nested := fa != enclosingField(fa)
			fa = enclosingField(fa)
			q, ok := fa.Target.(*ast.Ident)
			if nested && (!ok || !exclusiveFields[[2]string{q.Name, fa.Field}]) {
				continue
			}
			if !ok || lambdaCaptured[q.Name] || deferBare[q.Name] || deferField[[2]string{q.Name, fa.Field}] || !fieldOwner(q.Name) {
				continue
			}
			if loopRepeats(body, c, declNode[q.Name]) {
				continue
			}
			if mentionsBesideFields(c, q.Name, fa.Field) != 1 || !fieldPathDead(info, bf, c, q, fa.Field, order, fieldOf, updates) || withinLiteralOf(body, c, q.Name) {
				continue
			}
			if fieldDies[c] == nil {
				fieldDies[c] = map[*ast.FieldAccess]bool{}
			}
			fieldDies[c][fa] = true
		}
		return true
	})
	// An element of an owned iterator moves at its last read in its own loop,
	// whatever other declarations share its name.
	ast.Walk(body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok || inLambda[c] {
			return true
		}
		for _, a := range argParts(c) {
			aid, ok := a.(*ast.Ident)
			if !ok || escaping[aid.Name] || lambdaCaptured[aid.Name] {
				continue
			}
			v := bindingVar(bf, c, aid.Name)
			if v != nil && iterOwned[forEachElementOf(v)] && !borrowedAssigns[v] && !loopRepeats(body, c, v) && lastInBinding(bf, c, aid, order) {
				moves[aid] = true
			}
		}
		return true
	})
	unadmitted := map[string]bool{}
	moveAdmitted := moveAdmission(info, body, isParam, ownParam, declCount, occurrences, order, admitted, unpack, payloadOwned)
	for _, dies := range out {
		for name := range dies {
			if !isParam[name] && !moveAdmitted[name] {
				unadmitted[name] = true
			}
		}
	}
	return ArgDeaths{Dies: out, Repeating: repeating, Escaping: escaping, Occurrences: occurrences, Deferred: deferred, FieldDies: fieldDies, Moves: moves, Bound: bound, Unadmitted: unadmitted}
}

// argParts lists the arguments of c, with each struct literal argument
// replaced by its base and its field values, at any depth. The literal takes
// each part, so a part is an argument position for the last-use facts.
func argParts(c *ast.Call) []ast.Expr {
	var out []ast.Expr
	var add func(e ast.Expr)
	add = func(e ast.Expr) {
		sl, ok := e.(*ast.StructLit)
		if !ok {
			out = append(out, e)
			return
		}
		if sl.Base != nil {
			add(sl.Base)
		}
		for _, f := range sl.Fields {
			add(f.Value)
		}
	}
	for _, a := range c.Args {
		add(a)
	}
	return out
}

// One resource read of an unchanged owned field leaves no earlier alias.
// Scalar and primitive COW array copies do not borrow resources.
func exclusiveOwnedFields(body ast.Node, own map[string]bool, fields map[*ast.Ident]string, occurrences map[string]int, scalar map[ast.Expr]bool, arrays map[*ast.FieldAccess]bool) map[[2]string]bool {
	assigned := map[string]bool{}
	copied := map[*ast.Ident]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		if fa, field := n.(*ast.FieldAccess); field && (scalar[fa] || arrays[fa]) {
			if root, plain := enclosingField(fa).Target.(*ast.Ident); plain {
				copied[root] = true
			}
		}
		if a, ok := n.(*ast.Assign); ok {
			if id, ok := a.Target.(*ast.Ident); ok {
				assigned[id.Name] = true
			}
		}
		return true
	})
	selected := map[string]int{}
	reads := map[[2]string]int{}
	for id, field := range fields {
		selected[id.Name]++
		if !copied[id] {
			reads[[2]string{id.Name, field}]++
		}
	}
	out := map[[2]string]bool{}
	for field, count := range reads {
		name := field[0]
		out[field] = own[name] && !assigned[name] && count == 1 && selected[name] == occurrences[name]
	}
	return out
}

// enclosingField is the field of the named owner that contains a projection.
// Its existing last-use proof also covers a descendant of that field.
func enclosingField(fa *ast.FieldAccess) *ast.FieldAccess {
	for {
		parent, nested := fa.Target.(*ast.FieldAccess)
		if !nested {
			return fa
		}
		fa = parent
	}
}

// mentionsBesideFields counts the mentions of name in c that are not reads
// of a field other than field. A move of name.field leaves those fields to
// the caller.
func mentionsBesideFields(c *ast.Call, name, field string) int {
	n := 0
	ast.Walk(c, func(m ast.Node) bool {
		switch x := m.(type) {
		case *ast.FieldAccess:
			if id, ok := x.Target.(*ast.Ident); ok && id.Name == name && x.Field != field {
				return false
			}
		case *ast.Ident:
			if x.Name == name {
				n++
			}
		}
		return true
	})
	return n
}

// assignsWhole reports `name = value` where value does not read name.
// wholeAssignment gives the statement st when it assigns the whole of name.
func wholeAssignment(st ast.Stmt, name string) *ast.Assign {
	es, ok := st.(*ast.ExprStmt)
	if !ok {
		return nil
	}
	asn, ok := es.Expr.(*ast.Assign)
	if !ok {
		return nil
	}
	if id, ok := asn.Target.(*ast.Ident); ok && id.Name == name {
		return asn
	}
	return nil
}

// loopRepeats reports a loop that runs c again on the same owner: one that
// holds c but not the node that binds the owner. A nil decl is a parameter,
// which no loop binds afresh.
func loopRepeats(body ast.Node, c ast.Node, decl ast.Node) bool {
	found := false
	ast.Walk(body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch n.(type) {
		case *ast.While, *ast.Loop, *ast.For, *ast.ForEach:
			if nodeHolds(n, c) && (decl == nil || !nodeHolds(n, decl)) {
				found = true
			}
		}
		return !found
	})
	return found
}

// forEachElementOf names the lowered iterator a for-each variable reads
// its element from, or "" for any other let.
func forEachElementOf(v *ast.Var) string {
	idx, ok := v.Init.(*ast.Index)
	if !ok {
		return ""
	}
	id, ok := idx.Array.(*ast.Ident)
	if !ok || !strings.HasPrefix(id.Name, ast.ForEachIterPrefix) {
		return ""
	}
	return id.Name
}

// withinLiteralOf reports whether c sits inside a struct literal whose base
// is name: `S { ...q, f: g(q.f) }` is the superseded-field rule's shape, and
// that rule decides it.
func withinLiteralOf(body ast.Node, c *ast.Call, name string) bool {
	found := false
	ast.Walk(body, func(n ast.Node) bool {
		if found {
			return false
		}
		sl, ok := n.(*ast.StructLit)
		if !ok || sl.Base == nil {
			return true
		}
		if bid, ok := sl.Base.(*ast.Ident); !ok || bid.Name != name {
			return true
		}
		ast.Walk(sl, func(m ast.Node) bool {
			if mc, isCall := m.(*ast.Call); isCall && mc == c {
				found = true
			}
			return !found
		})
		return !found
	})
	return found
}

// fieldPathDead reports that no path after holder reads q.field. A mention
// of q reads the path unless it reads another field, or is the base of an
// update literal that replaces field. A later mention in a branch that
// holder does not reach is not a read, and no path goes on after a return.
func fieldPathDead(info *Info, bf *bodyFrames, holder ast.Node, q *ast.Ident, field string, order IdentOrder, fieldOf map[*ast.Ident]string, updates map[*ast.Ident]map[string]bool) bool {
	frames := bf.of(holder)
	if len(frames) == 0 {
		return false
	}
	readsPath := func(id *ast.Ident) bool {
		if id.Name != q.Name {
			return false
		}
		f, isField := fieldOf[id]
		return !(isField && f != field || !isField && updates[id][field])
	}
	reads := func(n ast.Node) bool {
		found := false
		ast.Walk(n, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok && readsPath(id) {
				found = true
			}
			return !found
		})
		return found
	}
	inner := frames[len(frames)-1]
	later := false
	ast.Walk(inner.stmts[inner.idx], func(m ast.Node) bool {
		if id, ok := m.(*ast.Ident); ok && order.Idx[id] > order.Idx[q] && readsPath(id) {
			later = true
		}
		return !later
	})
	if _, returns := inner.stmts[inner.idx].(*ast.Return); returns {
		return !later
	}
	return !later && !liveFromReads(info, frames, len(frames)-1, q.Name, bindingLevel(frames, q.Name), reads)
}

// UpdateBases maps the base of each update literal `S { ...q, f: v }` to the
// fields the literal replaces.
func UpdateBases(body ast.Node) map[*ast.Ident]map[string]bool {
	out := map[*ast.Ident]map[string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		sl, ok := n.(*ast.StructLit)
		if !ok || sl.Base == nil {
			return true
		}
		if id, ok := sl.Base.(*ast.Ident); ok {
			fields := map[string]bool{}
			for _, f := range sl.Fields {
				fields[f.Name] = true
			}
			out[id] = fields
		}
		return true
	})
	return out
}

// freshInit reports an initialiser whose value no other name holds. A plain
// named call qualifies unless its result can borrow from an argument.
func freshInit(info *Info, init ast.Expr) bool {
	if info != nil {
		if value, ok := info.EnumConstructions[init]; ok && len(value.Payloads) == 0 {
			return true
		}
	}
	switch init := init.(type) {
	case *ast.TryOp:
		return freshInit(info, init.Inner)
	case *ast.Call:
		if freshAssociatedResult(init) {
			return true
		}
		// An `own` result borrows no argument, also through a function value.
		if info != nil && info.CallOwnedResult != nil && info.CallOwnedResult(init) {
			return true
		}
		if info != nil {
			// A variant construction from another module has a qualified
			// callee. It is fresh on the same terms as an unqualified one.
			if _, isVariant := info.EnumConstructions[init]; isVariant {
				return info.CallBorrows == nil || !info.CallBorrows(init)
			}
		}
		if _, named := init.Callee.(*ast.Ident); !named {
			return false
		}
		if init.Method != nil && (info == nil || info.DeclaredMethod == nil || !info.DeclaredMethod(init)) {
			return true
		}
		return info == nil || info.CallBorrows == nil || !info.CallBorrows(init)
	case *ast.ArrayLit, *ast.StringLit:
		return true
	case *ast.StructLit:
		return init.Base == nil || freshInit(info, init.Base)
	case *ast.MatchExpr:
		return freshMatchValue(info, init)
	case *ast.IfExpr:
		return freshScopedValue(info, nil, init.Then) && freshScopedValue(info, nil, init.Else)
	case *ast.BlockExpr:
		return init.Tail != nil && freshScopedValue(info, init, init.Tail)
	case *ast.TupleLit:
		return freshScopedValue(info, nil, init)
	case *ast.FieldAccess:
		return tupleProjection(init) && freshInit(info, init.Target)
	}
	return false
}

// tupleProjection reports an element read `t.0`. A struct field cannot have
// a numeric name.
func tupleProjection(fa *ast.FieldAccess) bool {
	if fa.Field == "" {
		return false
	}
	for _, r := range fa.Field {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// projectionRoot is the value under a chain of element reads `v.1.0`.
func projectionRoot(e ast.Expr) ast.Expr {
	for {
		fa, ok := e.(*ast.FieldAccess)
		if !ok || !tupleProjection(fa) {
			return e
		}
		e = fa.Target
	}
}

// A compound block result can take unmodified local payloads from fresh values.
func freshScopedValue(info *Info, block *ast.BlockExpr, value ast.Expr) bool {
	switch x := value.(type) {
	case *ast.TupleLit:
		for _, element := range x.Elems {
			if !freshScopedValue(info, block, element) {
				return false
			}
		}
		return true
	case *ast.NumberLit, *ast.BoolLit:
		return true
	case *ast.Call:
		if info != nil {
			if constructor, ok := info.EnumConstructions[x]; ok && len(constructor.Payloads) == len(x.Args) {
				for i, argument := range x.Args {
					if (info.TypeHoldsNoHandle == nil || !info.TypeHoldsNoHandle(constructor.Payloads[i])) && !freshScopedValue(info, block, argument) {
						return false
					}
				}
				return true
			}
		}
		return freshInit(info, value) && (info == nil || info.CallScopedFresh == nil || info.CallScopedFresh(x))
	case *ast.Ident:
		if block == nil {
			return freshInit(info, value)
		}
		body := &ast.Block{Stmts: append(slices.Clone(block.Stmts), &ast.ExprStmt{Expr: block.Tail})}
		reads := 0
		ast.Walk(body, func(node ast.Node) bool {
			if id, ok := node.(*ast.Ident); ok && id.Name == x.Name {
				reads++
			}
			if assign, ok := node.(*ast.Assign); ok && StmtReferencesName(assign.Target, x.Name) {
				reads += 2
			}
			return true
		})
		if reads != 1 || DeferOrLambdaNames(body)[x.Name] {
			return false
		}
		for i := len(block.Stmts) - 1; i >= 0; i-- {
			if !bindsName(block.Stmts[i], x.Name) {
				continue
			}
			switch binding := block.Stmts[i].(type) {
			case *ast.Var:
				return freshScopedValue(info, nil, binding.Init)
			case *ast.Destructure:
				return binding.AtName == "" && freshScopedValue(info, nil, binding.Init)
			}
			return false
		}
		return freshInit(info, value)
	case *ast.MatchExpr:
		return ownedMatchValue(info, x, func(e ast.Expr) bool { return freshScopedValue(info, block, e) }, func(ast.Expr) bool { return false })
	}
	return freshInit(info, value)
}

// freshMatchValue reports a match on a fresh value whose each variant arm
// yields a payload it binds, a fresh value, or leaves.
func freshMatchValue(info *Info, m *ast.MatchExpr) bool {
	if _, named := m.Tag.(*ast.Ident); named || m.StructMatch != "" || m.IsFloat || len(m.Arms) == 0 || !freshInit(info, m.Tag) {
		return false
	}
	for _, arm := range m.Arms {
		if arm.VariantName == "" && !arm.IsWildcard || len(arm.TupleElems) > 0 || !armYieldsFresh(info, arm) {
			return false
		}
	}
	return true
}

func armYieldsFresh(info *Info, arm *ast.MatchExprArm) bool {
	value := arm.Body
	var block *ast.BlockExpr
	if b, ok := value.(*ast.BlockExpr); ok {
		if len(b.Stmts) > 0 && divergesStmt(info, b.Stmts[len(b.Stmts)-1]) {
			return true
		}
		if b.Tail == nil {
			return false
		}
		value = b.Tail
		block = b
	}
	if id, ok := value.(*ast.Ident); ok && (slices.Contains(arm.Binders(), id.Name) || block != nil && slices.ContainsFunc(block.Stmts, func(st ast.Stmt) bool { return bindsName(st, id.Name) })) {
		return freshArmPayload(info, block, id)
	}
	return freshScopedValue(info, block, value)
}

// freshArmPayload checks the binding that the arm returns: its payload, or a
// local of its block. A nested shadow does not change that binding.
func freshArmPayload(info *Info, block *ast.BlockExpr, id *ast.Ident) bool {
	if block == nil {
		return true
	}
	body := &ast.Block{Stmts: append(slices.Clone(block.Stmts), &ast.ExprStmt{Expr: id})}
	for i := len(block.Stmts) - 1; i >= 0; i-- {
		if bindsName(block.Stmts[i], id.Name) {
			if _, ok := block.Stmts[i].(*ast.Var); !ok {
				return false
			}
			break
		}
	}
	bf := newBodyFrames(body)
	bound := bindingVar(bf, id, id.Name)
	if bound != nil && !freshInit(info, bound.Init) {
		return false
	}
	fresh := true
	ast.Walk(body, func(n ast.Node) bool {
		if a, ok := n.(*ast.Assign); ok {
			if target, ok := a.Target.(*ast.Ident); ok && target.Name == id.Name && bindingVar(bf, a, id.Name) == bound && !freshInit(info, a.Value) && !freshLocalReplacement(info, bf, a, a.Value, IdentOrderOf(body)) {
				fresh = false
			}
		}
		return fresh
	})
	return fresh
}

// A replacement can take an unmodified fresh local at its last read.
func freshLocalReplacement(info *Info, bf *bodyFrames, holder ast.Node, value ast.Expr, order IdentOrder) bool {
	id, ok := value.(*ast.Ident)
	if !ok || bf.deferOrLambda()[id.Name] {
		return false
	}
	source := bindingVar(bf, holder, id.Name)
	if source == nil || !freshInit(info, source.Init) || loopRepeats(bf.body, holder, source) || !lastInBinding(bf, holder, id, order) {
		return false
	}
	for _, a := range bf.assignsTo(id.Name) {
		if bindingVar(bf, a, id.Name) == source {
			return false
		}
	}
	return true
}

// moveAdmission names the locals that can move into an own parameter: each
// admitted local, and each alias `let a = b`, declared once outside a loop,
// that takes an owned b at its last use and is given only owned values after
// that. An owned value is a fresh value, an unpack, an owned name, or a
// self-update `T { ...a, f: v }`.
func moveAdmission(info *Info, body *ast.Block, isParam, ownParam map[string]bool, declCount, occurrences map[string]int, order IdentOrder, admitted func(string) bool, unpack func(ast.Expr) bool, payloadOwned func(string) bool) map[string]bool {
	looped := loopedDecls(body)
	aliasOf := map[string]*ast.Ident{}
	assigns := map[string][]ast.Expr{}
	ast.Walk(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Var:
			src, ok := x.Init.(*ast.Ident)
			if !ok {
				src, ok = spreadSource(x.Init)
			}
			if ok && declCount[x.Name] == 1 && !looped[x.Name] && order.IsLast(src) {
				aliasOf[x.Name] = src
			}
		case *ast.Assign:
			if t, ok := x.Target.(*ast.Ident); ok {
				assigns[t.Name] = append(assigns[t.Name], x.Value)
			}
		}
		return true
	})
	in := map[string]bool{}
	for name := range occurrences {
		if !isParam[name] && (admitted(name) || aliasOf[name] != nil) {
			in[name] = true
		}
	}
	owned := func(name string) bool {
		return ownParam[name] && declCount[name] == 0 || payloadOwned(name) || in[name]
	}
	valueOwned := func(target string, v ast.Expr) bool {
		if freshInit(info, v) || unpack(v) {
			return true
		}
		switch x := v.(type) {
		case *ast.Ident:
			return owned(x.Name)
		case *ast.StructLit:
			b, ok := x.Base.(*ast.Ident)
			return ok && b.Name == target
		}
		return false
	}
	for changed := true; changed; {
		changed = false
		for name := range in {
			if admitted(name) {
				continue
			}
			keep := aliasOf[name] != nil && owned(aliasOf[name].Name)
			for _, v := range assigns[name] {
				keep = keep && valueOwned(name, v)
			}
			if !keep {
				delete(in, name)
				changed = true
			}
		}
	}
	return in
}

// spreadSource is the base of a struct literal `T { ...src, f: v }`. The
// literal takes the rest of src, as an alias does. A field value can read a
// field of src that the literal replaces, as `i: src.i + 1` does, because
// the literal does not take that field. The result is the last mention of
// src in the literal: the literal takes src there.
func spreadSource(e ast.Expr) (*ast.Ident, bool) {
	sl, ok := e.(*ast.StructLit)
	if !ok {
		return nil, false
	}
	src, ok := sl.Base.(*ast.Ident)
	if !ok {
		return nil, false
	}
	replaced := map[string]bool{}
	for _, f := range sl.Fields {
		replaced[f.Name] = true
	}
	last := src
	for _, f := range sl.Fields {
		if !StmtReferencesName(f.Value, src.Name) {
			continue
		}
		mention, ok := lastReplacedRead(f.Value, src.Name, replaced)
		if !ok {
			return nil, false
		}
		if identAfter(mention, last) {
			last = mention
		}
	}
	return last, true
}

// lastReplacedRead is the last mention of name in e, when each mention reads
// one of the replaced fields of it.
func lastReplacedRead(e ast.Expr, name string, replaced map[string]bool) (*ast.Ident, bool) {
	var last *ast.Ident
	ok := true
	ast.Walk(e, func(n ast.Node) bool {
		if !ok {
			return false
		}
		switch x := n.(type) {
		case *ast.Lambda:
			if lambdaFreeNames(x)[name] {
				ok = false
			}
			return false
		case *ast.FieldAccess:
			if id, isIdent := x.Target.(*ast.Ident); isIdent && id.Name == name {
				if !replaced[x.Field] {
					ok = false
				} else if last == nil || identAfter(id, last) {
					last = id
				}
				return false
			}
		case *ast.Ident:
			if x.Name == name {
				ok = false
			}
		case *ast.Assign:
			if t, isIdent := x.Target.(*ast.Ident); isIdent && t.Name == name {
				ok = false
			}
		}
		return ok
	})
	return last, ok && last != nil
}

func identAfter(a, b *ast.Ident) bool {
	return a.P.Line > b.P.Line || a.P.Line == b.P.Line && a.P.Col > b.P.Col
}

// loopedDecls names the locals that a declaration inside a loop binds.
func loopedDecls(body *ast.Block) map[string]bool {
	looped := map[string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.While, *ast.Loop, *ast.For, *ast.ForEach:
			ast.Walk(n, func(m ast.Node) bool {
				switch d := m.(type) {
				case *ast.Var:
					looped[d.Name] = true
				case *ast.Destructure:
					for _, name := range d.Names {
						looped[name] = true
					}
				}
				return true
			})
		}
		return true
	})
	return looped
}

// matchBinds names the payloads that a match on a fresh call binds, and the
// payloads that a match on another value binds.
func matchBinds(info *Info, body *ast.Block) (fresh, stale map[string]bool) {
	fresh = map[string]bool{}
	stale = map[string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		if m, ok := n.(*ast.Match); ok {
			_, named := m.Tag.(*ast.Ident)
			taken := !named && freshInit(info, m.Tag)
			for _, arm := range m.Arms {
				names, _ := arm.OwnBindings()
				for _, b := range names {
					if taken {
						fresh[b] = true
					} else {
						stale[b] = true
					}
				}
			}
		}
		return true
	})
	return fresh, stale
}

// assignedOwned reports a value an assignment to target stores that no
// other name holds: a fresh value, an unpack, a fresh local, a payload a
// match took, a self-update `T { ...target, f: v }`, or a call whose result
// borrows from no argument but target itself, also through a match payload.
func assignedOwned(info *Info, target string, v ast.Expr, freshLocal map[string]bool, payloadOwned func(string) bool, payloadOf func(*ast.Ident, string) bool, unpack func(ast.Expr) bool) bool {
	if freshInit(info, v) || unpack(v) {
		return true
	}
	switch x := v.(type) {
	case *ast.TryOp:
		return assignedOwned(info, target, x.Inner, freshLocal, payloadOwned, payloadOf, unpack)
	case *ast.IfExpr:
		return assignedOwned(info, target, x.Then, freshLocal, payloadOwned, payloadOf, unpack) && assignedOwned(info, target, x.Else, freshLocal, payloadOwned, payloadOf, unpack)
	case *ast.BlockExpr:
		return x.Tail != nil && assignedOwned(info, target, x.Tail, freshLocal, payloadOwned, payloadOf, unpack)
	case *ast.Ident:
		return freshLocal[x.Name] || payloadOwned(x.Name) || payloadOf(x, target)
	case *ast.StructLit:
		b, ok := x.Base.(*ast.Ident)
		return ok && b.Name == target
	case *ast.Call:
		_, named := x.Callee.(*ast.Ident)
		return named && info != nil && info.CallBorrowsBeside != nil && !info.CallBorrowsBeside(x, target)
	}
	return false
}

// copiedArgs names direct scalar field reads before an argument that is name
// alone. A scalar-returning call can consume name through an aggregate, so
// its result type alone does not make it a copy of a field.
func copiedArgs(scope ast.Node, name string, scalar map[ast.Expr]bool) map[ast.Expr]bool {
	out := map[ast.Expr]bool{}
	if len(scalar) == 0 {
		return out
	}
	ast.Walk(scope, func(m ast.Node) bool {
		c, isCall := m.(*ast.Call)
		if !isCall {
			return true
		}
		parts := argParts(c)
		for k, a := range parts {
			if aid, ok := a.(*ast.Ident); ok && aid.Name == name {
				for _, before := range parts[:k] {
					fa, isField := before.(*ast.FieldAccess)
					if !isField || !scalar[before] {
						continue
					}
					if target, ok := fa.Target.(*ast.Ident); ok && target.Name == name {
						out[before] = true
					}
				}
			}
		}
		return true
	})
	return out
}

// ownedAliases names the locals declared once, outside a loop, from an own
// parameter or a payload that a match took, at its last use, and given only
// owned values after that (stale names the locals given another value).
func ownedAliases(info *Info, body *ast.Block, ownParam map[string]bool, declCount map[string]int, order IdentOrder, freshLocal map[string]bool, stale map[string]bool, payloadOwned func(string) bool) map[string]bool {
	looped := loopedDecls(body)
	sole := func(name string) bool {
		return declCount[name] == 1 && !looped[name] && !stale[name]
	}
	out := map[string]bool{}
	ownedAtLastUse := func(e ast.Expr) bool {
		src, ok := e.(*ast.Ident)
		return ok && (ownParam[src.Name] && declCount[src.Name] == 0 || payloadOwned(src.Name) || out[src.Name]) && order.IsLast(src)
	}
	freshAtLastUse := func(e ast.Expr) bool {
		src, ok := e.(*ast.Ident)
		return ok && freshLocal[src.Name] && sole(src.Name) && order.IsLast(src)
	}
	movedPayload := movedPayloads(body, ownParam, declCount, order, func(name string) bool {
		return ownParam[name] && declCount[name] == 0 || payloadOwned(name) || freshLocal[name] && sole(name) || out[name]
	})
	ast.Walk(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Var:
			source := x.Init
			if base, ok := spreadSource(source); ok {
				source = base
			}
			if !sole(x.Name) {
				break
			}
			if ownedAtLastUse(source) || freshAtLastUse(source) {
				out[x.Name] = true
			} else if m, ok := source.(*ast.MatchExpr); ok && ownedMatchValue(info, m, ownedAtLastUse, freshAtLastUse) {
				out[x.Name] = true
			}
		case *ast.Destructure:
			// The fields that the destructure takes from an own parameter, a
			// payload a match took, or a fresh local at its last use are
			// owned, as the source is.
			src, isID := x.Init.(*ast.Ident)
			if x.AtName != "" || !ownedAtLastUse(x.Init) && !freshAtLastUse(x.Init) && !(isID && movedPayload(src.Name) && order.IsLast(src)) {
				return true
			}
			for i, name := range x.Names {
				if sole(name) && (i >= len(x.Nested) || x.Nested[i] == nil) {
					out[name] = true
				}
			}
		}
		return true
	})
	return out
}

// A match that consumes an owned local transfers an unmodified payload into
// its result. A sibling field of the source does not retain that payload.
func ownedMatchValue(info *Info, m *ast.MatchExpr, ownedAtLastUse, freshAtLastUse func(ast.Expr) bool) bool {
	if m.StructMatch != "" || m.IsFloat || len(m.Arms) == 0 || !ownedAtLastUse(m.Tag) && !freshAtLastUse(m.Tag) {
		return false
	}
	for _, arm := range m.Arms {
		if arm.VariantName == "" && !arm.IsWildcard || len(arm.TupleElems) > 0 || !armYieldsFresh(info, arm) {
			return false
		}
	}
	return true
}

// movedPayloads reports the payloads of a match on an owned name at its last
// use. The match takes the value, so the payload is owned. A name is owned
// when owned says so, or when it is such a payload.
func movedPayloads(body *ast.Block, ownParam map[string]bool, declCount map[string]int, order IdentOrder, owned func(string) bool) func(string) bool {
	tags := map[string][]ast.Expr{}
	ast.Walk(body, func(n ast.Node) bool {
		if m, ok := n.(*ast.Match); ok {
			for _, arm := range m.Arms {
				for _, b := range arm.Binders() {
					tags[b] = append(tags[b], m.Tag)
				}
			}
		}
		return true
	})
	var moved func(name string, seen map[string]bool) bool
	moved = func(name string, seen map[string]bool) bool {
		if ownParam[name] || declCount[name] != 0 || seen[name] || len(tags[name]) == 0 {
			return false
		}
		seen[name] = true
		defer delete(seen, name)
		for _, tag := range tags[name] {
			src, ok := tag.(*ast.Ident)
			if !ok || !order.IsLast(src) || !owned(src.Name) && !moved(src.Name, seen) {
				return false
			}
		}
		return true
	}
	return func(name string) bool { return moved(name, map[string]bool{}) }
}

// heldByEnclosingCall reports whether a call that encloses c reads name
// outside c.
func (b *bodyFrames) heldByEnclosingCall(c *ast.Call, name string) bool {
	if b.enclosing == nil {
		b.enclosing = map[*ast.Call][]*ast.Call{}
		b.held = map[*ast.Call]map[string]bool{}
		ast.Walk(b.body, func(n ast.Node) bool {
			p, ok := n.(*ast.Call)
			if !ok {
				return true
			}
			ast.Walk(p, func(m ast.Node) bool {
				if mc, ok := m.(*ast.Call); ok && mc != p {
					b.enclosing[mc] = append(b.enclosing[mc], p)
				}
				return true
			})
			return false
		})
	}
	names, ok := b.held[c]
	if !ok {
		names = map[string]bool{}
		for _, p := range b.enclosing[c] {
			ast.Walk(p, func(m ast.Node) bool {
				if mc, ok := m.(*ast.Call); ok && mc == c {
					return false
				}
				if id, ok := m.(*ast.Ident); ok {
					names[id.Name] = true
				}
				return true
			})
		}
		b.held[c] = names
	}
	return names[name]
}

func markSupersededFields(out map[*ast.Call]map[string]bool, sl *ast.StructLit, target string) {
	if sl.Base == nil {
		return
	}
	if bid, ok := sl.Base.(*ast.Ident); !ok || bid.Name != target {
		return
	}
	for _, f := range sl.Fields {
		var c *ast.Call
		callsOutsideLambdas(f.Value, func(call *ast.Call) bool {
			if _, named := call.Callee.(*ast.Ident); !named {
				return false
			}
			direct := 0
			for _, a := range call.Args {
				if fa, ok := a.(*ast.FieldAccess); ok && fa.Field == f.Name {
					if id, ok := fa.Target.(*ast.Ident); ok && id.Name == target {
						direct++
					}
				}
			}
			if direct == 1 {
				c = call
			}
			return c != nil
		})
		if c == nil {
			continue
		}
		total := 0
		ast.Walk(sl, func(m ast.Node) bool {
			if fa, ok := m.(*ast.FieldAccess); ok && fa.Field == f.Name {
				if id, ok := fa.Target.(*ast.Ident); ok && id.Name == target {
					total++
				}
			}
			return true
		})
		if total != 1 {
			continue
		}
		if out[c] == nil {
			out[c] = map[string]bool{}
		}
		out[c][target+"."+f.Name] = true
	}
}

// lastUseArgs returns the arguments at their last use, and the subset of
// them that are unadmitted locals (see ArgDeaths.Unadmitted).
func lastUseArgs(fn *ast.FuncDecl, info *Info, handleFree func(ast.Type) bool, scalar map[ast.Expr]bool) (map[ast.Expr]bool, map[ast.Expr]bool) {
	defer distinctBindings(fn)()
	return lastUseArgsOf(fn, info, handleFree, scalar)
}

func lastUseArgsOf(fn *ast.FuncDecl, info *Info, handleFree func(ast.Type) bool, scalar map[ast.Expr]bool) (map[ast.Expr]bool, map[ast.Expr]bool) {
	out := map[ast.Expr]bool{}
	loose := map[ast.Expr]bool{}
	if fn.Body == nil {
		return out, loose
	}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		inner, ok := n.(*ast.FuncDecl)
		// A lambda is decided as a function of its own, so a name it declares
		// can move at its last use in the lambda's body.
		if lm, isLambda := n.(*ast.Lambda); isLambda {
			inner, ok = &ast.FuncDecl{Params: lm.Params, ReturnOwn: lm.ReturnOwn, Body: lm.Body}, true
		}
		if ok && inner != fn {
			innerOut, innerLoose := lastUseArgsOf(inner, info, handleFree, scalar)
			for e := range innerOut {
				out[e] = true
			}
			for e := range innerLoose {
				loose[e] = true
			}
			return false
		}
		return true
	})
	d := callArgDeaths(fn, info, handleFree, scalar)
	isParam := map[string]bool{}
	ownParam := map[string]bool{}
	for _, p := range fn.Params {
		isParam[p.Name] = true
		if p.Own {
			ownParam[p.Name] = true
		}
	}
	local := map[string]bool{}
	nested := map[*ast.Call]bool{}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Var:
			if !isParam[x.Name] {
				local[x.Name] = true
			}
		case *ast.Destructure:
			for _, name := range x.Names {
				if !isParam[name] {
					local[name] = true
				}
			}
		case *ast.FuncDecl, *ast.Lambda:
			ast.Walk(x, func(m ast.Node) bool {
				if c, ok := m.(*ast.Call); ok {
					nested[c] = true
				}
				return true
			})
		}
		return true
	})
	for call, dies := range d.Dies {
		if nested[call] {
			continue
		}
		for _, a := range argParts(call) {
			if id, ok := a.(*ast.Ident); ok && dies[id.Name] && (local[id.Name] || d.Bound[id.Name]) && (!d.Escaping[id.Name] || d.Deferred[call]) {
				out[id] = true
				if d.Unadmitted[id.Name] {
					loose[id] = true
				}
			}
		}
	}
	for call, fields := range d.FieldDies {
		if nested[call] {
			continue
		}
		for fa := range fields {
			if id, ok := fa.Target.(*ast.Ident); ok && (local[id.Name] || ownParam[id.Name] || d.Bound[id.Name]) {
				out[fa] = true
			}
		}
	}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		if fa, ok := n.(*ast.FieldAccess); ok && out[enclosingField(fa)] {
			out[fa] = true
		}
		return true
	})

	nestedExpr := map[ast.Expr]bool{}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.FuncDecl, *ast.Lambda:
			ast.Walk(n, func(m ast.Node) bool {
				if e, ok := m.(ast.Expr); ok {
					nestedExpr[e] = true
				}
				return true
			})
			return false
		}
		return true
	})
	for e := range d.Moves {
		if nestedExpr[e] {
			continue
		}
		switch t := e.(type) {
		case *ast.Ident:
			if (local[t.Name] || d.Bound[t.Name] || ownParam[t.Name]) && !d.Escaping[t.Name] {
				out[e] = true
				delete(loose, e)
			}
		case *ast.FieldAccess:
			if id, ok := t.Target.(*ast.Ident); ok && (local[id.Name] || ownParam[id.Name] || d.Bound[id.Name]) {
				out[e] = true
			}
		}
	}
	return out, loose
}

func storeTarget(st ast.Stmt) (string, *ast.Assign) {
	es, isExpr := st.(*ast.ExprStmt)
	if !isExpr {
		return "", nil
	}
	asn, isAsn := es.Expr.(*ast.Assign)
	if !isAsn || asn.Value == nil {
		return "", nil
	}
	t, isID := asn.Target.(*ast.Ident)
	if !isID {
		return "", nil
	}
	return t.Name, asn
}

func reboundAfter(gap []ast.Stmt, name string) bool {
	for _, st := range gap {
		switch st.(type) {
		case *ast.Var, *ast.Destructure, *ast.ExprStmt:
		default:
			return false
		}
		if StmtReferencesName(st, name) || holdsTry(st) {
			return false
		}
	}
	return true
}

func holdsTry(n ast.Node) bool {
	found := false
	ast.Walk(n, func(m ast.Node) bool {
		switch m.(type) {
		case *ast.TryOp:
			found = true
		case *ast.Lambda, *ast.FuncDecl:
			return false
		}
		return !found
	})
	return found
}

// liveFrame is one block on the path from a function body to a statement:
// the block's statements, the index of the statement on the path, and the
// loop whose body the block is, if any.
type liveFrame struct {
	stmts []ast.Stmt
	idx   int
	loop  ast.Stmt
	label string
}

func nodeHolds(n ast.Node, target ast.Node) bool {
	found := false
	ast.Walk(n, func(m ast.Node) bool {
		if found {
			return false
		}
		if m == target {
			found = true
		}
		return !found
	})
	return found
}

// bodyFrames answers questions about one body, which the analysis does not
// change, and keeps each answer.
type bodyFrames struct {
	body      *ast.Block
	frames    map[ast.Node][]liveFrame
	escapes   map[string]bool
	assigns   map[string][]*ast.Assign
	enclosing map[*ast.Call][]*ast.Call // the outermost calls that hold each call
	held      map[*ast.Call]map[string]bool
}

func newBodyFrames(body *ast.Block) *bodyFrames {
	return &bodyFrames{body: body}
}

// of is the chain of blocks from the body down to the innermost statement
// that holds target. A statement inside an expression-level block or a
// deferred action counts as its enclosing statement.
func (b *bodyFrames) of(target ast.Node) []liveFrame {
	if b.frames == nil {
		b.frames = map[ast.Node][]liveFrame{}
		if b.body != nil {
			b.index(b.body.Stmts, nil, "", nil)
		}
	}
	f := b.frames[target]
	return f[:len(f):len(f)]
}

// index gives each node under stmts its chain. A node that two places hold
// keeps the chain of the first statement, of a then branch before its else
// branch, of the first match arm, and of the innermost statement.
func (b *bodyFrames) index(stmts []ast.Stmt, loop ast.Stmt, label string, outer []liveFrame) {
	for i, st := range stmts {
		chain := append(outer[:len(outer):len(outer)], liveFrame{stmts: stmts, idx: i, loop: loop, label: label})
		switch x := st.(type) {
		case *ast.Block:
			b.index(x.Stmts, nil, "", chain)
		case *ast.If:
			b.indexBody(x.Then, nil, "", chain)
			b.indexBody(x.Else, nil, "", chain)
		case *ast.Match:
			for _, arm := range x.Arms {
				if arm.Body != nil {
					b.index(arm.Body.Stmts, nil, "", chain)
				}
			}
		case *ast.While:
			b.indexBody(x.Body, x, x.Label, chain)
		case *ast.Loop:
			b.indexBody(x.Body, x, x.Label, chain)
		case *ast.For:
			b.indexBody(x.Body, x, x.Label, chain)
		case *ast.ForEach:
			b.indexBody(x.Body, x, x.Label, chain)
		}
		ast.Walk(st, func(n ast.Node) bool {
			if _, ok := b.frames[n]; !ok {
				b.frames[n] = chain
			}
			return true
		})
	}
}

// indexBody indexes a branch or a loop body, which is a block or one
// statement.
func (b *bodyFrames) indexBody(body ast.Stmt, loop ast.Stmt, label string, outer []liveFrame) {
	if blk, ok := body.(*ast.Block); ok {
		b.index(blk.Stmts, loop, label, outer)
	} else if body != nil {
		b.index([]ast.Stmt{body}, loop, label, outer)
	}
}

// assignsTo is each assignment in the body whose target is the name.
func (b *bodyFrames) assignsTo(name string) []*ast.Assign {
	if b.assigns == nil {
		b.assigns = map[string][]*ast.Assign{}
		ast.Walk(b.body, func(n ast.Node) bool {
			if a, ok := n.(*ast.Assign); ok {
				if target, ok := a.Target.(*ast.Ident); ok {
					b.assigns[target.Name] = append(b.assigns[target.Name], a)
				}
			}
			return true
		})
	}
	return b.assigns[name]
}

// deferOrLambda is DeferOrLambdaNames of the body.
func (b *bodyFrames) deferOrLambda() map[string]bool {
	if b.escapes == nil {
		b.escapes = DeferOrLambdaNames(b.body)
	}
	return b.escapes
}

// stmtJumps is the control that leaves a statement without completing it:
// a return or an exit call, and the breaks and continues that escape it
// with the label each one names ("" for the innermost loop).
type stmtJumps struct {
	returns   bool
	breaks    []string
	continues []string
}

func jumpsOf(info *Info, st ast.Stmt) stmtJumps {
	var out stmtJumps
	inner := map[string]bool{}
	ast.Walk(st, func(m ast.Node) bool {
		switch x := m.(type) {
		case *ast.Lambda, *ast.FuncDecl, *ast.Defer:
			return false
		case *ast.Loop:
			inner[x.Label] = true
		case *ast.While:
			inner[x.Label] = true
		case *ast.For:
			inner[x.Label] = true
		case *ast.ForEach:
			inner[x.Label] = true
		}
		return true
	})
	escapes := func(label string, nested bool) bool {
		if label == "" {
			return !nested
		}
		return !inner[label]
	}
	var walk func(n ast.Node, nested bool)
	walk = func(n ast.Node, nested bool) {
		ast.Walk(n, func(m ast.Node) bool {
			switch x := m.(type) {
			case *ast.Lambda, *ast.FuncDecl, *ast.Defer:
				return false
			case *ast.Return:
				out.returns = true
			case *ast.Call:
				if info != nil && info.IntrinsicCalls[x].Kind == IntrinsicExit {
					out.returns = true
				}
			case *ast.Break:
				if escapes(x.Label, nested) {
					out.breaks = append(out.breaks, x.Label)
				}
				return false
			case *ast.Continue:
				if escapes(x.Label, nested) {
					out.continues = append(out.continues, x.Label)
				}
				return false
			case *ast.Loop:
				walk(x.Body, true)
				return false
			case *ast.While:
				walk(x.Cond, nested)
				walk(x.Body, true)
				return false
			case *ast.For:
				if x.Init != nil {
					walk(x.Init, nested)
				}
				if x.Cond != nil {
					walk(x.Cond, nested)
				}
				if x.Step != nil {
					walk(x.Step, nested)
				}
				walk(x.Body, true)
				return false
			case *ast.ForEach:
				walk(x.Iter, nested)
				walk(x.Body, true)
				return false
			}
			return true
		})
	}
	walk(st, false)
	return out
}

// neverReturns reports a statement that calls a function which never
// returns.
func neverReturns(info *Info, st ast.Stmt) bool {
	es, ok := st.(*ast.ExprStmt)
	return ok && info != nil && callNeverReturns(es.Expr, info.NoReturn, true)
}

// NoReturnFuncs names the plain functions whose body never returns: on every
// path it ends in a call to exit, in a call to another such function, or in
// a loop that never breaks.
func NoReturnFuncs(prog *ast.Program) map[string]bool {
	out := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, fn := range prog.Funcs {
			if fn.Body == nil || fn.Receiver != nil || fn.MethodRecv != "" || out[fn.Name] {
				continue
			}
			bound := false
			for _, p := range fn.Params {
				bound = bound || p.Name == "exit"
			}
			if stmtsExit(fn.Body.Stmts, out, bound) {
				out[fn.Name] = true
				changed = true
			}
		}
	}
	return out
}

func stmtsExit(stmts []ast.Stmt, noReturn map[string]bool, bound bool) bool {
	return len(stmts) > 0 && stmtExits(stmts[len(stmts)-1], noReturn, bound)
}

func stmtExits(st ast.Stmt, noReturn map[string]bool, bound bool) bool {
	switch x := st.(type) {
	case *ast.ExprStmt:
		return callNeverReturns(x.Expr, noReturn, bound)
	case *ast.Block:
		return stmtsExit(x.Stmts, noReturn, bound)
	case *ast.If:
		return x.Else != nil && stmtExits(x.Then, noReturn, bound) && stmtExits(x.Else, noReturn, bound)
	case *ast.Match:
		for _, arm := range x.Arms {
			if arm.Body == nil || !stmtsExit(arm.Body.Stmts, noReturn, bound) {
				return false
			}
		}
		return len(x.Arms) > 0
	case *ast.Loop:
		return !loopCanBreak(x.Body, x.Label)
	case *ast.While:
		lit, ok := x.Cond.(*ast.BoolLit)
		return ok && lit.Value && !loopCanBreak(x.Body, x.Label)
	}
	return false
}

// bound: a parameter named exit hides the builtin, so the check names only
// the functions noReturn holds.
func callNeverReturns(e ast.Expr, noReturn map[string]bool, bound bool) bool {
	c, ok := e.(*ast.Call)
	if !ok {
		return false
	}
	id, ok := c.Callee.(*ast.Ident)
	if !ok {
		return false
	}
	if id.Name == "exit" && !bound {
		return true
	}
	return noReturn[id.Name]
}

// divergesStmt reports a statement that leaves on every path.
func divergesStmt(info *Info, st ast.Stmt) bool {
	switch x := st.(type) {
	case *ast.Return, *ast.Break, *ast.Continue:
		return true
	case *ast.ExprStmt:
		call, ok := x.Expr.(*ast.Call)
		return ok && info != nil && info.IntrinsicCalls[call].Kind == IntrinsicExit
	case *ast.Loop:
		return !loopCanBreak(x.Body, x.Label)
	case *ast.While:
		if lit, ok := x.Cond.(*ast.BoolLit); ok && lit.Value {
			return !loopCanBreak(x.Body, x.Label)
		}
		return false
	case *ast.Block:
		return len(x.Stmts) > 0 && divergesStmt(info, x.Stmts[len(x.Stmts)-1])
	case *ast.If:
		return x.Else != nil && divergesStmt(info, x.Then) && divergesStmt(info, x.Else)
	case *ast.Match:
		for i, arm := range x.Arms {
			if letElseDivergentArm(x, i) {
				continue
			}
			if arm.Body == nil || len(arm.Body.Stmts) == 0 || !divergesStmt(info, arm.Body.Stmts[len(arm.Body.Stmts)-1]) {
				return false
			}
		}
		return len(x.Arms) > 0
	}
	return false
}

func declaresName(n ast.Node, name string) bool {
	found := false
	ast.Walk(n, func(m ast.Node) bool {
		if found {
			return false
		}
		switch x := m.(type) {
		case *ast.Lambda, *ast.FuncDecl:
			return false
		case *ast.Var:
			found = x.Name == name
		case *ast.Destructure:
			found = slices.Contains(x.Names, name)
		case *ast.Match:
			for _, arm := range x.Arms {
				found = found || slices.Contains(arm.Binders(), name)
			}
		case *ast.MatchExpr:
			for _, arm := range x.Arms {
				found = found || slices.Contains(arm.Binders(), name)
			}
		}
		return !found
	})
	return found
}

// loopFrameLevel is the level of the body frame of the loop a jump names:
// the innermost loop for "", else the loop carrying the label. -1 when none.
func loopFrameLevel(frames []liveFrame, level int, label string) int {
	for l := level; l >= 0; l-- {
		if frames[l].loop == nil {
			continue
		}
		if label == "" || frames[l].label == label {
			return l
		}
	}
	return -1
}

// loopHeadLive reports whether the loop whose body is frames[l] reads name
// on its next iteration: name is mentioned in the loop and is not a binding
// the body declares afresh each time round.
func loopHeadLive(frames []liveFrame, l int, name string) bool {
	loop := frames[l].loop
	if declaresName(loop, name) {
		return false
	}
	return StmtReferencesName(loop, name)
}

// liveAfter reports whether name can be read again on some path after the
// statement that holds c, the call, match or let that consumes it. A path
// that reaches the end of a loop body, or continues, comes back to every
// use in that loop.
func liveAfter(info *Info, bf *bodyFrames, c ast.Node, aid *ast.Ident, order IdentOrder) bool {
	return liveAfterBinding(info, bf, c, aid, order, nil)
}

func liveAfterBinding(info *Info, bf *bodyFrames, c ast.Node, aid *ast.Ident, order IdentOrder, decl ast.Node) bool {
	frames := bf.of(c)
	if len(frames) == 0 {
		return true
	}
	inner := frames[len(frames)-1]
	st := inner.stmts[inner.idx]
	if neverReturns(info, st) {
		return false
	}
	// A loop inside the statement runs the call again each round: the call
	// sits in a loop's head, or in a loop inside an expression-level block.
	if loopRepeats(st, c, decl) {
		return true
	}
	reach := settleReach{frames: frames, level: len(frames) - 1, scope: bindingLevel(frames, aid.Name)}
	if m, ok := st.(*ast.Match); ok && st != c && nodeHolds(m.Tag, c) {
		if live, settled := callTagSettles(info, m, aid, order, reach); live || settled {
			return live
		}
		return liveFrom(info, frames, len(frames)-1, aid.Name, bindingLevel(frames, aid.Name))
	}
	if m, ok := c.(*ast.Match); ok && st == c {
		live, settled := matchTagLive(info, m, aid.Name, reach)
		if live {
			return true
		}
		if settled {
			return false
		}
		return liveFrom(info, frames, len(frames)-1, aid.Name, bindingLevel(frames, aid.Name))
	}
	later := false
	var visit func(ast.Node) bool
	visit = func(m ast.Node) bool {
		if branch, ok := m.(*ast.MatchExpr); ok {
			for _, arm := range branch.Arms {
				if nodeHolds(arm.Body, aid) {
					ast.Walk(arm.Body, visit)
					return false
				}
			}
		}
		if branch, ok := m.(*ast.IfExpr); ok {
			if nodeHolds(branch.Then, aid) {
				ast.Walk(branch.Then, visit)
				return false
			}
			if nodeHolds(branch.Else, aid) {
				ast.Walk(branch.Else, visit)
				return false
			}
		}
		if lm, ok := m.(*ast.Lambda); ok && !lambdaFreeNames(lm)[aid.Name] {
			return false
		}
		if id, ok := m.(*ast.Ident); ok && id.Name == aid.Name && order.Idx[id] > order.Idx[aid] {
			later = true
		}
		return !later
	}
	ast.Walk(st, visit)
	if later {
		return true
	}
	if _, returns := st.(*ast.Return); returns {
		return false
	}
	if target, assignment := storeTarget(st); target == aid.Name && assignment != nil && nodeHolds(assignment.Value, c) {
		return false
	}
	return liveFrom(info, frames, len(frames)-1, aid.Name, bindingLevel(frames, aid.Name))
}

// matchTagLive reports whether the value a match takes from the local name is
// read inside the match. An arm that binds nothing leaves the local whole, so
// its reads are not reads of the taken value. settled reports that every arm
// that binds a payload gives the local a new value or leaves the statement
// before its body ends, so the statements after the match do not read the
// taken value.
func matchTagLive(info *Info, m *ast.Match, name string, reach settleReach) (live, settled bool) {
	settled = true
	for _, arm := range m.Arms {
		if len(arm.Binders()) == 0 {
			continue
		}
		if arm.Guard != nil && StmtReferencesName(arm.Guard, name) {
			return true, false
		}
		if arm.Body == nil {
			settled = false
			continue
		}
		armLive, armSettled := stmtsSettle(info, arm.Body.Stmts, name, reach)
		if armLive {
			return true, false
		}
		if !armSettled {
			settled = false
		}
	}
	return false, settled
}

// callTagSettles decides a local that a call in a match tag takes. live
// reports a later read in the tag, or a read in a guard or an arm before a
// whole assignment. settled reports that every arm assigns the local whole
// or leaves the statement, so no later statement reads the taken value.
func callTagSettles(info *Info, m *ast.Match, aid *ast.Ident, order IdentOrder, reach settleReach) (live, settled bool) {
	later := false
	ast.Walk(m.Tag, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == aid.Name && order.Idx[id] > order.Idx[aid] {
			later = true
		}
		return !later
	})
	if later {
		return true, false
	}
	settled = len(m.Arms) > 0
	for _, arm := range m.Arms {
		if arm.Guard != nil && StmtReferencesName(arm.Guard, aid.Name) {
			return true, false
		}
		if arm.Body == nil {
			settled = false
			continue
		}
		armLive, armSettled := stmtsSettle(info, arm.Body.Stmts, aid.Name, reach)
		if armLive {
			return true, false
		}
		if !armSettled {
			settled = false
		}
	}
	return false, settled
}

// settleReach is where a statement list sits: a break or a continue in it
// leaves to a loop of frames, seen from level, for a binding at scope.
type settleReach struct {
	frames []liveFrame
	level  int
	scope  int
	// reads, when set, reports a node that can read the taken value. A nil
	// reads counts each mention of the name.
	reads func(ast.Node) bool
}

func (r settleReach) refs(n ast.Node, name string) bool {
	if r.reads != nil {
		return r.reads(n)
	}
	return StmtReferencesName(n, name)
}

// stmtsSettle walks a statement list that follows a take of name. live
// reports a read of name before a whole assignment to it, also after a break
// or a continue. settled reports that every path through the list assigns
// name whole or leaves the list early, so the statements after it do not
// read the taken value.
func stmtsSettle(info *Info, stmts []ast.Stmt, name string, reach settleReach) (live, settled bool) {
	for _, st := range stmts {
		// The value is read before the store, so only its reads can see
		// the taken value.
		if asn := wholeAssignment(st, name); asn != nil {
			return reach.refs(asn.Value, name), !reach.refs(asn.Value, name)
		}
		switch x := st.(type) {
		case *ast.Match:
			if reach.refs(x.Tag, name) {
				return true, false
			}
			allSettled := true
			for _, arm := range x.Arms {
				if arm.Guard != nil && reach.refs(arm.Guard, name) {
					return true, false
				}
				if arm.Body == nil {
					allSettled = false
					continue
				}
				armLive, armSettled := stmtsSettle(info, arm.Body.Stmts, name, reach)
				if armLive {
					return true, false
				}
				if !armSettled {
					allSettled = false
				}
			}
			if allSettled && len(x.Arms) > 0 {
				return false, true
			}
			continue
		case *ast.Block:
			bLive, bSettled := stmtsSettle(info, x.Stmts, name, reach)
			if bLive || bSettled {
				return bLive, bSettled
			}
			continue
		case *ast.If:
			if reach.refs(x.Cond, name) {
				return true, false
			}
			branches := [][]ast.Stmt{blockStmts(x.Then)}
			if x.Else != nil {
				branches = append(branches, blockStmts(x.Else))
			}
			allSettled := x.Else != nil
			for _, b := range branches {
				bLive, bSettled := stmtsSettle(info, b, name, reach)
				if bLive {
					return true, false
				}
				if !bSettled {
					allSettled = false
				}
			}
			if allSettled {
				return false, true
			}
			continue
		}
		if reach.refs(st, name) {
			return true, false
		}
		if jumpsLive(info, reach.frames, reach.level, jumpsOf(info, st), name, reach.scope, reach.reads) {
			return true, false
		}
		if divergesStmt(info, st) || neverReturns(info, st) {
			return false, true
		}
	}
	return false, false
}

func blockStmts(st ast.Stmt) []ast.Stmt {
	if b, ok := st.(*ast.Block); ok {
		return b.Stmts
	}
	return []ast.Stmt{st}
}

func liveFrom(info *Info, frames []liveFrame, level int, name string, scope int) bool {
	return liveFromReads(info, frames, level, name, scope, nil)
}

// liveFromReads is liveFrom with a read test, as settleReach.reads.
func liveFromReads(info *Info, frames []liveFrame, level int, name string, scope int, reads func(ast.Node) bool) bool {
	f := frames[level]
	reach := settleReach{frames: frames, level: level, scope: scope, reads: reads}
	// An initializer reads the outer binding before its declaration hides it.
	// Later reads in this block name the new binding. Jumps and scope exits
	// can still reach the outer value, so keep those paths.
	shadowed := bindsName(f.stmts[f.idx], name)
	for j := f.idx + 1; j < len(f.stmts); j++ {
		if shadowed {
			st := f.stmts[j]
			if jumpsLive(info, frames, level, jumpsOf(info, st), name, scope, reads) {
				return true
			}
			if divergesStmt(info, st) || neverReturns(info, st) {
				return false
			}
			continue
		}
		// A new value on every path replaces the local before any read of
		// the old one.
		live, settled := stmtsSettle(info, f.stmts[j:j+1], name, reach)
		if live {
			return true
		}
		if settled {
			return false
		}
		shadowed = bindsName(f.stmts[j], name)
	}
	if level <= scope {
		// The binding ends with the block that declares it.
		return false
	}
	if f.loop != nil && (scope >= 0 || loopHeadLive(frames, level, name)) {
		return true
	}
	if level == 0 {
		return false
	}
	return liveFromReads(info, frames, level-1, name, scope, reads)
}

// jumpsLive reports a continue or a break of a statement at level that
// reaches a read of name. A jump to a loop at or inside the block of the
// binding leaves the binding.
func jumpsLive(info *Info, frames []liveFrame, level int, jumps stmtJumps, name string, scope int, reads func(ast.Node) bool) bool {
	for _, label := range jumps.continues {
		l := loopFrameLevel(frames, level, label)
		if l >= 0 && l <= scope {
			continue
		}
		if l < 0 || scope >= 0 || loopHeadLive(frames, l, name) || liveFromReads(info, frames, l-1, name, scope, reads) {
			return true
		}
	}
	for _, label := range jumps.breaks {
		l := loopFrameLevel(frames, level, label)
		if l >= 0 && l <= scope {
			continue
		}
		if l < 0 || liveFromReads(info, frames, l-1, name, scope, reads) {
			return true
		}
	}
	return false
}

// bindingLevel is the level of the innermost frame that binds name before the
// statement that the frame holds: the block that the read binding belongs to.
// A match arm binds its names for its body. It is -1 when no frame binds
// name, as for a parameter.
func bindingLevel(frames []liveFrame, name string) int {
	for l := len(frames) - 1; l >= 0; l-- {
		f := frames[l]
		switch loop := f.loop.(type) {
		case *ast.ForEach:
			if loop.Var == name {
				return l
			}
		case *ast.For:
			if loop.Init != nil && bindsName(loop.Init, name) {
				return l
			}
		}
		for j := 0; j < f.idx; j++ {
			if bindsName(f.stmts[j], name) {
				return l
			}
		}
		if l > 0 && armBinds(frames[l-1], f, name) {
			return l
		}
	}
	return -1
}

// armBinds reports whether the arm whose body is f binds name, when f is the
// body of an arm of the match that parent holds.
func armBinds(parent, f liveFrame, name string) bool {
	m, ok := parent.stmts[parent.idx].(*ast.Match)
	if !ok {
		return false
	}
	for _, arm := range m.Arms {
		if arm.Body != nil && nodeHolds(arm.Body, f.stmts[f.idx]) {
			return slices.Contains(arm.Binders(), name)
		}
	}
	return false
}

// lastInBinding reports whether no later read of name belongs to the binding
// that aid reads: no read after aid inside the block that declares it.
func lastInBinding(bf *bodyFrames, c ast.Node, aid *ast.Ident, order IdentOrder) bool {
	frames := bf.of(c)
	scope := bindingLevel(frames, aid.Name)
	if scope < 0 {
		return order.IsLast(aid)
	}
	f := frames[scope]
	at := order.Idx[aid]
	later := false
	for _, st := range f.stmts[f.idx:] {
		ast.Walk(st, func(m ast.Node) bool {
			if lm, ok := m.(*ast.Lambda); ok && !lambdaFreeNames(lm)[aid.Name] {
				return false
			}
			if id, ok := m.(*ast.Ident); ok && id.Name == aid.Name && order.Idx[id] > at {
				later = true
			}
			return !later
		})
	}
	return !later
}

// bindingVar is the let that a read of name at c belongs to, or nil when
// another kind of declaration or a parameter binds it.
func bindingVar(bf *bodyFrames, c ast.Node, name string) *ast.Var {
	frames := bf.of(c)
	l := bindingLevel(frames, name)
	if l < 0 {
		return nil
	}
	f := frames[l]
	for j := f.idx - 1; j >= 0; j-- {
		if bindsName(f.stmts[j], name) {
			v, _ := f.stmts[j].(*ast.Var)
			return v
		}
	}
	return nil
}

// bindsName reports whether the statement itself declares name, not a
// statement nested inside it.
func bindsName(st ast.Stmt, name string) bool {
	switch x := st.(type) {
	case *ast.Var:
		return x.Name == name
	case *ast.Destructure:
		return slices.Contains(x.Names, name)
	}
	return false
}
