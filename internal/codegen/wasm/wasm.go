// Package wasm emits WebAssembly text format (WAT) for a checked
// Program. Both this backend and the ARM32 emitter consume the same
// lowered ir.Program and share the optimisation pipeline (Inline,
// FuseTee, FlattenBranches, plus the PropagateCopies / ConstPropagate
// / Fold / ReduceStrength fixed-point cleanup) — new language
// features land once at the IR layer and both backends pick them up.
//
// Run the output with `wasmtime run --invoke main prog.wat` or
// convert to binary first with `wat2wasm prog.wat`.
package wasm

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
)

// Emit returns the WAT module text for prog.
//
// Programs that use strings, `print` or `putchar` cause the emitter to
// add a memory section, a WASI `fd_write` import and a small pair of
// runtime helpers ($putchar / $print). Programs that take functions as
// values cause it to emit a `funcref` table plus type declarations for
// each indirect-call signature. Modules that touch none of those stay
// free of the extra structure so they can be loaded under minimal
// hosts.
func Emit(prog *ast.Program, info *checker.Info) (string, error) {
	// ir.Lower runs closure conversion as a precondition (hoisting
	// nested functions, rewriting captures), then produces an
	// ir.Program. ir.Fold runs constant folding on the lowered ops
	// — picking up the post-lowering shapes the AST optimiser can't
	// see (collapsed ternaries / short-circuits, etc.). EmitFromIR
	// turns the folded program into WAT, reusing the module-level
	// scaffolding (runtime helpers, function table, closure cells,
	// data segments, exports) defined alongside it.
	ip, err := ir.Lower(prog, info)
	if err != nil {
		return "", err
	}
	ir.Inline(ip)
	ir.FuseTee(ip)
	ir.FlattenBranches(ip)
	// PropagateCopies + ConstPropagate + Fold + ReduceStrength
	// expose new opportunities for each other (a const propagated
	// into an arithmetic expression folds; the fold makes a tee
	// dead; dropping the tee makes constants adjacent for further
	// folding). Run them to a fixed point so the cascade settles.
	ir.OptimizeCleanup(ip)
	ir.EliminateDeadCode(ip)
	return EmitFromIR(prog, info, ip)
}

type generator struct {
	out     strings.Builder
	info    *checker.Info
	indent  int
	current *ast.FuncDecl
	// origTopLevelCount records how many functions the source had
	// before closure conversion appended hoisted ones. The first
	// origTopLevelCount entries get static closure cells (env=0)
	// because they're the only ones a `var f = name` reference can
	// reach by name.
	origTopLevelCount int

	// Runtime / strings / arrays.
	needsRuntime  bool
	needsArrays   bool
	needsStrEq       bool // any String == String / != comparison
	needsStrConcat   bool // any String + String concatenation
	needsStructs     bool
	needsBoundsCheck bool // any array or string Index expression appears
	needsClosures    bool // any FuncDecl was hoisted by closure conversion
	needsArgs        bool // any `args()` call appears — pulls in WASI args_*
	needsReadLine    bool // any `read_line()` call appears — pulls in WASI fd_read + helper + scratch slots
	needsEnv         bool // any `env(name)` call appears — pulls in WASI environ_* + helper + cache slots
	needsExit        bool // any `exit(code)` call appears — pulls in WASI proc_exit
	needsFileIO      bool // any `read_file` / `write_file` call — pulls in WASI path_open / fd_read / fd_close
	stringPool    map[string]int // value → pointer in linear memory
	stringEntries []stringEntry  // emission order (data segments)
	stringOffset  int            // next free byte for a string entry
	closuresBase  int            // start of the per-function closure-cell region

	// Function table / indirect calls. needsFuncTable is set if any
	// top-level function name appears in non-callee position (taken
	// as a value) OR if any call goes through a local — both need a
	// `funcref` table populated in declaration order. indirectSigs
	// lists each unique signature used by call_indirect, in the
	// order we first saw it; sigIndex maps a signature key to its
	// position in indirectSigs. funcIndex maps each top-level
	// function name to its table-element index.
	needsFuncTable bool
	funcIndex      map[string]int
	indirectSigs   []*ast.FuncType
	sigIndex       map[string]int
	// inTable[name] is true if function `name` needs to live in the
	// funcref table. Hoisted closure functions are always in the
	// table; top-level user functions only when referenced as a
	// value somewhere. Functions outside the table skip the
	// trailing __env parameter, so wasmtime's `--invoke main` keeps
	// working.
	inTable map[string]bool
	// tableIndex[name] gives the position of `name` in the funcref
	// table, populated lazily once the scan phase has run. Closure
	// cells use the same indices (cell i = (tableIndex i, env=0)).
	tableIndex   map[string]int
	tableEntries []string

	// funcDecls maps each (post-closure-conversion) function's name to
	// its AST FuncDecl. The IR-driven emitter uses it to look up the
	// hoisted closure target's Captures list at OpMakeClosure time —
	// per-capture types decide between i32.store and f32.store when
	// packing the env block.
	funcDecls map[string]*ast.FuncDecl
}

type stringEntry struct {
	offset int    // address of the 4-byte length prefix
	text   string
}

func (g *generator) line(s string) {
	g.out.WriteString(strings.Repeat("  ", g.indent))
	g.out.WriteString(s)
	g.out.WriteByte('\n')
}
func (g *generator) linef(format string, args ...any) {
	g.line(fmt.Sprintf(format, args...))
}

// envParamPresent reports whether fn already carries the synthetic
// `__env` parameter that closure conversion appends to hoisted local
// functions. Top-level functions don't carry it natively; we add the
// param at emit time when needsFuncTable is on.
func envParamPresent(fn *ast.FuncDecl) bool {
	if len(fn.Params) == 0 {
		return false
	}
	last := fn.Params[len(fn.Params)-1]
	return last.Name == "__env"
}

