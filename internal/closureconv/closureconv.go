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
	c := &converter{info: info, hoisted: map[string]int{}, funcIdx: map[string]int{}, ptrW: ptrW}
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
}

func (c *converter) freshName(orig string) string {
	c.hoisted[orig]++
	return fmt.Sprintf("__closure_%s_%d", orig, len(c.hoisted))
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
	for i, s := range b.Stmts {
		ns, err := c.rewriteStmt(s, hoistedFor)
		if err != nil {
			return err
		}
		b.Stmts[i] = ns
	}
	return nil
}

// captureCtx tracks the captures in scope while rewriting a hoisted
// function's body. byName maps each capture's original name to the
// byte offset (4 * index) inside the env block.
type captureCtx struct {
	byName  map[string]capInfo
	envName string // always "$__env" but kept here for readability
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
	case *ast.Arena:
		return n, c.rewriteBlock(n.Body, ctx)
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
		return n, nil
	case *ast.ExprStmt:
		ne, err := c.rewriteExpr(n.Expr, ctx)
		if err != nil {
			return nil, err
		}
		n.Expr = ne
		return n, nil
	case *ast.Switch:
		nt, err := c.rewriteExpr(n.Tag, ctx)
		if err != nil {
			return nil, err
		}
		n.Tag = nt
		for _, k := range n.Cases {
			for i, v := range k.Values {
				nv, err := c.rewriteExpr(v, ctx)
				if err != nil {
					return nil, err
				}
				k.Values[i] = nv
			}
			if err := c.rewriteBlock(k.Body, ctx); err != nil {
				return nil, err
			}
		}
		if n.Default != nil {
			if err := c.rewriteBlock(n.Default, ctx); err != nil {
				return nil, err
			}
		}
		return n, nil
	}
	return s, nil
}

func (c *converter) rewriteExpr(e ast.Expr, ctx *captureCtx) (ast.Expr, error) {
	switch n := e.(type) {
	case *ast.Ident:
		if ctx != nil {
			if ci, ok := ctx.byName[n.Name]; ok {
				return &ast.CaptureRef{P: n.P, Name: n.Name, Offset: ci.offset, Type: ci.typ}, nil
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
	hoistedName := c.freshName(origName)

	// Build the capture context for the hoisted body. Captures that
	// are themselves captures of the enclosing function need their
	// values forwarded to the new env, but the body still reads them
	// through its own env — the offset is just whatever index we
	// assign here.
	//
	// Per-stride offset accumulator: 4-byte slots for most types,
	// 8-byte slots for i64 / u64 / f64 (so the capture's full
	// bit-pattern survives). Sub-i32 (u8 / i8 / u16 / i16) round
	// up to a 4-byte slot to keep alignment trivial — the env
	// block is written sequentially and aligned-i64 reads after
	// a sub-i32 capture would otherwise straddle a 4-byte
	// boundary in the unhelpful direction.
	ctx := &captureCtx{byName: map[string]capInfo{}, envName: "$__env"}
	off := int32(0)
	for _, cap := range fn.Captures {
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
