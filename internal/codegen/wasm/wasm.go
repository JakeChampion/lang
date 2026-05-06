// Package wasm emits WebAssembly text format (WAT) for a checked
// Program. It's a second backend alongside the ARM32 emitter, sharing
// the same AST.
//
// What's covered in v1:
//
//   * numbers (i32) and booleans (also i32 — 0 / non-zero)
//   * all arithmetic, comparison, logical, bitwise and shift operators
//   * functions (with up to ∞ params), recursion, direct calls
//   * `if` / `else`, `while`, `for`, `return`, `break`, `continue`
//   * `var` locals
//
// What's NOT covered yet (would each be a follow-up PR): strings,
// arrays, I/O builtins (`print`, `putchar`), function values
// (`call_indirect` + a function table). Programs using those today
// will still parse and type-check; emitting them returns an error
// here.
//
// Run the output with `wasmtime run --invoke main prog.wat` or
// convert to binary first with `wat2wasm prog.wat`.
package wasm

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/closureconv"
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
	// Closure-convert in place so the rest of this function sees a
	// flat program of top-level functions only. Local FuncDecl
	// statements have been replaced with `var name = MakeClosure{...}`
	// and capture references with CaptureRef nodes.
	origTopLevelCount := len(prog.Funcs)
	if err := closureconv.Convert(prog, info); err != nil {
		return "", err
	}
	g := &generator{
		info:              info,
		origTopLevelCount: origTopLevelCount,
		stringPool: map[string]int{},
		funcIndex:  map[string]int{},
		sigIndex:   map[string]int{},
		inTable:    map[string]bool{},
	}
	for i, fn := range prog.Funcs {
		g.funcIndex[fn.Name] = i
		// Hoisted closure functions always live in the table —
		// we only ever call them indirectly. Original top-level
		// functions are added later if a value-position reference
		// to one is found by scanForIndirectCalls.
		if i >= origTopLevelCount {
			g.inTable[fn.Name] = true
		}
	}
	// Static layout reserves bytes 0..63 for runtime constants
	// (putchar buffer + iovecs + nwritten + newline byte). User
	// strings normally start at offset 64; when closures are in use
	// the next 8*N bytes hold one pre-initialised closure cell per
	// top-level function (so `var f = name` resolves to a stable
	// closure pointer) and strings start past that.
	g.closuresBase = 64
	g.stringOffset = 64
	g.scanForRuntimeUses(prog)
	g.scanForArrayUses(prog)
	g.scanForStructUses(prog)
	g.scanForIndirectCalls(prog)
	g.scanForStringEq(prog)
	g.scanForStringConcat(prog)
	g.scanForBoundsCheck(prog)
	// Closure conversion may have appended hoisted functions; their
	// presence is what flips on the closure ABI for the whole module.
	// Programs without nested functions keep the legacy bare-index
	// representation for function values, since rewriting all of
	// them onto closure cells just to support a feature they don't
	// use is a needless cost.
	if len(prog.Funcs) > g.origTopLevelCount {
		g.needsClosures = true
		g.needsFuncTable = true
		g.needsRuntime = true
		g.needsArrays = true
	}
	// Build the table-resident set in source-declaration order: any
	// top-level function the value-reference scan flagged, then all
	// hoisted closure entries. Their indices in the funcref table
	// drive both call_indirect and the closure cells.
	g.tableIndex = map[string]int{}
	for _, fn := range prog.Funcs {
		if g.inTable[fn.Name] {
			g.tableIndex[fn.Name] = len(g.tableEntries)
			g.tableEntries = append(g.tableEntries, fn.Name)
		}
	}
	if g.needsClosures {
		// Reserve closure cells immediately after the runtime block
		// so the static layout has a stable, deterministic shape.
		g.stringOffset = g.closuresBase + 8*len(g.tableEntries)
	}

	g.line("(module")
	g.indent++

	if g.needsRuntime {
		g.emitRuntimePreamble()
	}

	// Type declarations referenced by call_indirect.
	for i, sig := range g.indirectSigs {
		g.linef("(type $t%d %s)", i, g.watFuncType(sig))
	}

	for _, fn := range prog.Funcs {
		if err := g.emitFunc(fn); err != nil {
			return "", err
		}
	}

	if g.needsFuncTable {
		// The table only contains functions that are reachable
		// as values (referenced as `var f = name`) or that are
		// closure-converted hoisted entries. Non-value top-level
		// functions stay out of the table and keep the simpler
		// no-env signature so `--invoke main` works.
		g.linef("(table $fns %d funcref)", len(g.tableEntries))
		var elems []string
		for _, name := range g.tableEntries {
			elems = append(elems, "$"+name)
		}
		g.linef("(elem (i32.const 0) %s)", strings.Join(elems, " "))
	}

	if g.needsRuntime {
		g.emitDataSegments()
	}

	// Export every top-level function so the host can invoke any of
	// them. `main` is the conventional entry point.
	for _, fn := range prog.Funcs {
		g.linef(`(export %q (func $%s))`, fn.Name, fn.Name)
	}
	if g.needsRuntime {
		g.line(`(export "memory" (memory $mem))`)
	}
	g.indent--
	g.line(")")
	return g.out.String(), nil
}

