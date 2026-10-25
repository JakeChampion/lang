package fernsmith

import (
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/syntax/printer"
)

// The guards of the EMI blocks. They are @noinline, so the compiler cannot
// fold a guarded block away before the passes under test see it.
const emiGuards = `
@noinline function emi_false(): boolean { return false; }
@noinline function emi_true(): boolean { return true; }
`

// EMI rewrites a program in ways that must not change its output or its exit
// code (Orion, PLDI 2014; GraphicsFuzz, OOPSLA 2017). Each step does one of
// these things:
//
//   - dead: put a block behind a false guard, with a statement copied from
//     the same function
//   - live: move a statement into a block behind a true guard, with a copied
//     statement in the else block
//   - jump: put a break or a continue behind a false guard in a loop
//   - once: move a statement into a loop that breaks after one pass
//
// valid accepts or rejects the program after each step, and EMI keeps only
// the steps it accepts. EMI returns the program and the number of steps kept.
func EMI(src string, seed uint64, steps int, valid func(string) bool) (string, int) {
	if strings.Contains(src, "emi_false") || strings.Contains(src, "emi_true") {
		return src, 0
	}
	rng := seedRNG(seed)
	cur, kept := src+emiGuards, 0
	for i := 0; i < steps; i++ {
		next, ok := emiStep(cur, rng)
		if ok && valid(next) {
			cur = next
			kept++
		}
	}
	if kept == 0 {
		return src, 0
	}
	return cur, kept
}

type emiSite struct {
	blk  *ast.Block
	fn   int
	loop bool
	// A donor that holds the site would make the tree hold itself.
	outer []ast.Stmt
}

type emiWalk struct {
	sites  []emiSite
	donors [][]ast.Stmt
	fn     int
	outer  []ast.Stmt
}

func emiStep(src string, rng *rand.Rand) (string, bool) {
	prog, err := parser.Parse(src)
	if err != nil {
		return "", false
	}
	prog.Comments, prog.BlankLines = nil, nil
	w := &emiWalk{}
	for _, fn := range prog.Funcs {
		if fn.Body == nil || fn.Name == "emi_false" || fn.Name == "emi_true" {
			continue
		}
		w.fn = len(w.donors)
		w.donors = append(w.donors, nil)
		w.stmt(fn.Body, false)
	}
	if len(w.sites) == 0 {
		return "", false
	}
	s := w.sites[rng.IntN(len(w.sites))]
	d := w.donors[s.fn]
	if len(d) == 0 {
		return "", false
	}
	donor := d[rng.IntN(len(d))]
	if slices.Contains(s.outer, donor) {
		return "", false
	}
	switch rng.IntN(4) {
	case 0:
		at := rng.IntN(len(s.blk.Stmts) + 1)
		s.blk.Stmts = insertStmt(s.blk.Stmts, at, emiIf("emi_false", donor, nil))
	case 1:
		if len(s.blk.Stmts) == 0 {
			return "", false
		}
		at := rng.IntN(len(s.blk.Stmts))
		if !emiWrappable(s.blk.Stmts[at]) {
			return "", false
		}
		var els ast.Stmt
		if rng.IntN(2) == 0 {
			els = donor
		}
		s.blk.Stmts[at] = emiIf("emi_true", s.blk.Stmts[at], els)
	case 2:
		if len(s.blk.Stmts) == 0 {
			return "", false
		}
		at := rng.IntN(len(s.blk.Stmts))
		if !emiWrappable(s.blk.Stmts[at]) || emiJumps(s.blk.Stmts[at]) {
			return "", false
		}
		s.blk.Stmts[at] = &ast.Loop{Body: &ast.Block{Stmts: []ast.Stmt{s.blk.Stmts[at], &ast.Break{}}}}
	default:
		if !s.loop {
			return "", false
		}
		var jump ast.Stmt = &ast.Break{}
		if rng.IntN(2) == 0 {
			jump = &ast.Continue{}
		}
		at := rng.IntN(len(s.blk.Stmts) + 1)
		s.blk.Stmts = insertStmt(s.blk.Stmts, at, emiIf("emi_false", jump, nil))
	}
	return printer.Format(prog), true
}

func (w *emiWalk) stmt(s ast.Stmt, loop bool) {
	switch s := s.(type) {
	case *ast.Block:
		if s.Sugar != nil {
			w.stmt(s.Sugar.Body, true)
			return
		}
		w.sites = append(w.sites, emiSite{blk: s, fn: w.fn, loop: loop, outer: slices.Clone(w.outer)})
		for _, st := range s.Stmts {
			if _, ok := st.(*ast.Block); !ok {
				w.donors[w.fn] = append(w.donors[w.fn], st)
			}
			w.outer = append(w.outer, st)
			w.stmt(st, loop)
			w.outer = w.outer[:len(w.outer)-1]
		}
	case *ast.If:
		// An assert is an If whose Then block the printer reads back as one call.
		if s.IsAssert {
			return
		}
		w.stmt(s.Then, loop)
		if s.Else != nil {
			w.stmt(s.Else, loop)
		}
	case *ast.While:
		w.stmt(s.Body, true)
	case *ast.Loop:
		w.stmt(s.Body, true)
	case *ast.For:
		w.stmt(s.Body, true)
	case *ast.ForEach:
		w.stmt(s.Body, true)
	case *ast.Match:
		// The arms of a let-else are the parser's, not blocks of the source.
		if s.Origin == ast.OriginLetElse {
			return
		}
		for _, a := range s.Arms {
			w.stmt(a.Body, loop)
		}
	}
}

// emiWrappable reports whether a statement means the same inside a block of
// its own. A declaration does not, because its name goes out of scope at the
// end of the block, and neither does a defer, which runs at that end.
func emiWrappable(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.Var, *ast.Destructure, *ast.Defer:
		return false
	case *ast.Match:
		return s.Origin != ast.OriginLetElse
	}
	return true
}

// emiJumps reports whether a statement holds a break or a continue, which a
// loop around it would take for its own.
func emiJumps(s ast.Stmt) bool {
	found := false
	ast.Walk(s, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.Break, *ast.Continue:
			found = true
		}
		return !found
	})
	return found
}

func emiIf(guard string, then, els ast.Stmt) *ast.If {
	cond := &ast.Call{Callee: &ast.Ident{Name: guard}}
	out := &ast.If{Cond: cond, Then: &ast.Block{Stmts: []ast.Stmt{then}}}
	if els != nil {
		out.Else = &ast.Block{Stmts: []ast.Stmt{els}}
	}
	return out
}

func insertStmt(stmts []ast.Stmt, at int, s ast.Stmt) []ast.Stmt {
	out := append([]ast.Stmt{}, stmts[:at]...)
	out = append(out, s)
	return append(out, stmts[at:]...)
}