// scanEnumUses returns true if the program actually constructs or
// matches an enum. The auto-injected Option / Result decls show
// up in `prog.Enums` on every program, so a `len(prog.Enums) > 0`
// check would over-trigger the bump allocator on programs that
// never use enums. We look at *Match statements (any match
// implies a scrutinee that came from somewhere — usually a
// variant call earlier) and at calls whose callee name matches a
// variant in any registered enum.
func scanEnumUses(prog *ast.Program) bool {
	variants := map[string]bool{}
	for _, ed := range prog.Enums {
		for _, v := range ed.Variants {
			variants[v.Name] = true
		}
	}
	found := false
	var walk func(any)
	walk = func(n any) {
		if found {
			return
		}
		switch x := n.(type) {
		case *ast.Match:
			found = true
		case *ast.Call:
			if id, ok := x.Callee.(*ast.Ident); ok && variants[id.Name] {
				found = true
				return
			}
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.Ident:
			if variants[x.Name] {
				found = true
			}
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.While:
			walk(x.Cond)
			walk(x.Body)
		case *ast.For:
			if x.Init != nil {
				walk(x.Init)
			}
			walk(x.Cond)
			if x.Step != nil {
				walk(x.Step)
			}
			walk(x.Body)
		case *ast.Switch:
			walk(x.Tag)
			for _, k := range x.Cases {
				for _, v := range k.Values {
					walk(v)
				}
				walk(k.Body)
			}
			if x.Default != nil {
				walk(x.Default)
			}
		case *ast.Return:
			if x.Value != nil {
				walk(x.Value)
			}
		case *ast.Var:
			walk(x.Init)
		case *ast.ExprStmt:
			walk(x.Expr)
		case *ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ast.Unary:
			walk(x.Operand)
		case *ast.Index:
			walk(x.Array)
			walk(x.Idx)
		case *ast.ArrayLit:
			for _, e := range x.Elems {
				walk(e)
			}
		case *ast.Assign:
			walk(x.Target)
			walk(x.Value)
		case *ast.Ternary:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *ast.StructLit:
			for _, f := range x.Fields {
				walk(f.Value)
			}
		case *ast.FieldAccess:
			walk(x.Target)
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
	return found
}

// scanForIOBuiltins records which I/O builtins the program calls so
// the preamble can pull in only the WASI imports + helpers it
// actually needs. Unlike scanForArrayUses, this walker does NOT
// short-circuit — every call site is checked, because the per-
// builtin flags are independent.
func (g *generator) scanForIOBuiltins(prog *ast.Program) {
	var walk func(any)
	walk = func(n any) {
		switch x := n.(type) {
		case *ast.Call:
			if id, ok := x.Callee.(*ast.Ident); ok {
				switch id.Name {
				case "args":
					g.needsArgs = true
				case "read_line":
					g.needsReadLine = true
				case "env":
					g.needsEnv = true
				case "exit":
					g.needsExit = true
				case "read_file", "write_file":
					g.needsFileIO = true
				}
			}
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.While:
			walk(x.Cond)
			walk(x.Body)
		case *ast.For:
			if x.Init != nil {
				walk(x.Init)
			}
			walk(x.Cond)
			if x.Step != nil {
				walk(x.Step)
			}
			walk(x.Body)
		case *ast.Return:
			if x.Value != nil {
				walk(x.Value)
			}
		case *ast.Var:
			walk(x.Init)
		case *ast.ExprStmt:
			walk(x.Expr)
		case *ast.Switch:
			walk(x.Tag)
			for _, k := range x.Cases {
				for _, v := range k.Values {
					walk(v)
				}
				walk(k.Body)
			}
			if x.Default != nil {
				walk(x.Default)
			}
		case *ast.Match:
			walk(x.Tag)
			for _, arm := range x.Arms {
				walk(arm.Body)
			}
		case *ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ast.Unary:
			walk(x.Operand)
		case *ast.Index:
			walk(x.Array)
			walk(x.Idx)
		case *ast.ArrayLit:
			for _, e := range x.Elems {
				walk(e)
			}
		case *ast.Assign:
			walk(x.Target)
			walk(x.Value)
		case *ast.Ternary:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *ast.StructLit:
			for _, f := range x.Fields {
				walk(f.Value)
			}
		case *ast.FieldAccess:
			walk(x.Target)
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
}

// scanForArrayUses pre-walks the program and sets needsArrays if any
// ArrayLit, Index, or Index-target Assign appears. Arrays imply the
// runtime preamble (memory + bump allocator).
func (g *generator) scanForArrayUses(prog *ast.Program) {
	var walk func(any)
	walk = func(n any) {
		if g.needsArrays {
			return
		}
		switch x := n.(type) {
		case *ast.ArrayLit, *ast.Index:
			g.needsArrays = true
			g.needsRuntime = true
		case *ast.Assign:
			if _, isIdx := x.Target.(*ast.Index); isIdx {
				g.needsArrays = true
				g.needsRuntime = true
			}
			walk(x.Target)
			walk(x.Value)
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.While:
			walk(x.Cond)
			walk(x.Body)
		case *ast.For:
			if x.Init != nil {
				walk(x.Init)
			}
			walk(x.Cond)
			if x.Step != nil {
				walk(x.Step)
			}
			walk(x.Body)
		case *ast.Return:
			if x.Value != nil {
				walk(x.Value)
			}
		case *ast.Var:
			walk(x.Init)
		case *ast.ExprStmt:
			walk(x.Expr)
		case *ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ast.Unary:
			walk(x.Operand)
		case *ast.Call:
			// `args()` / `read_line()` / `env()` build length-
			// prefixed strings at runtime, so they need the array
			// preamble (bump allocator). `exit()` is detected in
			// scanForIOBuiltins, since this scan short-circuits as
			// soon as needsArrays is set.
			if id, ok := x.Callee.(*ast.Ident); ok {
				switch id.Name {
				case "args", "read_line", "env", "read_file", "write_file":
					g.needsArrays = true
					g.needsRuntime = true
				}
			}
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.Switch:
			walk(x.Tag)
			for _, k := range x.Cases {
				for _, v := range k.Values {
					walk(v)
				}
				walk(k.Body)
			}
			if x.Default != nil {
				walk(x.Default)
			}
		case *ast.Match:
			walk(x.Tag)
			for _, arm := range x.Arms {
				walk(arm.Body)
			}
		case *ast.Ternary:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
}
// scanForStructUses pre-walks the program and sets needsStructs if any
// StructLit appears. Structs share the bump allocator with arrays, so
// the runtime preamble lights up either way.
func (g *generator) scanForStructUses(prog *ast.Program) {
	var walk func(any)
	walk = func(n any) {
		if g.needsStructs {
			return
		}
		switch x := n.(type) {
		case *ast.StructLit:
			g.needsStructs = true
			g.needsRuntime = true
		case *ast.FieldAccess:
			walk(x.Target)
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.While:
			walk(x.Cond)
			walk(x.Body)
		case *ast.For:
			if x.Init != nil {
				walk(x.Init)
			}
			walk(x.Cond)
			if x.Step != nil {
				walk(x.Step)
			}
			walk(x.Body)
		case *ast.Return:
			if x.Value != nil {
				walk(x.Value)
			}
		case *ast.Var:
			walk(x.Init)
		case *ast.ExprStmt:
			walk(x.Expr)
		case *ast.Switch:
			walk(x.Tag)
			for _, k := range x.Cases {
				for _, v := range k.Values {
					walk(v)
				}
				walk(k.Body)
			}
			if x.Default != nil {
				walk(x.Default)
			}
		case *ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ast.Unary:
			walk(x.Operand)
		case *ast.Assign:
			walk(x.Target)
			walk(x.Value)
		case *ast.Call:
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.Index:
			walk(x.Array)
			walk(x.Idx)
		case *ast.ArrayLit:
			for _, e := range x.Elems {
				walk(e)
			}
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
}

// scanForIndirectCalls walks every function body and records the
// signatures that will need `(type $tN ...)` declarations for use
// with call_indirect, plus whether the program touches the function
// table at all.
//
// The two triggers:
//
//   - an Ident referring to a top-level function appears in
//     non-callee position (taken as a value, e.g. `var f = add`); it
//     needs to materialise as the table index, which means the table
//     must exist;
//   - a Call whose callee resolves to a local of *FuncType (rather
//     than to a top-level function name) lowers to call_indirect and
//     needs the corresponding `(type $tN ...)` declaration.
func (g *generator) scanForIndirectCalls(prog *ast.Program) {
	for _, fn := range prog.Funcs {
		g.current = fn
		g.scanIndirectStmt(fn.Body)
	}
	g.current = nil
}

func (g *generator) scanIndirectStmt(s ast.Stmt) {
	switch x := s.(type) {
	case *ast.Block:
		for _, ss := range x.Stmts {
			g.scanIndirectStmt(ss)
		}
	case *ast.If:
		g.scanIndirectExpr(x.Cond, false)
		g.scanIndirectStmt(x.Then)
		if x.Else != nil {
			g.scanIndirectStmt(x.Else)
		}
	case *ast.While:
		g.scanIndirectExpr(x.Cond, false)
		g.scanIndirectStmt(x.Body)
	case *ast.For:
		if x.Init != nil {
			g.scanIndirectStmt(x.Init)
		}
		g.scanIndirectExpr(x.Cond, false)
		if x.Step != nil {
			g.scanIndirectStmt(x.Step)
		}
		g.scanIndirectStmt(x.Body)
	case *ast.Return:
		if x.Value != nil {
			g.scanIndirectExpr(x.Value, false)
		}
	case *ast.Var:
		g.scanIndirectExpr(x.Init, false)
	case *ast.ExprStmt:
		g.scanIndirectExpr(x.Expr, false)
	case *ast.Switch:
		g.scanIndirectExpr(x.Tag, false)
		for _, k := range x.Cases {
			for _, v := range k.Values {
				g.scanIndirectExpr(v, false)
			}
			g.scanIndirectStmt(k.Body)
		}
		if x.Default != nil {
			g.scanIndirectStmt(x.Default)
		}
	case *ast.Match:
		g.scanIndirectExpr(x.Tag, false)
		for _, arm := range x.Arms {
			g.scanIndirectStmt(arm.Body)
		}
	}
}

// scanIndirectExpr walks an expression tree. inCalleePos is true when
// the expression sits directly in `Call.Callee` — that single position
// is where a top-level function name doesn't trigger the table.
func (g *generator) scanIndirectExpr(e ast.Expr, inCalleePos bool) {
	switch x := e.(type) {
	case *ast.Ident:
		if !inCalleePos {
			if _, ok := g.funcIndex[x.Name]; ok {
				g.needsFuncTable = true
				g.inTable[x.Name] = true
			}
		}
	case *ast.Binary:
		g.scanIndirectExpr(x.Left, false)
		g.scanIndirectExpr(x.Right, false)
	case *ast.Unary:
		g.scanIndirectExpr(x.Operand, false)
	case *ast.Index:
		g.scanIndirectExpr(x.Array, false)
		g.scanIndirectExpr(x.Idx, false)
	case *ast.ArrayLit:
		for _, el := range x.Elems {
			g.scanIndirectExpr(el, false)
		}
	case *ast.Assign:
		g.scanIndirectExpr(x.Target, false)
		g.scanIndirectExpr(x.Value, false)
	case *ast.Ternary:
		g.scanIndirectExpr(x.Cond, false)
		g.scanIndirectExpr(x.Then, false)
		g.scanIndirectExpr(x.Else, false)
	case *ast.Call:
		// Walk args first.
		for _, a := range x.Args {
			g.scanIndirectExpr(a, false)
		}
		// Then decide whether this is direct or indirect.
		if id, ok := x.Callee.(*ast.Ident); ok {
			if _, isTopLevel := g.funcIndex[id.Name]; isTopLevel {
				// direct call — callee Ident is in callee position
				g.scanIndirectExpr(x.Callee, true)
				return
			}
			// Local of function type → indirect call.
			ft := g.localFuncType(g.current, id.Name)
			if ft != nil {
				g.needsFuncTable = true
				g.recordSig(ft)
			}
			g.scanIndirectExpr(x.Callee, true)
		} else {
			g.scanIndirectExpr(x.Callee, false)
		}
	}
}

// localFuncType returns the function type of a local identifier
// (parameter or `var`) in fn, or nil if the name doesn't resolve to a
// function-typed local in that scope.
func (g *generator) localFuncType(fn *ast.FuncDecl, name string) *ast.FuncType {
	if fn != nil {
		for _, p := range fn.Params {
			if p.Name == name {
				if ft, ok := p.Type.(*ast.FuncType); ok {
					return ft
				}
				return nil
			}
		}
	}
	if vars, ok := g.info.Locals[fn]; ok {
		for _, v := range vars {
			if v.Name == name {
				if ft, ok := v.Type.(*ast.FuncType); ok {
					return ft
				}
				return nil
			}
		}
	}
	return nil
}

// recordSig assigns ft a stable index in indirectSigs, deduplicating
// by structural signature key.
func (g *generator) recordSig(ft *ast.FuncType) int {
	key := sigKey(ft)
	if idx, ok := g.sigIndex[key]; ok {
		return idx
	}
	idx := len(g.indirectSigs)
	g.sigIndex[key] = idx
	g.indirectSigs = append(g.indirectSigs, ft)
	return idx
}

func sigKey(ft *ast.FuncType) string {
	var b strings.Builder
	b.WriteByte('(')
	for i, p := range ft.Params {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(p.String())
	}
	b.WriteString(")->")
	b.WriteString(ft.Result.String())
	return b.String()
}

// watFuncType renders a *FuncType as the WAT `(func ...)` body used
// in `(type $tN (func ...))` declarations. Under the closure ABI
// every table entry carries a trailing `__env i32` parameter, so the
// signature has one more param than the user-visible type. Legacy
// programs (no nested functions) skip the env param and pass bare
// table indices.
func (g *generator) watFuncType(ft *ast.FuncType) string {
	var b strings.Builder
	b.WriteString("(func")
	for _, p := range ft.Params {
		t, _ := watType(p)
		b.WriteString(" (param ")
		b.WriteString(t)
		b.WriteByte(')')
	}
	if g.needsClosures {
		b.WriteString(" (param i32)") // env pointer
	}
	if !ast.Equal(ft.Result, ast.VoidType{}) {
		t, _ := watType(ft.Result)
		b.WriteString(" (result ")
		b.WriteString(t)
		b.WriteByte(')')
	}
	b.WriteByte(')')
	return b.String()
}

// scanForStringEq pre-walks the program and sets needsStrEq if any
// `==` or `!=` between strings appears, so emitRuntimePreamble knows
// to include the $__str_eq helper. The helper reads from linear
// memory, so it implies needsRuntime as well.
func (g *generator) scanForStringEq(prog *ast.Program) {
	var walk func(any)
	walk = func(n any) {
		if g.needsStrEq {
			return
		}
		switch x := n.(type) {
		case *ast.Binary:
			if x.IsStringCmp {
				g.needsStrEq = true
				g.needsRuntime = true
				return
			}
			walk(x.Left)
			walk(x.Right)
		case *ast.Unary:
			walk(x.Operand)
		case *ast.Call:
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.Index:
			walk(x.Array)
			walk(x.Idx)
		case *ast.ArrayLit:
			for _, e := range x.Elems {
				walk(e)
			}
		case *ast.Assign:
			walk(x.Target)
			walk(x.Value)
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.While:
			walk(x.Cond)
			walk(x.Body)
		case *ast.For:
			if x.Init != nil {
				walk(x.Init)
			}
			walk(x.Cond)
			if x.Step != nil {
				walk(x.Step)
			}
			walk(x.Body)
		case *ast.Return:
			if x.Value != nil {
				walk(x.Value)
			}
		case *ast.Var:
			walk(x.Init)
		case *ast.ExprStmt:
			walk(x.Expr)
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
}

// scanForStringConcat pre-walks the program and sets needsStrConcat
// if any `+` between strings appears. The helper allocates a fresh
// buffer via `__lang_alloc`, so it pulls in needsArrays as well.
func (g *generator) scanForStringConcat(prog *ast.Program) {
	var walk func(any)
	walk = func(n any) {
		if g.needsStrConcat {
			return
		}
		switch x := n.(type) {
		case *ast.Binary:
			if x.IsStringConcat {
				g.needsStrConcat = true
				g.needsRuntime = true
				g.needsArrays = true
				return
			}
			walk(x.Left)
			walk(x.Right)
		case *ast.Unary:
			walk(x.Operand)
		case *ast.Call:
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.Index:
			walk(x.Array)
			walk(x.Idx)
		case *ast.ArrayLit:
			for _, e := range x.Elems {
				walk(e)
			}
		case *ast.Assign:
			walk(x.Target)
			walk(x.Value)
		case *ast.Ternary:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *ast.StructLit:
			for _, f := range x.Fields {
				walk(f.Value)
			}
		case *ast.FieldAccess:
			walk(x.Target)
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.While:
			walk(x.Cond)
			walk(x.Body)
		case *ast.For:
			if x.Init != nil {
				walk(x.Init)
			}
			walk(x.Cond)
			if x.Step != nil {
				walk(x.Step)
			}
			walk(x.Body)
		case *ast.Switch:
			walk(x.Tag)
			for _, k := range x.Cases {
				for _, v := range k.Values {
					walk(v)
				}
				walk(k.Body)
			}
			if x.Default != nil {
				walk(x.Default)
			}
		case *ast.Match:
			walk(x.Tag)
			for _, arm := range x.Arms {
				walk(arm.Body)
			}
		case *ast.Return:
			if x.Value != nil {
				walk(x.Value)
			}
		case *ast.Var:
			walk(x.Init)
		case *ast.ExprStmt:
			walk(x.Expr)
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
}

// scanForBoundsCheck pre-walks the program and sets needsBoundsCheck
// if any Index expression appears. The helpers it triggers
// ($__arr_idx / $__str_idx) read the length prefix from linear
// memory, so it implies needsRuntime.
func (g *generator) scanForBoundsCheck(prog *ast.Program) {
	var walk func(any)
	walk = func(n any) {
		if g.needsBoundsCheck {
			return
		}
		switch x := n.(type) {
		case *ast.Index:
			g.needsBoundsCheck = true
			g.needsRuntime = true
			return
		case *ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ast.Unary:
			walk(x.Operand)
		case *ast.Call:
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.ArrayLit:
			for _, e := range x.Elems {
				walk(e)
			}
		case *ast.Assign:
			walk(x.Target)
			walk(x.Value)
		case *ast.Ternary:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *ast.StructLit:
			for _, f := range x.Fields {
				walk(f.Value)
			}
		case *ast.FieldAccess:
			walk(x.Target)
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.While:
			walk(x.Cond)
			walk(x.Body)
		case *ast.For:
			if x.Init != nil {
				walk(x.Init)
			}
			walk(x.Cond)
			if x.Step != nil {
				walk(x.Step)
			}
			walk(x.Body)
		case *ast.Switch:
			walk(x.Tag)
			for _, k := range x.Cases {
				for _, v := range k.Values {
					walk(v)
				}
				walk(k.Body)
			}
			if x.Default != nil {
				walk(x.Default)
			}
		case *ast.Match:
			walk(x.Tag)
			for _, arm := range x.Arms {
				walk(arm.Body)
			}
		case *ast.Return:
			if x.Value != nil {
				walk(x.Value)
			}
		case *ast.Var:
			walk(x.Init)
		case *ast.ExprStmt:
			walk(x.Expr)
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
}

// scanForRuntimeUses pre-walks the program and sets needsRuntime if
// any string literal, `print` call or `putchar` call appears.
func (g *generator) scanForRuntimeUses(prog *ast.Program) {
	var walk func(any)
	walk = func(n any) {
		if g.needsRuntime {
			return
		}
		switch x := n.(type) {
		case *ast.StringLit:
			g.needsRuntime = true
		case *ast.Call:
			if id, ok := x.Callee.(*ast.Ident); ok {
				switch id.Name {
				case "print", "write", "eprint", "putchar", "args", "read_line", "env", "exit", "read_file", "write_file":
					g.needsRuntime = true
					return
				}
			}
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.While:
			walk(x.Cond)
			walk(x.Body)
		case *ast.For:
			if x.Init != nil {
				walk(x.Init)
			}
			walk(x.Cond)
			if x.Step != nil {
				walk(x.Step)
			}
			walk(x.Body)
		case *ast.Return:
			if x.Value != nil {
				walk(x.Value)
			}
		case *ast.Var:
			walk(x.Init)
		case *ast.ExprStmt:
			walk(x.Expr)
		case *ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ast.Unary:
			walk(x.Operand)
		case *ast.Assign:
			walk(x.Target)
			walk(x.Value)
		case *ast.Index:
			walk(x.Array)
			walk(x.Idx)
		case *ast.ArrayLit:
			for _, e := range x.Elems {
				walk(e)
			}
		case *ast.Switch:
			walk(x.Tag)
			for _, k := range x.Cases {
				for _, v := range k.Values {
					walk(v)
				}
				walk(k.Body)
			}
			if x.Default != nil {
				walk(x.Default)
			}
		case *ast.Match:
			walk(x.Tag)
			for _, arm := range x.Arms {
				walk(arm.Body)
			}
		case *ast.Ternary:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
}

// internString assigns an address to s the first time we see it and
// reuses it on repeats. The returned pointer skips the 4-byte length
// prefix, so callers can do `i32.load (sub ptr 4)` to recover length.
func (g *generator) internString(s string) int {
	if ptr, ok := g.stringPool[s]; ok {
		return ptr
	}
	g.needsRuntime = true
	off := g.stringOffset
	g.stringEntries = append(g.stringEntries, stringEntry{offset: off, text: s})
	ptr := off + 4
	g.stringPool[s] = ptr
	g.stringOffset = off + 4 + len(s)
	return ptr
}

// emitRuntimePreamble emits the WASI import, the linear memory, and
// two helper functions ($putchar, $print). They share a small block
// of static memory — see the data segments at the end of the module.
//
// Memory layout for the runtime constants (offsets in bytes):
//
//	 0..3   putchar i32 buffer (only the low byte is used)
//	 4..11  putchar iovec  { ptr=0, len=1 }   pre-initialised
//	12..15  putchar nwritten
//	16..23  print iovec[0] { ptr=string, len=L }  set per call
//	24..31  print iovec[1] { ptr=32, len=1 }      pre-initialised
//	32      newline byte 0x0A                    pre-initialised
//	36..39  print nwritten
//	40..43  bump-allocator pointer (when needsArrays)
//	44..47  args() cache pointer (0 = not yet built; non-zero = ptr)
//	48..51  args_sizes_get out: argc
//	52..55  args_sizes_get out: argv buffer size
//	56..63  read_line iovec { ptr=68, len=1 }    pre-initialised
//	64..67  read_line nread out
//	68..71  read_line single-byte buffer (only byte 68 used)
//	72..75  env init flag (0 = uninitialised, 1 = environ_get done)
//	76..79  env count (number of "KEY=VALUE" entries after init)
//	80..83  env_ptrs heap pointer (filled by environ_get)
//	84..87  environ_sizes_get out: count
//	88..91  environ_sizes_get out: bufsize
//	96+     string data, each entry: 4-byte length prefix then bytes
func (g *generator) emitRuntimePreamble() {
	g.line(`(import "wasi_snapshot_preview1" "fd_write" (func $__wasi_fd_write (param i32 i32 i32 i32) (result i32)))`)
	if g.needsArgs {
		g.line(`(import "wasi_snapshot_preview1" "args_sizes_get" (func $__wasi_args_sizes_get (param i32 i32) (result i32)))`)
		g.line(`(import "wasi_snapshot_preview1" "args_get" (func $__wasi_args_get (param i32 i32) (result i32)))`)
	}
	if g.needsReadLine {
		g.line(`(import "wasi_snapshot_preview1" "fd_read" (func $__wasi_fd_read (param i32 i32 i32 i32) (result i32)))`)
	}
	if g.needsEnv {
		g.line(`(import "wasi_snapshot_preview1" "environ_sizes_get" (func $__wasi_environ_sizes_get (param i32 i32) (result i32)))`)
		g.line(`(import "wasi_snapshot_preview1" "environ_get" (func $__wasi_environ_get (param i32 i32) (result i32)))`)
	}
	if g.needsExit {
		g.line(`(import "wasi_snapshot_preview1" "proc_exit" (func $__wasi_proc_exit (param i32)))`)
	}
	if g.needsFileIO {
		// path_open / fd_read / fd_close — fd_write is already
		// imported above for `print`. fd_read shares with the
		// stdin reader's import; if both flags are on we still
		// only emit it once.
		g.line(`(import "wasi_snapshot_preview1" "path_open" (func $__wasi_path_open (param i32 i32 i32 i32 i32 i64 i64 i32 i32) (result i32)))`)
		if !g.needsReadLine {
			g.line(`(import "wasi_snapshot_preview1" "fd_read" (func $__wasi_fd_read (param i32 i32 i32 i32) (result i32)))`)
		}
		g.line(`(import "wasi_snapshot_preview1" "fd_close" (func $__wasi_fd_close (param i32) (result i32)))`)
	}
	g.line(`(memory $mem 1)`)

	if g.needsArrays || g.needsStructs {
		// $__lang_alloc bumps the allocator pointer at memory[40] and
		// returns the address that was there before the bump. There's
		// no free — arrays in lang are immutable but not GC'd.
		g.line(`(func $__lang_alloc (param $size i32) (result i32)`)
		g.indent++
		g.line(`(local $ptr i32)`)
		// ptr = mem[40]
		g.line(`i32.const 40`)
		g.line(`i32.load`)
		g.line(`local.set $ptr`)
		// mem[40] = ptr + size
		g.line(`i32.const 40`)
		g.line(`local.get $ptr`)
		g.line(`local.get $size`)
		g.line(`i32.add`)
		g.line(`i32.store`)
		g.line(`local.get $ptr`)
		g.indent--
		g.line(`)`)
	}

	if g.needsStrEq {
		// $__str_eq compares two length-prefixed strings byte-by-byte.
		// Returns 1 if equal, 0 otherwise. Identical pointers short-circuit
		// to true; lengths are read from `ptr - 4` (4-byte little-endian
		// prefix) before the byte loop.
		g.line(`(func $__str_eq (param $a i32) (param $b i32) (result i32)`)
		g.indent++
		g.line(`(local $la i32) (local $lb i32) (local $i i32)`)
		g.line(`local.get $a`)
		g.line(`local.get $b`)
		g.line(`i32.eq`)
		g.line(`if (result i32)`)
		g.indent++
		g.line(`i32.const 1`)
		g.indent--
		g.line(`else`)
		g.indent++
		// la = mem[a-4]; lb = mem[b-4]
		g.line(`local.get $a`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.line(`local.set $la`)
		g.line(`local.get $b`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.line(`local.set $lb`)
		g.line(`local.get $la`)
		g.line(`local.get $lb`)
		g.line(`i32.ne`)
		g.line(`if (result i32)`)
		g.indent++
		g.line(`i32.const 0`)
		g.indent--
		g.line(`else`)
		g.indent++
		// for (i=0; i<la; i++) if (a[i] != b[i]) return 0
		g.line(`i32.const 0`)
		g.line(`local.set $i`)
		g.line(`block $end`)
		g.indent++
		g.line(`loop $loop`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`local.get $la`)
		g.line(`i32.eq`)
		g.line(`br_if $end`)
		g.line(`local.get $a`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`i32.load8_u`)
		g.line(`local.get $b`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`i32.load8_u`)
		g.line(`i32.ne`)
		g.line(`if`)
		g.indent++
		g.line(`i32.const 0`)
		g.line(`return`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $i`)
		g.line(`i32.const 1`)
		g.line(`i32.add`)
		g.line(`local.set $i`)
		g.line(`br $loop`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`end`)
		g.line(`i32.const 1`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`)`)
	}

	if g.needsStrConcat {
		// $__str_concat allocates a fresh length-prefixed buffer holding
		// the bytes of `a` followed by the bytes of `b`. Both inputs
		// point at the first byte of their content; their lengths live
		// at `ptr - 4`. The bump allocator is shared with arrays.
		g.line(`(func $__str_concat (param $a i32) (param $b i32) (result i32)`)
		g.indent++
		g.line(`(local $la i32) (local $lb i32) (local $base i32) (local $dst i32) (local $i i32)`)
		// la / lb
		g.line(`local.get $a`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.line(`local.set $la`)
		g.line(`local.get $b`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.line(`local.set $lb`)
		// base = __lang_alloc(la + lb + 4)
		g.line(`local.get $la`)
		g.line(`local.get $lb`)
		g.line(`i32.add`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`call $__lang_alloc`)
		g.line(`local.set $base`)
		// store length prefix at base
		g.line(`local.get $base`)
		g.line(`local.get $la`)
		g.line(`local.get $lb`)
		g.line(`i32.add`)
		g.line(`i32.store`)
		// dst = base + 4
		g.line(`local.get $base`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`local.set $dst`)
		// Copy a's bytes: for (i=0; i<la; i++) dst[i] = a[i]
		g.line(`i32.const 0`)
		g.line(`local.set $i`)
		g.line(`block $aend`)
		g.indent++
		g.line(`loop $aloop`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`local.get $la`)
		g.line(`i32.eq`)
		g.line(`br_if $aend`)
		g.line(`local.get $dst`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`local.get $a`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`i32.load8_u`)
		g.line(`i32.store8`)
		g.line(`local.get $i`)
		g.line(`i32.const 1`)
		g.line(`i32.add`)
		g.line(`local.set $i`)
		g.line(`br $aloop`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`end`)
		// Copy b's bytes: for (i=0; i<lb; i++) dst[la+i] = b[i]
		g.line(`i32.const 0`)
		g.line(`local.set $i`)
		g.line(`block $bend`)
		g.indent++
		g.line(`loop $bloop`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`local.get $lb`)
		g.line(`i32.eq`)
		g.line(`br_if $bend`)
		g.line(`local.get $dst`)
		g.line(`local.get $la`)
		g.line(`i32.add`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`local.get $b`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`i32.load8_u`)
		g.line(`i32.store8`)
		g.line(`local.get $i`)
		g.line(`i32.const 1`)
		g.line(`i32.add`)
		g.line(`local.set $i`)
		g.line(`br $bloop`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`end`)
		// Return the content pointer (base + 4).
		g.line(`local.get $dst`)
		g.indent--
		g.line(`)`)
	}

	if g.needsBoundsCheck {
		// $__arr_idx and $__str_idx return the byte address of element i
		// in a length-prefixed array / string, trapping if i is out of
		// range. The length lives at `base - 4` (4-byte little-endian
		// prefix). Stride differs (4 for arrays, 1 for strings) so we
		// emit two specialised helpers rather than threading a stride
		// argument.
		g.line(`(func $__arr_idx (param $base i32) (param $i i32) (result i32)`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`i32.const 0`)
		g.line(`i32.lt_s`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $i`)
		g.line(`local.get $base`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.line(`i32.ge_u`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $base`)
		g.line(`local.get $i`)
		g.line(`i32.const 4`)
		g.line(`i32.mul`)
		g.line(`i32.add`)
		g.indent--
		g.line(`)`)

		g.line(`(func $__str_idx (param $base i32) (param $i i32) (result i32)`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`i32.const 0`)
		g.line(`i32.lt_s`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $i`)
		g.line(`local.get $base`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.line(`i32.ge_u`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $base`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.indent--
		g.line(`)`)
	}

	// putchar(n)
	g.line(`(func $putchar (param $n i32)`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`local.get $n`)
	g.line(`i32.store8`)
	g.line(`i32.const 1`)  // fd = stdout
	g.line(`i32.const 4`)  // iovs = &iovec at offset 4
	g.line(`i32.const 1`)  // iovs_len = 1
	g.line(`i32.const 12`) // nwritten = &offset 12
	g.line(`call $__wasi_fd_write`)
	g.line(`drop`)
	g.indent--
	g.line(`)`)

	// print(s) — writes the string and a newline (matching the arm32
	// puts-based lowering). We split this into TWO single-iovec
	// fd_write calls because some wasmtime versions silently drop all
	// but the first iovec when iovs_len > 1.
	g.line(`(func $print (param $s i32)`)
	g.indent++
	g.emitFdWriteString(1, "$s")
	// Second call: write the newline. iovec at offset 24 is pre-init
	// to (ptr=32, len=1) by a data segment; memory[32] is '\n'.
	g.emitFdWriteNewline(1)
	g.indent--
	g.line(`)`)

	// write(s) — stdout without a trailing newline. Same shape as
	// $print's first half; users compose their own newlines / field
	// separators when they want.
	g.line(`(func $write (param $s i32)`)
	g.indent++
	g.emitFdWriteString(1, "$s")
	g.indent--
	g.line(`)`)

	// eprint(s) — `print` shape but routed to fd=2 (stderr). Two
	// fd_write calls for the same iovs_len=1 reason as $print.
	g.line(`(func $eprint (param $s i32)`)
	g.indent++
	g.emitFdWriteString(2, "$s")
	g.emitFdWriteNewline(2)
	g.indent--
	g.line(`)`)

	if g.needsArgs {
		g.emitArgsHelper()
	}
	if g.needsReadLine {
		g.emitReadLineHelper()
	}
	if g.needsEnv {
		g.emitEnvHelper()
	}
	if g.needsExit {
		g.emitExitHelper()
	}
	if g.needsFileIO {
		g.emitFileIOHelpers()
	}
}

// emitFdWriteString emits one fd_write call that writes a single
// length-prefixed string to `fd`. local is the wasm local holding
// the string's data pointer (e.g. "$s"); the string's length
// lives at `local - 4`. Reuses iovec[0] at offset 16.
func (g *generator) emitFdWriteString(fd int, local string) {
	g.line(`i32.const 16`)
	g.linef(`local.get %s`, local)
	g.line(`i32.store`)
	g.line(`i32.const 20`)
	g.linef(`local.get %s`, local)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`i32.store`)
	g.linef(`i32.const %d`, fd)
	g.line(`i32.const 16`) // iovs ptr
	g.line(`i32.const 1`)  // iovs_len = 1
	g.line(`i32.const 36`) // nwritten
	g.line(`call $__wasi_fd_write`)
	g.line(`drop`)
}

// emitFdWriteNewline emits one fd_write call that writes the
// pre-initialised newline iovec at offset 24 (memory[32]='\n')
// to `fd`. Used by `$print` and `$eprint` after their string
// write.
func (g *generator) emitFdWriteNewline(fd int) {
	g.linef(`i32.const %d`, fd)
	g.line(`i32.const 24`) // iovs ptr (newline iovec)
	g.line(`i32.const 1`)
	g.line(`i32.const 36`)
	g.line(`call $__wasi_fd_write`)
	g.line(`drop`)
}

// emitArgsHelper writes the lazy-initialising `$args` runtime
// function. The first call materialises a length-prefixed
// string[] from the WASI argv buffer and caches its pointer at
// memory offset 44; subsequent calls return the cached pointer
// without going back to the host.
//
// The materialised array layout is the standard one used elsewhere
// for `string[]`:
//
//	[ length prefix : i32 ][ s0_ptr : i32 ][ s1_ptr : i32 ] ...
//
// where each `sK_ptr` points to the bytes of a length-prefixed
// string allocated separately on the bump heap.
func (g *generator) emitArgsHelper() {
	g.line(`(func $args (result i32)`)
	g.indent++
	g.line(`(local $cached i32)`)
	g.line(`(local $argc i32)`)
	g.line(`(local $bufsize i32)`)
	g.line(`(local $argv_ptrs i32)`)
	g.line(`(local $argv_buf i32)`)
	g.line(`(local $result i32)`)
	g.line(`(local $i i32)`)
	g.line(`(local $cstr i32)`)
	g.line(`(local $end i32)`)
	g.line(`(local $strlen i32)`)
	g.line(`(local $sbase i32)`)
	g.line(`(local $j i32)`)

	// Fast path: cached.
	g.line(`i32.const 44`)
	g.line(`i32.load`)
	g.line(`local.tee $cached`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $cached`)
	g.indent--
	g.line(`else`)
	g.indent++

	// Slow path: ask the host how many args + how big a buffer.
	g.line(`i32.const 48`)
	g.line(`i32.const 52`)
	g.line(`call $__wasi_args_sizes_get`)
	g.line(`drop`)
	g.line(`i32.const 48`)
	g.line(`i32.load`)
	g.line(`local.set $argc`)
	g.line(`i32.const 52`)
	g.line(`i32.load`)
	g.line(`local.set $bufsize`)

	// Allocate scratch buffers for the host call. argv_ptrs gets
	// argc * 4 bytes; argv_buf gets bufsize bytes (which already
	// covers every NUL-terminated argv string back-to-back).
	g.line(`local.get $argc`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $argv_ptrs`)
	g.line(`local.get $bufsize`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $argv_buf`)
	g.line(`local.get $argv_ptrs`)
	g.line(`local.get $argv_buf`)
	g.line(`call $__wasi_args_get`)
	g.line(`drop`)

	// Allocate the result string[]: length prefix (4 bytes) +
	// argc * 4 entry pointers. $result lands on the entries (the
	// language's `string[]` convention is that the value is the
	// data pointer, with the length at value-4).
	g.line(`local.get $argc`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`local.get $argc`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $result`)

	// For each argv entry: walk the C string to find its length,
	// allocate a fresh length-prefixed buffer, copy the bytes, and
	// stash the resulting string pointer at result[i].
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $end_outer`)
	g.indent++
	g.line(`loop $outer`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $argc`)
	g.line(`i32.eq`)
	g.line(`br_if $end_outer`)

	// cstr = argv_ptrs[i] (each entry is an i32)
	g.line(`local.get $argv_ptrs`)
	g.line(`local.get $i`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $cstr`)

	// strlen: scan from cstr until a NUL byte.
	g.line(`local.get $cstr`)
	g.line(`local.set $end`)
	g.line(`block $end_strlen`)
	g.indent++
	g.line(`loop $strlen`)
	g.indent++
	g.line(`local.get $end`)
	g.line(`i32.load8_u`)
	g.line(`i32.eqz`)
	g.line(`br_if $end_strlen`)
	g.line(`local.get $end`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $end`)
	g.line(`br $strlen`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $end`)
	g.line(`local.get $cstr`)
	g.line(`i32.sub`)
	g.line(`local.set $strlen`)

	// Allocate strlen+4 bytes; write length prefix.
	g.line(`local.get $strlen`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $sbase`)
	g.line(`local.get $sbase`)
	g.line(`local.get $strlen`)
	g.line(`i32.store`)

	// Byte-copy cstr[0..strlen) into sbase+4.
	g.line(`i32.const 0`)
	g.line(`local.set $j`)
	g.line(`block $end_copy`)
	g.indent++
	g.line(`loop $copy`)
	g.indent++
	g.line(`local.get $j`)
	g.line(`local.get $strlen`)
	g.line(`i32.eq`)
	g.line(`br_if $end_copy`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`local.get $cstr`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.store8`)
	g.line(`local.get $j`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $j`)
	g.line(`br $copy`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// result[i] = sbase + 4 (the data pointer; length lives at -4)
	g.line(`local.get $result`)
	g.line(`local.get $i`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.store`)

	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $outer`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// Cache and return.
	g.line(`i32.const 44`)
	g.line(`local.get $result`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)
}

// emitReadLineHelper writes `$read_line`, a one-byte-at-a-time
// stdin reader. Each iteration calls fd_read on the iovec at
// offset 56 (pre-set to read into byte 68); the loop exits at
// EOF (nread==0) or when the read byte is `\n`.
//
// The result is an `Option[string]` heap object: `Some(line)`
// when at least one byte was read (the line preserves its
// trailing `\n`); `None` when the first read came back empty
// (EOF). Tag 0 is `Some`, tag 1 is `None` — the canonical order
// from the auto-injected Option enum, hardcoded here.
func (g *generator) emitReadLineHelper() {
	g.line(`(func $read_line (result i32)`)
	g.indent++
	g.line(`(local $start i32)`) // start of accumulated bytes on the heap
	g.line(`(local $cur i32)`)   // next byte to write into
	g.line(`(local $byte i32)`)
	g.line(`(local $sbase i32)`)
	g.line(`(local $sptr i32)`)
	g.line(`(local $len i32)`)
	g.line(`(local $i i32)`)

	// Allocate a 0-byte placeholder so $start anchors the heap
	// position; we'll keep alloc'ing one byte at a time.
	g.line(`i32.const 0`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $start`)
	g.line(`local.get $start`)
	g.line(`local.set $cur`)

	g.line(`block $end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	// fd_read(fd=0, iovs=56, iovs_len=1, nread=64)
	g.line(`i32.const 0`)
	g.line(`i32.const 56`)
	g.line(`i32.const 1`)
	g.line(`i32.const 64`)
	g.line(`call $__wasi_fd_read`)
	g.line(`drop`)
	// EOF when nread == 0
	g.line(`i32.const 64`)
	g.line(`i32.load`)
	g.line(`i32.eqz`)
	g.line(`br_if $end`)
	// Append the byte. Allocate one byte to advance the heap and
	// store the read byte into it.
	g.line(`i32.const 1`)
	g.line(`call $__lang_alloc`)
	g.line(`drop`)
	g.line(`i32.const 68`)
	g.line(`i32.load8_u`)
	g.line(`local.tee $byte`)
	g.line(`local.get $cur`)
	g.line(`local.get $byte`)
	g.line(`i32.store8`)
	// cur += 1; break if newline
	g.line(`local.get $cur`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $cur`)
	g.line(`local.get $byte`)
	g.line(`i32.const 10`)
	g.line(`i32.eq`)
	g.line(`br_if $end`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// len = cur - start
	g.line(`local.get $cur`)
	g.line(`local.get $start`)
	g.line(`i32.sub`)
	g.line(`local.set $len`)

	// EOF on first byte → None. Allocate 4 bytes for the tag
	// and return early. The Option layout convention (tag 1 =
	// None) is hardcoded here to match the auto-injected enum.
	g.line(`local.get $len`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $sbase`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// Allocate the result string: 4 (length prefix) + len bytes.
	g.line(`local.get $len`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $sbase`)
	g.line(`local.get $sbase`)
	g.line(`local.get $len`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $sptr`)

	// Byte-copy the accumulated buffer into the result.
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $copy_end`)
	g.indent++
	g.line(`loop $copy`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $len`)
	g.line(`i32.eq`)
	g.line(`br_if $copy_end`)
	g.line(`local.get $sptr`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`local.get $start`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.store8`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $copy`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// Wrap the materialised string in `Some(sptr)`. Layout:
	// [tag=0 : i32][str_ptr : i32] (8 bytes total). Caller does
	// match-arm load of payload[0] to recover sptr.
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $sbase`) // reuse $sbase as the option ptr
	g.line(`i32.const 0`)
	g.line(`i32.store`) // tag = 0 (Some)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $sptr`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`return`)
	g.indent--
	g.line(`)`)
}

// emitNoneHelper / emitEnvHelper marker comment
// initialises the environ buffers via WASI; subsequent calls
// reuse the cached pointers. The lookup walks the cached
// environ pointer table, comparing each "KEY=VALUE" entry
// against `name` up to the `=`. On match, a fresh
// length-prefixed string is allocated for the VALUE half.
// Missing keys return the empty string (data pointer to a
// pre-built 0-length string entry).
func (g *generator) emitEnvHelper() {
	g.line(`(func $env (param $name i32) (result i32)`)
	g.indent++
	g.line(`(local $name_len i32)`)
	g.line(`(local $count i32)`)
	g.line(`(local $bufsize i32)`)
	g.line(`(local $env_ptrs i32)`)
	g.line(`(local $env_buf i32)`)
	g.line(`(local $i i32)`)
	g.line(`(local $entry i32)`)
	g.line(`(local $j i32)`)
	g.line(`(local $vlen i32)`)
	g.line(`(local $vstart i32)`)
	g.line(`(local $sbase i32)`)
	g.line(`(local $sptr i32)`)
	g.line(`(local $matches i32)`)
	g.line(`(local $k i32)`)

	// Lazily init the environ buffers.
	g.line(`i32.const 72`)
	g.line(`i32.load`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	// environ_sizes_get(*count_out=84, *bufsize_out=88)
	g.line(`i32.const 84`)
	g.line(`i32.const 88`)
	g.line(`call $__wasi_environ_sizes_get`)
	g.line(`drop`)
	g.line(`i32.const 84`)
	g.line(`i32.load`)
	g.line(`local.set $count`)
	g.line(`i32.const 88`)
	g.line(`i32.load`)
	g.line(`local.set $bufsize`)
	// Allocate env_ptrs (count * 4 bytes) and env_buf (bufsize bytes).
	g.line(`local.get $count`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $env_ptrs`)
	g.line(`local.get $bufsize`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $env_buf`)
	g.line(`local.get $env_ptrs`)
	g.line(`local.get $env_buf`)
	g.line(`call $__wasi_environ_get`)
	g.line(`drop`)
	// Cache.
	g.line(`i32.const 76`)
	g.line(`local.get $count`)
	g.line(`i32.store`)
	g.line(`i32.const 80`)
	g.line(`local.get $env_ptrs`)
	g.line(`i32.store`)
	g.line(`i32.const 72`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.indent--
	g.line(`end`)

	// Load cached count + ptr table.
	g.line(`i32.const 76`)
	g.line(`i32.load`)
	g.line(`local.set $count`)
	g.line(`i32.const 80`)
	g.line(`i32.load`)
	g.line(`local.set $env_ptrs`)

	// name length is stored at name-4.
	g.line(`local.get $name`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $name_len`)

	// for i in 0..count
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $outer_end`)
	g.indent++
	g.line(`loop $outer`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $count`)
	g.line(`i32.eq`)
	g.line(`br_if $outer_end`)

	// entry = env_ptrs[i]
	g.line(`local.get $env_ptrs`)
	g.line(`local.get $i`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $entry`)

	// Compare entry[0..name_len] with name[0..name_len], then
	// require entry[name_len] == '='. matches=1 if all good.
	g.line(`i32.const 1`)
	g.line(`local.set $matches`)
	g.line(`i32.const 0`)
	g.line(`local.set $j`)
	g.line(`block $cmp_end`)
	g.indent++
	g.line(`loop $cmp`)
	g.indent++
	g.line(`local.get $j`)
	g.line(`local.get $name_len`)
	g.line(`i32.eq`)
	g.line(`br_if $cmp_end`)
	g.line(`local.get $entry`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.get $name`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.ne`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`local.set $matches`)
	g.line(`br $cmp_end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $j`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $j`)
	g.line(`br $cmp`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// If matches and entry[name_len]=='=', this is our entry.
	g.line(`local.get $matches`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $entry`)
	g.line(`local.get $name_len`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.const 61`) // '='
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	// Found. vstart = entry + name_len + 1; scan for NUL.
	g.line(`local.get $entry`)
	g.line(`local.get $name_len`)
	g.line(`i32.add`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $vstart`)
	g.line(`local.get $vstart`)
	g.line(`local.set $k`)
	g.line(`block $vlen_end`)
	g.indent++
	g.line(`loop $vlen_loop`)
	g.indent++
	g.line(`local.get $k`)
	g.line(`i32.load8_u`)
	g.line(`i32.eqz`)
	g.line(`br_if $vlen_end`)
	g.line(`local.get $k`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $k`)
	g.line(`br $vlen_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $k`)
	g.line(`local.get $vstart`)
	g.line(`i32.sub`)
	g.line(`local.set $vlen`)

	// Allocate result and copy.
	g.line(`local.get $vlen`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $sbase`)
	g.line(`local.get $sbase`)
	g.line(`local.get $vlen`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $sptr`)
	g.line(`i32.const 0`)
	g.line(`local.set $j`)
	g.line(`block $vcopy_end`)
	g.indent++
	g.line(`loop $vcopy`)
	g.indent++
	g.line(`local.get $j`)
	g.line(`local.get $vlen`)
	g.line(`i32.eq`)
	g.line(`br_if $vcopy_end`)
	g.line(`local.get $sptr`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`local.get $vstart`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.store8`)
	g.line(`local.get $j`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $j`)
	g.line(`br $vcopy`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Found: wrap the materialised string pointer in
	// `Some(sptr)`. Layout matches read_line: 8 bytes total
	// with tag at +0 and the string pointer at +4.
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $sbase`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $sptr`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $outer`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// Not found: return `None` — a 4-byte heap object with
	// tag = 1.
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $sbase`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.indent--
	g.line(`)`)
}

// emitExitHelper writes `$exit(code)`, a one-line wrapper around
// the WASI proc_exit import. WASI's proc_exit takes the status
// code as its only parameter and never returns. We expose it as
// a void-returning lang function; the wasm validator is happy
// because the wrapper itself doesn't need a result type.
func (g *generator) emitExitHelper() {
	g.line(`(func $exit (param $code i32)`)
	g.indent++
	g.line(`local.get $code`)
	g.line(`call $__wasi_proc_exit`)
	g.indent--
	g.line(`)`)
}

// emitFileIOHelpers writes `$read_file`, `$write_file`, and the
// shared `$__build_io_error` helper that maps a WASI errno to the
// matching `IoError` variant. Variant indices are hardcoded to
// match the auto-injected enum (NotFound=0, PermissionDenied=1,
// AlreadyExists=2, InvalidUtf8=3, Interrupted=4, Unsupported=5,
// Other=6).
//
// `read_file` allocates 4 KiB chunks in a loop, packing them
// contiguously on the bump heap by un-bumping any unused tail.
// On EOF / partial read, the contiguous region from $start to
// the bump pointer is the file content; we then allocate a
// length-prefixed result string and memcpy into it. Files
// larger than the available linear memory will simply OOM the
// allocator — streaming via Reader/Writer (Phase 4b) is the
// fix when that matters.
//
// `write_file` opens with O_CREAT | O_TRUNC and a single
// fd_write. Multi-iovec splitting isn't necessary because the
// WASI API accepts an arbitrary buffer length.
//
// Path interpretation is governed by the WASI preopen at fd 3
// — wasmtime users pass `--dir=...` to make a directory
// accessible. Paths are relative to that preopen; absolute
// paths fail with EBADF.
func (g *generator) emitFileIOHelpers() {
	// Pre-intern the static "io error" message so $__build_io_error
	// can reach it via a constant pointer rather than rebuilding
	// the string on every error path.
	ioErrMsgPtr := g.internString("io error")
	g.line(`(func $__build_io_error (param $errno i32) (param $path i32) (result i32)`)
	g.indent++
	g.line(`(local $result i32)`)
	// Errno-to-variant table. Common cases get a typed variant;
	// anything else falls through to Other(path, "io error").
	// WASI Preview 1 errno values: noent=44, acces=2, exist=20,
	// intr=27, notsup=58.
	g.emitIoErrorCase(2, 1, true)   // EACCES → PermissionDenied(path)
	g.emitIoErrorCase(20, 2, true)  // EEXIST → AlreadyExists(path)
	g.emitIoErrorCase(27, 4, false) // EINTR  → Interrupted
	g.emitIoErrorCase(44, 0, true)  // ENOENT → NotFound(path)
	g.emitIoErrorCase(58, 5, false) // ENOTSUP → Unsupported
	// Default: Other(path, "io error"). Allocate 12 bytes:
	// [tag=6, path_ptr, msg_ptr].
	g.line(`i32.const 12`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 6`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $path`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.linef(`i32.const %d`, ioErrMsgPtr)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`)`)

	g.emitReadFileHelper()
	g.emitWriteFileHelper()
}

// emitIoErrorCase writes one branch of the errno → variant
// dispatch in `$__build_io_error`. `errno` is the WASI errno
// to match; `tagIdx` is the IoError variant index;
// `withPathPayload` is true for variants that carry the path
// (NotFound, PermissionDenied, AlreadyExists) and false for
// the payload-less ones (Interrupted, Unsupported).
func (g *generator) emitIoErrorCase(errno, tagIdx int, withPathPayload bool) {
	g.line(`local.get $errno`)
	g.linef(`i32.const %d`, errno)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	if withPathPayload {
		g.line(`i32.const 8`)
	} else {
		g.line(`i32.const 4`)
	}
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.linef(`i32.const %d`, tagIdx)
	g.line(`i32.store`)
	if withPathPayload {
		g.line(`local.get $result`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`local.get $path`)
		g.line(`i32.store`)
	}
	g.line(`local.get $result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
}

// emitReadFileHelper writes `$read_file(path) -> Result[string, IoError]`.
// The chunk-loop strategy is described above the parent
// emitFileIOHelpers comment.
func (g *generator) emitReadFileHelper() {
	g.line(`(func $read_file (param $path i32) (result i32)`)
	g.indent++
	g.line(`(local $fd_buf i32)`)
	g.line(`(local $fd i32)`)
	g.line(`(local $errno i32)`)
	g.line(`(local $iovec i32)`)
	g.line(`(local $nread i32)`)
	g.line(`(local $chunk i32)`)
	g.line(`(local $n i32)`)
	g.line(`(local $start i32)`)
	g.line(`(local $total i32)`)
	g.line(`(local $result i32)`)
	g.line(`(local $sptr i32)`)
	g.line(`(local $sbase i32)`)
	g.line(`(local $i i32)`)

	// Allocate scratch (fd_buf + iovec + nread) BEFORE the
	// chunk-loop anchor so the chunks remain heap-contiguous.
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $fd_buf`)
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $iovec`)
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $nread`)

	// path_open(preopen=3, lookup_flags=1 (symlink_follow),
	//           path_data, path_len, oflags=0,
	//           rights_base=read|seek|tell|filestat_get,
	//           rights_inheriting=0, fdflags=0, fd_buf).
	// wasmtime validates the Rights bits as an enum; passing
	// `-1` (all bits) trips its TryFromIntError. The minimum
	// rights set that lets us read + behave normally is
	// fd_read (2) | fd_seek (4) | fd_tell (32) |
	// fd_filestat_get (0x200000) = 0x200026.
	g.line(`i32.const 3`)
	g.line(`i32.const 1`)
	g.line(`local.get $path`)
	g.line(`local.get $path`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`i32.const 0`)
	g.line(`i64.const 0x200026`)
	g.line(`i64.const 0`)
	g.line(`i32.const 0`)
	g.line(`local.get $fd_buf`)
	g.line(`call $__wasi_path_open`)
	g.line(`local.set $errno`)
	g.line(`local.get $errno`)
	g.line(`if`)
	g.indent++
	// Build Err(__build_io_error(errno, path)).
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $errno`)
	g.line(`local.get $path`)
	g.line(`call $__build_io_error`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	g.line(`local.get $fd_buf`)
	g.line(`i32.load`)
	g.line(`local.set $fd`)

	// Anchor the chunk-loop's heap region.
	g.line(`i32.const 0`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $start`)
	g.line(`i32.const 0`)
	g.line(`local.set $total`)

	g.line(`block $loop_end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	// Allocate a 4 KiB chunk and read into it.
	g.line(`i32.const 4096`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $chunk`)
	g.line(`local.get $iovec`)
	g.line(`local.get $chunk`)
	g.line(`i32.store`)
	g.line(`local.get $iovec`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.const 4096`)
	g.line(`i32.store`)
	g.line(`local.get $fd`)
	g.line(`local.get $iovec`)
	g.line(`i32.const 1`)
	g.line(`local.get $nread`)
	g.line(`call $__wasi_fd_read`)
	g.line(`drop`)
	g.line(`local.get $nread`)
	g.line(`i32.load`)
	g.line(`local.set $n`)
	// EOF: un-bump the empty chunk we just allocated and exit.
	g.line(`local.get $n`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 40`)
	g.line(`i32.const 40`)
	g.line(`i32.load`)
	g.line(`i32.const 4096`)
	g.line(`i32.sub`)
	g.line(`i32.store`)
	g.line(`br $loop_end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $total`)
	g.line(`local.get $n`)
	g.line(`i32.add`)
	g.line(`local.set $total`)
	// Partial read: un-bump the unused tail and exit.
	g.line(`local.get $n`)
	g.line(`i32.const 4096`)
	g.line(`i32.lt_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 40`)
	g.line(`i32.const 40`)
	g.line(`i32.load`)
	g.line(`i32.const 4096`)
	g.line(`local.get $n`)
	g.line(`i32.sub`)
	g.line(`i32.sub`)
	g.line(`i32.store`)
	g.line(`br $loop_end`)
	g.indent--
	g.line(`end`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	g.line(`local.get $fd`)
	g.line(`call $__wasi_fd_close`)
	g.line(`drop`)

	// Allocate the result string with a length prefix and copy.
	g.line(`local.get $total`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $sbase`)
	g.line(`local.get $sbase`)
	g.line(`local.get $total`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $sptr`)

	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $copy_end`)
	g.indent++
	g.line(`loop $copy`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $total`)
	g.line(`i32.eq`)
	g.line(`br_if $copy_end`)
	g.line(`local.get $sptr`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`local.get $start`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.store8`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $copy`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// Build Ok(sptr): 8 bytes [tag=0, sptr].
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $sptr`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`)`)
}

// emitWriteFileHelper writes
// `$write_file(path, content) -> Option[IoError]`.
// One path_open + one fd_write + fd_close. Returns `None`
// (4-byte heap object, tag=1) on success and `Some(err)`
// (8-byte, tag=0 + ioerr_ptr) on failure.
func (g *generator) emitWriteFileHelper() {
	g.line(`(func $write_file (param $path i32) (param $content i32) (result i32)`)
	g.indent++
	g.line(`(local $fd_buf i32)`)
	g.line(`(local $fd i32)`)
	g.line(`(local $errno i32)`)
	g.line(`(local $iovec i32)`)
	g.line(`(local $nwritten i32)`)
	g.line(`(local $result i32)`)

	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $fd_buf`)
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $iovec`)
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $nwritten`)

	// path_open(preopen=3, lookup_flags=1, path_data, path_len,
	//           oflags=O_CREAT|O_TRUNC=9,
	//           rights_base=fd_write|fd_seek|fd_filestat_get,
	//           rights_inheriting=0, fdflags=0, fd_buf).
	// fd_write=0x40 | fd_seek=0x4 | fd_filestat_get=0x200000
	//   = 0x200044.
	g.line(`i32.const 3`)
	g.line(`i32.const 1`)
	g.line(`local.get $path`)
	g.line(`local.get $path`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`i32.const 9`)
	g.line(`i64.const 0x200044`)
	g.line(`i64.const 0`)
	g.line(`i32.const 0`)
	g.line(`local.get $fd_buf`)
	g.line(`call $__wasi_path_open`)
	g.line(`local.set $errno`)
	g.line(`local.get $errno`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 0`) // Some
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $errno`)
	g.line(`local.get $path`)
	g.line(`call $__build_io_error`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	g.line(`local.get $fd_buf`)
	g.line(`i32.load`)
	g.line(`local.set $fd`)

	// fd_write(fd, iovec(content_data, content_len), 1, nwritten)
	g.line(`local.get $iovec`)
	g.line(`local.get $content`)
	g.line(`i32.store`)
	g.line(`local.get $iovec`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $content`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`i32.store`)
	g.line(`local.get $fd`)
	g.line(`local.get $iovec`)
	g.line(`i32.const 1`)
	g.line(`local.get $nwritten`)
	g.line(`call $__wasi_fd_write`)
	g.line(`local.set $errno`)

	g.line(`local.get $fd`)
	g.line(`call $__wasi_fd_close`)
	g.line(`drop`)

	// On write failure, return Some(io_error_from_errno).
	g.line(`local.get $errno`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $errno`)
	g.line(`local.get $path`)
	g.line(`call $__build_io_error`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// Success: return None — 4-byte heap object with tag=1.
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $result`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`)`)
}

// emitDataSegments writes the static-memory initialisers: the runtime
// iovecs / newline byte plus every interned string with its 4-byte
// little-endian length prefix.
func (g *generator) emitDataSegments() {
	// putchar iovec at 4: ptr=0, len=1
	g.line(`(data (i32.const 4) "\00\00\00\00\01\00\00\00")`)
	// print iovec[1] at 24: ptr=32 (=newline byte), len=1
	g.line(`(data (i32.const 24) "\20\00\00\00\01\00\00\00")`)
	// newline byte at 32
	g.line(`(data (i32.const 32) "\0a")`)
	if g.needsReadLine {
		// read_line iovec at 56: ptr=68 (one-byte buffer), len=1
		g.line(`(data (i32.const 56) "\44\00\00\00\01\00\00\00")`)
	}
	// Per-function closure cells: 8 bytes each at closuresBase+8*i,
	// pre-initialised with (table_idx=i, env_ptr=0). Only the
	// originally top-level functions get cells; hoisted (closure-
	// converted) entries are reached through fresh closures the
	// MakeClosure code allocates per construction.
	if g.needsClosures {
		// Each in-table top-level (i.e. value-referenced) function
		// gets a static cell; hoisted closures get fresh cells per
		// MakeClosure invocation. Cell i contains (table-idx i,
		// env_ptr=0).
		for i, name := range g.tableEntries {
			if g.funcIndex[name] >= g.origTopLevelCount {
				continue // hoisted entry; no static cell
			}
			g.linef(`(data (i32.const %d) "%s%s")`, g.closuresBase+8*i, encodeI32(i), encodeI32(0))
		}
	}
	// strings
	for _, s := range g.stringEntries {
		g.linef(`(data (i32.const %d) "%s%s")`, s.offset, encodeI32(len(s.text)), wasmEscape(s.text))
	}
	// Bump-allocator initial pointer at offset 40. We seed it past the
	// end of the strings, rounded up to 4 bytes for i32 access.
	if g.needsArrays {
		start := g.stringOffset
		if start < 64 {
			start = 64
		}
		if start%4 != 0 {
			start += 4 - (start % 4)
		}
		g.linef(`(data (i32.const 40) "%s")`, encodeI32(start))
	}
}

// encodeI32 returns a four-byte little-endian byte string in WAT data
// escape form (e.g. 13 → `\0d\00\00\00`).
func encodeI32(n int) string {
	return fmt.Sprintf(`\%02x\%02x\%02x\%02x`,
		byte(n), byte(n>>8), byte(n>>16), byte(n>>24))
}

// wasmEscape encodes the contents of a string literal for inclusion in
// a WAT data segment: printable ASCII apart from `"` and `\` is kept
// verbatim, everything else becomes `\xx`.
func wasmEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c >= 0x20 && c <= 0x7e:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, `\%02x`, c)
		}
	}
	return b.String()
}

// ---------- type mapping ----------

func watType(t ast.Type) (string, error) {
	switch t.(type) {
	case ast.NumberType, ast.BoolType, ast.StringType:
		// Strings are pointers into linear memory, so they're i32 too.
		return "i32", nil
	case ast.ArrayType:
		// Arrays are pointers into linear memory.
		return "i32", nil
	case *ast.FuncType:
		// Function values are table indices.
		return "i32", nil
	case ast.StructType, ast.EnumType:
		// Struct and enum values are heap pointers.
		return "i32", nil
	case ast.FloatType:
		return "f32", nil
	}
	return "", fmt.Errorf("wasm: type %s isn't supported by this backend yet", t)
}
