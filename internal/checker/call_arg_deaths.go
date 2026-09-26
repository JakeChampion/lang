package checker

import "github.com/jakechampion/lang/internal/ast"

// StmtReferencesName reports whether any *ast.Ident named `name` appears
// anywhere in the subtree `st` — the shared occurrence predicate behind the
// last-use / deadness scans (#4480). Shared by computeReuseSources,
// computePreciseDrops, and flowsIntoUncountedAlias.
func StmtReferencesName(st ast.Node, name string) bool {
	found := false
	ast.Walk(st, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}

// IdentOrder is the shared ident-occurrence-order fact (#4480): every
// *ast.Ident in the function body numbered in pre-order (ast.Walk visit
// order), plus each name's highest occurrence number. `IsLast` is the
// last-use test the move analyses hang off — an occurrence is the local's
// LAST when no later occurrence of the same name exists anywhere in the
// body. Previously built verbatim by both computeMovedLocals and
// computeArraySetIncs and threaded pairwise into markConstructionMoves.
// The statement-INDEX deadness scans (computePreciseDrops /
// computeReuseSources) are a different fact by design — top-level /
// per-block statement position, not ident occurrence — and stay separate.
type IdentOrder struct {
	Idx  map[*ast.Ident]int
	last map[string]int
}

func IdentOrderOf(body ast.Node) IdentOrder {
	o := IdentOrder{Idx: map[*ast.Ident]int{}, last: map[string]int{}}
	n := 0
	ast.Walk(body, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok {
			n++
			o.Idx[id] = n
			if n > o.last[id.Name] {
				o.last[id.Name] = n
			}
		}
		return true
	})
	return o
}

// IsLast reports whether this occurrence is the highest-numbered occurrence
// of its name — the "last use anywhere in the body" test.
func (o IdentOrder) IsLast(id *ast.Ident) bool {
	return o.Idx[id] == o.last[id.Name]
}

// DeferOrLambdaNames returns the names still readable once the statement that
// Mentions them completes: anything referenced under a defer action or inside a
// lambda body, since a closure can run later and read a captured binding.
// Conservative — any occurrence of the name is enough. Both return-position
// exemptions below rest on it.
func DeferOrLambdaNames(body ast.Node) map[string]bool {
	esc := map[string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		var sub ast.Node
		switch d := n.(type) {
		case *ast.Defer:
			sub = d.Expr
		case *ast.Lambda:
			sub = n
		default:
			return true
		}
		ast.Walk(sub, func(m ast.Node) bool {
			if id, isIdent := m.(*ast.Ident); isIdent {
				esc[id.Name] = true
			}
			return true
		})
		// Descend anyway: nested statements hold names of their own.
		return true
	})
	return esc
}

// RenameRoots maps a local that RENAMES another binding — `var c: C = c0;`,
// where c0 is named nowhere else in the body — to the name it renames, chased
// through chained renames to the root.
//
// A rename is one binding spelled twice: nothing after it can read the source,
// so the two names share every buffer with no second live reader. Every
// analysis that asks "which binding is this name?" has to agree on that, or
// the answer splits — callArgDeaths admitting the rename on its source's
// footing while computeGrowParams stopped propagating at it withdrew the
// caller-side bracket from a buffer the caller still read (#8498).
//
// A name declared twice is excluded: the occurrence census cannot tell its two
// bindings apart.
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

// ArgDeaths is CallArgDeaths' verdict, with the three facts it was computed
// from that a caller widening it needs again.
type ArgDeaths struct {
	// Dies names, per call, the argument idents (and "x.f" field keys) that
	// are dead once the call has them.
	Dies map[*ast.Call]map[string]bool
	// Repeating is every call a loop or lambda body reaches.
	Repeating map[*ast.Call]bool
	// Escaping is every name read under a defer or inside a lambda.
	Escaping map[string]bool
	// Occurrences counts each name's idents across the body.
	Occurrences map[string]int
}

