// Package closureconv hoists local function declarations to the
// top-level Funcs list and rewrites their bodies so the codegen
// backend sees a flat program of top-level functions only.
//
// Each hoisted function gets a generated unique name and an extra
// trailing parameter `__env: number` (an i32 pointer to a heap-
// allocated env block). References to captured outer-scope
// variables are rewritten from *ast.Ident to *ast.CaptureRef, which
// codegen lowers as loads from the env parameter at the recorded
// offset. At the original def site, the *ast.FuncDecl statement is
// replaced with `var name = makeClosure(funcIndex, [captures])`,
// where the synthetic *ast.MakeClosure node tells codegen to
// allocate the env, populate it with current capture values, and
// build the 8-byte closure pair.
package closureconv

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// Convert mutates prog in place: every IsLocal *ast.FuncDecl in any
// function body is hoisted to prog.Funcs (with a renamed identifier
// and a synthetic env parameter), captures inside its body are
// rewritten as *ast.CaptureRef nodes, and the original statement is
// replaced with `var <orig-name> = MakeClosure{...}`.
func Convert(prog *ast.Program, info *checker.Info) error {
	return ConvertWith(prog, info, 4)
}

// ConvertWith is the pointer-width-aware variant. `ptrW` is 4
// on wasm32 / 8 on arm64; it sizes pointer-typed capture slots
// in the synthesised env block so heap addresses round-trip on
// arm64-darwin (>= 4 GiB heap).
func ConvertWith(prog *ast.Program, info *checker.Info, ptrW int) error {
	c := &converter{
		info:          info,
		hoisted:       map[string]int{},
		funcIdx:       map[string]int{},
		ptrW:          ptrW,
		nextHoistName: map[*ast.FuncDecl]string{},
	}
	// Original top-level functions occupy table indices 0..N-1; track
	// them so the synthetic env-call signature uses stable indices and
	// MakeClosure nodes can name them by table position.
	for i, fn := range prog.Funcs {
		c.funcIdx[fn.Name] = i
	}
	// Walk every top-level body and rewrite inner FuncDecl statements.
	for _, fn := range prog.Funcs {
		c.outerFn = fn
		c.hostFn = fn
		if err := c.rewriteBlock(fn.Body, nil); err != nil {
			return err
		}
	}
	c.outerFn = nil
	c.hostFn = nil
	// Append hoisted functions after the rewrite finishes so the
	// indices we stamped into MakeClosure nodes match prog.Funcs.
	prog.Funcs = append(prog.Funcs, c.appended...)
	return nil
}

type converter struct {
	info *checker.Info
	// hoisted gives each hoisted function its globally-unique name,
	// generated from a counter to avoid clashing with user names.
	hoisted map[string]int
	// funcIdx maps a top-level function name to its position in
	// prog.Funcs (i.e. its funcref-table index).
	funcIdx  map[string]int
	appended []*ast.FuncDecl
	// hostFn is the FuncDecl that owns the body we're currently
	// walking. For top-level entries it equals outerFn; for
	// nested local functions, it's the hoisted FuncDecl whose
	// body recursive rewriteBlock calls are processing. Vars
	// introduced by hoisting nested closures are registered
	// against hostFn so each hoisted function has a complete
	// per-function locals list at IR time.
	hostFn *ast.FuncDecl
	// outerFn is the top-level FuncDecl whose body we're currently
	// rewriting. Var statements introduced for `MakeClosure` are
	// recorded under it in info.Locals so the codegen pass declares
	// the right locals.
	outerFn *ast.FuncDecl
	// ptrW is the target's heap-pointer width in bytes — sizes
	// pointer-typed capture slots.
	ptrW int

	// nextHoistName overrides freshName for a specific FuncDecl
	// pointer. Set by the rewriteBlock pre-scan for sibling
	// clusters so hoist() uses the pre-assigned name (which
	// matches the entry in the siblings map that body-rewrites
	// reference).
	nextHoistName map[*ast.FuncDecl]string
	// currentSiblings is the active sibling map at the current
	// block-walk depth. Threads through to each FuncDecl's
	// hoist() so the body rewrite can recognise sibling calls
	// independent of the captureCtx (which is per-hoist).
	currentSiblings map[string]string
}

func (c *converter) freshName(orig string) string {
	c.hoisted[orig]++
	// Per-origin counter — using `len(c.hoisted)` would collide
	// when the same orig (e.g. "lambda" for every anonymous
	// function expression) gets hoisted more than once: both
	// hoists see the same map length and produce the same
	// `__closure_lambda_1` symbol, which the assembler rejects.
	return fmt.Sprintf("__closure_%s_%d", orig, c.hoisted[orig])
}

