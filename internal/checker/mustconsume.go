package checker

// E067 — `@must_consume` obligation checking (docs/MUST-CONSUME.md;
// plan item E1 of docs/NICHE-BORROWS-PLAN.md). A value whose type is
// marked `@must_consume` must be CONSUMED at least once on every
// control-flow path before its binding leaves scope. Consuming uses:
// passing it as a call argument, returning it, matching on it
// (destructure), binding it to another local (`var y = x` — the
// obligation transfers to y, which is itself tracked), or storing it
// into another @must_consume container. Field reads and method calls
// are neutral. The checker is an OBLIGATION checker, not a memory-
// safety checker — RC keeps every shape memory-safe regardless; E067
// exists to catch the forgotten-obligation bug class (unsent
// response, unclosed socket, uncommitted transaction) at compile
// time, Vale-Higher-RAII / Austral style, with zero runtime cost.
//
// Slice-1 scope (deliberately conservative — the design doc's rules,
// E063-shaped):
//   - intra-procedural; every call argument position transfers the
//     obligation to the callee (matching RC ownership transfer);
//   - at-least-once, not exactly-once (no move tracking);
//   - storing into an UNMARKED container (array/tuple/map literal, or
//     an unmarked struct/enum) is a violation at the store site — the
//     obligation would be laundered out of the checked world;
//   - lambda captures of marked values are violations (obligation
//     flow into closures needs design; forbid first);
//   - loop bodies are opaque to the path analysis: a consuming use
//     inside a `while`/`for` body does not discharge the obligation
//     (the zero-iteration path would leak it) — consume after the
//     loop as well, or bind inside the body. `break`/`continue`
//     seen at the binding's own scope depth (binding declared in
//     that loop body) end the scope and must be consumed-before;
//   - known accepted holes (documented in `fern explain E067`):
//     paths that diverge inside a nested loop, labeled break across
//     scopes, shadowing in nested blocks, and `defer`-based
//     discharge (a follow-up will model defer's exit edges).

import "github.com/jakechampion/lang/internal/ast"

// checkMustConsume is the per-function E067 entry point, invoked
// alongside checkSliceEscape/checkStrEscape after the body check.
func (c *checker) checkMustConsume(fn *ast.FuncDecl) {
	if fn.Body == nil {
		return
	}
	// Parameters of marked type carry the obligation in from the
	// caller: the whole body is their scope. EXCEPT `own` params —
	// an own-marked param declares this function the value's SINK
	// (the callee owns and may reclaim it; checkOwnedParams already
	// enforces affine use). E067's at-least-once on the caller side
	// plus own's at-most-once on the sink side compose to
	// exactly-once across the call boundary.
	for _, p := range fn.Params {
		if p.Own {
			continue
		}
		if tn, ok := c.mustConsumeTypeName(p.Type); ok {
			c.mcCheckBinding(fn, p.Name, tn, fn.P, fn.Body.Stmts)
		}
	}
	c.mcWalkBlock(fn, fn.Body)
}

// mcWalkBlock finds marked-type `var` bindings at every block depth
// and checks each against the remainder of ITS block (its scope).
func (c *checker) mcWalkBlock(fn *ast.FuncDecl, b *ast.Block) {
	if b == nil {
		return
	}
	for i, s := range b.Stmts {
		if v, ok := s.(*ast.Var); ok {
			t := v.Type
			if t == nil {
				t = c.info.VarTypes[v]
			}
			if tn, ok := c.mustConsumeTypeName(t); ok {
				c.mcCheckBinding(fn, v.Name, tn, v.P, b.Stmts[i+1:])
			}
		}
		// Recurse into nested scopes to find their bindings.
		switch x := s.(type) {
		case *ast.Block:
			c.mcWalkBlock(fn, x)
		case *ast.If:
			c.mcWalkStmtBlock(fn, x.Then)
			c.mcWalkStmtBlock(fn, x.Else)
		case *ast.While:
			c.mcWalkStmtBlock(fn, x.Body)
		case *ast.Loop:
			c.mcWalkStmtBlock(fn, x.Body)
		case *ast.For:
			c.mcWalkStmtBlock(fn, x.Body)
		case *ast.Match:
			for _, arm := range x.Arms {
				c.mcWalkBlock(fn, arm.Body)
			}
		}
	}
}

func (c *checker) mcWalkStmtBlock(fn *ast.FuncDecl, s ast.Stmt) {
	if s == nil {
		return
	}
	if b, ok := s.(*ast.Block); ok {
		c.mcWalkBlock(fn, b)
		return
	}
	// Single-statement arm (`if (c) return;`): no Var can bind here.
}