// CallArgDeaths marks, per call node, the ident arguments whose value can
// no longer be observed through that binding in this function after the
// call, so the #4873 bracket may skip them. Four shapes qualify:
//
//   - the strict self-reassign `x = f(.., x, ..)`: the RHS is exactly the
//     call and x occurs in it exactly once, directly as an argument — the
//     old binding is overwritten by the result (the #5056 move-and-rebind
//     shape, sans the `own` requirement);
//   - the return-position `return f(.., x, ..)` under the same
//     exactly-once rule: a return exits the function (loop or not), so no
//     later read exists. This is what keeps recursive accumulator tails
//     (`return walk(acc, …)`) on the in-place fast path — bracketing them
//     would force one copy per recursion level, the #4838 O(n²) class;
//   - the SOLE-OCCURRENCE shape (#6036): a PARAMETER read exactly once in
//     the whole body, at a straight-line position — no later read of the
//     binding exists at all, whatever the syntax around the call. This is
//     what covers `var t = f(b, v); return t;` and the inner call of
//     `return f(f(b, v), v + 1)`, neither of which is a reassign or a
//     direct return argument, yet both of which were paying one
//     full-buffer copy per call;
//   - the LAST-OCCURRENCE shape: the read at this call is the binding's
//     textually last (IdentOrder.IsLast) and the call is enclosed by no
//     loop or lambda body, so control passes it once and nothing reads the
//     binding again. Admitted for a param and for a `var` local whose
//     initialiser is a direct call to a named function — the state-
//     threading chain `var a = s.emit(o); var b = a.emit(o); return b;`,
//     where every receiver is at its last use. Each of those was paying a
//     full-buffer copy per link, which is O(n²) bytes over a chain: the
//     self-host lowering threads its LowerState this way and one 400-arm
//     `else if` chain bumped 40 MB in `emit` alone. A local that RENAMES
//     an admitted name at that name's only occurrence — `var c: C = c0;`
//     on a parameter — is the same binding spelled twice, so it is
//     admitted on the source's footing.
//
// The last-occurrence test needs the no-loop gate to be sound at all:
// inside a loop the "last" occurrence re-executes, and an unbracketed
// in-place growth would be observed by the next iteration (interp
// copies). A name read inside a defer or a lambda is excluded outright —
// those run after the syntactic position that looks final.
//
// For a LOCAL the death verdict also needs the binding not to be an alias
// of something else still live: `var t = holder; f(t)` makes `t`'s last
// use unbracketed while `holder` still reads the same field buffers, and
// binding a struct incs the BOX, not the buffers inside it. A direct-call
// initialiser cannot be that: its result is either freshly allocated or
// shares a buffer with an argument, and an argument shares only when the
// callee grew it in place — which required that argument to have died at
// ITS call, so nothing observes the sharing. That is the same induction
// #4873's caller-side containment already rests on, and the transitive
// closure in computeGrowParams consults this map, so a buffer passed on
// unbracketed propagates as a growable position of the enclosing
// function's own parameter.
//
// A rename is not that alias either, for the plainer reason that its source is
// never named again — and computeGrowParams resolves through it (RenameRoots)
// so the closure does not stop at the new name.
//
// It lives here rather than in internal/ir so the checker can ask it too: E051
// has to admit exactly the arguments it marks dead at an `own` position
// (#9541). The IR widens it with the param-field deaths only it can see, and
// only ever adds.
func CallArgDeaths(fn *ast.FuncDecl, info *Info) ArgDeaths {
	body := fn.Body
	out := map[*ast.Call]map[string]bool{}
	// Occurrence census over the whole body, for the sole-occurrence shape.
	// A shadowing inner declaration of the same name inflates the count,
	// which only ever withholds the death verdict — the safe direction.
	occurrences := map[string]int{}
	ast.Walk(body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			occurrences[id.Name]++
		}
		return true
	})
	isParam := map[string]bool{}
	// frameOwns is the set of names whose value this frame reclaims: an
	// `own` parameter and every local. A borrowed parameter is excluded —
	// its box belongs to the caller.
	frameOwns := map[string]bool{}
	for _, p := range fn.Params {
		isParam[p.Name] = true
		if p.Own {
			frameOwns[p.Name] = true
		}
	}
	ast.Walk(body, func(n ast.Node) bool {
		if v, isVar := n.(*ast.Var); isVar && !isParam[v.Name] {
			frameOwns[v.Name] = true
		}
		return true
	})
	// Locals bound from a direct call to a named function — the only local
	// binding form the last-occurrence shape admits (see above). A name
	// declared more than once is dropped: the occurrence order cannot tell
	// the two bindings apart.
	callInitLocal := map[string]bool{}
	declCount := map[string]int{}
	ast.Walk(body, func(n ast.Node) bool {
		v, isVar := n.(*ast.Var)
		if !isVar {
			return true
		}
		declCount[v.Name]++
		if c, isCall := v.Init.(*ast.Call); isCall {
			if _, named := c.Callee.(*ast.Ident); named {
				callInitLocal[v.Name] = true
			}
		}
		return true
	})
	for name, n := range declCount {
		if n > 1 {
			delete(callInitLocal, name)
		}
	}
	// A local UNPACKED from a call-init local's field — `var eqL = park(…);
	// var sl = eqL.state;` — is admitted on the same footing. The alias
	// exclusion asks whether another live name in this frame reads the same
	// buffers, and the unpack is the only reader of that field: `h.f` occurs
	// once in the body and every other mention of `h` selects a different
	// field, so nothing survives the last use of `q` that names `h.f`. The
	// self-host's `lower_expr_binary` threads its state through exactly this
	// shape twice per string comparison, and each link was paying one copy of
	// the whole op list.
	unpackInitLocal := map[string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		v, isVar := n.(*ast.Var)
		if !isVar || declCount[v.Name] != 1 {
			return true
		}
		fa, isField := v.Init.(*ast.FieldAccess)
		if !isField {
			return true
		}
		hid, isID := fa.Target.(*ast.Ident)
		if !isID || !callInitLocal[hid.Name] {
			return true
		}
		reads, selections, mentions := 0, 0, 0
		ast.Walk(body, func(m ast.Node) bool {
			switch x := m.(type) {
			case *ast.FieldAccess:
				if id, ok := x.Target.(*ast.Ident); ok && id.Name == hid.Name {
					selections++
					if x.Field == fa.Field {
						reads++
					}
				}
			case *ast.Ident:
				if x.Name == hid.Name {
					mentions++
				}
			}
			return true
		})
		// Every field selection also walks its target Ident, so h is named
		// only by selections exactly when the two counts agree.
		if reads == 1 && selections == mentions {
			unpackInitLocal[v.Name] = true
		}
		return true
	})
	// A local RENAMED from an already-admitted name — `var c: C = c0;` on a
	// parameter, the line every state-threading function in the self-host
	// lowering opens with — is admitted on that name's footing. The alias
	// exclusion asks whether another live name in this frame reads the same
	// buffers, and a rename taking the source's ONLY occurrence leaves none.
	// Without this the rename withheld the death from every call in the chain
	// below it, so the container reaching each field append was at rc 2 and
	// copied the whole buffer (#8498).
	aliasInitLocal := map[string]bool{}
	for name, root := range RenameRoots(body) {
		if isParam[root] || callInitLocal[root] || unpackInitLocal[root] {
			aliasInitLocal[name] = true
		}
	}
	admitted := func(name string) bool {
		return isParam[name] || callInitLocal[name] || unpackInitLocal[name] || aliasInitLocal[name]
	}
	// markOnce marks `name` dead at the call inside `scope` that takes it,
	// when scope names it exactly once and that occurrence is a direct
	// argument. Every shape below hands it the expression whose evaluation is
	// the name's last chance to be read — an assignment's value, a returned
	// expression, or the call itself.
	//
	// The call taking the name need not be scope's OUTERMOST:
	// `c = emit(emit(c, v), v)` hands c to the inner one, and the store
	// supersedes c either way, so the death belongs where the argument is.
	// Stopping at the top level left that spelling — and the method chain
	// `c = c.emit(v).emit(v)` that desugars to it — paying a full-buffer copy
	// per link inside a loop, where the last-occurrence shapes cannot help
	// (#8696).
	//
	// Naming it exactly once is the whole guard, and it is also what makes the
	// site unambiguous: a second read anywhere in scope would see the buffer
	// the callee grew, so `c = emit(emit(c, v), c.insts.len())` declines.
	markOnce := func(scope ast.Expr, name string) {
		total := 0
		ast.Walk(scope, func(m ast.Node) bool {
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
			for _, a := range c.Args {
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
			// `return S { ...x, f: g(.., x.f, ..) }` is the same superseded
			// field as the assignment form: nothing runs after the return,
			// so the old field value cannot be read back through x. It needs
			// one condition the assignment does not, because there is no
			// store to x here — the FRAME must own x. A borrowed parameter's
			// box outlives the call, and its caller can still read the field
			// the callee grew in place.
			if sl, isLit := st.Value.(*ast.StructLit); isLit {
				if sl.Base != nil {
					if bid, ok := sl.Base.(*ast.Ident); ok && frameOwns[bid.Name] {
						markSupersededFields(out, sl, bid.Name)
					}
				}
				return true
			}
			c, ok := st.Value.(*ast.Call)
			if !ok {
				return true
			}
			for _, a := range c.Args {
				if aid, ok := a.(*ast.Ident); ok {
					markOnce(c, aid.Name)
				}
			}
		}
		return true
	})
	// The TWO-STATEMENT spelling of the self-reassign shape. `x = f(…, x, …)`
	// is one statement and matched above; a step that hands something back
	// beside the cursor — a label id, an offset, a slot number — or that just
	// names the result before storing it, spells the same thing as two:
	//
	//	let (c2, p) = emit(c, op);   var c2 = emit(c, op);
	//	c = c2;                      c = c2;
	//
	// The store still supersedes x before any other statement runs, so no
	// later read can reach the old buffer through it, exactly as in the
	// one-statement form. Neither half matched on its own: the binding
	// statement stores to a new name, and `c = c2` names no call. Inside a
	// loop the last-occurrence shapes are out too (`repeating`), so nothing
	// marked the argument dead and every call paid a full-buffer copy — 920 ms
	// against 0 ms for the same emit written as one statement, over 20000
	// appends (#8633).
	//
	// The store's value must not READ x: `var y = f(x); x = g(x);` would hand
	// g the buffer the callee just grew.
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
			es, isExpr := blk.Stmts[i+1].(*ast.ExprStmt)
			if !isExpr {
				continue
			}
			asn, isAsn := es.Expr.(*ast.Assign)
			if !isAsn || asn.Value == nil {
				continue
			}
			t, isID := asn.Target.(*ast.Ident)
			if !isID || StmtReferencesName(asn.Value, t.Name) {
				continue
			}
			markOnce(c, t.Name)
		}
		return true
	})
	// Sole-occurrence shape. `repeating` is every call reachable from a
	// loop or lambda body — a single textual read there is still many
	// dynamic reads, so those calls are excluded.
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
		for _, a := range c.Args {
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
	// Last-occurrence shape. Same no-loop / no-lambda gate as above, plus the
	// defer-and-lambda exclusion (a capture is read when the closure runs, not
	// where it is written) and markOnce's exactly-once-in-this-call rule, so a
	// second read inside the same call cannot observe the first's growth.
	escaping := DeferOrLambdaNames(body)
	order := IdentOrderOf(body)
	ast.Walk(body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok || repeating[c] {
			return true
		}
		for _, a := range c.Args {
			aid, ok := a.(*ast.Ident)
			if !ok || escaping[aid.Name] || !order.IsLast(aid) {
				continue
			}
			if !admitted(aid.Name) {
				continue
			}
			// An enclosing call may already hold the value (#9879).
			if heldByEnclosingCall(body, c, aid.Name) {
				continue
			}
			markOnce(c, aid.Name)
		}
		return true
	})
	// Path-last-occurrence shape. `order.IsLast` is a TEXTUAL test, and the
	// self-host lowering is written as a chain of `if (…) { … return …; }`
	// branches that each thread the state once: every one of them has a
	// textually later read, on a path that cannot also have run. So a read is
	// equally final when the statement list it sits in RETURNS before
	// mentioning the name again — control leaves the function from inside this
	// block, so no later statement of the body is reachable. The same no-loop
	// / no-lambda / exactly-once gates as the textual shape apply; a `break` or
	// `continue` that could leave the block before the return withdraws it,
	// since control would then reach the code after it.
	stmtIdx := callBlockPositions(body)
	ast.Walk(body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok || repeating[c] {
			return true
		}
		for _, a := range c.Args {
			aid, ok := a.(*ast.Ident)
			if !ok || escaping[aid.Name] || order.IsLast(aid) {
				continue
			}
			if !admitted(aid.Name) {
				continue
			}
			// An ARRAY position is excluded. The death withdraws the bracket
			// around the argument's OWN buffer there, so the callee grows the
			// caller's buffer in place and the superseded generation is left to
			// a bare __fern_rc_dec — which decrements to zero without freeing
			// (the typed drop half of reclaim is not built), so that buffer and
			// every element it holds stay live. The conformance leak census
			// reads it as 115 extra unpaired allocations over five regex
			// fixtures. The textual shape reaches the same gap where it already
			// applies; this one is new, and the cliff gate puts the array half
			// of it at 0.02% of the bytes, so it is not taken.
			if arrayArgPosition(info, c, aid) {
				continue
			}
			// No heldByEnclosingCall gate here, unlike the textual shape
			// above: returnsBeforeReading already requires the whole
			// STATEMENT to name it once, and an enclosing call is in that
			// statement, so #9879's shape cannot reach this line. Relaxing
			// that count reopens it.
			if !returnsBeforeReading(stmtIdx, c, aid.Name) {
				continue
			}
			markOnce(c, aid.Name)
		}
		return true
	})
	return ArgDeaths{Dies: out, Repeating: repeating, Escaping: escaping, Occurrences: occurrences}
}