// detectMutualRecSCCs is the closureconv-side mirror of the
// checker's same-named helper. Both walks must agree on which
// names participate in a mutual-recursion SCC — the checker
// uses the result to decide what to skip in capture analysis,
// closureconv uses it to drive the null-env direct-call
// rewrites. Returns the set of FuncDecl names that participate
// in an SCC of size ≥ 2.
func detectMutualRecSCCs(fns []*ast.FuncDecl) map[string]bool {
	siblings := map[string]*ast.FuncDecl{}
	for _, fn := range fns {
		siblings[fn.Name] = fn
	}
	adj := map[string][]string{}
	for _, fn := range fns {
		seen := map[string]bool{}
		walkBodyForNames(fn.Body, fn.Name, siblings, seen)
		out := make([]string, 0, len(seen))
		for name := range seen {
			out = append(out, name)
		}
		adj[fn.Name] = out
	}
	index := 0
	indices := map[string]int{}
	lowlinks := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	out := map[string]bool{}
	var strongconnect func(name string)
	strongconnect = func(name string) {
		indices[name] = index
		lowlinks[name] = index
		index++
		stack = append(stack, name)
		onStack[name] = true
		for _, succ := range adj[name] {
			if _, ok := indices[succ]; !ok {
				strongconnect(succ)
				if lowlinks[succ] < lowlinks[name] {
					lowlinks[name] = lowlinks[succ]
				}
			} else if onStack[succ] {
				if indices[succ] < lowlinks[name] {
					lowlinks[name] = indices[succ]
				}
			}
		}
		if lowlinks[name] == indices[name] {
			var scc []string
			for {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[top] = false
				scc = append(scc, top)
				if top == name {
					break
				}
			}
			if len(scc) >= 2 {
				for _, n := range scc {
					out[n] = true
				}
			}
		}
	}
	for _, fn := range fns {
		if _, ok := indices[fn.Name]; !ok {
			strongconnect(fn.Name)
		}
	}
	return out
}

func walkBodyForNames(b *ast.Block, selfName string, siblings map[string]*ast.FuncDecl, seen map[string]bool) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		walkStmtForNames(st, selfName, siblings, seen)
	}
}

func walkStmtForNames(s ast.Stmt, selfName string, siblings map[string]*ast.FuncDecl, seen map[string]bool) {
	switch n := s.(type) {
	case *ast.Block:
		walkBodyForNames(n, selfName, siblings, seen)
	case *ast.If:
		walkExprForNames(n.Cond, selfName, siblings, seen)
		walkStmtForNames(n.Then, selfName, siblings, seen)
		walkStmtForNames(n.Else, selfName, siblings, seen)
	case *ast.While:
		walkExprForNames(n.Cond, selfName, siblings, seen)
		walkStmtForNames(n.Body, selfName, siblings, seen)
	case *ast.Loop:
		walkStmtForNames(n.Body, selfName, siblings, seen)
	case *ast.For:
		walkStmtForNames(n.Init, selfName, siblings, seen)
		walkExprForNames(n.Cond, selfName, siblings, seen)
		walkStmtForNames(n.Step, selfName, siblings, seen)
		walkStmtForNames(n.Body, selfName, siblings, seen)
	case *ast.Return:
		walkExprForNames(n.Value, selfName, siblings, seen)
	case *ast.Var:
		walkExprForNames(n.Init, selfName, siblings, seen)
	case *ast.Destructure:
		walkExprForNames(n.Init, selfName, siblings, seen)
		for _, sub := range n.Nested {
			if sub != nil {
				walkStmtForNames(sub, selfName, siblings, seen)
			}
		}
	case *ast.ExprStmt:
		walkExprForNames(n.Expr, selfName, siblings, seen)
	case *ast.Match:
		walkExprForNames(n.Tag, selfName, siblings, seen)
		for _, arm := range n.Arms {
			if arm.Literal != nil {
				walkExprForNames(arm.Literal, selfName, siblings, seen)
			}
			walkExprForNames(arm.Guard, selfName, siblings, seen)
			walkBodyForNames(arm.Body, selfName, siblings, seen)
		}
	case *ast.Defer:
		walkExprForNames(n.Expr, selfName, siblings, seen)
	}
}

