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
	return EmitWithOptions(prog, info, EmitOptions{})
}

// EmitOptions tunes the WAT emission. Each field is conservative —
// the zero value matches what the original `Emit` produced before
// options were introduced.
type EmitOptions struct {
	// Preview2 swaps any builtin that has a native WASI Preview 2
	// equivalent off its preview-1 import (currently:
	// `random_bytes` -> `wasi:random/random.get-random-bytes`).
	// Other imports stay on preview-1 and are translated by the
	// preview-1 adapter at `wasm-tools component new` time. Setting
	// this also exports `cabi_realloc` so the host can allocate
	// component-model `list<u8>` return buffers in our linear
	// memory.
	Preview2 bool
}

// EmitWithOptions is the option-aware sibling of Emit. The two share
// the same lowering pipeline (closure conversion → IR → IR opts →
// EmitFromIR); the options only influence the WAT layer.
func EmitWithOptions(prog *ast.Program, info *checker.Info, opts EmitOptions) (string, error) {
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
	return EmitFromIRWithOptions(prog, info, ip, opts)
}

type generator struct {
	out      strings.Builder
	info     *checker.Info
	indent   int
	current  *ast.FuncDecl
	preview2 bool // emit native preview-2 imports + cabi_realloc; otherwise stay on preview-1 names.
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
	needsArena       bool // any `arena_save` / `arena_restore` call — emits the two heap-cursor helpers
	needsRandomBytes bool // any `random_bytes(n)` call — pulls in WASI random_get
	needsTcp         bool // any tcp_* call — pulls in WASI sock_accept + fd_read/fd_write/fd_close on socket fds
	needsFileIO      bool // any `read_file` / `write_file` call — pulls in WASI path_open / fd_read / fd_close
	needsStreamingIO bool // any open_reader / open_writer / Reader|Writer method call — extends needsFileIO with the streaming helpers
	needsStdStreams  bool // any stdin() / stdout() / stderr() call — emits trivial constructors that wrap fd 0 / 1 / 2 in Reader / Writer
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
				case "env":
					g.needsEnv = true
				case "exit":
					g.needsExit = true
				case "arena_save", "arena_restore":
					g.needsArena = true
				case "random_bytes":
					g.needsRandomBytes = true
					g.needsArrays = true
					g.needsRuntime = true
				case "tcp_listen", "tcp_accept", "tcp_recv", "tcp_send", "tcp_close":
					g.needsTcp = true
					g.needsArrays = true
					g.needsRuntime = true
				case "read_file", "write_file":
					g.needsFileIO = true
				case "open_reader", "open_writer", "open_appender":
					g.needsFileIO = true
					g.needsStreamingIO = true
				case "stdin", "stdout", "stderr":
					// stdin/stdout/stderr by themselves
					// don't need the file I/O machinery,
					// but the only point of having them is
					// to call .read_line() / .write() etc.
					// — those methods light up
					// needsStreamingIO via the
					// __method_Reader_* / __method_Writer_*
					// scan a few lines down. Set the
					// dedicated flag for the constructor
					// helper itself.
					g.needsStdStreams = true
				}
				// Method calls on Reader/Writer arrive here as
				// post-checker mangled `__method_Reader_*` /
				// `__method_Writer_*` names; trip the streaming
				// IO flag for any of them.
				if strings.HasPrefix(id.Name, "__method_Reader_") ||
					strings.HasPrefix(id.Name, "__method_Writer_") {
					g.needsFileIO = true
					g.needsStreamingIO = true
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
				case "args", "env", "read_file", "write_file",
					"open_reader", "open_writer", "open_appender",
					"stdin", "stdout", "stderr":
					g.needsArrays = true
					g.needsRuntime = true
				case "arena_save", "arena_restore":
					// Arena helpers read/write the bump cursor
					// at memory[40]. The data segment that seeds
					// that cursor is gated on needsArrays, so
					// we trip that flag here too.
					g.needsArrays = true
					g.needsRuntime = true
				}
				if strings.HasPrefix(id.Name, "__method_Reader_") ||
					strings.HasPrefix(id.Name, "__method_Writer_") {
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
				case "print", "write", "eprint", "putchar", "args", "env", "exit",
					"read_file", "write_file", "open_reader", "open_writer", "open_appender",
					"stdin", "stdout", "stderr":
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
//	92..103 preview-2 canonical-ABI retptr area (12 bytes: enough for
//	         result<list<u8>, stream-error> as well as the smaller
//	         (ptr, len) pair from `get-random-bytes`). Single-threaded,
//	         so the slot is shared between calls.
//	104..107 preview-2 stdout output-stream resource handle (cached on
//	         first $print / $write / $putchar call)
//	108..111 preview-2 stderr output-stream resource handle (cached on
//	         first $eprint call)
//	112..115 preview-2 stream-handle init flags
//	         (bit 0 = stdout cached, bit 1 = stderr cached,
//	          bit 2 = stdin cached)
//	116..119 preview-2 stdin input-stream resource handle (cached on
//	         first $read_line call)
//	96+     string data, each entry: 4-byte length prefix then bytes
//	  (string data starts at 120 instead of 96 when preview-2 streams
//	  are in play; see EmitFromIRWithOptions.)
func (g *generator) emitRuntimePreamble() {
	g.line(`(import "wasi_snapshot_preview1" "fd_write" (func $__wasi_fd_write (param i32 i32 i32 i32) (result i32)))`)
	if g.needsArgs {
		g.line(`(import "wasi_snapshot_preview1" "args_sizes_get" (func $__wasi_args_sizes_get (param i32 i32) (result i32)))`)
		g.line(`(import "wasi_snapshot_preview1" "args_get" (func $__wasi_args_get (param i32 i32) (result i32)))`)
	}
	if g.needsReadLine || g.needsTcp {
		g.line(`(import "wasi_snapshot_preview1" "fd_read" (func $__wasi_fd_read (param i32 i32 i32 i32) (result i32)))`)
	}
	if g.needsEnv {
		g.line(`(import "wasi_snapshot_preview1" "environ_sizes_get" (func $__wasi_environ_sizes_get (param i32 i32) (result i32)))`)
		g.line(`(import "wasi_snapshot_preview1" "environ_get" (func $__wasi_environ_get (param i32 i32) (result i32)))`)
	}
	if g.needsExit {
		g.line(`(import "wasi_snapshot_preview1" "proc_exit" (func $__wasi_proc_exit (param i32)))`)
	}
	if g.preview2 {
		// Preview-2 stdio: get-stdout / get-stderr / get-stdin
		// return resource handles; output-stream.blocking-write-
		// and-flush takes (handle, ptr, len, retptr) and flushes
		// synchronously (matches the existing fd_write semantics
		// closely enough to drop in for $print / $write / $eprint
		// / $putchar). input-stream.blocking-read takes (handle,
		// len_u64, retptr) and writes a result<list<u8>,
		// stream-error> back through the retptr — see
		// emitReadLineHelperPreview2 for the call site. The
		// preview-1 fd_write / fd_read imports above stay, since
		// file I/O and TCP still go through them (via the
		// adapter) — those migrate in subsequent steps.
		g.line(`(import "wasi:cli/stdout@0.2.0" "get-stdout" (func $__wasi_get_stdout (result i32)))`)
		g.line(`(import "wasi:cli/stderr@0.2.0" "get-stderr" (func $__wasi_get_stderr (result i32)))`)
		g.line(`(import "wasi:io/streams@0.2.0" "[method]output-stream.blocking-write-and-flush" (func $__wasi_blocking_write_and_flush (param i32 i32 i32 i32)))`)
		if g.needsReadLine || g.needsStreamingIO {
			// `__method_Reader_read_line` also dispatches into the
			// preview-2 streams path when the Reader's fd is 0
			// (i.e. wraps stdin), so the imports come in for the
			// streaming-IO case too — not just bare `read_line()`.
			g.line(`(import "wasi:cli/stdin@0.2.0" "get-stdin" (func $__wasi_get_stdin (result i32)))`)
			g.line(`(import "wasi:io/streams@0.2.0" "[method]input-stream.blocking-read" (func $__wasi_blocking_read (param i32 i64 i32)))`)
		}
	}
	if g.needsRandomBytes {
		if g.preview2 {
			// Component-model lowered signature for
			//   get-random-bytes: func(len: u64) -> list<u8>
			// is `(param i64 i32)` where i32 is a "return area"
			// pointer. The host calls our exported
			// `cabi_realloc` to allocate a buffer in our linear
			// memory, fills it with the random bytes, then
			// writes the (ptr, len) pair to the return area.
			// See cmd/lang/wit/lang.wit for the world this
			// import comes from; the WAT-level module name
			// `wasi:random/random@0.2.0` matches the legacy
			// canonical-ABI mangling that wit-bindgen-rust and
			// `wasm-tools component embed --dummy-names legacy`
			// also produce.
			g.line(`(import "wasi:random/random@0.2.0" "get-random-bytes" (func $__wasi_random_get_p2 (param i64 i32)))`)
		} else {
			g.line(`(import "wasi_snapshot_preview1" "random_get" (func $__wasi_random_get (param i32 i32) (result i32)))`)
		}
	}
	if g.needsTcp {
		// sock_accept(sock, fdflags, fd_out_ptr) -> errno —
		// returns the new fd via the out-pointer. Wasmtime
		// supports this on host-preopened TCP listeners
		// (`wasmtime --tcp-listen=0.0.0.0:PORT prog.wasm`).
		g.line(`(import "wasi_snapshot_preview1" "sock_accept" (func $__wasi_sock_accept (param i32 i32 i32) (result i32)))`)
		// fd_read / fd_close already cover recv / close on
		// socket fds. fd_read is imported above (gated on
		// needsReadLine || needsTcp); fd_close is below
		// (gated on needsFileIO || needsTcp).
	}
	if g.needsFileIO {
		// path_open / fd_read / fd_close — fd_write is already
		// imported above for `print`. fd_read shares with the
		// stdin reader's / TCP recv's import; if any of those
		// flags is on we still only emit it once.
		g.line(`(import "wasi_snapshot_preview1" "path_open" (func $__wasi_path_open (param i32 i32 i32 i32 i32 i64 i64 i32 i32) (result i32)))`)
		if !g.needsReadLine && !g.needsTcp {
			g.line(`(import "wasi_snapshot_preview1" "fd_read" (func $__wasi_fd_read (param i32 i32 i32 i32) (result i32)))`)
		}
	}
	if g.needsFileIO || g.needsTcp {
		g.line(`(import "wasi_snapshot_preview1" "fd_close" (func $__wasi_fd_close (param i32) (result i32)))`)
	}
	g.line(`(memory $mem 1)`)

	if g.needsArrays || g.needsStructs {
		// $__lang_alloc bumps the allocator pointer at memory[40] and
		// returns the address that was there before the bump. There's
		// no free — arrays in lang are immutable but not GC'd.
		//
		// Grows memory on demand: if the post-bump end goes past the
		// current memory size (in pages), call `memory.grow` for the
		// shortfall. The original implementation skipped this, which
		// was fine for tiny programs but breaks under preview-2 — the
		// preview-1 adapter calls our exported `cabi_realloc` (which
		// hits this allocator) requesting a full 64 KiB page at
		// startup and the canonical-ABI runtime check fails as soon
		// as we hand back a pointer past the current memory.
		g.line(`(func $__lang_alloc (param $size i32) (result i32)`)
		g.indent++
		g.line(`(local $ptr i32) (local $end i32) (local $need i32)`)
		// ptr = mem[40]
		g.line(`i32.const 40`)
		g.line(`i32.load`)
		g.line(`local.set $ptr`)
		// end = ptr + size
		g.line(`local.get $ptr`)
		g.line(`local.get $size`)
		g.line(`i32.add`)
		g.line(`local.set $end`)
		// need = ((end + 65535) >> 16) - memory.size
		g.line(`local.get $end`)
		g.line(`i32.const 65535`)
		g.line(`i32.add`)
		g.line(`i32.const 16`)
		g.line(`i32.shr_u`)
		g.line(`memory.size`)
		g.line(`i32.sub`)
		g.line(`local.set $need`)
		// if (i32) need > 0: memory.grow need (drop result; trust host).
		g.line(`local.get $need`)
		g.line(`i32.const 0`)
		g.line(`i32.gt_s`)
		g.line(`if`)
		g.indent++
		g.line(`local.get $need`)
		g.line(`memory.grow`)
		g.line(`drop`)
		g.indent--
		g.line(`end`)
		// mem[40] = end
		g.line(`i32.const 40`)
		g.line(`local.get $end`)
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

	if g.preview2 {
		g.emitStreamsStdioHelpers()
	}

	// putchar(n)
	g.line(`(func $putchar (param $n i32)`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`local.get $n`)
	g.line(`i32.store8`)
	if g.preview2 {
		// blocking-write-and-flush(stdout, ptr=0, len=1).
		// memory[0] holds the byte we just stored.
		g.line(`call $__stdout_handle`)
		g.line(`i32.const 0`)
		g.line(`i32.const 1`)
		g.line(`call $__streams_write`)
	} else {
		g.line(`i32.const 1`)  // fd = stdout
		g.line(`i32.const 4`)  // iovs = &iovec at offset 4
		g.line(`i32.const 1`)  // iovs_len = 1
		g.line(`i32.const 12`) // nwritten = &offset 12
		g.line(`call $__wasi_fd_write`)
		g.line(`drop`)
	}
	g.indent--
	g.line(`)`)

	// print(s) — writes the string and a newline (matching the arm32
	// puts-based lowering). On preview-1 we split into TWO single-iovec
	// fd_write calls because some wasmtime versions silently drop all
	// but the first iovec when iovs_len > 1; on preview-2 the same
	// pattern still works (two blocking-write-and-flush calls), it just
	// goes through wasi:io/streams instead of fd_write.
	g.line(`(func $print (param $s i32)`)
	g.indent++
	if g.preview2 {
		g.emitStreamsWriteString("$__stdout_handle", "$s")
		g.emitStreamsWriteNewline("$__stdout_handle")
	} else {
		g.emitFdWriteString(1, "$s")
		// Second call: write the newline. iovec at offset 24 is pre-init
		// to (ptr=32, len=1) by a data segment; memory[32] is '\n'.
		g.emitFdWriteNewline(1)
	}
	g.indent--
	g.line(`)`)

	// write(s) — stdout without a trailing newline. Same shape as
	// $print's first half; users compose their own newlines / field
	// separators when they want.
	g.line(`(func $write (param $s i32)`)
	g.indent++
	if g.preview2 {
		g.emitStreamsWriteString("$__stdout_handle", "$s")
	} else {
		g.emitFdWriteString(1, "$s")
	}
	g.indent--
	g.line(`)`)

	// eprint(s) — `print` shape but routed to stderr.
	g.line(`(func $eprint (param $s i32)`)
	g.indent++
	if g.preview2 {
		g.emitStreamsWriteString("$__stderr_handle", "$s")
		g.emitStreamsWriteNewline("$__stderr_handle")
	} else {
		g.emitFdWriteString(2, "$s")
		g.emitFdWriteNewline(2)
	}
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
	if g.needsArena {
		g.emitArenaHelpers()
	}
	if g.needsRandomBytes {
		g.emitRandomBytesHelper()
	}
	if g.preview2 {
		// `cabi_realloc` is the canonical-ABI allocator the host
		// invokes to materialise dynamically-sized return values
		// (e.g. `list<u8>` from `get-random-bytes` or
		// `input-stream.blocking-read`) in our linear memory.
		// Always emit it under preview-2 — its cost is one
		// trivially-tiny function; tracking individual import
		// dependencies isn't worth the gating complexity.
		g.emitCabiRealloc()
	}
	if g.needsTcp {
		g.emitTcpHelpers()
	}
	if g.needsFileIO {
		g.emitFileIOHelpers()
	}
	if g.needsStdStreams {
		g.emitStdStreamHelpers()
	}
}

// emitStdStreamHelpers writes `$stdin`, `$stdout`, `$stderr` —
// trivial constructors that allocate a 4-byte Reader / Writer
// struct around fd 0 / 1 / 2. Called repeatedly each yields a
// fresh struct; that's a small allocation cost for a usually-
// once-per-program lookup, in exchange for not needing static
// memory slots or a cached-once flag.
func (g *generator) emitStdStreamHelpers() {
	g.emitStdStream("$stdin", 0)
	g.emitStdStream("$stdout", 1)
	g.emitStdStream("$stderr", 2)
}

func (g *generator) emitStdStream(name string, fd int) {
	g.linef(`(func %s (result i32)`, name)
	g.indent++
	g.line(`(local $r i32)`)
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $r`)
	g.line(`local.get $r`)
	g.linef(`i32.const %d`, fd)
	g.line(`i32.store`)
	g.line(`local.get $r`)
	g.indent--
	g.line(`)`)
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

// emitStreamsStdioHelpers writes the preview-2 stdio helpers:
//   - $__stdout_handle / $__stderr_handle: lazily call get-stdout
//     / get-stderr and cache the resource handle in static memory
//     (handles are opaque ints and 0 is a valid handle, so the
//     cache uses an init-flag bitfield rather than a 0-sentinel);
//   - $__streams_write: a single blocking-write-and-flush call
//     against (handle, ptr, len). Result <_, stream-error> is
//     written to the shared retptr slot at offset 92 and ignored
//     — failures on stdio in a CLI are effectively unrecoverable.
//
// Memory slots come from the runtime layout block above:
//
//	104..107 stdout handle
//	108..111 stderr handle
//	112..115 init flags  (bit 0 = stdout cached, bit 1 = stderr cached,
//	                      bit 2 = stdin cached)
//	116..119 stdin handle (only when needsReadLine && preview2)
//	 92..103 result<_, stream-error> retptr area (12 bytes; shared with
//	         result<list<u8>, stream-error>)
func (g *generator) emitStreamsStdioHelpers() {
	g.emitStreamHandleAccessor("$__stdout_handle", "$__wasi_get_stdout", 104, 1)
	g.emitStreamHandleAccessor("$__stderr_handle", "$__wasi_get_stderr", 108, 2)
	if g.needsReadLine || g.needsStreamingIO {
		g.emitStreamHandleAccessor("$__stdin_handle", "$__wasi_get_stdin", 116, 4)
		g.emitStdinStreamsReadLine()
	}

	// $__streams_write(handle, ptr, len) — wrap blocking-write-and-flush.
	g.line(`(func $__streams_write (param $handle i32) (param $ptr i32) (param $len i32)`)
	g.indent++
	g.line(`local.get $handle`)
	g.line(`local.get $ptr`)
	g.line(`local.get $len`)
	g.line(`i32.const 92`) // shared retptr; result<_, stream-error> ignored
	g.line(`call $__wasi_blocking_write_and_flush`)
	g.indent--
	g.line(`)`)
}

// emitStreamHandleAccessor writes a `name (result i32)` helper that
// returns the cached resource handle at memory[handleSlot]. On the
// first call, it invokes `getterImport` (e.g. $__wasi_get_stdout),
// stores the result, and sets the corresponding bit (1 << (initBit-1)
// in our convention) in the init-flags byte at offset 112. The
// init-flag indirection is necessary because resource handles are
// opaque ints where 0 is a valid value, so we can't use a 0-sentinel
// to detect "not yet cached".
func (g *generator) emitStreamHandleAccessor(name, getterImport string, handleSlot, initMask int) {
	g.linef(`(func %s (result i32)`, name)
	g.indent++
	g.line(`(local $h i32)`)
	g.line(`i32.const 112`)
	g.line(`i32.load`)
	g.linef(`i32.const %d`, initMask)
	g.line(`i32.and`)
	g.line(`if (result i32)`)
	g.indent++
	g.linef(`i32.const %d`, handleSlot)
	g.line(`i32.load`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.linef(`call %s`, getterImport)
	g.line(`local.tee $h`)
	// Store handle at handleSlot, then OR initMask into the flag byte.
	g.linef(`i32.const %d`, handleSlot)
	g.line(`local.get $h`)
	g.line(`i32.store`)
	g.line(`i32.const 112`)
	g.line(`i32.const 112`)
	g.line(`i32.load`)
	g.linef(`i32.const %d`, initMask)
	g.line(`i32.or`)
	g.line(`i32.store`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)
}

// emitStreamsWriteString emits the call sequence for writing a
// length-prefixed string through the preview-2 streams API:
// load the cached handle, push (data_ptr, len), call
// $__streams_write. `handleAccessor` is the WAT name of the
// `$__stdout_handle` / `$__stderr_handle` helper to use; `local`
// is the wasm local holding the string's data pointer (e.g. "$s")
// — the length lives at `local - 4`, the same shape every other
// string-passing helper expects.
func (g *generator) emitStreamsWriteString(handleAccessor, local string) {
	g.linef(`call %s`, handleAccessor)
	g.linef(`local.get %s`, local)
	g.linef(`local.get %s`, local)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`call $__streams_write`)
}

// emitStreamsWriteNewline emits one $__streams_write call against
// the pre-initialised newline byte at memory[32]. Used by $print
// / $eprint after the string body to mirror the arm32 puts-based
// lowering.
func (g *generator) emitStreamsWriteNewline(handleAccessor string) {
	g.linef(`call %s`, handleAccessor)
	g.line(`i32.const 32`) // newline byte
	g.line(`i32.const 1`)
	g.line(`call $__streams_write`)
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
// stdin reader. Each iteration reads one byte; the loop exits at
// EOF or when the read byte is `\n`. The preview-1 path uses
// fd_read on the iovec at offset 56 (which points at byte 68);
// the preview-2 path is a single delegation to the private
// `$__stdin_read_line` helper that emitStreamsStdioHelpers writes
// (also reused by `__method_Reader_read_line` for Readers wrapping
// stdin).
//
// The result is an `Option[string]` heap object: `Some(line)`
// when at least one byte was read (the line preserves its
// trailing `\n`); `None` when the first read came back empty
// (EOF). Tag 0 is `Some`, tag 1 is `None` — the canonical order
// from the auto-injected Option enum, hardcoded here.
func (g *generator) emitReadLineHelper() {
	if g.preview2 {
		g.line(`(func $read_line (result i32)`)
		g.indent++
		g.line(`call $__stdin_read_line`)
		g.indent--
		g.line(`)`)
		return
	}
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

// emitStdinStreamsReadLine writes the private
// `$__stdin_read_line` helper — the streams-flavoured stdin line
// reader shared between the bare `$read_line` global and
// `__method_Reader_read_line`'s fd==0 fast-path. Reads stdin one
// byte at a time via
// `wasi:io/streams.input-stream.blocking-read(1)`, accumulating
// into a heap buffer, and packages the result as `Option[string]`
// — `None` on EOF before any byte, `Some(line)` (newline included)
// otherwise.
//
// The accumulator is a separate, growable allocation rather than
// the implicit cursor-based anchor the preview-1 path uses.
// Reason: each blocking-read goes through the host's
// `cabi_realloc`, which shares our bump cursor — so the host's
// per-byte buffers and our would-be accumulator slots interleave
// in the heap, and the "cursor advances by exactly 1 per
// iteration" invariant the preview-1 helper relies on no longer
// holds. Initial buffer is 64 bytes; we double on overflow and
// `memory.copy` the prefix into the new region.
//
// EOF detection: the canonical-ABI result discriminant at the
// retptr is non-zero (`Err(stream-error)`) or the returned list is
// length 0. The error resource leaks here — we don't import
// `[resource-drop]error`. Acceptable trade-off for now: a CLI
// program hits stream errors at most once per process lifetime.
func (g *generator) emitStdinStreamsReadLine() {
	g.line(`(func $__stdin_read_line (result i32)`)
	g.indent++
	g.line(`(local $buf i32) (local $buf_size i32) (local $cur_offset i32)`)
	g.line(`(local $byte i32) (local $list_ptr i32)`)
	g.line(`(local $new_buf i32) (local $new_size i32)`)
	g.line(`(local $sbase i32) (local $sptr i32)`)

	// Initial accumulator: 64 bytes. Doubles on overflow.
	g.line(`i32.const 64`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $buf`)
	g.line(`i32.const 64`)
	g.line(`local.set $buf_size`)
	g.line(`i32.const 0`)
	g.line(`local.set $cur_offset`)

	g.line(`block $end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	// blocking-read(handle, 1, retptr=92).
	g.line(`call $__stdin_handle`)
	g.line(`i64.const 1`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_blocking_read`)

	// Outer disc at retptr+0; non-zero = Err = treat as EOF.
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`br_if $end`)

	// list_len at retptr+8; zero-length = EOF on blocking read.
	g.line(`i32.const 100`)
	g.line(`i32.load`)
	g.line(`i32.eqz`)
	g.line(`br_if $end`)

	// list_ptr at retptr+4 → byte = mem[list_ptr].
	g.line(`i32.const 96`)
	g.line(`i32.load`)
	g.line(`local.set $list_ptr`)
	g.line(`local.get $list_ptr`)
	g.line(`i32.load8_u`)
	g.line(`local.set $byte`)

	// Grow the buffer if it's full. new_size = buf_size * 2.
	g.line(`local.get $cur_offset`)
	g.line(`local.get $buf_size`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf_size`)
	g.line(`i32.const 1`)
	g.line(`i32.shl`)
	g.line(`local.set $new_size`)
	g.line(`local.get $new_size`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $new_buf`)
	g.line(`local.get $new_buf`)
	g.line(`local.get $buf`)
	g.line(`local.get $cur_offset`)
	g.line(`memory.copy`)
	g.line(`local.get $new_buf`)
	g.line(`local.set $buf`)
	g.line(`local.get $new_size`)
	g.line(`local.set $buf_size`)
	g.indent--
	g.line(`end`)

	// mem[buf + cur_offset] = byte
	g.line(`local.get $buf`)
	g.line(`local.get $cur_offset`)
	g.line(`i32.add`)
	g.line(`local.get $byte`)
	g.line(`i32.store8`)

	// cur_offset += 1
	g.line(`local.get $cur_offset`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $cur_offset`)

	// Break on newline.
	g.line(`local.get $byte`)
	g.line(`i32.const 10`)
	g.line(`i32.eq`)
	g.line(`br_if $end`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// Empty (no bytes consumed) = None. Tag 1 = None per the
	// auto-injected Option enum layout.
	g.line(`local.get $cur_offset`)
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

	// Materialise as a length-prefixed string.
	g.line(`local.get $cur_offset`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $sbase`)
	g.line(`local.get $sbase`)
	g.line(`local.get $cur_offset`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $sptr`)

	// memory.copy(sptr, buf, cur_offset)
	g.line(`local.get $sptr`)
	g.line(`local.get $buf`)
	g.line(`local.get $cur_offset`)
	g.line(`memory.copy`)

	// Wrap in Some(sptr): tag=0 + payload.
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $sbase`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $sptr`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
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

// emitArenaHelpers writes `$arena_save` and `$arena_restore`,
// the bump-cursor snapshot/restore pair the language exposes
// for long-running servers. The bump pointer lives at
// memory[40] (see `$__lang_alloc`); save reads it, restore
// writes it. No allocation, no syscall — same shape as the
// arm32 helpers.
func (g *generator) emitArenaHelpers() {
	g.line(`(func $arena_save (result i32)`)
	g.indent++
	g.line(`i32.const 40`)
	g.line(`i32.load`)
	g.indent--
	g.line(`)`)
	g.line(`(func $arena_restore (param $h i32)`)
	g.indent++
	g.line(`i32.const 40`)
	g.line(`local.get $h`)
	g.line(`i32.store`)
	g.indent--
	g.line(`)`)
}

// emitRandomBytesHelper writes `$random_bytes(n)`, allocating
// a fresh length-prefixed lang string of n bytes and filling
// it via the WASI `random_get` import. Returns the data
// pointer (post-prefix), matching the runtime ABI of every
// other string-producing builtin.
//
// WASI `random_get(buf, n)` fills the buffer with cryptographic-
// quality random bytes (errno is ignored; the runtime treats
// any failure as program-fatal, same as our other helpers).
// emitTcpHelpers emits the WASM/WASI counterparts of the
// TCP-socket builtins. WASI Preview 1 doesn't expose a way
// to *open* a listening socket from the guest, so the host
// is expected to pre-open one (`wasmtime --tcp-listen=…
// prog.wasm`) — `tcp_listen` returns the first preopened
// socket fd, which wasmtime conventionally places at fd 3.
// `tcp_accept` calls the experimental `sock_accept` import;
// `tcp_recv` / `tcp_send` reuse `fd_read` / `fd_write` (which
// work on socket fds the same as on file fds); `tcp_close`
// is `fd_close`.
func (g *generator) emitTcpHelpers() {
	// $tcp_listen(port) — port arg is ignored; we return the
	// first preopened socket fd. wasmtime starts numbering
	// preopens at 3 (after stdin/stdout/stderr).
	g.line(`(func $tcp_listen (param $port i32) (result i32)`)
	g.indent++
	g.line(`i32.const 3`)
	g.indent--
	g.line(`)`)
	// $tcp_accept(sock) — call sock_accept with the result-fd
	// pointer at memory[64] (a scratch slot in the runtime
	// reserved area). Returns the new fd, or `-errno` on error.
	g.line(`(func $tcp_accept (param $sock i32) (result i32)`)
	g.indent++
	g.line(`(local $err i32)`)
	g.line(`local.get $sock`)
	g.line(`i32.const 0`) // fdflags=0
	g.line(`i32.const 64`) // out-pointer for new fd
	g.line(`call $__wasi_sock_accept`)
	g.line(`local.tee $err`)
	g.line(`if (result i32)`)
	g.indent++
	// errno != 0 → return -err so callers see a negative number.
	g.line(`local.get $err`)
	g.line(`i32.const 0`)
	g.line(`i32.sub`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`i32.const 64`)
	g.line(`i32.load`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)
	// $tcp_recv(fd, max) — issue an fd_read into a fresh
	// buffer; return the resulting string (length = bytes
	// read; 0 on EOF/error).
	g.line(`(func $tcp_recv (param $fd i32) (param $max i32) (result i32)`)
	g.indent++
	g.line(`(local $data i32)`)
	g.line(`(local $err i32)`)
	// data = __lang_alloc(max + 5) + 4
	g.line(`local.get $max`)
	g.line(`i32.const 5`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $data`)
	// iovec at memory[56]: { base = data, len = max }
	g.line(`i32.const 56`)
	g.line(`local.get $data`)
	g.line(`i32.store`)
	g.line(`i32.const 60`)
	g.line(`local.get $max`)
	g.line(`i32.store`)
	// fd_read(fd, iovs=56, iovs_len=1, nread_out=64)
	g.line(`local.get $fd`)
	g.line(`i32.const 56`)
	g.line(`i32.const 1`)
	g.line(`i32.const 64`)
	g.line(`call $__wasi_fd_read`)
	g.line(`local.set $err`)
	// nread = err == 0 ? *(i32*)64 : 0
	g.line(`local.get $err`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const 0`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`i32.const 64`)
	g.line(`i32.load`)
	g.indent--
	g.line(`end`)
	// Store length prefix at data - 4.
	g.line(`local.set $err`) // reuse $err to hold nread
	g.line(`local.get $data`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`local.get $err`)
	g.line(`i32.store`)
	// Trailing NUL.
	g.line(`local.get $data`)
	g.line(`local.get $err`)
	g.line(`i32.add`)
	g.line(`i32.const 0`)
	g.line(`i32.store8`)
	g.line(`local.get $data`)
	g.indent--
	g.line(`)`)
	// $tcp_send(fd, data) — fd_write of the entire data
	// buffer. Returns bytes written or -errno.
	g.line(`(func $tcp_send (param $fd i32) (param $data i32) (result i32)`)
	g.indent++
	g.line(`(local $err i32)`)
	// iovec at memory[56]: { base=data, len=*(data-4) }
	g.line(`i32.const 56`)
	g.line(`local.get $data`)
	g.line(`i32.store`)
	g.line(`i32.const 60`)
	g.line(`local.get $data`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`i32.store`)
	// fd_write(fd, iovs=56, iovs_len=1, nwritten_out=64)
	g.line(`local.get $fd`)
	g.line(`i32.const 56`)
	g.line(`i32.const 1`)
	g.line(`i32.const 64`)
	g.line(`call $__wasi_fd_write`)
	g.line(`local.tee $err`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`local.get $err`)
	g.line(`i32.sub`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`i32.const 64`)
	g.line(`i32.load`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)
	// $tcp_close(fd) — fd_close, returns 0 or -errno.
	g.line(`(func $tcp_close (param $fd i32) (result i32)`)
	g.indent++
	g.line(`(local $err i32)`)
	g.line(`local.get $fd`)
	g.line(`call $__wasi_fd_close`)
	g.line(`local.tee $err`)
	g.line(`i32.const 0`)
	g.line(`i32.sub`)
	g.indent--
	g.line(`)`)
}

func (g *generator) emitRandomBytesHelper() {
	if g.preview2 {
		g.emitRandomBytesHelperPreview2()
		return
	}
	g.line(`(func $random_bytes (param $n i32) (result i32)`)
	g.indent++
	g.line(`(local $data i32)`)
	// data = __lang_alloc(n + 4) + 4 — same allocation shape as
	// args() / env() / strcat. Trailing NUL is one extra byte
	// the alloc rounds up to anyway.
	g.line(`local.get $n`)
	g.line(`i32.const 5`) // 4 prefix + 1 NUL
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $data`)
	// Store length prefix at data - 4.
	g.line(`local.get $data`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`local.get $n`)
	g.line(`i32.store`)
	// random_get(data, n) — result errno ignored.
	g.line(`local.get $data`)
	g.line(`local.get $n`)
	g.line(`call $__wasi_random_get`)
	g.line(`drop`)
	// Trailing NUL at data + n.
	g.line(`local.get $data`)
	g.line(`local.get $n`)
	g.line(`i32.add`)
	g.line(`i32.const 0`)
	g.line(`i32.store8`)
	g.line(`local.get $data`)
	g.indent--
	g.line(`)`)
}

// emitRandomBytesHelperPreview2 emits `$random_bytes` against the
// preview-2 `wasi:random/random.get-random-bytes` import. The
// canonical-ABI lowered signature is `(param i64 i32)`: the i64 is
// the requested length and the i32 is a "return area" pointer where
// the host writes a (ptr, len) pair. The host calls our exported
// `cabi_realloc` to allocate the buffer in our linear memory, fills
// it, and writes back. We then memcpy those bytes into a
// length-prefixed + NUL-terminated string-shape allocation so the
// rest of the runtime sees the same layout it does on the
// preview-1 path.
//
// Memory[92..99] is the static return-area slot — see the runtime
// memory layout comment near `emitRuntimePreamble`. Reserved only
// when `needsRandomBytes && preview2` so it doesn't displace
// strings/closures unnecessarily.
func (g *generator) emitRandomBytesHelperPreview2() {
	g.line(`(func $random_bytes (param $n i32) (result i32)`)
	g.indent++
	g.line(`(local $data i32) (local $host_ptr i32) (local $host_len i32)`)
	// get-random-bytes(len: u64, retptr) — host writes (ptr, len) at retptr.
	g.line(`local.get $n`)
	g.line(`i64.extend_i32_u`)
	g.line(`i32.const 92`) // retptr slot
	g.line(`call $__wasi_random_get_p2`)
	// Read back (host_ptr, host_len) from the retptr slot.
	g.line(`i32.const 92`)
	g.line(`i32.load`)
	g.line(`local.set $host_ptr`)
	g.line(`i32.const 96`)
	g.line(`i32.load`)
	g.line(`local.set $host_len`)
	// Allocate string-shape buffer: 4-byte length prefix + bytes + NUL.
	g.line(`local.get $host_len`)
	g.line(`i32.const 5`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $data`)
	// Length prefix at data - 4.
	g.line(`local.get $data`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`local.get $host_len`)
	g.line(`i32.store`)
	// memory.copy(dest=data, src=host_ptr, n=host_len). The host
	// allocated host_ptr via cabi_realloc, so it lives in our
	// linear memory and memory.copy can move it freely.
	g.line(`local.get $data`)
	g.line(`local.get $host_ptr`)
	g.line(`local.get $host_len`)
	g.line(`memory.copy`)
	// Trailing NUL at data + host_len.
	g.line(`local.get $data`)
	g.line(`local.get $host_len`)
	g.line(`i32.add`)
	g.line(`i32.const 0`)
	g.line(`i32.store8`)
	g.line(`local.get $data`)
	g.indent--
	g.line(`)`)
}

// emitCabiRealloc emits the canonical-ABI realloc entry point that
// the preview-2 host invokes to allocate `list<u8>` (and other
// dynamically-sized) return buffers in our linear memory. The
// signature is fixed by the canonical ABI:
//
//	(orig_ptr, orig_size, align, new_size) -> ptr
//
// Random-bytes only ever calls it with orig_ptr=0 (fresh
// allocation) and align=1, so we ignore the realloc-shrink and
// realloc-grow cases for now. If a future preview-2 import needs
// real reallocation, the body needs to grow a memcpy from the
// previous buffer.
func (g *generator) emitCabiRealloc() {
	g.line(`(func $cabi_realloc (param $orig_ptr i32) (param $orig_size i32) (param $align i32) (param $new_size i32) (result i32)`)
	g.indent++
	g.line(`local.get $new_size`)
	g.line(`call $__lang_alloc`)
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
	if g.needsStreamingIO {
		g.emitStreamingIOHelpers()
	}
}

// emitStreamingIOHelpers writes the runtime functions backing
// `open_reader` / `open_writer` / `open_appender` plus the
// auto-injected Reader / Writer methods. Layout for the
// returned struct values is just `[fd : i32]` — 4 bytes total
// — so $__build_reader / $__build_writer reuse the same store
// pattern.
func (g *generator) emitStreamingIOHelpers() {
	g.emitOpenReaderHelper()
	g.emitOpenWriterHelper(false)
	g.emitOpenAppenderHelper()
	g.emitReaderReadLineMethod()
	g.emitReaderReadChunkMethod()
	g.emitReaderCloseMethod()
	g.emitWriterWriteMethod()
	g.emitWriterCloseMethod()
}

// emitOpenReaderHelper writes `$open_reader(path) ->
// Result[Reader, IoError]`. Calls path_open with the read
// rights set; on success, allocates a 4-byte Reader struct
// holding the fd and wraps it in Ok. On failure builds an Err
// via the shared __build_io_error helper.
func (g *generator) emitOpenReaderHelper() {
	g.emitOpenHelper("$open_reader", 0, 0x200026, 0)
}

// emitOpenWriterHelper writes `$open_writer(path)`. oflags is
// O_CREAT|O_TRUNC=9, rights are fd_write|fd_seek|fd_filestat_get.
// (The `appendMode` parameter is unused for now; appender
// uses the same code path with a different fdflags.)
func (g *generator) emitOpenWriterHelper(_ bool) {
	g.emitOpenHelper("$open_writer", 9, 0x200044, 0)
}

// emitOpenAppenderHelper writes `$open_appender(path)`. Same
// rights as open_writer, but oflags = O_CREAT only (1) so the
// existing content stays, and fdflags = O_APPEND (1) so every
// write goes to the end.
func (g *generator) emitOpenAppenderHelper() {
	g.emitOpenHelper("$open_appender", 1, 0x200044, 1)
}

// emitOpenHelper is the shared body of the three open_*
// constructors. `name` is the wat function name; `oflags` /
// `rights` / `fdflags` are the path_open immediates.
func (g *generator) emitOpenHelper(name string, oflags int, rights int64, fdflags int) {
	g.linef(`(func %s (param $path i32) (result i32)`, name)
	g.indent++
	g.line(`(local $fd_buf i32)`)
	g.line(`(local $fd i32)`)
	g.line(`(local $errno i32)`)
	g.line(`(local $reader i32)`)
	g.line(`(local $result i32)`)

	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $fd_buf`)

	g.line(`i32.const 3`)
	g.line(`i32.const 1`)
	g.line(`local.get $path`)
	g.line(`local.get $path`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.linef(`i32.const %d`, oflags)
	g.linef(`i64.const %d`, rights)
	g.line(`i64.const 0`)
	g.linef(`i32.const %d`, fdflags)
	g.line(`local.get $fd_buf`)
	g.line(`call $__wasi_path_open`)
	g.line(`local.set $errno`)
	g.line(`local.get $errno`)
	g.line(`if`)
	g.indent++
	// Build Err(io_error_from_errno) — Result.Err = tag 1.
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

	// Allocate the Reader / Writer struct (4 bytes — single fd
	// field at offset 0).
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $reader`)
	g.line(`local.get $reader`)
	g.line(`local.get $fd`)
	g.line(`i32.store`)

	// Build Ok(reader): 8 bytes [tag=0, reader_ptr].
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $reader`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`)`)
}

// emitReaderReadLineMethod writes the Reader.read_line method.
// Reads bytes from r.fd one at a time into the static
// 1-byte buffer at memory[68], scanning for `\n`. The
// matched bytes accumulate on the bump heap (1-byte allocs
// in a row keep them contiguous); the final length-prefixed
// string lives just past them.
//
// The static iovec at offset 56 (ptr=68, len=1) is reused
// across calls — the caller is single-threaded so concurrent
// access can't happen. Heap-allocating the iovec doesn't work
// because the byte-grain allocator drifts the bump pointer
// off 4-byte alignment, which wasmtime rejects on fd_read.
//
// The mangled name matches what the checker emits at call
// sites after rewriting `r.read_line()` →
// `__method_Reader_read_line(r)`.
func (g *generator) emitReaderReadLineMethod() {
	g.line(`(func $__method_Reader_read_line (param $r i32) (result i32)`)
	g.indent++
	g.line(`(local $fd i32)`)
	g.line(`(local $start i32)`)
	g.line(`(local $cur i32)`)
	g.line(`(local $byte i32)`)
	g.line(`(local $sbase i32)`)
	g.line(`(local $sptr i32)`)
	g.line(`(local $len i32)`)
	g.line(`(local $i i32)`)
	g.line(`(local $result i32)`)

	g.line(`local.get $r`)
	g.line(`i32.load`)
	g.line(`local.set $fd`)

	if g.preview2 {
		// Preview-2 routes stdin reads (fd=0) through native
		// wasi:io/streams instead of the preview-1 fd_read this
		// method otherwise uses. The bytes that come back via
		// the streams adapter are identical, but we save a hop
		// through the wasi-preview1 adapter component for the
		// hottest read path.
		g.line(`local.get $fd`)
		g.line(`i32.eqz`)
		g.line(`if`)
		g.indent++
		g.line(`call $__stdin_read_line`)
		g.line(`return`)
		g.indent--
		g.line(`end`)
	}

	// Reset the static iovec to (ptr=68, len=1). The data
	// segment seeds it at module-init, but Writer.write and
	// Reader.read_chunk both stomp it, so we re-set it here.
	g.line(`i32.const 56`)
	g.line(`i32.const 68`)
	g.line(`i32.store`)
	g.line(`i32.const 60`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)

	// Anchor for accumulated bytes — heap position right now.
	g.line(`i32.const 0`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $start`)
	g.line(`local.get $start`)
	g.line(`local.set $cur`)

	g.line(`block $end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	// fd_read(fd, iovs=56, iovs_len=1, nread=64)
	g.line(`local.get $fd`)
	g.line(`i32.const 56`)
	g.line(`i32.const 1`)
	g.line(`i32.const 64`)
	g.line(`call $__wasi_fd_read`)
	g.line(`drop`)
	g.line(`i32.const 64`)
	g.line(`i32.load`)
	g.line(`i32.eqz`)
	g.line(`br_if $end`)
	// Append the byte: alloc(1) advances the heap, then store
	// the byte we just read into the new slot.
	g.line(`i32.const 1`)
	g.line(`call $__lang_alloc`)
	g.line(`drop`)
	g.line(`i32.const 68`)
	g.line(`i32.load8_u`)
	g.line(`local.tee $byte`)
	g.line(`local.get $cur`)
	g.line(`local.get $byte`)
	g.line(`i32.store8`)
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

	// len = cur - start.
	g.line(`local.get $cur`)
	g.line(`local.get $start`)
	g.line(`i32.sub`)
	g.line(`local.set $len`)

	// EOF on first byte → Option.None (4 bytes, tag=1).
	g.line(`local.get $len`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $result`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// Materialise length-prefixed string.
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
	// memcpy from start to sptr.
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

	// Some(sptr): 8 bytes [tag=0, sptr].
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

// emitReaderReadChunkMethod writes Reader.read_chunk(size).
// Single fd_read into a heap buffer of capacity `size`,
// trimming any unused tail before returning Some(string).
// Uses the static iovec at memory[56] and nread at
// memory[64] — same alignment reasoning as read_line.
func (g *generator) emitReaderReadChunkMethod() {
	g.line(`(func $__method_Reader_read_chunk (param $r i32) (param $size i32) (result i32)`)
	g.indent++
	g.line(`(local $fd i32)`)
	g.line(`(local $sbase i32)`)
	g.line(`(local $sptr i32)`)
	g.line(`(local $n i32)`)
	g.line(`(local $result i32)`)

	g.line(`local.get $r`)
	g.line(`i32.load`)
	g.line(`local.set $fd`)

	// Allocate `4 + size` for the prefix + data; iovec
	// points into that data so a successful read fills the
	// data slot directly.
	g.line(`local.get $size`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $sbase`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $sptr`)

	// Point the static iovec at our chunk buffer.
	g.line(`i32.const 56`)
	g.line(`local.get $sptr`)
	g.line(`i32.store`)
	g.line(`i32.const 60`)
	g.line(`local.get $size`)
	g.line(`i32.store`)

	g.line(`local.get $fd`)
	g.line(`i32.const 56`)
	g.line(`i32.const 1`)
	g.line(`i32.const 64`)
	g.line(`call $__wasi_fd_read`)
	g.line(`drop`)

	g.line(`i32.const 64`)
	g.line(`i32.load`)
	g.line(`local.set $n`)

	// EOF — un-bump the buffer and return None.
	g.line(`local.get $n`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 40`)
	g.line(`i32.const 40`)
	g.line(`i32.load`)
	g.line(`local.get $size`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.sub`)
	g.line(`i32.store`)
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $result`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// Tighten the buffer: write the actual length and un-bump
	// any unused tail.
	g.line(`local.get $sbase`)
	g.line(`local.get $n`)
	g.line(`i32.store`)
	g.line(`local.get $n`)
	g.line(`local.get $size`)
	g.line(`i32.lt_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 40`)
	g.line(`i32.const 40`)
	g.line(`i32.load`)
	g.line(`local.get $size`)
	g.line(`local.get $n`)
	g.line(`i32.sub`)
	g.line(`i32.sub`)
	g.line(`i32.store`)
	g.indent--
	g.line(`end`)

	// Some(sptr).
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

// emitReaderCloseMethod / emitWriterCloseMethod share the same
// shape — fd_close, return Option[IoError]. We define a
// single helper template and call it twice with different
// receiver / function names.
func (g *generator) emitReaderCloseMethod() {
	g.emitCloseMethod("$__method_Reader_close")
}

func (g *generator) emitWriterCloseMethod() {
	g.emitCloseMethod("$__method_Writer_close")
}

func (g *generator) emitCloseMethod(name string) {
	g.linef(`(func %s (param $r i32) (result i32)`, name)
	g.indent++
	g.line(`(local $fd i32)`)
	g.line(`(local $rc i32)`)
	g.line(`(local $result i32)`)
	g.line(`local.get $r`)
	g.line(`i32.load`)
	g.line(`local.set $fd`)
	g.line(`local.get $fd`)
	g.line(`call $__wasi_fd_close`)
	g.line(`local.set $rc`)
	g.line(`local.get $rc`)
	g.line(`if`)
	g.indent++
	// Some(io_error_from_errno).
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $rc`)
	// We don't have the path here — pass the empty string
	// pointer interned by build_io_error's data segment.
	// For now use the "io error" string as both path and
	// message; calling sites that need the path can wrap
	// the close themselves.
	g.linef(`i32.const %d`, g.internString(""))
	g.line(`call $__build_io_error`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// None.
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $result`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`)`)
}

// emitWriterWriteMethod writes Writer.write(s). Single
// fd_write of the entire string. Returns None on success,
// Some(IoError) on failure. Uses the static iovec at
// memory[56] and nwritten at memory[64] (same slots the
// stdin reader / Reader.read_line use — single-threaded so
// reuse is safe).
func (g *generator) emitWriterWriteMethod() {
	g.line(`(func $__method_Writer_write (param $w i32) (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $fd i32)`)
	g.line(`(local $rc i32)`)
	g.line(`(local $result i32)`)
	g.line(`local.get $w`)
	g.line(`i32.load`)
	g.line(`local.set $fd`)
	// Static iovec at 56: ptr=$s, len=$s.length.
	g.line(`i32.const 56`)
	g.line(`local.get $s`)
	g.line(`i32.store`)
	g.line(`i32.const 60`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`i32.store`)
	g.line(`local.get $fd`)
	g.line(`i32.const 56`)
	g.line(`i32.const 1`)
	g.line(`i32.const 64`)
	g.line(`call $__wasi_fd_write`)
	g.line(`local.set $rc`)
	g.line(`local.get $rc`)
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
	g.line(`local.get $rc`)
	g.linef(`i32.const %d`, g.internString(""))
	g.line(`call $__build_io_error`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Success: None.
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $result`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`)`)
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
	if g.needsReadLine || g.needsStreamingIO {
		// read_line iovec at 56: ptr=68 (one-byte buffer), len=1.
		// The Reader.read_line method reuses this static slot
		// because heap-allocated iovecs aren't reliably aligned
		// (the byte-grain accumulator drifts the bump pointer)
		// and wasmtime requires 4-byte alignment on fd_read's
		// iovs argument. Both helpers run on the single thread
		// of execution, so static reuse is safe.
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