// heldByEnclosingCall reports whether a call that strictly contains `c` also
// names `name` outside `c`. The last-occurrence shape reads the text:
// `IsLast` asks whether anything reads the name LATER, which is the wrong
// question when the other read is EARLIER and its value is still in flight.
//
// `f(x, g(x))` evaluates `x` for f, then calls g. The operand is on the stack
// with nothing holding a count for it, so calling g's occurrence the last use
// hands g a value f is about to read — and a callee that may steal from a
// consumed argument then blanks a field under f's feet. That is #9879, where
// `a.zip(a.flip(1))` on a struct with two array fields segfaulted on every
// compiled backend while the interpreter was correct.
//
// Cheapest sound test: any earlier mention inside an enclosing call withdraws
// the death. It declines some occurrences that are harmless — `f(x.len(), g(x))`
// materialises an i32, not a reference — which costs an optimisation, never
// correctness.
func heldByEnclosingCall(body ast.Node, c *ast.Call, name string) bool {
	found := false
	ast.Walk(body, func(n ast.Node) bool {
		if found {
			return false
		}
		p, isCall := n.(*ast.Call)
		if !isCall || p == c || !callContains(p, c) {
			return true
		}
		ast.Walk(p, func(m ast.Node) bool {
			if found {
				return false
			}
			if mc, isC := m.(*ast.Call); isC && mc == c {
				return false
			}
			if id, isID := m.(*ast.Ident); isID && id.Name == name {
				found = true
			}
			return true
		})
		return !found
	})
	return found
}