func walkExprForNames(e ast.Expr, selfName string, siblings map[string]*ast.FuncDecl, seen map[string]bool) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.Ident:
		if n.Name != selfName {
			if _, ok := siblings[n.Name]; ok {
				seen[n.Name] = true
			}
		}
	case *ast.Binary:
		walkExprForNames(n.Left, selfName, siblings, seen)
		walkExprForNames(n.Right, selfName, siblings, seen)
	case *ast.Unary:
		walkExprForNames(n.Operand, selfName, siblings, seen)
	case *ast.CastExpr:
		walkExprForNames(n.Inner, selfName, siblings, seen)
	case *ast.DowncastExpr:
		walkExprForNames(n.Inner, selfName, siblings, seen)
	case *ast.SliceExpr:
		walkExprForNames(n.Source, selfName, siblings, seen)
		walkExprForNames(n.Low, selfName, siblings, seen)
		walkExprForNames(n.High, selfName, siblings, seen)
	case *ast.Call:
		walkExprForNames(n.Callee, selfName, siblings, seen)
		for _, a := range n.Args {
			walkExprForNames(a, selfName, siblings, seen)
		}
	case *ast.Index:
		walkExprForNames(n.Array, selfName, siblings, seen)
		walkExprForNames(n.Idx, selfName, siblings, seen)
	case *ast.ArrayLit:
		for _, el := range n.Elems {
			walkExprForNames(el, selfName, siblings, seen)
		}
	case *ast.Assign:
		walkExprForNames(n.Target, selfName, siblings, seen)
		walkExprForNames(n.Value, selfName, siblings, seen)
	case *ast.IfExpr:
		walkExprForNames(n.Cond, selfName, siblings, seen)
		walkExprForNames(n.Then, selfName, siblings, seen)
		walkExprForNames(n.Else, selfName, siblings, seen)
	case *ast.TryOp:
		walkExprForNames(n.Inner, selfName, siblings, seen)
	case *ast.MatchExpr:
		walkExprForNames(n.Tag, selfName, siblings, seen)
		for _, arm := range n.Arms {
			if arm.Literal != nil {
				walkExprForNames(arm.Literal, selfName, siblings, seen)
			}
			walkExprForNames(arm.Guard, selfName, siblings, seen)
			walkExprForNames(arm.Body, selfName, siblings, seen)
		}
	case *ast.BlockExpr:
		for _, st := range n.Stmts {
			walkStmtForNames(st, selfName, siblings, seen)
		}
		walkExprForNames(n.Tail, selfName, siblings, seen)
	case *ast.StructLit:
		for _, f := range n.Fields {
			walkExprForNames(f.Value, selfName, siblings, seen)
		}
	case *ast.FieldAccess:
		walkExprForNames(n.Target, selfName, siblings, seen)
	case *ast.FString:
		for _, p := range n.Parts {
			walkExprForNames(p.Expr, selfName, siblings, seen)
		}
		walkExprForNames(n.Desugared, selfName, siblings, seen)
	case *ast.TupleLit:
		for _, el := range n.Elems {
			walkExprForNames(el, selfName, siblings, seen)
		}
	case *ast.MapLit:
		for _, ent := range n.Entries {
			walkExprForNames(ent.Key, selfName, siblings, seen)
			walkExprForNames(ent.Value, selfName, siblings, seen)
		}
	case *ast.Lambda:
		walkBodyForNames(n.Body, selfName, siblings, seen)
	}
}

// rewriteBlock walks a block and replaces inner FuncDecl statements
// with closure-creation Vars. The hoistedFor parameter is non-nil
// only when we're walking a hoisted function's own body; in that
// case captured-name idents inside expressions are replaced with
// CaptureRef nodes.
func (c *converter) rewriteBlock(b *ast.Block, hoistedFor *captureCtx) error {
	if b == nil {
		return nil
	}
	// Mutual-recursion sibling-cluster pre-scan: identify local
	// FuncDecls in this block, pre-assign their hoisted names,
	// then detect which form an SCC (true mutual cycle). Only
	// SCC members get the null-env direct-call rewrite — plain
	// forward references capture normally (the closureconv pass
	// already builds the env entry for them; the surrounding
	// `var <name> = MakeClosure{...}` Var is initialised before
	// any caller reads it because Stmts run in source order).
	var localFns []*ast.FuncDecl
	for _, s := range b.Stmts {
		fn, ok := s.(*ast.FuncDecl)
		if !ok || !fn.IsLocal {
			continue
		}
		hoistName := c.freshName(fn.Name)
		c.nextHoistName[fn] = hoistName
		localFns = append(localFns, fn)
	}
	var sccMembers map[string]string
	if len(localFns) > 1 {
		// Re-run SCC detection over the local FuncDecls. Mirrors
		// the checker's `detectMutualRecSCCs` so we both agree
		// on which names are cycle members.
		ofInterest := map[string]*ast.FuncDecl{}
		for _, fn := range localFns {
			ofInterest[fn.Name] = fn
		}
		inScc := detectMutualRecSCCs(localFns)
		sccMembers = map[string]string{}
		for name := range inScc {
			sccMembers[name] = c.nextHoistName[ofInterest[name]]
		}
	}
	prevSiblings := c.currentSiblings
	if len(sccMembers) > 0 {
		// Merge with the enclosing context's mutual-rec map so
		// nested SCCs at different depths all see the right
		// rewrite targets.
		merged := map[string]string{}
		if hoistedFor != nil {
			for k, v := range hoistedFor.siblings {
				merged[k] = v
			}
		} else {
			for k, v := range prevSiblings {
				merged[k] = v
			}
		}
		for k, v := range sccMembers {
			merged[k] = v
		}
		c.currentSiblings = merged
		if hoistedFor != nil {
			hoistedFor.siblings = merged
		}
	}
	for i, s := range b.Stmts {
		ns, err := c.rewriteStmt(s, hoistedFor)
		if err != nil {
			return err
		}
		b.Stmts[i] = ns
	}
	c.currentSiblings = prevSiblings
	return nil
}