// mcCheckBinding runs the at-least-once analysis for one binding over
// the remainder of its scope, plus the intra-block overwrite scan.
func (c *checker) mcCheckBinding(fn *ast.FuncDecl, name, typeName string, bindPos ast.Position, rest []ast.Stmt) {
	consumed, violated := c.mcSeq(rest, name, typeName)
	if !consumed || violated {
		c.errfCode(bindPos, "E067",
			"value of @must_consume type %q bound to %q may go out of scope without being consumed — pass it, return it, match on it, or store it into another @must_consume type on every path",
			typeName, name)
		return
	}
	// Overwrite scan (same block, straight-line): re-assigning the
	// binding before any consuming use silently drops the old value.
	// The re-scan detects only (mcStmtConsumesQuiet) — the reporting
	// pass above already emitted any laundering/capture errors.
	for _, s := range rest {
		if c.mcStmtConsumesQuiet(s, name) {
			return
		}
		if es, ok := s.(*ast.ExprStmt); ok {
			if as, ok := es.Expr.(*ast.Assign); ok {
				if id, ok := as.Target.(*ast.Ident); ok && id.Name == name {
					c.errfCode(as.P, "E067",
						"overwriting %q while it still holds an unconsumed @must_consume %q value — consume the old value first",
						name, typeName)
					return
				}
			}
		}
	}
}

// mcSeq reports (consumedOnAllPaths, divergedUnconsumed) for the
// statement sequence. A path that returns / breaks / continues out of
// the scope without a consuming use sets the second flag; a sequence
// that falls off the end unconsumed simply reports false for the
// first (the caller decides that's the scope-exit violation).
func (c *checker) mcSeq(stmts []ast.Stmt, name, typeName string) (bool, bool) {
	for _, s := range stmts {
		if c.mcStmtConsumes(s, name, typeName) {
			return true, false
		}
		switch x := s.(type) {
		case *ast.Return:
			// Path ends here without having consumed.
			return false, true
		case *ast.Break, *ast.Continue:
			// At the binding's own scope depth this exits the
			// binding's enclosing loop-body scope. (Loop bodies
			// below are opaque, so we only ever see these when the
			// binding itself lives in the loop body.)
			return false, true
		case *ast.Block:
			con, div := c.mcSeq(x.Stmts, name, typeName)
			if div {
				return false, true
			}
			if con {
				return true, false
			}
		case *ast.If:
			tc, tv := c.mcArm(x.Then, name, typeName)
			ec, ev := false, false
			if x.Else != nil {
				ec, ev = c.mcArm(x.Else, name, typeName)
			}
			if tv || ev {
				return false, true
			}
			if tc && x.Else != nil && ec {
				return true, false
			}
			// Some path through the if did not consume — the rest
			// of the sequence must cover it. (Paths that already
			// consumed tolerate a second consuming use: this is an
			// at-least-once checker.)
		case *ast.Match:
			if c.mcIsBinding(x.Tag, name) {
				return true, false // destructuring consumes
			}
			// A parser-desugared pattern binding (`if let`, Origin != "")
			// contributes only its source destructure; its arms don't
			// participate in the all-arms path logic, so the obligation
			// still has to be discharged by the rest of the sequence.
			if x.Origin != "" {
				break
			}
			all := len(x.Arms) > 0
			for _, arm := range x.Arms {
				ac, av := c.mcSeq(arm.Body.Stmts, name, typeName)
				if av {
					return false, true
				}
				if !ac {
					all = false
				}
			}
			if all {
				return true, false
			}
		}
		// While / For / Loop bodies are opaque (see file header);
		// Defer is neutral in slice 1.
	}
	return false, false
}

// mcArm evaluates one if-arm (block or single statement).
func (c *checker) mcArm(s ast.Stmt, name, typeName string) (bool, bool) {
	if s == nil {
		return false, false
	}
	if b, ok := s.(*ast.Block); ok {
		return c.mcSeq(b.Stmts, name, typeName)
	}
	return c.mcSeq([]ast.Stmt{s}, name, typeName)
}

// mcStmtConsumes reports whether the statement itself definitely
// contains a consuming use of the binding (laundering/capture
// violations are reported as they are found, and count as consuming
// so each misuse is reported exactly once).
// mcStmtConsumesQuiet is the detection-only variant used by the
// overwrite re-scan (the reporting pass has already emitted any
// laundering/capture errors for these statements).
func (c *checker) mcStmtConsumesQuiet(s ast.Stmt, name string) bool {
	return c.mcStmtConsumesImpl(s, name, "", false)
}

func (c *checker) mcStmtConsumes(s ast.Stmt, name, typeName string) bool {
	return c.mcStmtConsumesImpl(s, name, typeName, true)
}