// callContains reports whether target is somewhere inside p, p itself aside.
func callContains(p *ast.Call, target *ast.Call) bool {
	if p == target {
		return false
	}
	found := false
	ast.Walk(p, func(n ast.Node) bool {
		if found {
			return false
		}
		if c, ok := n.(*ast.Call); ok && c == target {
			found = true
			return false
		}
		return true
	})
	return found
}

// arrayArgPosition reports whether `aid` is an argument of `c` at a parameter
// position of ARRAY type. A call whose callee has no known signature answers
// true: an unresolvable position is treated as the array case.
func arrayArgPosition(info *Info, c *ast.Call, aid *ast.Ident) bool {
	if info == nil {
		return true
	}
	callee, isID := c.Callee.(*ast.Ident)
	if !isID {
		return true
	}
	sig := info.FuncSigs[callee.Name]
	if sig == nil {
		return true
	}
	for i, a := range c.Args {
		if id, ok := a.(*ast.Ident); !ok || id != aid {
			continue
		}
		if i >= len(sig.Params) {
			return true
		}
		_, isArr := sig.Params[i].(ast.ArrayType)
		return isArr
	}
	return true
}

// blockPos locates a call in the innermost statement list holding it.
type blockPos struct {
	blk *ast.Block
	idx int
}