type generator struct {
	out     strings.Builder
	info    *checker.Info
	indent  int
	loopLbl []loopLabels
	labelN  int
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
	stringPool    map[string]int // value → pointer in linear memory
	stringEntries []stringEntry  // emission order (data segments)
	stringOffset  int            // next free byte for a string entry
	closuresBase  int            // start of the per-function closure-cell region
	// Per-function: a stable assignment of WAT local names to each
	// ArrayLit AST node, so codegen can save its base into the right
	// scratch slot even when ArrayLits are nested.
	arrLocal map[*ast.ArrayLit]string
	// Per-function: a stable assignment of WAT local names to each
	// StructLit AST node — same role as arrLocal but for struct
	// constructors.
	structLocal map[*ast.StructLit]string

	// Per-function: incrementing counter giving each Switch its own
	// `$__sw_N` scratch local for the tag value.
	switchN int

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

type loopLabels struct {
	breakL, continueL string
}

func (g *generator) line(s string) {
	g.out.WriteString(strings.Repeat("  ", g.indent))
	g.out.WriteString(s)
	g.out.WriteByte('\n')
}
func (g *generator) linef(format string, args ...any) {
	g.line(fmt.Sprintf(format, args...))
}
func (g *generator) freshLabel(stem string) string {
	g.labelN++
	return fmt.Sprintf("$%s%d", stem, g.labelN)
}

// hasMakeClosure reports whether n contains any *ast.MakeClosure
// node — i.e. whether closure conversion replaced an inner FuncDecl
// somewhere inside it. The codegen pass uses this to decide whether
// to declare the two scratch locals MakeClosure relies on.
func hasMakeClosure(n any) bool {
	found := false
	var walk func(any)
	walk = func(n any) {
		if found || n == nil {
			return
		}
		switch x := n.(type) {
		case *ast.MakeClosure:
			found = true
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
		}
	}
	walk(n)
	return found
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

// ---------- function emit ----------

func (g *generator) emitFunc(fn *ast.FuncDecl) error {
	g.current = fn
	defer func() { g.current = nil }()

	header := fmt.Sprintf("(func $%s", fn.Name)
	hasEnv := false
	if g.needsClosures && g.inTable[fn.Name] && !envParamPresent(fn) {
		// Closure ABI: every function in the table accepts a trailing
		// `__env i32` parameter so the indirect-call signature is
		// uniform. Direct calls pass i32.const 0; closure-converted
		// hoisted functions read captures through it. Non-table
		// functions stay env-less so the host can `--invoke` them.
		hasEnv = true
	}
	for _, p := range fn.Params {
		typ, err := watType(p.Type)
		if err != nil {
			return fmt.Errorf("function %q: param %s: %w", fn.Name, p.Name, err)
		}
		header += fmt.Sprintf(" (param $%s %s)", p.Name, typ)
	}
	if hasEnv {
		header += " (param $__env i32)"
	}
	if !ast.Equal(fn.ReturnType, ast.VoidType{}) {
		typ, err := watType(fn.ReturnType)
		if err != nil {
			return fmt.Errorf("function %q: result: %w", fn.Name, err)
		}
		header += fmt.Sprintf(" (result %s)", typ)
	}
	g.line(header)
	g.indent++

	// Locals must be declared up-front in WAT, before any instruction.
	for _, v := range g.info.Locals[fn] {
		typ, err := watType(v.Type)
		if err != nil {
			return fmt.Errorf("function %q: var %s: %w", fn.Name, v.Name, err)
		}
		g.linef("(local $%s %s)", v.Name, typ)
	}
	// Each ArrayLit gets its own scratch i32 local that holds the
	// allocator's returned base address while the element stores run.
	// One per node lets nested literals like `[ [1,2], [3,4] ]` work
	// without trampling each other's base pointers.
	g.arrLocal = map[*ast.ArrayLit]string{}
	collectArrayLits(fn.Body, func(a *ast.ArrayLit) {
		name := fmt.Sprintf("$__arr_%d", len(g.arrLocal))
		g.arrLocal[a] = name
		g.linef("(local %s i32)", name)
	})
	// One i32 scratch per Switch in source order, so we can store the
	// tag value once and compare it against each case value.
	g.switchN = 0
	swCount := countSwitches(fn.Body)
	for i := 1; i <= swCount; i++ {
		g.linef("(local $__sw_%d i32)", i)
	}
	// One i32 scratch local per StructLit, holding the alloc-returned
	// base while field stores run.
	g.structLocal = map[*ast.StructLit]string{}
	collectStructLits(fn.Body, func(sl *ast.StructLit) {
		name := fmt.Sprintf("$__sl_%d", len(g.structLocal))
		g.structLocal[sl] = name
		g.linef("(local %s i32)", name)
	})
	// Two scratch i32 locals reserved for any MakeClosure expression
	// in the body. We declare them only when the function actually
	// constructs at least one closure; the cost otherwise is two
	// unused locals.
	if g.needsClosures && hasMakeClosure(fn.Body) {
		g.line("(local $__cl_scratch i32)")
		g.line("(local $__env_scratch i32)")
	}

	for _, s := range fn.Body.Stmts {
		if err := g.stmt(s); err != nil {
			return err
		}
	}
	// If the body falls off the end and the function expects a result,
	// produce 0 so the WASM validator stays happy.
	if !ast.Equal(fn.ReturnType, ast.VoidType{}) {
		if ast.Equal(fn.ReturnType, ast.FloatType{}) {
			g.line("f32.const 0")
		} else {
			g.line("i32.const 0")
		}
	}

	g.indent--
	g.line(")")
	return nil
}

// ---------- statements ----------

func (g *generator) stmt(s ast.Stmt) error {
	switch n := s.(type) {
	case *ast.Block:
		for _, ss := range n.Stmts {
			if err := g.stmt(ss); err != nil {
				return err
			}
		}
	case *ast.If:
		if err := g.expr(n.Cond); err != nil {
			return err
		}
		g.line("if")
		g.indent++
		if err := g.stmt(n.Then); err != nil {
			return err
		}
		g.indent--
		if n.Else != nil {
			g.line("else")
			g.indent++
			if err := g.stmt(n.Else); err != nil {
				return err
			}
			g.indent--
		}
		g.line("end")
	case *ast.While:
		brk := g.freshLabel("break")
		cont := g.freshLabel("loop")
		g.linef("block %s", brk)
		g.indent++
		g.linef("loop %s", cont)
		g.indent++
		if err := g.expr(n.Cond); err != nil {
			return err
		}
		g.line("i32.eqz")
		g.linef("br_if %s", brk)
		g.loopLbl = append(g.loopLbl, loopLabels{breakL: brk, continueL: cont})
		if err := g.stmt(n.Body); err != nil {
			return err
		}
		g.loopLbl = g.loopLbl[:len(g.loopLbl)-1]
		g.linef("br %s", cont)
		g.indent--
		g.line("end")
		g.indent--
		g.line("end")
	case *ast.For:
		brk := g.freshLabel("break")
		loop := g.freshLabel("loop")
		cont := g.freshLabel("cont")
		if n.Init != nil {
			if err := g.stmt(n.Init); err != nil {
				return err
			}
		}
		g.linef("block %s", brk)
		g.indent++
		g.linef("loop %s", loop)
		g.indent++
		if err := g.expr(n.Cond); err != nil {
			return err
		}
		g.line("i32.eqz")
		g.linef("br_if %s", brk)
		// Inner block lets `continue` jump out of the body so the step
		// still runs before the next iteration.
		g.linef("block %s", cont)
		g.indent++
		g.loopLbl = append(g.loopLbl, loopLabels{breakL: brk, continueL: cont})
		if err := g.stmt(n.Body); err != nil {
			return err
		}
		g.loopLbl = g.loopLbl[:len(g.loopLbl)-1]
		g.indent--
		g.line("end")
		if n.Step != nil {
			if err := g.stmt(n.Step); err != nil {
				return err
			}
		}
		g.linef("br %s", loop)
		g.indent--
		g.line("end")
		g.indent--
		g.line("end")
	case *ast.Break:
		if len(g.loopLbl) == 0 {
			return fmt.Errorf("wasm: break outside of a loop")
		}
		g.linef("br %s", g.loopLbl[len(g.loopLbl)-1].breakL)
	case *ast.Continue:
		if len(g.loopLbl) == 0 {
			return fmt.Errorf("wasm: continue outside of a loop")
		}
		g.linef("br %s", g.loopLbl[len(g.loopLbl)-1].continueL)
	case *ast.Return:
		if n.Value != nil {
			if err := g.expr(n.Value); err != nil {
				return err
			}
		}
		g.line("return")
	case *ast.Var:
		if err := g.expr(n.Init); err != nil {
			return err
		}
		g.linef("local.set $%s", n.Name)
	case *ast.ExprStmt:
		if err := g.expr(n.Expr); err != nil {
			return err
		}
		// Any value left on the stack by the expression has to be
		// dropped — WAT validation requires a balanced stack at
		// statement boundaries.
		if g.leavesValue(n.Expr) {
			g.line("drop")
		}
	case *ast.Switch:
		return g.switchStmt(n)
	default:
		return fmt.Errorf("wasm: unsupported statement %T", s)
	}
	return nil
}

// switchStmt lowers a switch into:
//
//	(local $__sw_N i32 / f32)        ;; tag scratch
//	block $end
//	  ;; tag → local
//	  ;; if (tag == v1 || tag == v2) { body1; br $end }
//	  ;; if (tag == v3)              { body2; br $end }
//	  ;; default body  (or nothing)
//	end
//
// `break` inside a case body finds $end via the loopLbl stack. `continue`
// passes through to the enclosing loop's continue label.
func (g *generator) switchStmt(n *ast.Switch) error {
	g.switchN++
	scratch := fmt.Sprintf("$__sw_%d", g.switchN)
	end := g.freshLabel("sw_end")

	// Evaluate the tag once, store in scratch local.
	if err := g.expr(n.Tag); err != nil {
		return err
	}
	g.linef("local.set %s", scratch)

	// Determine the break/continue labels to publish for the body.
	// Continue (if used) propagates to the enclosing loop.
	var parentCont string
	if len(g.loopLbl) > 0 {
		parentCont = g.loopLbl[len(g.loopLbl)-1].continueL
	}
	g.linef("block %s", end)
	g.indent++
	g.loopLbl = append(g.loopLbl, loopLabels{breakL: end, continueL: parentCont})

	for _, k := range n.Cases {
		// Build a chained `local.get tag; i32.eq vN; or; or; ... if ... end`.
		for i, v := range k.Values {
			g.linef("local.get %s", scratch)
			if err := g.expr(v); err != nil {
				return err
			}
			g.line("i32.eq")
			if i > 0 {
				g.line("i32.or")
			}
		}
		g.line("if")
		g.indent++
		for _, s := range k.Body.Stmts {
			if err := g.stmt(s); err != nil {
				return err
			}
		}
		g.linef("br %s", end)
		g.indent--
		g.line("end")
	}
	if n.Default != nil {
		for _, s := range n.Default.Stmts {
			if err := g.stmt(s); err != nil {
				return err
			}
		}
	}

	g.loopLbl = g.loopLbl[:len(g.loopLbl)-1]
	g.indent--
	g.line("end")
	return nil
}

// leavesValue reports whether e leaves a value on the WASM stack so
// the caller knows whether to emit `drop` after it. We use the
// checker's per-function signatures to spot void-returning calls.
func (g *generator) leavesValue(e ast.Expr) bool {
	if c, ok := e.(*ast.Call); ok {
		if id, ok := c.Callee.(*ast.Ident); ok {
			if sig, ok := g.info.FuncSigs[id.Name]; ok {
				return !ast.Equal(sig.Result, ast.VoidType{})
			}
		}
		return true
	}
	// `arr[i] = v` and `p.field = v` lower to i32.store with no
	// result on the stack, so we mustn't emit a `drop` afterward in
	// stmt-position.
	if a, ok := e.(*ast.Assign); ok {
		if _, isIdx := a.Target.(*ast.Index); isIdx {
			return false
		}
		if _, isField := a.Target.(*ast.FieldAccess); isField {
			return false
		}
	}
	return true
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

// countSwitches reports the number of *ast.Switch nodes nested inside
// n. The WAT backend uses the count to declare one i32 scratch local
// per switch up front (locals must precede instructions).
func countSwitches(n any) int {
	count := 0
	var walk func(any)
	walk = func(n any) {
		switch x := n.(type) {
		case *ast.Switch:
			count++
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
	walk(n)
	return count
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

// collectStructLits visits every StructLit in n in source order. The
// per-function map of scratch locals is built from this walk.
func collectStructLits(n any, visit func(*ast.StructLit)) {
	switch x := n.(type) {
	case *ast.StructLit:
		visit(x)
		for _, f := range x.Fields {
			collectStructLits(f.Value, visit)
		}
	case *ast.Block:
		for _, s := range x.Stmts {
			collectStructLits(s, visit)
		}
	case *ast.If:
		collectStructLits(x.Cond, visit)
		collectStructLits(x.Then, visit)
		if x.Else != nil {
			collectStructLits(x.Else, visit)
		}
	case *ast.While:
		collectStructLits(x.Cond, visit)
		collectStructLits(x.Body, visit)
	case *ast.For:
		if x.Init != nil {
			collectStructLits(x.Init, visit)
		}
		collectStructLits(x.Cond, visit)
		if x.Step != nil {
			collectStructLits(x.Step, visit)
		}
		collectStructLits(x.Body, visit)
	case *ast.Return:
		if x.Value != nil {
			collectStructLits(x.Value, visit)
		}
	case *ast.Var:
		collectStructLits(x.Init, visit)
	case *ast.ExprStmt:
		collectStructLits(x.Expr, visit)
	case *ast.Switch:
		collectStructLits(x.Tag, visit)
		for _, k := range x.Cases {
			for _, v := range k.Values {
				collectStructLits(v, visit)
			}
			collectStructLits(k.Body, visit)
		}
		if x.Default != nil {
			collectStructLits(x.Default, visit)
		}
	case *ast.Binary:
		collectStructLits(x.Left, visit)
		collectStructLits(x.Right, visit)
	case *ast.Unary:
		collectStructLits(x.Operand, visit)
	case *ast.Call:
		collectStructLits(x.Callee, visit)
		for _, a := range x.Args {
			collectStructLits(a, visit)
		}
	case *ast.Index:
		collectStructLits(x.Array, visit)
		collectStructLits(x.Idx, visit)
	case *ast.ArrayLit:
		for _, e := range x.Elems {
			collectStructLits(e, visit)
		}
	case *ast.Assign:
		collectStructLits(x.Target, visit)
		collectStructLits(x.Value, visit)
	case *ast.FieldAccess:
		collectStructLits(x.Target, visit)
	}
}

// collectArrayLits visits every ArrayLit in n in source order. Source
// order is what gives nested ArrayLits a deterministic local-name
// mapping at emitFunc time.
func collectArrayLits(n any, visit func(*ast.ArrayLit)) {
	switch x := n.(type) {
	case *ast.ArrayLit:
		visit(x)
		for _, e := range x.Elems {
			collectArrayLits(e, visit)
		}
	case *ast.Block:
		for _, s := range x.Stmts {
			collectArrayLits(s, visit)
		}
	case *ast.If:
		collectArrayLits(x.Cond, visit)
		collectArrayLits(x.Then, visit)
		if x.Else != nil {
			collectArrayLits(x.Else, visit)
		}
	case *ast.While:
		collectArrayLits(x.Cond, visit)
		collectArrayLits(x.Body, visit)
	case *ast.For:
		if x.Init != nil {
			collectArrayLits(x.Init, visit)
		}
		collectArrayLits(x.Cond, visit)
		if x.Step != nil {
			collectArrayLits(x.Step, visit)
		}
		collectArrayLits(x.Body, visit)
	case *ast.Return:
		if x.Value != nil {
			collectArrayLits(x.Value, visit)
		}
	case *ast.Var:
		collectArrayLits(x.Init, visit)
	case *ast.ExprStmt:
		collectArrayLits(x.Expr, visit)
	case *ast.Binary:
		collectArrayLits(x.Left, visit)
		collectArrayLits(x.Right, visit)
	case *ast.Unary:
		collectArrayLits(x.Operand, visit)
	case *ast.Call:
		collectArrayLits(x.Callee, visit)
		for _, a := range x.Args {
			collectArrayLits(a, visit)
		}
	case *ast.Index:
		collectArrayLits(x.Array, visit)
		collectArrayLits(x.Idx, visit)
	case *ast.Assign:
		collectArrayLits(x.Target, visit)
		collectArrayLits(x.Value, visit)
	case *ast.Switch:
		collectArrayLits(x.Tag, visit)
		for _, k := range x.Cases {
			for _, v := range k.Values {
				collectArrayLits(v, visit)
			}
			collectArrayLits(k.Body, visit)
		}
		if x.Default != nil {
			collectArrayLits(x.Default, visit)
		}
	case *ast.Ternary:
		collectArrayLits(x.Cond, visit)
		collectArrayLits(x.Then, visit)
		collectArrayLits(x.Else, visit)
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
				if id.Name == "print" || id.Name == "putchar" {
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
//	64+     string data, each entry: 4-byte length prefix then bytes
func (g *generator) emitRuntimePreamble() {
	g.line(`(import "wasi_snapshot_preview1" "fd_write" (func $__wasi_fd_write (param i32 i32 i32 i32) (result i32)))`)
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
	// First call: write the string. iovec[0] at offset 16 = (s, len).
	g.line(`i32.const 16`)
	g.line(`local.get $s`)
	g.line(`i32.store`)
	g.line(`i32.const 20`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`i32.store`)
	g.line(`i32.const 1`)  // fd = stdout
	g.line(`i32.const 16`) // iovs ptr
	g.line(`i32.const 1`)  // iovs_len = 1
	g.line(`i32.const 36`) // nwritten
	g.line(`call $__wasi_fd_write`)
	g.line(`drop`)
	// Second call: write the newline. iovec at offset 24 is pre-init
	// to (ptr=32, len=1) by a data segment; memory[32] is '\n'.
	g.line(`i32.const 1`)
	g.line(`i32.const 24`)
	g.line(`i32.const 1`)
	g.line(`i32.const 36`)
	g.line(`call $__wasi_fd_write`)
	g.line(`drop`)
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

// ---------- expressions ----------

func (g *generator) expr(e ast.Expr) error {
	switch n := e.(type) {
	case *ast.NumberLit:
		g.linef("i32.const %d", n.Value)
	case *ast.BoolLit:
		if n.Value {
			g.line("i32.const 1")
		} else {
			g.line("i32.const 0")
		}
	case *ast.FloatLit:
		g.linef("f32.const %g", n.Value)
	case *ast.StringLit:
		// String literals are interned in linear memory; the value
		// pushed on the stack is the address of the first byte (after
		// the 4-byte length prefix).
		g.linef("i32.const %d", g.internString(n.Value))
	case *ast.Ident:
		// Top-level function names taken as a value materialise as
		// either a pointer into the static closure-cell region (when
		// closures are in use; the cell is 8 bytes of (idx, env=0))
		// or as a bare table index (legacy path, function values are
		// raw i32s).
		if _, ok := g.funcIndex[n.Name]; ok && g.localFuncType(g.current, n.Name) == nil {
			if g.needsClosures {
				ti := g.tableIndex[n.Name]
				g.linef("i32.const %d", g.closuresBase+8*ti)
			} else {
				// Legacy path (no closures): function values are
				// bare table indices. Without the inTable
				// distinction the index lines up with funcIndex.
				g.linef("i32.const %d", g.funcIndex[n.Name])
			}
			return nil
		}
		g.linef("local.get $%s", n.Name)
	case *ast.Unary:
		switch n.Op {
		case "-":
			if n.IsFloat {
				if err := g.expr(n.Operand); err != nil {
					return err
				}
				g.line("f32.neg")
				break
			}
			g.line("i32.const 0")
			if err := g.expr(n.Operand); err != nil {
				return err
			}
			g.line("i32.sub")
		case "!":
			if err := g.expr(n.Operand); err != nil {
				return err
			}
			g.line("i32.eqz")
		default:
			return fmt.Errorf("wasm: unsupported unary %q", n.Op)
		}
	case *ast.Binary:
		return g.binary(n)
	case *ast.Call:
		return g.call(n)
	case *ast.Assign:
		return g.assign(n)
	case *ast.Ternary:
		return g.ternary(n)
	case *ast.StructLit:
		return g.structLit(n)
	case *ast.FieldAccess:
		return g.fieldAccess(n)
	case *ast.ArrayLit:
		return g.arrayLit(n)
	case *ast.Index:
		// `s[i]` and `a[i]` go through bounds-checking helpers that trap
		// on out-of-range access; the helper returns the byte address
		// of the element so we can finish with the right load.
		if err := g.expr(n.Array); err != nil {
			return err
		}
		if err := g.expr(n.Idx); err != nil {
			return err
		}
		if n.IsString {
			g.line("call $__str_idx")
			g.line("i32.load8_u")
			break
		}
		g.line("call $__arr_idx")
		g.line("i32.load")
	case *ast.CaptureRef:
		// Inside a hoisted local function: load the captured value
		// from `__env + offset`. Floats use f32.load; everything
		// else is an i32 (number, boolean — captures are restricted
		// to scalars in this PR).
		g.line("local.get $__env")
		g.linef("i32.const %d", n.Offset)
		g.line("i32.add")
		if _, ok := n.Type.(ast.FloatType); ok {
			g.line("f32.load")
		} else {
			g.line("i32.load")
		}
	case *ast.MakeClosure:
		return g.makeClosure(n)
	default:
		return fmt.Errorf("wasm: unsupported expression %T", e)
	}
	return nil
}

// makeClosure allocates the env block, populates it with each
// capture's current value, allocates an 8-byte closure pair
// `{fn_idx, env_ptr}`, and leaves the closure pointer on the stack.
// The fn_idx written into the pair is the function's *table* index
// (which call_indirect uses), not the position in prog.Funcs.
func (g *generator) makeClosure(n *ast.MakeClosure) error {
	envBytes := len(n.Captures) * 4
	tIdx, ok := g.tableIndex[n.FuncName]
	if !ok {
		return fmt.Errorf("wasm: closure target %q not in funcref table (compiler bug)", n.FuncName)
	}
	g.line("i32.const 8")
	g.line("call $__lang_alloc")
	g.line("local.tee $__cl_scratch")
	g.linef("i32.const %d", tIdx)
	g.line("i32.store") // fn_idx at +0
	if envBytes > 0 {
		// Allocate env block.
		g.linef("i32.const %d", envBytes)
		g.line("call $__lang_alloc")
		g.line("local.set $__env_scratch")
		// Store each capture in turn.
		for i, capExpr := range n.Captures {
			g.line("local.get $__env_scratch")
			g.linef("i32.const %d", i*4)
			g.line("i32.add")
			if err := g.expr(capExpr); err != nil {
				return err
			}
			// Use f32.store when the capture is a float; checker
			// has already constrained captures to scalar types.
			if isFloatExpr(capExpr) {
				g.line("f32.store")
			} else {
				g.line("i32.store")
			}
		}
		// Store env_ptr at closure+4.
		g.line("local.get $__cl_scratch")
		g.line("i32.const 4")
		g.line("i32.add")
		g.line("local.get $__env_scratch")
		g.line("i32.store")
	} else {
		// No captures: env_ptr = 0.
		g.line("local.get $__cl_scratch")
		g.line("i32.const 4")
		g.line("i32.add")
		g.line("i32.const 0")
		g.line("i32.store")
	}
	// Push the closure pointer back as the expression's value.
	g.line("local.get $__cl_scratch")
	return nil
}

// isFloatExpr is a best-effort check used by closure conversion to
// pick between i32.store and f32.store when packing captures into
// the env block. CaptureRef carries its declared type; everything
// else is treated as i32.
func isFloatExpr(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.CaptureRef:
		_, ok := x.Type.(ast.FloatType)
		return ok
	case *ast.FloatLit:
		return true
	}
	return false
}

// arrayLit allocates 4 + N*4 bytes via $__lang_alloc (4 bytes for the
// length prefix, then N*4 for elements), stores N at the prefix and
// each element at content+i*4, and pushes the *content* address (base
// + 4) onto the stack. The layout matches strings: callers can read
// the length from `addr - 4` to bounds-check or implement `len(a)`.
// The per-ArrayLit local prevents nested literals from clobbering
// each other's bases.
func (g *generator) arrayLit(n *ast.ArrayLit) error {
	local, ok := g.arrLocal[n]
	if !ok {
		return fmt.Errorf("wasm: array literal missing scratch local (compiler bug)")
	}
	g.linef("i32.const %d", 4+len(n.Elems)*4)
	g.line("call $__lang_alloc")
	g.linef("local.tee %s", local)
	// Write the length prefix at base, then advance the local to point
	// at the first element so subsequent stores use simple offsets.
	g.linef("i32.const %d", len(n.Elems))
	g.line("i32.store")
	g.linef("local.get %s", local)
	g.line("i32.const 4")
	g.line("i32.add")
	g.linef("local.set %s", local)
	for i, el := range n.Elems {
		g.linef("local.get %s", local)
		g.linef("i32.const %d", i*4)
		g.line("i32.add")
		if err := g.expr(el); err != nil {
			return err
		}
		g.line("i32.store")
	}
	g.linef("local.get %s", local)
	return nil
}

// structLit allocates 4 bytes per field via the bump allocator and
// stores each initialiser at a fixed offset based on its declaration
// order in the StructDecl. The expression's value is the base pointer.
func (g *generator) structLit(n *ast.StructLit) error {
	local, ok := g.structLocal[n]
	if !ok {
		return fmt.Errorf("wasm: struct literal missing scratch local (compiler bug)")
	}
	sd := g.info.Structs[n.TypeName]
	if sd == nil {
		return fmt.Errorf("wasm: unknown struct %q", n.TypeName)
	}
	g.linef("i32.const %d", len(sd.Fields)*4)
	g.line("call $__lang_alloc")
	g.linef("local.set %s", local)
	for _, f := range n.Fields {
		offset := -1
		for i, df := range sd.Fields {
			if df.Name == f.Name {
				offset = i * 4
				break
			}
		}
		if offset < 0 {
			return fmt.Errorf("wasm: unknown field %q on struct %s (compiler bug)", f.Name, sd.Name)
		}
		g.linef("local.get %s", local)
		g.linef("i32.const %d", offset)
		g.line("i32.add")
		if err := g.expr(f.Value); err != nil {
			return err
		}
		g.line("i32.store")
	}
	g.linef("local.get %s", local)
	return nil
}

// fieldAccess lowers `e.field` to load(e + offset_of_field).
func (g *generator) fieldAccess(n *ast.FieldAccess) error {
	tt := g.exprType(n.Target)
	st, ok := tt.(ast.StructType)
	if !ok {
		return fmt.Errorf("wasm: field access on non-struct (compiler bug)")
	}
	sd := g.info.Structs[st.Name]
	if sd == nil {
		return fmt.Errorf("wasm: unknown struct %q (compiler bug)", st.Name)
	}
	offset := -1
	for i, df := range sd.Fields {
		if df.Name == n.Field {
			offset = i * 4
			break
		}
	}
	if offset < 0 {
		return fmt.Errorf("wasm: unknown field %q on struct %s (compiler bug)", n.Field, sd.Name)
	}
	if err := g.expr(n.Target); err != nil {
		return err
	}
	if offset > 0 {
		g.linef("i32.const %d", offset)
		g.line("i32.add")
	}
	g.line("i32.load")
	return nil
}

// exprType is a small re-deriver for a couple of codegen sites that
// need the static type of an expression (FieldAccess and field-target
// Assign). It mirrors the relevant cases of the checker's checkExpr
// using the type info that's already on idents/locals.
func (g *generator) exprType(e ast.Expr) ast.Type {
	switch x := e.(type) {
	case *ast.Ident:
		for _, vars := range g.info.Locals {
			for _, v := range vars {
				if v.Name == x.Name {
					return v.Type
				}
			}
		}
		if g.current != nil {
			for _, p := range g.current.Params {
				if p.Name == x.Name {
					return p.Type
				}
			}
		}
	case *ast.FieldAccess:
		t := g.exprType(x.Target)
		if st, ok := t.(ast.StructType); ok {
			if sd := g.info.Structs[st.Name]; sd != nil {
				for _, f := range sd.Fields {
					if f.Name == x.Field {
						return f.Type
					}
				}
			}
		}
	case *ast.StructLit:
		return ast.StructType{Name: x.TypeName}
	case *ast.Index:
		at := g.exprType(x.Array)
		if arr, ok := at.(ast.ArrayType); ok {
			return arr.Elem
		}
	}
	return nil
}

func (g *generator) binary(n *ast.Binary) error {
	// Short-circuit logical operators need lazy evaluation.
	switch n.Op {
	case "&&":
		// (left) (if (result i32) (then right) (else (i32.const 0)))
		if err := g.expr(n.Left); err != nil {
			return err
		}
		g.line("if (result i32)")
		g.indent++
		if err := g.expr(n.Right); err != nil {
			return err
		}
		g.indent--
		g.line("else")
		g.indent++
		g.line("i32.const 0")
		g.indent--
		g.line("end")
		return nil
	case "||":
		if err := g.expr(n.Left); err != nil {
			return err
		}
		g.line("if (result i32)")
		g.indent++
		g.line("i32.const 1")
		g.indent--
		g.line("else")
		g.indent++
		if err := g.expr(n.Right); err != nil {
			return err
		}
		g.indent--
		g.line("end")
		return nil
	}
	if err := g.expr(n.Left); err != nil {
		return err
	}
	if err := g.expr(n.Right); err != nil {
		return err
	}
	if n.IsStringCmp {
		// Both sides are string pointers; ask the runtime to compare
		// contents. `!=` is the negation of `==`.
		g.line("call $__str_eq")
		if n.Op == "!=" {
			g.line("i32.eqz")
		}
		return nil
	}
	if n.IsStringConcat {
		// Both sides are string pointers; ask the runtime to allocate a
		// new length-prefixed string holding their concatenation.
		g.line("call $__str_concat")
		return nil
	}
	if n.IsFloat {
		op, err := wasmFloatBinaryOp(n.Op)
		if err != nil {
			return err
		}
		g.line(op)
		return nil
	}
	op, err := wasmBinaryOp(n.Op)
	if err != nil {
		return err
	}
	g.line(op)
	return nil
}

func wasmFloatBinaryOp(op string) (string, error) {
	switch op {
	case "+":
		return "f32.add", nil
	case "-":
		return "f32.sub", nil
	case "*":
		return "f32.mul", nil
	case "/":
		return "f32.div", nil
	case "==":
		return "f32.eq", nil
	case "!=":
		return "f32.ne", nil
	case "<":
		return "f32.lt", nil
	case "<=":
		return "f32.le", nil
	case ">":
		return "f32.gt", nil
	case ">=":
		return "f32.ge", nil
	}
	return "", fmt.Errorf("wasm: unsupported float binary op %q", op)
}

func wasmBinaryOp(op string) (string, error) {
	switch op {
	case "+":
		return "i32.add", nil
	case "-":
		return "i32.sub", nil
	case "*":
		return "i32.mul", nil
	case "/":
		return "i32.div_s", nil
	case "%":
		return "i32.rem_s", nil
	case "&":
		return "i32.and", nil
	case "|":
		return "i32.or", nil
	case "^":
		return "i32.xor", nil
	case "<<":
		return "i32.shl", nil
	case ">>":
		return "i32.shr_s", nil
	case "==":
		return "i32.eq", nil
	case "!=":
		return "i32.ne", nil
	case "<":
		return "i32.lt_s", nil
	case "<=":
		return "i32.le_s", nil
	case ">":
		return "i32.gt_s", nil
	case ">=":
		return "i32.ge_s", nil
	}
	return "", fmt.Errorf("wasm: unsupported binary op %q", op)
}

func (g *generator) call(n *ast.Call) error {
	id, ok := n.Callee.(*ast.Ident)
	if !ok {
		return fmt.Errorf("wasm: indirect call from non-identifier expression")
	}
	// Inline `len(s)` / `len(arr)`: both string and array carry a
	// 4-byte little-endian length prefix at `ptr - 4`.
	if id.Name == "len" && len(n.Args) == 1 && g.localFuncType(g.current, id.Name) == nil {
		if _, isUser := g.funcIndex[id.Name]; !isUser {
			if _, isDeclared := g.info.FuncSigs[id.Name]; !isDeclared {
				if err := g.expr(n.Args[0]); err != nil {
					return err
				}
				g.line("i32.const 4")
				g.line("i32.sub")
				g.line("i32.load")
				return nil
			}
		}
	}
	// Direct call: callee is either a top-level user function or a
	// builtin (`print`/`putchar`) wired to a runtime helper, AND the
	// name isn't shadowed by a function-typed local.
	_, isTopLevel := g.funcIndex[id.Name]
	_, isBuiltin := g.info.FuncSigs[id.Name]
	if (isTopLevel || isBuiltin) && g.localFuncType(g.current, id.Name) == nil {
		for _, a := range n.Args {
			if err := g.expr(a); err != nil {
				return err
			}
		}
		// Under the closure ABI, only table-resident functions take
		// a trailing __env i32 (so wasmtime can still invoke
		// non-table entry points by their original signature).
		if g.needsClosures && isTopLevel && g.inTable[id.Name] {
			g.line("i32.const 0")
		}
		g.linef("call $%s", id.Name)
		return nil
	}
	// Indirect call through a function-typed local. The signature is
	// known from the local's declared type. Under the closure ABI
	// the local holds a heap pointer to {fn_idx, env_ptr}; legacy
	// programs (no nested functions) keep using bare table indices.
	ft := g.localFuncType(g.current, id.Name)
	if ft == nil {
		return fmt.Errorf("wasm: cannot resolve indirect callee %q", id.Name)
	}
	tIdx := g.recordSig(ft)
	for _, a := range n.Args {
		if err := g.expr(a); err != nil {
			return err
		}
	}
	if g.needsClosures {
		// Push env_ptr (closure + 4) then fn_idx (closure + 0). The
		// fn_idx must be the very last operand before call_indirect.
		g.linef("local.get $%s", id.Name)
		g.line("i32.const 4")
		g.line("i32.add")
		g.line("i32.load")
		g.linef("local.get $%s", id.Name)
		g.line("i32.load")
	} else {
		g.linef("local.get $%s", id.Name)
	}
	g.linef("call_indirect (type $t%d)", tIdx)
	return nil
}

func (g *generator) assign(n *ast.Assign) error {
	switch t := n.Target.(type) {
	case *ast.Ident:
		if err := g.expr(n.Value); err != nil {
			return err
		}
		g.linef("local.tee $%s", t.Name)
		return nil
	case *ast.Index:
		// arr[i] = v → store at the address $__arr_idx returned (which
		// already trapped on OOB). We don't try to leave the assigned
		// value on the stack; the leavesValue check in stmt special-
		// cases an Index-target Assign so no `drop` is emitted.
		if err := g.expr(t.Array); err != nil {
			return err
		}
		if err := g.expr(t.Idx); err != nil {
			return err
		}
		g.line("call $__arr_idx")
		if err := g.expr(n.Value); err != nil {
			return err
		}
		g.line("i32.store")
		return nil
	case *ast.FieldAccess:
		// p.field = v → store at base + offset_of_field. Like Index
		// assignment, leaves nothing on the stack; leavesValue's
		// special case suppresses the `drop`.
		tt := g.exprType(t.Target)
		st, ok := tt.(ast.StructType)
		if !ok {
			return fmt.Errorf("wasm: field assignment on non-struct (compiler bug)")
		}
		sd := g.info.Structs[st.Name]
		offset := -1
		for i, df := range sd.Fields {
			if df.Name == t.Field {
				offset = i * 4
				break
			}
		}
		if offset < 0 {
			return fmt.Errorf("wasm: unknown field %q on struct %s (compiler bug)", t.Field, st.Name)
		}
		if err := g.expr(t.Target); err != nil {
			return err
		}
		if offset > 0 {
			g.linef("i32.const %d", offset)
			g.line("i32.add")
		}
		if err := g.expr(n.Value); err != nil {
			return err
		}
		g.line("i32.store")
		return nil
	}
	return fmt.Errorf("wasm: unsupported assignment target %T", n.Target)
}

// ternary lowers `cond ? then : else` into WAT's `if (result T) ... else ... end`.
// The result type comes from the checker — IsFloat means f32, otherwise i32
// (which covers number, boolean, string-as-pointer, array-as-pointer, and
// function-as-table-index).
func (g *generator) ternary(n *ast.Ternary) error {
	if err := g.expr(n.Cond); err != nil {
		return err
	}
	resTyp := "i32"
	if n.IsFloat {
		resTyp = "f32"
	}
	g.linef("if (result %s)", resTyp)
	g.indent++
	if err := g.expr(n.Then); err != nil {
		return err
	}
	g.indent--
	g.line("else")
	g.indent++
	if err := g.expr(n.Else); err != nil {
		return err
	}
	g.indent--
	g.line("end")
	return nil
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
	case ast.StructType:
		// Struct values are pointers into linear memory.
		return "i32", nil
	case ast.FloatType:
		return "f32", nil
	}
	return "", fmt.Errorf("wasm: type %s isn't supported by this backend yet", t)
}
