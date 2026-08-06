package ir

import (
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

const resourceDropPrefix = "__resource_drop_"

// insert_resource_drops.go implements automatic drop for owned WIT resource
// handles — the headline of P5 slice 3 (docs/WIT-BRING-YOUR-OWN.md). When an
// `own R` local goes out of scope without being moved (returned, or passed to
// an `own` parameter), the compiler releases it by calling the resource's
// `[resource-drop]` import, so user code never writes a manual drop.
//
// It runs in LowerWith *before* eraseHandleTypes (it needs the handle types
// intact), and:
//   - synthesizes, once per dropped resource, a body-less
//     `@import("<iface>", "[resource-drop]<wit>")` drop function appended to
//     prog.Funcs (so the wasm backend imports it and the world-driven composer
//     — slice 2 — wires the canon resource.drop), and
//   - inserts `defer <drop>(local);` right after each kept handle's
//     declaration, reusing Fern's defer machinery, which runs the drop on every
//     function-exit path (and is a no-op when the declaration wasn't reached).
//
// Soundness over completeness: a handle whose use can't be proven non-consuming
// (anything but a borrow-parameter call argument) is treated as moved and left
// for its consumer to drop — leaking is safe, a double drop is not. Borrowed
// handles (`borrow R`) are never dropped.
func insertResourceDrops(prog *ast.Program, info *checker.Info) {
	if info == nil || len(info.Resources) == 0 {
		return
	}
	// Idempotency: LowerWith can run more than once on the same Program (the
	// diff oracle / multi-backend compiles). The synthesized drop functions are
	// the marker that this pass already ran — bail before inserting a second
	// defer (which would double-drop) or a duplicate import.
	for _, fn := range prog.Funcs {
		if strings.HasPrefix(fn.Name, resourceDropPrefix) {
			return
		}
	}
	var neededOrder []string
	needed := map[string]bool{}
	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		kept := keptHandleLocals(fn, info)
		if len(kept) == 0 {
			continue
		}
		insertDefersInBlock(fn.Body, kept)
		for _, res := range kept {
			if !needed[res] {
				needed[res] = true
				neededOrder = append(neededOrder, res)
			}
		}
	}
	// One body-less @import drop function per dropped resource, in
	// deterministic (first-seen) order.
	for _, res := range neededOrder {
		rd := info.Resources[res]
		name := resourceDropFuncName(res)
		params := []ast.Param{{Name: "h", Type: ast.HandleType{Resource: res}}}
		prog.Funcs = append(prog.Funcs, &ast.FuncDecl{
			Name:          name,
			ImportIface:   rd.ImportIface,
			ImportWITName: "[resource-drop]" + rd.ImportWITName,
			Params:        params,
			ReturnType:    ast.VoidType{},
		})
		// Register the signature so any FuncSigs-driven lowering of the inserted
		// call resolves (erased to (i32)->void alongside the rest by
		// eraseHandleTypes, which runs after this pass).
		info.FuncSigs[name] = &ast.FuncType{Params: []ast.Type{ast.HandleType{Resource: res}}, Result: ast.VoidType{}}
	}
}

func resourceDropFuncName(res string) string { return resourceDropPrefix + res }

// keptHandleLocals returns the owned-handle local variables in fn that are safe
// to auto-drop, mapped to their resource name. A handle is kept unless it is
// moved (escapes via return or a consuming use) or its resource has no WIT
// `@import` binding to drop through.
func keptHandleLocals(fn *ast.FuncDecl, info *checker.Info) map[string]string {
	// Candidate owned-handle locals: name → resource (droppable only).
	candidates := map[string]string{}
	ast.Walk(fn, func(n ast.Node) bool {
		if v, ok := n.(*ast.Var); ok {
			if h, ok := v.Type.(ast.HandleType); ok && !h.Borrowed {
				if rd, ok := info.Resources[h.Resource]; ok && rd.ImportIface != "" && rd.ImportWITName != "" {
					candidates[v.Name] = h.Resource
				}
			}
		}
		return true
	})
	if len(candidates) == 0 {
		return nil
	}
	moved := movedHandles(fn, candidates, info)
	kept := map[string]string{}
	for name, res := range candidates {
		if !moved[name] {
			kept[name] = res
		}
	}
	return kept
}

// movedHandles returns the candidate handle names that escape their scope. A
// handle is considered NOT moved only at the occurrences where it is a call
// argument bound to a `borrow` parameter; every other occurrence (a return
// value, a consuming/own call argument, a struct/array/tuple/closure capture,
// an assignment, …) is treated as a move. Uses *Ident pointer identity to tell
// the safe borrow-argument occurrences apart from all other uses.
func movedHandles(fn *ast.FuncDecl, candidates map[string]string, info *checker.Info) map[string]bool {
	safe := map[*ast.Ident]bool{}
	ast.Walk(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.Call)
		if !ok {
			return true
		}
		callee, ok := call.Callee.(*ast.Ident)
		if !ok {
			return true
		}
		sig, ok := info.FuncSigs[callee.Name]
		if !ok {
			return true
		}
		for i, arg := range call.Args {
			id, ok := arg.(*ast.Ident)
			if !ok || candidates[id.Name] == "" {
				continue
			}
			if i < len(sig.Params) {
				if h, ok := sig.Params[i].(ast.HandleType); ok && h.Borrowed {
					safe[id] = true // borrow argument: non-consuming
				}
			}
		}
		return true
	})
	moved := map[string]bool{}
	ast.Walk(fn, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if candidates[id.Name] != "" && !safe[id] {
			moved[id.Name] = true
		}
		return true
	})
	return moved
}

// insertDefersInBlock rewrites b.Stmts to insert a `defer <drop>(name);`
// immediately after each kept owned-handle declaration, and recurses into
// nested blocks. The defer is armed when the declaration is reached and runs on
// every function-exit path (Fern's existing defer machinery).
func insertDefersInBlock(b *ast.Block, kept map[string]string) {
	if b == nil {
		return
	}
	out := make([]ast.Stmt, 0, len(b.Stmts))
	for _, s := range b.Stmts {
		insertDefersInStmt(s, kept)
		out = append(out, s)
		if v, ok := s.(*ast.Var); ok {
			if res, ok := kept[v.Name]; ok {
				out = append(out, makeDropDefer(v, res))
			}
		}
	}
	b.Stmts = out
}

// insertDefersInStmt recurses into the nested blocks a statement holds.
func insertDefersInStmt(s ast.Stmt, kept map[string]string) {
	switch x := s.(type) {
	case *ast.Block:
		insertDefersInBlock(x, kept)
	case *ast.If:
		insertDefersInStmt(x.Then, kept)
		if x.Else != nil {
			insertDefersInStmt(x.Else, kept)
		}
	case *ast.While:
		insertDefersInStmt(x.Body, kept)
	case *ast.Loop:
		insertDefersInStmt(x.Body, kept)
	case *ast.For:
		insertDefersInStmt(x.Body, kept)
	case *ast.Match:
		for _, a := range x.Arms {
			if a.Body != nil {
				insertDefersInStmt(a.Body, kept)
			}
		}
	}
}

func makeDropDefer(v *ast.Var, res string) ast.Stmt {
	return &ast.Defer{
		P: v.P,
		Expr: &ast.Call{
			P:      v.P,
			Callee: &ast.Ident{P: v.P, Name: resourceDropFuncName(res)},
			Args:   []ast.Expr{&ast.Ident{P: v.P, Name: v.Name}},
		},
	}
}
