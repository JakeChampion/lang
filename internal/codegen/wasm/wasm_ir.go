// IR-driven WebAssembly text emitter. EmitFromIR is a parallel entry
// point to Emit: instead of walking the AST a second time to lay out
// each function body, it consumes a pre-lowered ir.Program. The
// module-level scaffolding (runtime helpers, function table, data
// segments, exports) still comes from the AST scans because those
// describe whole-module state the IR doesn't model.
//
// During the cutover, both Emit and EmitFromIR are kept side-by-side
// so we can compare their outputs on a corpus and burn in the IR
// path before flipping the driver.
package wasm

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
)

// EmitFromIR returns the WAT module text for prog using the lowered
// IR for each function body. The caller is responsible for having
// already run closure conversion (typically by going through
// ir.Lower, which does it internally) — this function does NOT call
// closureconv.Convert again.
//
// The order of prog.Funcs and ip.Funcs must match: ip.Funcs[i]
// describes the body of prog.Funcs[i].
func EmitFromIR(prog *ast.Program, info *checker.Info, ip *ir.Program) (string, error) {
	return EmitFromIRWithOptions(prog, info, ip, EmitOptions{})
}

// EmitFromIRWithOptions is the option-aware sibling of EmitFromIR.
// See `EmitOptions` for the available tuning knobs.
func EmitFromIRWithOptions(prog *ast.Program, info *checker.Info, ip *ir.Program, opts EmitOptions) (string, error) {
	// IR-level DCE may leave ip.Funcs as a subset of
	// prog.Funcs — every IR func still must have a matching
	// AST entry, but the reverse isn't required. The emit
	// loop further down looks each IR function up by name in
	// g.funcDecls (built from prog.Funcs).
	if len(prog.Funcs) < len(ip.Funcs) {
		return "", fmt.Errorf("wasm: prog has %d funcs but ir has %d (ir must be a subset)", len(prog.Funcs), len(ip.Funcs))
	}
	g := &generator{
		info:              info,
		httpHandler:       opts.HttpHandler,
		printMainResult:   opts.PrintMainResult,
		origTopLevelCount: countOrigTopLevel(prog),
		stateDecls:        prog.States,
		needsPersistent:   len(prog.States) > 0,
		stringPool:        map[string]int{},
		funcIndex:         map[string]int{},
		sigIndex:          map[string]int{},
		inTable:           map[string]bool{},
		funcDecls:         map[string]*ast.FuncDecl{},
		emittedFuncs:      map[string]bool{},
	}
	for i, fn := range prog.Funcs {
		g.funcIndex[fn.Name] = i
		g.funcDecls[fn.Name] = fn
		// Hoisted closure functions (those whose name starts with
		// `__closure_`) are appended after the original top-level
		// entries; they always live in the table.
		if i >= g.origTopLevelCount {
			g.inTable[fn.Name] = true
		}
	}
	g.closuresBase = 64
	g.stringOffset = 64

	g.scanForRuntimeUses(prog)
	g.scanForArrayUses(prog)
	g.scanForStructUses(prog)
	g.scanForIOBuiltins(prog)
	if g.printMainResult {
		// `_start` calls `int_to_string` to format main()'s i32
		// return; the helper itself lives in the lang prelude
		// now. Treeshake's `extras` parameter (set elsewhere
		// via the same flag) keeps the prelude function alive
		// even if user code doesn't reference it; here we just
		// pull the runtime in for the helper's bump-allocs.
		g.needsRuntime = true
	}
	if g.httpHandler {
		// The handle wrapper allocates HttpRequest / HttpResponse
		// structs, copies bodies into lang-shape strings, and
		// builds outgoing-response — all of which lean on the
		// bump allocator + the struct field-store machinery.
		// Plus an implicit per-request arena scope around the
		// whole wrapper body (see emitHttpHandlerWrapper) so
		// every per-request allocation is reclaimed in one
		// pointer-store at handler exit.
		g.needsRuntime = true
		g.needsArrays = true
		g.needsStructs = true
		g.needsArena = true
	}
	g.scanForIndirectCalls(prog)
	g.scanForStringEq(prog)
	g.scanForStringConcat(prog)
	g.scanForBoundsCheck(prog)
	// read_file / write_file are implemented in terms of
	// $open_reader / $open_writer (emitStreamingIOHelpers emits
	// them), so promote needsFileIO to also pull in
	// needsStreamingIO. The cost is the Reader/Writer method
	// helpers landing in modules that wouldn't otherwise call them
	// directly — small, tree-shakeable; the alternative
	// (re-implementing the open pipeline inline) duplicates the
	// canonical-ABI machinery.
	if g.needsFileIO {
		g.needsStreamingIO = true
	}
	if len(prog.Funcs) > g.origTopLevelCount {
		g.needsClosures = true
		g.needsFuncTable = true
		g.needsRuntime = true
		g.needsArrays = true
	}
	// Variant construction (and the Option-shaped I/O builtins
	// which build their own enum object) needs the bump
	// allocator. The auto-injected Option / Result decls don't
	// imply usage on their own, so we scan for actual variant
	// references / match statements instead of enum-decl count.
	if scanEnumUses(prog) || g.needsReadLine || g.needsEnv {
		g.needsRuntime = true
		g.needsArrays = true
	}

	// Bump the string-data start past whatever runtime scratch we
	// claimed. The default base is 64; read_line claims 56..71;
	// env claims 72..91. Strings (and the heap that follows) move
	// to 96 when env is in play, 72 when only read_line is.
	if g.needsEnv {
		g.stringOffset = 96
		g.closuresBase = 96
	} else if g.needsReadLine {
		g.stringOffset = 72
		g.closuresBase = 72
	}
	// Preview-2 reserves memory[92..127] for canonical-ABI scratch:
	//   92..103 retptr area (sized for `result<list<u8>,
	//           stream-error>`; also fits the smaller canonical-ABI
	//           returns we use — `(ptr, len)` from
	//           `get-random-bytes` and `get-directories`,
	//           `result<X, error-code>` from `open-at` and the
	//           `*-via-stream` family). Doesn't fit `accept`'s
	//           16-byte `result<tuple<3 i32>, error-code>`, so
	//           tcp_accept allocates its retptr dynamically via
	//           `__lang_alloc(16)`,
	//   104..107 stdout output-stream handle,
	//   108..111 stderr output-stream handle,
	//   112..115 init flags for the cached handles
	//           (bits 0/1/2/3/4 = stdout/stderr/stdin/preopen/
	//           network),
	//   116..119 stdin input-stream handle,
	//   120..123 preopen descriptor handle (cached working dir),
	//   124..127 network handle (cached on first
	//           tcp_listen / tcp_accept).
	// Push the string base past those slots regardless of which
	// preview-2 features the program actually exercises. The cost
	// is 36 static bytes per module; the alternative (gating each
	// slot independently) is fragile.
	if g.stringOffset < 128 {
		g.stringOffset = 128
	}
	if g.closuresBase < 128 {
		g.closuresBase = 128
	}

	g.tableIndex = map[string]int{}
	for _, fn := range prog.Funcs {
		if g.inTable[fn.Name] {
			g.tableIndex[fn.Name] = len(g.tableEntries)
			g.tableEntries = append(g.tableEntries, fn.Name)
		}
	}
	if g.needsClosures {
		g.stringOffset = g.closuresBase + 8*len(g.tableEntries)
	}

	// Pre-register every indirect-call signature the IR mentions so
	// the type table is stable across both emitters.
	for _, irFn := range ip.Funcs {
		for _, op := range irFn.Ops {
			if op.Kind == ir.OpCallIndirect && op.Sig != nil {
				g.recordSig(op.Sig)
			}
		}
	}

	g.line("(module")
	g.indent++

	// Under preview-2, emitRuntimePreamble holds $cabi_realloc and
	// the imports the host always wires (stdout / stderr handles,
	// `wasi:io/streams.blocking-write-and-flush`, plus the output-
	// stream drop), and we always export `cabi_realloc` from the
	// component boundary. Force needsRuntime so the helper is
	// available even for tiny programs that don't otherwise touch
	// runtime helpers (e.g. a float-arithmetic compute that just
	// returns an int).
	g.needsRuntime = true
	g.emitRuntimePreamble()

	for i, sig := range g.indirectSigs {
		g.linef("(type $t%d %s)", i, g.watFuncType(sig))
	}

	// Iterate the IR's function list (which post-DCE may be a
	// subset of the AST's prog.Funcs) and look up each
	// matching FuncDecl by name. Decoupling the two slices
	// lets IR-level dead-function elimination drop unused
	// bodies while leaving prog.Funcs intact for the AST scans
	// (scanForStringConcat / scanForArrayUses / etc., all of
	// which walk the AST not the IR).
	for _, irFn := range ip.Funcs {
		fn := g.funcDecls[irFn.Name]
		if fn == nil {
			return "", fmt.Errorf("wasm/ir: ir func %q has no matching ast.FuncDecl", irFn.Name)
		}
		if err := g.emitFuncFromIR(fn, irFn); err != nil {
			return "", err
		}
		g.emittedFuncs[irFn.Name] = true
	}

	if g.needsFuncTable {
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

	// Export only functions that survived IR-level DCE — the
	// emit loop below iterates ip.Funcs, so any export whose
	// $name isn't in g.emittedFuncs would dangle.
	for _, fn := range prog.Funcs {
		if !g.emittedFuncs[fn.Name] {
			continue
		}
		g.linef(`(export %q (func $%s))`, fn.Name, fn.Name)
	}
	// state{}-block runtime init: the synthesised
	// `__state_init` runs at module instantiation, before any
	// host-callable export. wasm's `(start FN)` section is the
	// canonical wire-up — every conforming runtime invokes it
	// once when the module is instantiated, ahead of any
	// caller-driven entry point. Pairs with the global
	// declarations emitted by `emitStateGlobals` (those carry
	// zero / null placeholders for non-literal init; the
	// `(start)` body fills them in).
	if g.emittedFuncs["__state_init"] {
		g.line(`(start $__state_init)`)
	}
	// WASI command convention (preview-1 and preview-2 alike) wants
	// `_start` as the entry point — that's what `wasmtime run`
	// invokes, and what the wasi-preview1-component-adapter needs to
	// see when wrapping us into a Component Model component. We
	// already export `main`; emit a thin `_start (() -> ())` wrapper
	// that calls it and drops any return value, then export it.
	//
	// Test harness escape hatch: with `EmitOptions.PrintMainResult`
	// set, `_start` formats main's i32 result through int_to_string
	// and prints it before returning. The WASM e2e tests rely on
	// this to observe main's value over stdout — components don't
	// expose `--invoke main` and `wasi:cli/exit` clamps the exit
	// code to 0/1, so stdout is the only channel.
	if mainFn := g.funcDecls["main"]; mainFn != nil && !g.httpHandler {
		g.line(`(func $_start`)
		g.indent++
		g.line(`(call $main)`)
		mainReturnsInt := !ast.Equal(mainFn.ReturnType, ast.VoidType{}) && ast.Equal(mainFn.ReturnType, ast.NumberType{})
		if g.printMainResult && mainReturnsInt {
			g.line(`call $int_to_string`)
			g.line(`call $print`)
		} else if !ast.Equal(mainFn.ReturnType, ast.VoidType{}) {
			g.line(`drop`)
		}
		g.indent--
		g.line(`)`)
		g.line(`(export "_start" (func $_start))`)
	} else if g.httpHandler {
		// Proxy components don't run a `main()` — `wasmtime serve`
		// invokes the exported `wasi:http/incoming-handler.handle`
		// per request — but the wasi-preview1-component-adapter
		// (`command.wasm`) still wires its own `wasi:cli/run.run`
		// to a `_start` core export, so we emit an empty stub.
		// Switching to the dedicated `proxy.wasm` adapter would
		// drop this requirement; we don't ship that yet.
		g.line(`(func $_start)`)
		g.line(`(export "_start" (func $_start))`)
	}
	if g.needsRuntime {
		g.line(`(export "memory" (memory $mem))`)
	}
	// Component-model contract: any host import that returns a
	// dynamically-sized type (list<u8>, string, …) uses
	// `cabi_realloc` to allocate space in the guest's linear
	// memory before writing the bytes. Both `get-random-bytes`
	// and `input-stream.blocking-read` use it; export
	// unconditionally rather than gate on each individual import.
	g.line(`(export "cabi_realloc" (func $cabi_realloc))`)
	g.indent--
	g.line(")")
	return g.out.String(), nil
}

// countOrigTopLevel returns the number of original (pre-closure-
// conversion) top-level functions in prog. Hoisted closure functions
// have synthetic `__closure_*` names, so we count anything that
// doesn't start with that prefix.
func countOrigTopLevel(prog *ast.Program) int {
	n := 0
	for _, fn := range prog.Funcs {
		if !strings.HasPrefix(fn.Name, "__closure_") {
			n++
		}
	}
	return n
}

// emitFuncFromIR writes one function header + body using the lowered
// IR for the body. Locals are declared by walking the IR Func's
// Params + Locals + NumScratch; the body itself is a flat translation
// of each Op to its WAT equivalent.
func (g *generator) emitFuncFromIR(fn *ast.FuncDecl, irFn *ir.Func) error {
	g.current = fn
	defer func() { g.current = nil }()

	// Function header: `(func $name (param ...) [(param $__env i32)] (result T))`.
	header := fmt.Sprintf("(func $%s", fn.Name)
	hasEnv := false
	if g.needsClosures && g.inTable[fn.Name] && !envParamPresent(fn) {
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

	// User vars: declared by the checker and carried on irFn.Locals
	// in slot order.
	for _, v := range irFn.Locals {
		typ, err := watType(v.Type)
		if err != nil {
			return fmt.Errorf("function %q: var %s: %w", fn.Name, v.Name, err)
		}
		g.linef("(local $%s %s)", v.Name, typ)
	}
	// Synthetic scratches the IR conjured for ArrayLit / StructLit /
	// Switch / closure helpers (always i32) and for inlined callees
	// (mixed: each takes its callee-slot type). Addressed by index
	// just like user vars; we name them deterministically so WAT
	// validation has something to point at.
	for i, t := range irFn.ScratchTypes {
		typ, err := watType(t)
		if err != nil {
			return fmt.Errorf("function %q: scratch %d: %w", fn.Name, i, err)
		}
		g.linef("(local $__scratch_%d %s)", i, typ)
	}
	// Closure-construction helpers, if any OpMakeClosure appears in
	// the body. We pre-scan every MakeClosure / MakeEnv site, look
	// at the hoisted target's per-capture types, and declare typed
	// scratch locals — one pool per wat type (i32 / i64 / f32 /
	// f64) sized to the maximum count of that type at any single
	// construction site. Per-construction emit then names temps
	// `__cap_<wat-type>_<idx-within-type>` so type-mismatched stores
	// can't happen (captured f64 lands in an f64 scratch, captured
	// i32 in an i32 scratch, etc).
	capPool := scanCapturePool(irFn.Ops, g)
	if capPool.any {
		g.line("(local $__cl_scratch i32)")
		g.line("(local $__env_scratch i32)")
		for i := 0; i < capPool.i32; i++ {
			g.linef("(local $__cap_i32_%d i32)", i)
		}
		for i := 0; i < capPool.i64; i++ {
			g.linef("(local $__cap_i64_%d i64)", i)
		}
		for i := 0; i < capPool.f32; i++ {
			g.linef("(local $__cap_f32_%d f32)", i)
		}
		for i := 0; i < capPool.f64; i++ {
			g.linef("(local $__cap_f64_%d f64)", i)
		}
	}
	// Indirect calls under the closure ABI need a scratch to hold the
	// closure pointer while we tear it apart into env+fn_idx.
	if g.needsClosures && containsIndirectCall(irFn.Ops) {
		g.line("(local $__call_scratch i32)")
	}

	// Walk the IR ops, emitting one (or a small block of) WAT lines
	// per op.
	for i := range irFn.Ops {
		if err := g.emitOp(irFn, i); err != nil {
			return err
		}
	}

	// Implicit return-value padding so the validator stays happy when
	// the body falls off the end without a final return.
	if !ast.Equal(fn.ReturnType, ast.VoidType{}) && !endsWithReturn(irFn.Ops) {
		if ft, isFloat := fn.ReturnType.(ast.FloatType); isFloat {
			if ft.NormalWidth() == 64 {
				g.line("f64.const 0")
			} else {
				g.line("f32.const 0")
			}
		} else {
			g.line("i32.const 0")
		}
	}

	g.indent--
	g.line(")")
	return nil
}

// maxClosureCaptures returns the largest capture count of any
// OpMakeClosure / OpMakeEnv in ops, or -1 if neither appears.
// Both ops emit through the same wat path and need the same
// per-capture scratch locals.
func maxClosureCaptures(ops []ir.Op) int {
	max := -1
	for _, op := range ops {
		if (op.Kind == ir.OpMakeClosure || op.Kind == ir.OpMakeEnv) && int(op.I32) > max {
			max = int(op.I32)
		}
	}
	return max
}

// containsIndirectCall reports whether any OpCallIndirect appears in
// ops. The IR-driven emitter declares a `$__call_scratch` local only
// when it might use it.
func containsIndirectCall(ops []ir.Op) bool {
	for _, op := range ops {
		if op.Kind == ir.OpCallIndirect {
			return true
		}
	}
	return false
}

// endsWithReturn reports whether ops finishes with a return op, so we
// know whether to pad the body with a synthetic zero.
func endsWithReturn(ops []ir.Op) bool {
	if len(ops) == 0 {
		return false
	}
	switch ops[len(ops)-1].Kind {
	case ir.OpReturn, ir.OpReturnVoid:
		return true
	}
	return false
}

// slotName returns the WAT local name for IR slot index idx in irFn.
// Params come first, then user locals, then `__scratch_N` for any
// synthetic slot the lowering pass conjured.
func slotName(fn *ast.FuncDecl, irFn *ir.Func, idx int32) string {
	if int(idx) < len(fn.Params) {
		return fn.Params[idx].Name
	}
	idx -= int32(len(fn.Params))
	if int(idx) < len(irFn.Locals) {
		return irFn.Locals[idx].Name
	}
	idx -= int32(len(irFn.Locals))
	return fmt.Sprintf("__scratch_%d", idx)
}

// blockTypeSuffix returns the `(result T)` clause for a structured
// block / loop / if op, or "" for a void block.
func blockTypeSuffix(bt int32) string {
	switch bt {
	case ir.BlockTypeI32:
		return " (result i32)"
	case ir.BlockTypeF32:
		return " (result f32)"
	case ir.BlockTypeI64:
		return " (result i64)"
	case ir.BlockTypeF64:
		return " (result f64)"
	}
	return ""
}

// emitOp translates one IR op to the matching WAT lines. The opIndex
// argument is used for ops that need to look ahead at sibling ops
// (none today, but the signature gives us room for future passes).
func (g *generator) emitOp(irFn *ir.Func, opIndex int) error {
	op := irFn.Ops[opIndex]
	// intPrefix returns "i32" or "i64" based on the op's Width
	// annotation. Width=0 falls back to i32 so older IR shapes
	// (and synthetic ops we emit without a width) keep working.
	intPrefix := func() string {
		if op.Width == 64 {
			return "i64"
		}
		return "i32"
	}
	// floatPrefix mirrors intPrefix for f32 / f64 ops. Width=0
	// keeps the historical f32 default so non-float-specific code
	// paths that emit float ops without setting Width stay
	// correct.
	floatPrefix := func() string {
		if op.Width == 64 {
			return "f64"
		}
		return "f32"
	}
	// signSuffix returns "_s" for signed ops and "_u" for
	// unsigned. Used by div / rem / shr / comparison ops where
	// the wasm op set differs by signedness.
	signSuffix := func() string {
		if op.Unsigned {
			return "_u"
		}
		return "_s"
	}
	switch op.Kind {
	case ir.OpConstI32:
		g.linef("i32.const %d", op.I32)
	case ir.OpConstI64:
		g.linef("i64.const %d", op.I64)
	case ir.OpExtendI32S:
		g.line("i64.extend_i32_s")
	case ir.OpExtendI32U:
		g.line("i64.extend_i32_u")
	case ir.OpWrapI64:
		g.line("i32.wrap_i64")
	case ir.OpFPromoteF32:
		g.line("f64.promote_f32")
	case ir.OpFDemoteF64:
		g.line("f32.demote_f64")
	case ir.OpSignExtend8:
		g.line("i32.extend8_s")
	case ir.OpSignExtend16:
		g.line("i32.extend16_s")
	case ir.OpFConvertI32:
		// Width selects f32 vs f64; Unsigned selects _u vs _s.
		w := op.Width
		if w == 0 {
			w = 32
		}
		suf := "_s"
		if op.Unsigned {
			suf = "_u"
		}
		if w == 64 {
			g.line("f64.convert_i32" + suf)
		} else {
			g.line("f32.convert_i32" + suf)
		}
	case ir.OpFConvertI64:
		w := op.Width
		if w == 0 {
			w = 32
		}
		suf := "_s"
		if op.Unsigned {
			suf = "_u"
		}
		if w == 64 {
			g.line("f64.convert_i64" + suf)
		} else {
			g.line("f32.convert_i64" + suf)
		}
	case ir.OpITruncF32:
		w := op.Width
		if w == 0 {
			w = 32
		}
		suf := "_s"
		if op.Unsigned {
			suf = "_u"
		}
		if w == 64 {
			g.line("i64.trunc_sat_f32" + suf)
		} else {
			g.line("i32.trunc_sat_f32" + suf)
		}
	case ir.OpITruncF64:
		w := op.Width
		if w == 0 {
			w = 32
		}
		suf := "_s"
		if op.Unsigned {
			suf = "_u"
		}
		if w == 64 {
			g.line("i64.trunc_sat_f64" + suf)
		} else {
			g.line("i32.trunc_sat_f64" + suf)
		}
	case ir.OpConstF32:
		g.linef("f32.const %g", op.F32)
	case ir.OpConstF64:
		g.linef("f64.const %g", op.F64)
	case ir.OpConstStr:
		g.linef("i32.const %d", g.internString(op.Str))
	case ir.OpConstFunc:
		// In closure mode, function values are static cell pointers;
		// in legacy mode they're bare table indices. Both reach into
		// tableIndex — funcIndex (position in prog.Funcs) is wrong
		// for legacy mode whenever the table doesn't include every
		// declared function, since call_indirect dispatches on the
		// table position, not the source position.
		ti, ok := g.tableIndex[op.Str]
		if !ok {
			return fmt.Errorf("wasm/ir: function %q not in table", op.Str)
		}
		if g.needsClosures {
			g.linef("i32.const %d", g.closuresBase+8*ti)
		} else {
			g.linef("i32.const %d", ti)
		}
	case ir.OpLoadLocal:
		g.linef("local.get $%s", slotName(g.current, irFn, op.I32))
	case ir.OpStoreLocal:
		g.linef("local.set $%s", slotName(g.current, irFn, op.I32))
	case ir.OpTeeLocal:
		g.linef("local.tee $%s", slotName(g.current, irFn, op.I32))
	case ir.OpLoadGlobal:
		g.linef("global.get $state_%s", op.Str)
	case ir.OpStoreGlobal:
		g.linef("global.set $state_%s", op.Str)
	case ir.OpPersistentSet:
		// Toggle the state-allocator-mode flag. The wat shim
		// `__lang_set_persistent_mode(flag)` writes the new mode
		// to mem[48] and returns the previous mode so callers
		// can restore on exit. I32 carries the new mode (1 =
		// persistent, 0 = arena).
		g.linef("i32.const %d", op.I32)
		g.line("call $__lang_set_persistent_mode")
	case ir.OpPersistentRestore:
		// Pop the previously-saved mode off the operand stack
		// and write it back through the same wat shim. The
		// shim's return value (the now-replaced mode) is
		// discarded — restoration happens once per save site.
		g.line("call $__lang_set_persistent_mode")
		g.line("drop")
	case ir.OpAdd:
		g.linef("%s.add", intPrefix())
	case ir.OpSub:
		g.linef("%s.sub", intPrefix())
	case ir.OpMul:
		g.linef("%s.mul", intPrefix())
	case ir.OpDivS:
		g.linef("%s.div%s", intPrefix(), signSuffix())
	case ir.OpRemS:
		g.linef("%s.rem%s", intPrefix(), signSuffix())
	case ir.OpAnd:
		g.linef("%s.and", intPrefix())
	case ir.OpOr:
		g.linef("%s.or", intPrefix())
	case ir.OpXor:
		g.linef("%s.xor", intPrefix())
	case ir.OpShl:
		g.linef("%s.shl", intPrefix())
	case ir.OpShrS:
		g.linef("%s.shr%s", intPrefix(), signSuffix())
	case ir.OpNot:
		g.linef("%s.eqz", intPrefix())
	case ir.OpEq:
		g.linef("%s.eq", intPrefix())
	case ir.OpNe:
		g.linef("%s.ne", intPrefix())
	case ir.OpLtS:
		g.linef("%s.lt%s", intPrefix(), signSuffix())
	case ir.OpLeS:
		g.linef("%s.le%s", intPrefix(), signSuffix())
	case ir.OpGtS:
		g.linef("%s.gt%s", intPrefix(), signSuffix())
	case ir.OpGeS:
		g.linef("%s.ge%s", intPrefix(), signSuffix())
	case ir.OpFAdd:
		g.linef("%s.add", floatPrefix())
	case ir.OpFSub:
		g.linef("%s.sub", floatPrefix())
	case ir.OpFMul:
		g.linef("%s.mul", floatPrefix())
	case ir.OpFDiv:
		g.linef("%s.div", floatPrefix())
	case ir.OpFNeg:
		g.linef("%s.neg", floatPrefix())
	case ir.OpFEq:
		g.linef("%s.eq", floatPrefix())
	case ir.OpFNe:
		g.linef("%s.ne", floatPrefix())
	case ir.OpFLt:
		g.linef("%s.lt", floatPrefix())
	case ir.OpFLe:
		g.linef("%s.le", floatPrefix())
	case ir.OpFGt:
		g.linef("%s.gt", floatPrefix())
	case ir.OpFGe:
		g.linef("%s.ge", floatPrefix())
	case ir.OpLoad:
		g.linef("%s.load", intPrefix())
	case ir.OpStore:
		g.linef("%s.store", intPrefix())
	case ir.OpFLoad:
		g.linef("%s.load", floatPrefix())
	case ir.OpFStore:
		g.linef("%s.store", floatPrefix())
	case ir.OpLoadByte:
		g.line("i32.load8_u")
	case ir.OpStoreI8:
		g.line("i32.store8")
	case ir.OpStoreI16:
		g.line("i32.store16")
	case ir.OpLoadI8S:
		g.line("i32.load8_s")
	case ir.OpLoadI16U:
		g.line("i32.load16_u")
	case ir.OpLoadI16S:
		g.line("i32.load16_s")
	case ir.OpAlloc:
		g.line("call $__lang_alloc")
	case ir.OpStrEq:
		g.line("call $__str_eq")
	case ir.OpStrConcat:
		g.line("call $__str_concat")
	case ir.OpBlock:
		g.linef("block%s", blockTypeSuffix(op.I32))
		g.indent++
	case ir.OpLoop:
		g.linef("loop%s", blockTypeSuffix(op.I32))
		g.indent++
	case ir.OpIf:
		g.linef("if%s", blockTypeSuffix(op.I32))
		g.indent++
	case ir.OpElse:
		g.indent--
		g.line("else")
		g.indent++
	case ir.OpEnd:
		g.indent--
		g.line("end")
	case ir.OpBr:
		g.linef("br %d", op.I32)
	case ir.OpBrIf:
		g.linef("br_if %d", op.I32)
	case ir.OpDrop:
		g.line("drop")
	case ir.OpReturn, ir.OpReturnVoid:
		g.line("return")
	case ir.OpCallDirect:
		// Top-level user functions in the closure ABI take a
		// trailing __env i32 — pass 0 since the call is direct.
		_, isUser := g.funcIndex[op.Str]
		if g.needsClosures && isUser && g.inTable[op.Str] {
			g.line("i32.const 0")
		}
		name := op.Str
		if dst, ok := codegenAliasMap[name]; ok {
			name = dst
		}
		g.linef("call $%s", name)
	case ir.OpCallIndirect:
		if op.Sig == nil {
			return fmt.Errorf("wasm/ir: OpCallIndirect missing sig")
		}
		tIdx := g.recordSig(op.Sig)
		if g.needsClosures {
			// Stack at this point: [args..., closure_ptr]. We need
			// [args..., env_ptr, fn_idx] for call_indirect.
			g.line("local.set $__call_scratch")
			g.line("local.get $__call_scratch")
			g.line("i32.const 4")
			g.line("i32.add")
			g.line("i32.load")
			g.line("local.get $__call_scratch")
			g.line("i32.load")
		}
		g.linef("call_indirect (type $t%d)", tIdx)
	case ir.OpCallClosureDirect:
		// Defunctionalised closure call: caller already
		// pushed (args..., env_ptr) onto the stack — just
		// dispatch directly to the hoisted target. No
		// `i32.const 0` env stub like OpCallDirect's
		// table-callee path emits, since the env_ptr is
		// already in place from the inlined closure-pair
		// load the defunctionalise pass synthesised.
		g.linef("call $%s", op.Str)
	case ir.OpMakeClosure, ir.OpMakeEnv:
		return g.emitMakeClosureFromIR(op)
	default:
		return fmt.Errorf("wasm/ir: unsupported op %s", op.Kind)
	}
	return nil
}

// emitMakeClosureFromIR consumes the N captures from the top of the
// captureSlotSize returns the env-block slot footprint for a
// capture of type `t`. Mirrors closureconv.captureSlotSize so
// the per-capture offsets the IR encodes line up with the
// stores codegen emits here. 4-byte default; 8 bytes for the
// wide types (i64 / u64 / f64).
func captureSlotSize(t ast.Type) int {
	if ast.ElemSizeBytes(t) == 8 {
		return 8
	}
	return 4
}

// captureWatTypeKind classifies a capture's lang type into the
// wat type the temp scratch must be declared as. Pointer-shaped
// types and sub-i32 ints both lower to i32 at the wat layer;
// only the four primitive wasm types matter here.
type captureWatType int

const (
	capI32 captureWatType = iota
	capI64
	capF32
	capF64
)

func captureWatKind(t ast.Type) captureWatType {
	switch x := t.(type) {
	case ast.FloatType:
		if x.NormalWidth() == 64 {
			return capF64
		}
		return capF32
	case ast.NumberType:
		if x.NormalWidth() == 64 {
			return capI64
		}
	}
	return capI32
}

// capPoolCounts records the per-type max temp count needed
// across all closure-construction sites in a function. Each
// site uses a fresh count per type starting from 0, so the
// pool size is the worst-case sum at any single site.
type capPoolCounts struct {
	i32, i64, f32, f64 int
	any                bool
}

// scanCapturePool walks the IR ops and computes per-type max
// scratch needs. For each OpMakeClosure / OpMakeEnv, look up
// the hoisted target's Captures list (already type-stamped by
// closureconv) and tally per-wat-type counts at that site,
// then update the global maxima.
func scanCapturePool(ops []ir.Op, g *generator) capPoolCounts {
	var pool capPoolCounts
	for _, op := range ops {
		if op.Kind != ir.OpMakeClosure && op.Kind != ir.OpMakeEnv {
			continue
		}
		pool.any = true
		hoisted := g.lookupFunc(op.Str)
		if hoisted == nil {
			continue
		}
		var site capPoolCounts
		for _, capParam := range hoisted.Captures {
			switch captureWatKind(capParam.Type) {
			case capI32:
				site.i32++
			case capI64:
				site.i64++
			case capF32:
				site.f32++
			case capF64:
				site.f64++
			}
		}
		if site.i32 > pool.i32 {
			pool.i32 = site.i32
		}
		if site.i64 > pool.i64 {
			pool.i64 = site.i64
		}
		if site.f32 > pool.f32 {
			pool.f32 = site.f32
		}
		if site.f64 > pool.f64 {
			pool.f64 = site.f64
		}
	}
	return pool
}

// captureStoreOpAndSize picks the wat store op + slot byte
// width for storing a capture's value into the env block. The
// load side is symmetric and lives in the IR's CaptureRef case
// (uses payloadLoadOp's per-width dispatch).
func captureStoreOpAndSize(t ast.Type) (string, int) {
	switch x := t.(type) {
	case ast.FloatType:
		if x.NormalWidth() == 64 {
			return "f64.store", 8
		}
		return "f32.store", 4
	case ast.NumberType:
		if x.NormalWidth() == 64 {
			return "i64.store", 8
		}
	}
	// 4-byte default — i32 / u32 / sub-i32 / pointers all
	// fit. Sub-i32 captures get padded up so a later
	// 8-byte slot stays aligned-enough for wasm's
	// (alignment-hint-less) i64/f64 ops.
	return "i32.store", 4
}

// stack (in reverse, into per-capture temps), allocates an env block
// and a closure pair `{fn_idx, env_ptr}`, and pushes the closure
// pointer. Per-capture types come from the hoisted FuncDecl's
// Captures list, which closureconv populated with the right ast.Type
// for each captured outer-scope variable.
func (g *generator) emitMakeClosureFromIR(op ir.Op) error {
	envOnly := op.Kind == ir.OpMakeEnv
	if !envOnly {
		if _, ok := g.tableIndex[op.Str]; !ok {
			return fmt.Errorf("wasm/ir: closure target %q not in funcref table", op.Str)
		}
	}
	hoisted := g.lookupFunc(op.Str)
	if hoisted == nil {
		return fmt.Errorf("wasm/ir: closure target %q not found in program", op.Str)
	}
	n := int(op.I32)
	if n != len(hoisted.Captures) {
		return fmt.Errorf("wasm/ir: closure %q expects %d captures, got %d",
			op.Str, len(hoisted.Captures), n)
	}

	// Compute typed-temp names per capture slot. We need names
	// up front because we drain the stack in REVERSE order
	// (top-of-stack is the last capture) but emit stores in
	// FORWARD order. Each slot's temp uses the wat-type pool
	// declared at the function prelude (`__cap_<wat-type>_<n>`),
	// where <n> is the capture's index within its type at this
	// site. Two slots of the same wat type at the same site
	// take consecutive `<n>` values.
	tempNames := make([]string, n)
	{
		var c capPoolCounts
		for i, capParam := range hoisted.Captures {
			switch captureWatKind(capParam.Type) {
			case capI32:
				tempNames[i] = fmt.Sprintf("$__cap_i32_%d", c.i32)
				c.i32++
			case capI64:
				tempNames[i] = fmt.Sprintf("$__cap_i64_%d", c.i64)
				c.i64++
			case capF32:
				tempNames[i] = fmt.Sprintf("$__cap_f32_%d", c.f32)
				c.f32++
			case capF64:
				tempNames[i] = fmt.Sprintf("$__cap_f64_%d", c.f64)
				c.f64++
			}
		}
	}

	// Pop captures into typed temps so we can rebind them to env
	// offsets in declaration order. The top of stack is the LAST
	// capture, so we pop from N-1 down to 0.
	for i := n - 1; i >= 0; i-- {
		g.linef("local.set %s", tempNames[i])
	}

	// Allocate the env block when there are captures and stash its
	// pointer in $__env_scratch. Per-capture slot stride: 4 bytes
	// default (pointer / sub-i32 / i32 / f32); 8 bytes for i64 /
	// u64 / f64 so the capture's full bit pattern survives.
	// Sub-i32 (u8 / i8 / u16 / i16) uses a 4-byte slot — the
	// corresponding closureconv offset accumulator pads to match.
	if n > 0 {
		envSize := 0
		for _, capParam := range hoisted.Captures {
			envSize += captureSlotSize(capParam.Type)
		}
		g.linef("i32.const %d", envSize)
		g.line("call $__lang_alloc")
		g.line("local.set $__env_scratch")
		off := 0
		for i, capParam := range hoisted.Captures {
			g.line("local.get $__env_scratch")
			if off > 0 {
				g.linef("i32.const %d", off)
				g.line("i32.add")
			}
			g.linef("local.get %s", tempNames[i])
			storeOp, slot := captureStoreOpAndSize(capParam.Type)
			g.line(storeOp)
			off += slot
		}
	}

	if envOnly {
		// Push env_ptr (or 0 when there are no captures). The
		// pair allocation is skipped — every reader of this
		// closure was rewritten by Defunctionalise +
		// ElideClosurePair to consume env_ptr directly.
		if n > 0 {
			g.line("local.get $__env_scratch")
		} else {
			g.line("i32.const 0")
		}
		return nil
	}

	tIdx := g.tableIndex[op.Str]
	// Allocate the 8-byte closure pair. Use local.tee so the result
	// stays on the stack for the trailing fn_idx store.
	g.line("i32.const 8")
	g.line("call $__lang_alloc")
	g.line("local.tee $__cl_scratch")
	g.linef("i32.const %d", tIdx)
	g.line("i32.store") // fn_idx at +0

	// env_ptr at +4: the env_scratch pointer we built above, or 0
	// when there are no captures.
	g.line("local.get $__cl_scratch")
	g.line("i32.const 4")
	g.line("i32.add")
	if n > 0 {
		g.line("local.get $__env_scratch")
	} else {
		g.line("i32.const 0")
	}
	g.line("i32.store")

	// Push the closure pointer as the expression's value.
	g.line("local.get $__cl_scratch")
	return nil
}

// lookupFunc returns the FuncDecl named name in g's program-equivalent
// view. emitFuncFromIR has access to one FuncDecl at a time via
// g.current; for cross-function lookups we need the whole list,
// which the generator keeps via funcIndex's keys + a small scan.
//
// In practice we only need this for closure targets — those are the
// hoisted `__closure_*` functions, all present in the parent program.
// To avoid threading the program through the generator, EmitFromIR
// stashes a name→FuncDecl map on g.funcDecls before walking bodies.
func (g *generator) lookupFunc(name string) *ast.FuncDecl {
	return g.funcDecls[name]
}