func (c *checker) mcStmtConsumesImpl(s ast.Stmt, name, typeName string, report bool) bool {
	switch x := s.(type) {
	case *ast.ExprStmt:
		return c.mcExprConsumes(x.Expr, name, typeName, report)
	case *ast.Var:
		// `var y = x;` — the obligation transfers to y (tracked at
		// its own binding site when its type is marked).
		if c.mcIsBinding(x.Init, name) {
			return true
		}
		return c.mcExprConsumes(x.Init, name, typeName, report)
	case *ast.Return:
		if x.Value == nil {
			return false
		}
		if c.mcIsBinding(x.Value, name) {
			return true
		}
		return c.mcExprConsumes(x.Value, name, typeName, report)
	case *ast.Match:
		if c.mcIsBinding(x.Tag, name) {
			return true
		}
	}
	return false
}

func (c *checker) mcIsBinding(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

// mcExprConsumes scans an expression tree for a consuming use of the
// bare binding. Call arguments consume; storing into marked
// containers consumes; storing into unmarked containers or capturing
// in a lambda is a violation (reported here, treated as consuming so
// the binding isn't double-reported).
func (c *checker) mcExprConsumes(e ast.Expr, name, typeName string, report bool) bool {
	if e == nil {
		return false
	}
	consumed := false
	ast.Walk(e, func(n ast.Node) bool {
		if consumed {
			return false
		}
		switch x := n.(type) {
		case *ast.Assign:
			// `y = x;` — the obligation transfers to y (itself
			// tracked when marked). Assignments are expressions
			// wrapped in ExprStmt.
			if c.mcIsBinding(x.Value, name) {
				consumed = true
				return false
			}
		case *ast.Call:
			// A bare binding in ARGUMENT position transfers the
			// obligation to the callee. The method-call receiver
			// (FieldAccess callee base) is neutral by design.
			for _, a := range x.Args {
				if c.mcIsBinding(a, name) {
					consumed = true
					return false
				}
			}
		case *ast.MatchExpr:
			if c.mcIsBinding(x.Tag, name) {
				consumed = true
				return false
			}
		case *ast.StructLit:
			for _, f := range x.Fields {
				if c.mcIsBinding(f.Value, name) {
					if _, marked := c.mustConsumeStructName(x.TypeName); marked {
						consumed = true
					} else {
						consumed = true
						if !report {
							return false
						}
						c.errfCode(x.P, "E067",
							"value of @must_consume type %q stored into unmarked struct %q — the consumption obligation is lost; mark %q @must_consume or consume the value directly",
							typeName, x.TypeName, x.TypeName)
					}
					return false
				}
			}
		case *ast.EnumLit:
			for _, a := range x.Args {
				if c.mcIsBinding(a, name) {
					if ed, ok := c.info.Enums[x.EnumName]; ok && ed.MustConsume {
						consumed = true
					} else {
						consumed = true
						if !report {
							return false
						}
						c.errfCode(x.P, "E067",
							"value of @must_consume type %q stored into unmarked enum %q — the consumption obligation is lost; mark %q @must_consume or consume the value directly",
							typeName, x.EnumName, x.EnumName)
					}
					return false
				}
			}
		case *ast.ArrayLit:
			for _, el := range x.Elems {
				if c.mcIsBinding(el, name) {
					consumed = true
					if report {
						c.errfCode(x.P, "E067",
							"value of @must_consume type %q stored into an array literal — arrays cannot carry consumption obligations; consume the value directly",
							typeName)
					}
					return false
				}
			}
		case *ast.TupleLit:
			for _, el := range x.Elems {
				if c.mcIsBinding(el, name) {
					consumed = true
					if report {
						c.errfCode(x.P, "E067",
							"value of @must_consume type %q stored into a tuple — tuples cannot carry consumption obligations; consume the value directly",
							typeName)
					}
					return false
				}
			}
		case *ast.Lambda:
			if mcMentions(x.Body, name) {
				consumed = true
				if report {
					c.errfCode(x.P, "E067",
						"value of @must_consume type %q captured by a closure — obligations cannot flow into closures; consume the value directly",
						typeName)
				}
				return false
			}
			return false // don't double-scan the lambda body
		}
		return true
	})
	return consumed
}

// mcMentions reports whether the block references the name at all.
func mcMentions(b *ast.Block, name string) bool {
	if b == nil {
		return false
	}
	found := false
	ast.Walk(b, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
			return false
		}
		return !found
	})
	return found
}

// mustConsumeTypeName resolves whether a type is a @must_consume
// struct or enum, returning its name.
func (c *checker) mustConsumeTypeName(t ast.Type) (string, bool) {
	switch x := t.(type) {
	case ast.StructType:
		return c.mustConsumeStructName(x.Name)
	case ast.EnumType:
		if ed, ok := c.info.Enums[x.Name]; ok && ed.MustConsume {
			return x.Name, true
		}
	}
	return "", false
}

func (c *checker) mustConsumeStructName(name string) (string, bool) {
	if sd, ok := c.info.Structs[name]; ok && sd.MustConsume {
		return name, true
	}
	return "", false
}