// captureCtx tracks the captures in scope while rewriting a hoisted
// function's body. byName maps each capture's original name to the
// byte offset (4 * index) inside the env block.
//
// `selfOrigName` / `selfHoistedName` track the function being
// hoisted right now so recursive self-references inside the body
// (e.g. `fact(n - 1)` inside `fact`'s body) can be rewritten to a
// direct call to the hoisted name + an `__env` arg forwarded
// through. The checker deliberately doesn't capture the function's
// own name to avoid a chicken-and-egg in the env's capture set, so
// the rewrite is the only thing that bridges the renamed top-level
// to the original recursive call site.
type captureCtx struct {
	byName          map[string]capInfo
	envName         string // always "$__env" but kept here for readability
	selfOrigName    string
	selfHoistedName string
	// siblings maps each sibling local FuncDecl's original name
	// to its hoisted top-level name. Set when the surrounding
	// block has multiple local FuncDecls that reference each
	// other (mutual recursion). The body-rewrite turns each
	// sibling call into a direct call to the hoisted name with
	// a null `__env` arg — the sibling has no real captures
	// (the checker skipped sibling references in capture
	// analysis), so the env block is unused.
	siblings map[string]string
}

type capInfo struct {
	offset int
	typ    ast.Type
}

// captureSlotSize returns the env-block slot footprint for a
// capture of type `t`. Wide scalar types (i64 / u64 / f64)
// take 8 bytes so their full bit pattern survives. Pointer-
// shaped heap refs (string, T[], structs, enums, slices,
// closures) take `ptrW` bytes — 4 on wasm32 (i32 heap pointer)
// or 8 on arm64 (full 64-bit pointer; arm64-darwin's heap is
// >= 4 GiB so the high bits MUST survive). Sub-i32 ints round
// up to 4 bytes because mixing 1- or 2-byte slots would force
// the codegen-side store path to track per-slot widths
// anyway, and captures are usually few.
func captureSlotSize(t ast.Type, ptrW int) int32 {
	// Two-word strings: a string capture is `(data, len)` —
	// two pointer-width slots in the env block. Matches the
	// codegen-side `arm64CaptureSlotSize` / wasm capture
	// layout (`docs/SSO-NATIVE-FLIP-STATUS.md`).
	if _, isStr := t.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
		return int32(2 * ptrW)
	}
	if ast.ElemSizeBytesFor(t, ptrW) == 8 {
		return 8
	}
	if ast.IsPointerType(t) {
		return int32(ptrW)
	}
	return 4
}

func (c *converter) rewriteStmt(s ast.Stmt, ctx *captureCtx) (ast.Stmt, error) {
	switch n := s.(type) {
	case *ast.FuncDecl:
		if !n.IsLocal {
			return n, nil
		}
		return c.hoist(n, ctx)
	case *ast.Block:
		return n, c.rewriteBlock(n, ctx)
	case *ast.If:
		nc, err := c.rewriteExpr(n.Cond, ctx)
		if err != nil {
			return nil, err
		}
		n.Cond = nc
		ns, err := c.rewriteStmt(n.Then, ctx)
		if err != nil {
			return nil, err
		}
		n.Then = ns
		if n.Else != nil {
			ns, err := c.rewriteStmt(n.Else, ctx)
			if err != nil {
				return nil, err
			}
			n.Else = ns
		}
		return n, nil
	case *ast.While:
		nc, err := c.rewriteExpr(n.Cond, ctx)
		if err != nil {
			return nil, err
		}
		n.Cond = nc
		ns, err := c.rewriteStmt(n.Body, ctx)
		if err != nil {
			return nil, err
		}
		n.Body = ns
		return n, nil
	case *ast.Loop:
		ns, err := c.rewriteStmt(n.Body, ctx)
		if err != nil {
			return nil, err
		}
		n.Body = ns
		return n, nil
	case *ast.For:
		if n.Init != nil {
			ns, err := c.rewriteStmt(n.Init, ctx)
			if err != nil {
				return nil, err
			}
			n.Init = ns
		}
		nc, err := c.rewriteExpr(n.Cond, ctx)
		if err != nil {
			return nil, err
		}
		n.Cond = nc
		if n.Step != nil {
			ns, err := c.rewriteStmt(n.Step, ctx)
			if err != nil {
				return nil, err
			}
			n.Step = ns
		}
		nb, err := c.rewriteStmt(n.Body, ctx)
		if err != nil {
			return nil, err
		}
		n.Body = nb
		return n, nil
	case *ast.Return:
		if n.Value != nil {
			nv, err := c.rewriteExpr(n.Value, ctx)
			if err != nil {
				return nil, err
			}
			n.Value = nv
		}
		return n, nil
	case *ast.Var:
		nv, err := c.rewriteExpr(n.Init, ctx)
		if err != nil {
			return nil, err
		}
		n.Init = nv
		return n, nil
	case *ast.Destructure:
		nv, err := c.rewriteExpr(n.Init, ctx)
		if err != nil {
			return nil, err
		}
		n.Init = nv
		for _, sub := range n.Nested {
			if sub == nil {
				continue
			}
			if _, err := c.rewriteStmt(sub, ctx); err != nil {
				return nil, err
			}
		}
		return n, nil
	case *ast.ExprStmt:
		ne, err := c.rewriteExpr(n.Expr, ctx)
		if err != nil {
			return nil, err
		}
		n.Expr = ne
		return n, nil
	case *ast.Match:
		nt, err := c.rewriteExpr(n.Tag, ctx)
		if err != nil {
			return nil, err
		}
		n.Tag = nt
		for _, arm := range n.Arms {
			if arm.Guard != nil {
				ng, err := c.rewriteExpr(arm.Guard, ctx)
				if err != nil {
					return nil, err
				}
				arm.Guard = ng
			}
			if arm.Body != nil {
				if err := c.rewriteBlock(arm.Body, ctx); err != nil {
					return nil, err
				}
			}
		}
		return n, nil
	case *ast.Defer:
		if n.Expr != nil {
			ne, err := c.rewriteExpr(n.Expr, ctx)
			if err != nil {
				return nil, err
			}
			n.Expr = ne
		}
		return n, nil
	}
	return s, nil
}