// callBlockPositions maps every call in `body` to its innermost enclosing
// statement list and the index of the statement it appears in.
func callBlockPositions(body ast.Node) map[*ast.Call]blockPos {
	var blocks []*ast.Block
	ast.Walk(body, func(n ast.Node) bool {
		if b, ok := n.(*ast.Block); ok {
			blocks = append(blocks, b)
		}
		return true
	})
	out := map[*ast.Call]blockPos{}
	// Pre-order, so an inner block overwrites the outer one's verdict.
	for _, b := range blocks {
		for i, st := range b.Stmts {
			ast.Walk(st, func(n ast.Node) bool {
				if c, ok := n.(*ast.Call); ok {
					out[c] = blockPos{blk: b, idx: i}
				}
				return true
			})
		}
	}
	return out
}

// returnsBeforeReading reports whether the statement list holding `c` returns
// out of the function before mentioning `name` again — so this read is the
// last one on every path that reaches it, whatever comes later in the body.
func returnsBeforeReading(pos map[*ast.Call]blockPos, c *ast.Call, name string) bool {
	bp, ok := pos[c]
	if !ok {
		return false
	}
	// Once in the whole statement, so nothing else in it reads the name after
	// the call — the statement-level twin of markOnce's rule.
	if Mentions(bp.blk.Stmts[bp.idx], name) != 1 {
		return false
	}
	for i := bp.idx + 1; i < len(bp.blk.Stmts); i++ {
		if Mentions(bp.blk.Stmts[i], name) != 0 || JumpEscapes(bp.blk.Stmts[i]) {
			return false
		}
		if _, isRet := bp.blk.Stmts[i].(*ast.Return); isRet {
			return true
		}
	}
	return false
}