func (c *converter) rewriteExpr(e ast.Expr, ctx *captureCtx) (ast.Expr, error) {
	switch n := e.(type) {
	case *ast.Lambda:
		// Anonymous function expression — synthesise a hoisted
		// FuncDecl with a fresh name, hoist it via the same
		// pipeline named local FuncDecls use, and return the
		// MakeClosure expression in the Lambda's place. The
		// surrounding expression sees a closure pair pointer at
		// runtime, indistinguishable from a named local fn's
		// closure value.
		hoistedName := c.freshName("lambda")
		fn := &ast.FuncDecl{
			P:          n.P,
			Name:       hoistedName,
			Params:     n.Params,
			ReturnType: n.ReturnType,
			Body:       n.Body,
			IsLocal:    true,
			Captures:   n.Captures,
		}
		// Re-key the body locals the checker registered against
		// the throwaway synthetic FuncDecl (see ast.Lambda.Synthetic)
		// onto the hoisted FuncDecl. Without this, `lowerFunc` reads
		// `info.Locals[fn]` and sees an empty list, then the body
		// walk hits "var X has no slot" on every `var x = ...`
		// inside the lambda body.
		if n.Synthetic != nil {
			if locals, ok := c.info.Locals[n.Synthetic]; ok {
				c.info.Locals[fn] = locals
				delete(c.info.Locals, n.Synthetic)
			}
		}
		// Build the lambda's own capture context — same shape
		// hoist() builds for named local FuncDecls. Walk the body
		// with the new context so captured-name idents inside it
		// resolve to CaptureRef nodes against the lambda's env.
		lamCtx := &captureCtx{
			byName:          map[string]capInfo{},
			envName:         "$__env",
			selfOrigName:    hoistedName,
			selfHoistedName: hoistedName,
		}
		off := int32(0)
		for _, cap := range fn.Captures {
			off = ast.CaptureAlign(off, cap.Type, c.ptrW)
			lamCtx.byName[cap.Name] = capInfo{offset: int(off), typ: cap.Type}
			off += captureSlotSize(cap.Type, c.ptrW)
		}
		// Append synthetic __env param + register the hoisted
		// signature in FuncSigs so indirect-call dispatch can
		// resolve the (type $tN) slot for OpCallIndirect.
		fn.Params = append(fn.Params, ast.Param{Name: "__env", Type: ast.NumberType{}})
		hoistedSig := &ast.FuncType{Result: fn.ReturnType}
		for _, p := range fn.Params {
			hoistedSig.Params = append(hoistedSig.Params, p.Type)
		}
		c.info.FuncSigs[hoistedName] = hoistedSig
		// Rewrite body references through lamCtx so captures
		// become CaptureRef and the rest passes through unchanged.
		prevHost := c.hostFn
		c.hostFn = fn
		if err := c.rewriteBlock(fn.Body, lamCtx); err != nil {
			c.hostFn = prevHost
			return nil, err
		}
		c.hostFn = prevHost
		idx := len(c.funcIdx) + len(c.appended)
		c.funcIdx[hoistedName] = idx
		c.appended = append(c.appended, fn)
		// Each capture's source expression is the outer-scope name;
		// re-walk through the surrounding context so nested
		// closures-inside-lambdas forward captures correctly.
		caps := make([]ast.Expr, len(n.Captures))
		for i, cap := range n.Captures {
			var src ast.Expr = &ast.Ident{P: n.P, Name: cap.Name}
			if rewritten, err := c.rewriteExpr(src, ctx); err == nil {
				src = rewritten
			}
			caps[i] = src
		}
		return &ast.MakeClosure{
			P:         n.P,
			FuncName:  hoistedName,
			FuncIndex: idx,
			Captures:  caps,
		}, nil
	case *ast.Ident:
		if ctx != nil {
			if ci, ok := ctx.byName[n.Name]; ok {
				return &ast.CaptureRef{P: n.P, Name: n.Name, Offset: ci.offset, Type: ci.typ}, nil
			}
			// Mutual-recursion sibling reference as a VALUE
			// (not a Call's callee — that case has its own
			// rewrite below). Members of a true mutual-recursion
			// SCC have NO real outer captures (we'd have errored
			// earlier), so each hoists to a zero-capture closure
			// and the static closure cell `{__closure_<sib>_<N>,
			// env=0}` is the right value. The IR's Ident lookup
			// on the hoisted name emits OpConstFunc which
			// produces exactly that cell pointer.
			if hoisted, isSibling := ctx.siblings[n.Name]; isSibling && n.Name != ctx.selfOrigName {
				return &ast.Ident{P: n.P, Name: hoisted}, nil
			}
		}
		return n, nil
	case *ast.Binary:
		nl, err := c.rewriteExpr(n.Left, ctx)
		if err != nil {
			return nil, err
		}
		n.Left = nl
		nr, err := c.rewriteExpr(n.Right, ctx)
		if err != nil {
			return nil, err
		}
		n.Right = nr
		return n, nil
	case *ast.Unary:
		nv, err := c.rewriteExpr(n.Operand, ctx)
		if err != nil {
			return nil, err
		}
		n.Operand = nv
		return n, nil
	case *ast.CastExpr:
		ni, err := c.rewriteExpr(n.Inner, ctx)
		if err != nil {
			return nil, err
		}
		n.Inner = ni
		return n, nil
	case *ast.DowncastExpr:
		ni, err := c.rewriteExpr(n.Inner, ctx)
		if err != nil {
			return nil, err
		}
		n.Inner = ni
		return n, nil
	case *ast.SliceExpr:
		ns, err := c.rewriteExpr(n.Source, ctx)
		if err != nil {
			return nil, err
		}
		n.Source = ns
		if n.Low != nil {
			nl, err := c.rewriteExpr(n.Low, ctx)
			if err != nil {
				return nil, err
			}
			n.Low = nl
		}
		if n.High != nil {
			nh, err := c.rewriteExpr(n.High, ctx)
			if err != nil {
				return nil, err
			}
			n.High = nh
		}
		return n, nil
	case *ast.Call:
		// Recursive self-reference inside the hoisted body:
		// `fact(n - 1)` where `fact` is the function whose body
		// we're rewriting. The checker skipped this in capture
		// collection (to avoid the chicken-and-egg of the env
		// needing the closure that needs the env), so it would
		// otherwise fall through unchanged — IR would then call
		// a top-level `fact` which no longer exists (it's now
		// `__closure_fact_1`). Rewrite to a direct call to the
		// hoisted name and forward our own `__env` through so
		// the recursive callee gets the same captured-state
		// block we have. Skip the Ident-as-CaptureRef rewrite
		// on the callee for this case (a captured value named
		// the same as the hoisted fn would be ambiguous, but
		// the checker's capture rule forbids that already).
		if ctx != nil && ctx.selfOrigName != "" {
			if id, ok := n.Callee.(*ast.Ident); ok && id.Name == ctx.selfOrigName {
				n.Callee = &ast.Ident{P: id.P, Name: ctx.selfHoistedName}
				for i, a := range n.Args {
					na, err := c.rewriteExpr(a, ctx)
					if err != nil {
						return nil, err
					}
					n.Args[i] = na
				}
				n.Args = append(n.Args, &ast.Ident{P: id.P, Name: "__env"})
				return n, nil
			}
		}
		// Sibling-cluster call: `isOdd(n - 1)` inside `isEven`'s
		// body where both siblings are local FuncDecls in the
		// same enclosing block. Rewrite to a direct call to the
		// sibling's hoisted name + a null env arg. Mutual
		// recursion would otherwise need cyclic env init (each
		// closure's env pointing at the other's pair, which
		// exists only after BOTH pairs are built). The checker
		// skipped sibling references in capture analysis, so each
		// hoisted sibling lowers to a zero-capture closure and
		// the body's sibling calls bypass the env entirely.
		if ctx != nil && len(ctx.siblings) > 0 {
			if id, ok := n.Callee.(*ast.Ident); ok {
				if hoisted, isSibling := ctx.siblings[id.Name]; isSibling && id.Name != ctx.selfOrigName {
					n.Callee = &ast.Ident{P: id.P, Name: hoisted}
					for i, a := range n.Args {
						na, err := c.rewriteExpr(a, ctx)
						if err != nil {
							return nil, err
						}
						n.Args[i] = na
					}
					// Null env — sibling has no real captures.
					n.Args = append(n.Args, &ast.NumberLit{P: id.P, Value: 0})
					return n, nil
				}
			}
		}
		nc, err := c.rewriteExpr(n.Callee, ctx)
		if err != nil {
			return nil, err
		}
		n.Callee = nc
		for i, a := range n.Args {
			na, err := c.rewriteExpr(a, ctx)
			if err != nil {
				return nil, err
			}
			n.Args[i] = na
		}
		return n, nil
	case *ast.Index:
		na, err := c.rewriteExpr(n.Array, ctx)
		if err != nil {
			return nil, err
		}
		n.Array = na
		ni, err := c.rewriteExpr(n.Idx, ctx)
		if err != nil {
			return nil, err
		}
		n.Idx = ni
		return n, nil
	case *ast.ArrayLit:
		for i, el := range n.Elems {
			ne, err := c.rewriteExpr(el, ctx)
			if err != nil {
				return nil, err
			}
			n.Elems[i] = ne
		}
		return n, nil
	case *ast.Assign:
		nt, err := c.rewriteExpr(n.Target, ctx)
		if err != nil {
			return nil, err
		}
		n.Target = nt
		nv, err := c.rewriteExpr(n.Value, ctx)
		if err != nil {
			return nil, err
		}
		n.Value = nv
		return n, nil
	case *ast.IfExpr:
		nc, err := c.rewriteExpr(n.Cond, ctx)
		if err != nil {
			return nil, err
		}
		n.Cond = nc
		nt, err := c.rewriteExpr(n.Then, ctx)
		if err != nil {
			return nil, err
		}
		n.Then = nt
		ne, err := c.rewriteExpr(n.Else, ctx)
		if err != nil {
			return nil, err
		}
		n.Else = ne
		return n, nil
	case *ast.TryOp:
		ni, err := c.rewriteExpr(n.Inner, ctx)
		if err != nil {
			return nil, err
		}
		n.Inner = ni
		return n, nil
	case *ast.MatchExpr:
		nt, err := c.rewriteExpr(n.Tag, ctx)
		if err != nil {
			return nil, err
		}
		n.Tag = nt
		for _, arm := range n.Arms {
			if arm.Guard != nil {
				ng, err := c.rewriteExpr(arm.Guard, ctx)
				if err != nil {
					return nil, err
				}
				arm.Guard = ng
			}
			nb, err := c.rewriteExpr(arm.Body, ctx)
			if err != nil {
				return nil, err
			}
			arm.Body = nb
		}
		return n, nil
	case *ast.BlockExpr:
		for idx, st := range n.Stmts {
			ns, err := c.rewriteStmt(st, ctx)
			if err != nil {
				return nil, err
			}
			n.Stmts[idx] = ns
		}
		if n.Tail != nil {
			nt, err := c.rewriteExpr(n.Tail, ctx)
			if err != nil {
				return nil, err
			}
			n.Tail = nt
		}
		return n, nil
	case *ast.StructLit:
		for i, f := range n.Fields {
			nv, err := c.rewriteExpr(f.Value, ctx)
			if err != nil {
				return nil, err
			}
			n.Fields[i].Value = nv
		}
		return n, nil
	case *ast.FieldAccess:
		nt, err := c.rewriteExpr(n.Target, ctx)
		if err != nil {
			return nil, err
		}
		n.Target = nt
		return n, nil
	case *ast.FString:
		// The checker desugared the FString into a `+`-chain of
		// to_string()-calls and stashed it on `n.Desugared`; the
		// IR walks that desugaring rather than n.Parts directly.
		// Without rewriting through Desugared, any captured-name
		// reference inside an `f"…{cap}…"` survives as a raw
		// Ident and the IR errors with "unresolved identifier".
		// Walk the Parts too so AST consumers that read the raw
		// form (formatter, LSP signatureHelp) see consistent
		// CaptureRef-annotated trees.
		for _, p := range n.Parts {
			if p.Expr != nil {
				ne, err := c.rewriteExpr(p.Expr, ctx)
				if err != nil {
					return nil, err
				}
				p.Expr = ne
			}
		}
		if n.Desugared != nil {
			nd, err := c.rewriteExpr(n.Desugared, ctx)
			if err != nil {
				return nil, err
			}
			n.Desugared = nd
		}
		return n, nil
	case *ast.TupleLit:
		for i, el := range n.Elems {
			ne, err := c.rewriteExpr(el, ctx)
			if err != nil {
				return nil, err
			}
			n.Elems[i] = ne
		}
		return n, nil
	case *ast.MapLit:
		for i := range n.Entries {
			nk, err := c.rewriteExpr(n.Entries[i].Key, ctx)
			if err != nil {
				return nil, err
			}
			n.Entries[i].Key = nk
			nv, err := c.rewriteExpr(n.Entries[i].Value, ctx)
			if err != nil {
				return nil, err
			}
			n.Entries[i].Value = nv
		}
		return n, nil
	}
	return e, nil
}

// hoist takes a local FuncDecl, gives it a unique top-level name,
// adds a synthetic `__env: number` parameter, rewrites its body's
// capture references, appends it to the converter's hoisted list,
// and returns the replacement Var statement that creates a closure
// at the original def site.
func (c *converter) hoist(fn *ast.FuncDecl, parentCtx *captureCtx) (ast.Stmt, error) {
	origName := fn.Name
	// Use the pre-assigned hoist name if the rewriteBlock
	// pre-scan claimed one for this FuncDecl (sibling-cluster
	// case). Otherwise generate a fresh one.
	hoistedName, ok := c.nextHoistName[fn]
	if !ok {
		hoistedName = c.freshName(origName)
	} else {
		delete(c.nextHoistName, fn)
	}

	// Build the capture context for the hoisted body. Captures that
	// are themselves captures of the enclosing function need their
	// values forwarded to the new env, but the body still reads them
	// through its own env — the offset is just whatever index we
	// assign here.
	//
	// Per-stride offset accumulator: 4-byte slots for most types,
	// 8-byte slots for i64 / u64 / f64 (so the capture's full
	// bit-pattern survives). Sub-i32 (u8) rounds up to a 4-byte
	// slot to keep alignment trivial — the env block is written
	// sequentially and aligned-i64 reads after a sub-i32 capture
	// would otherwise straddle a 4-byte boundary in the unhelpful
	// direction.
	ctx := &captureCtx{
		byName:          map[string]capInfo{},
		envName:         "$__env",
		selfOrigName:    origName,
		selfHoistedName: hoistedName,
		siblings:        c.currentSiblings,
	}
	off := int32(0)
	for _, cap := range fn.Captures {
		off = ast.CaptureAlign(off, cap.Type, c.ptrW)
		ctx.byName[cap.Name] = capInfo{offset: int(off), typ: cap.Type}
		off += captureSlotSize(cap.Type, c.ptrW)
	}

	// Add the synthetic env parameter as the function's last param.
	fn.Params = append(fn.Params, ast.Param{Name: "__env", Type: ast.NumberType{}})
	// Rename the function so the top-level table can host it without
	// colliding with user names.
	fn.Name = hoistedName
	// Update FuncSigs: the original local-name signature stays so the
	// outer body's calls keep type-checking; add a hoisted-name entry
	// (with the env param) so codegen can look up the indirect-call
	// signature.
	hoistedSig := &ast.FuncType{Result: fn.ReturnType}
	for _, p := range fn.Params {
		hoistedSig.Params = append(hoistedSig.Params, p.Type)
	}
	c.info.FuncSigs[hoistedName] = hoistedSig

	// Rewrite the body's captured-name references and any nested
	// closures. Swap hostFn so any Vars we introduce while
	// processing nested local functions inside this body get
	// attributed to THIS function's locals — not the outermost
	// top-level entry. Without that fix, IR processing of the
	// hoisted fn would fail to find a slot for the nested-
	// closure Var (it'd be in the top-level's locals list, not
	// this hoisted function's).
	prevHost := c.hostFn
	c.hostFn = fn
	if err := c.rewriteBlock(fn.Body, ctx); err != nil {
		c.hostFn = prevHost
		return nil, err
	}
	c.hostFn = prevHost

	// Reserve the hoisted function's table index now (after existing
	// top-level funcs and any earlier hoisted ones).
	idx := len(c.funcIdx) + len(c.appended)
	c.funcIdx[hoistedName] = idx
	c.appended = append(c.appended, fn)

	// Build the MakeClosure node. Each capture is the current value
	// of the named outer variable, rewritten through parentCtx so
	// that nested closures forward outer captures correctly.
	caps := make([]ast.Expr, len(fn.Captures))
	for i, cap := range fn.Captures {
		var src ast.Expr = &ast.Ident{P: fn.P, Name: cap.Name}
		if rewritten, err := c.rewriteExpr(src, parentCtx); err == nil {
			src = rewritten
		}
		caps[i] = src
	}
	mc := &ast.MakeClosure{
		P:         fn.P,
		FuncName:  hoistedName,
		FuncIndex: idx,
		Captures:  caps,
	}

	// The original local-name FuncSig describes the user-visible
	// signature without the env param; that's what calls bind
	// against. Indirect-call codegen knows to add the env arg.
	userSig := &ast.FuncType{Result: fn.ReturnType}
	for _, p := range fn.Params[:len(fn.Params)-1] { // drop trailing __env
		userSig.Params = append(userSig.Params, p.Type)
	}

	v := &ast.Var{
		P:    fn.P,
		Name: origName,
		Type: userSig,
		Init: mc,
	}
	// Register the new Var against its IMMEDIATE host function —
	// the one whose body the nested decl appeared in. For a
	// top-level body, host == outerFn. For a nested local
	// function inside another local function, host is the
	// surrounding hoisted FuncDecl, so each hoisted function
	// ends up with a complete per-function locals list at IR
	// time. Falling back to outerFn if hostFn isn't set would
	// re-introduce the chained-`use` slot bug.
	host := c.hostFn
	if host == nil {
		host = c.outerFn
	}
	if host != nil {
		c.info.VarTypes[v] = userSig
		c.info.Locals[host] = append(c.info.Locals[host], v)
	}
	return v, nil
}