// Mentions counts the occurrences of `name` in a statement subtree.
func Mentions(n ast.Node, name string) int {
	k := 0
	ast.Walk(n, func(m ast.Node) bool {
		if id, ok := m.(*ast.Ident); ok && id.Name == name {
			k++
		}
		return true
	})
	return k
}

// JumpEscapes reports whether a break / continue inside this statement can
// transfer control out of the statement list holding it — an unlabelled jump
// outside any loop the statement itself contains, or any labelled one.
func JumpEscapes(st ast.Stmt) bool {
	inLoop := map[ast.Node]bool{}
	ast.Walk(st, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.While, *ast.Loop, *ast.For, *ast.ForEach:
			ast.Walk(n, func(m ast.Node) bool { inLoop[m] = true; return true })
		}
		return true
	})
	out := false
	ast.Walk(st, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Break:
			if !inLoop[n] || x.Label != "" {
				out = true
			}
		case *ast.Continue:
			if !inLoop[n] || x.Label != "" {
				out = true
			}
		}
		return true
	})
	return out
}

// markSupersededFields handles the struct self-update `x = S { ...x, f:
// g(.., x.f, ..) }`: the field value's call receives `x.f` exactly once in
// the whole statement, and the statement's own store overwrites that field
// of x, so the old buffer cannot be observed through x afterwards — the
// field-level twin of the `x = f(x)` shape. The key is "x.f"; the bracket
// looks a single-hop field argument up under it. The callee's own in-place
// push retains what it grew, so the update's release of the old field value
// only decs — which is what kept this shape correct before field chains
// were bracketed at all, and what made every byte the x86 assembler emits a
// copy of the whole code buffer once they were.
func markSupersededFields(out map[*ast.Call]map[string]bool, sl *ast.StructLit, target string) {
	if sl.Base == nil {
		return
	}
	if bid, ok := sl.Base.(*ast.Ident); !ok || bid.Name != target {
		return
	}
	for _, f := range sl.Fields {
		c, ok := f.Value.(*ast.Call)
		if !ok {
			continue
		}
		if _, named := c.Callee.(*ast.Ident); !named {
			continue
		}
		direct := 0
		for _, a := range c.Args {
			if fa, ok := a.(*ast.FieldAccess); ok && fa.Field == f.Name {
				if id, ok := fa.Target.(*ast.Ident); ok && id.Name == target {
					direct++
				}
			}
		}
		if direct != 1 {
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

// lastUseArgs is the E051 admission for a local handed over at its last use
// (#9541): each ident argument of a direct call that CallArgDeaths marks dead
// there, names a `var` local of fn, and is read by no defer or lambda. Those
// are the positions computeOwnedArgMoves moves into an owned parameter.
// Calls inside a nested function or lambda are left out: those bodies are
// lowered as functions of their own.
func lastUseArgs(fn *ast.FuncDecl, info *Info) map[ast.Expr]bool {
	out := map[ast.Expr]bool{}
	if fn.Body == nil {
		return out
	}
	d := CallArgDeaths(fn, info)
	isParam := map[string]bool{}
	for _, p := range fn.Params {
		isParam[p.Name] = true
	}
	local := map[string]bool{}
	nested := map[*ast.Call]bool{}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Var:
			if !isParam[x.Name] {
				local[x.Name] = true
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
		if _, direct := call.Callee.(*ast.Ident); !direct || nested[call] {
			continue
		}
		for _, a := range call.Args {
			if id, ok := a.(*ast.Ident); ok && dies[id.Name] && local[id.Name] && !d.Escaping[id.Name] {
				out[id] = true
			}
		}
	}
	return out
}
