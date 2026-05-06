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
	if len(prog.Funcs) != len(ip.Funcs) {
		return "", fmt.Errorf("wasm: prog has %d funcs, ir has %d", len(prog.Funcs), len(ip.Funcs))
	}
	g := &generator{
		info:              info,
		origTopLevelCount: countOrigTopLevel(prog),
		stringPool:        map[string]int{},
		funcIndex:         map[string]int{},
		sigIndex:          map[string]int{},
		inTable:           map[string]bool{},
		funcDecls:         map[string]*ast.FuncDecl{},
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
	g.scanForIndirectCalls(prog)
	g.scanForStringEq(prog)
	g.scanForStringConcat(prog)
	g.scanForBoundsCheck(prog)
	if len(prog.Funcs) > g.origTopLevelCount {
		g.needsClosures = true
		g.needsFuncTable = true
		g.needsRuntime = true
		g.needsArrays = true
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

	if g.needsRuntime {
		g.emitRuntimePreamble()
	}

	for i, sig := range g.indirectSigs {
		g.linef("(type $t%d %s)", i, g.watFuncType(sig))
	}

	for i, fn := range prog.Funcs {
		if err := g.emitFuncFromIR(fn, ip.Funcs[i]); err != nil {
			return "", err
		}
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
	// Synthetic i32 scratches the IR conjured for ArrayLit / StructLit
	// / Switch / closure helpers. They're addressed by index just like
	// user vars; we name them deterministically so WAT validation has
	// something to point at.
	for i := int32(0); i < irFn.NumScratch; i++ {
		g.linef("(local $__scratch_%d i32)", i)
	}
	// Closure-construction helpers, if any OpMakeClosure appears in
	// the body. The capture-temp count is the max captures across all
	// MakeClosure ops in the function — they're popped from the stack
	// into temps before the env block is built.
	maxCaps := maxClosureCaptures(irFn.Ops)
	if maxCaps >= 0 {
		g.line("(local $__cl_scratch i32)")
		g.line("(local $__env_scratch i32)")
		for i := 0; i < maxCaps; i++ {
			g.linef("(local $__cap_%d i32)", i)
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

// maxClosureCaptures returns the largest capture count of any
// OpMakeClosure in ops, or -1 if there are no MakeClosure ops.
func maxClosureCaptures(ops []ir.Op) int {
	max := -1
	for _, op := range ops {
		if op.Kind == ir.OpMakeClosure && int(op.I32) > max {
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
	}
	return ""
}

// emitOp translates one IR op to the matching WAT lines. The opIndex
// argument is used for ops that need to look ahead at sibling ops
// (none today, but the signature gives us room for future passes).
func (g *generator) emitOp(irFn *ir.Func, opIndex int) error {
	op := irFn.Ops[opIndex]
	switch op.Kind {
	case ir.OpConstI32:
		g.linef("i32.const %d", op.I32)
	case ir.OpConstF32:
		g.linef("f32.const %g", op.F32)
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
	case ir.OpAdd:
		g.line("i32.add")
	case ir.OpSub:
		g.line("i32.sub")
	case ir.OpMul:
		g.line("i32.mul")
	case ir.OpDivS:
		g.line("i32.div_s")
	case ir.OpRemS:
		g.line("i32.rem_s")
	case ir.OpAnd:
		g.line("i32.and")
	case ir.OpOr:
		g.line("i32.or")
	case ir.OpXor:
		g.line("i32.xor")
	case ir.OpShl:
		g.line("i32.shl")
	case ir.OpShrS:
		g.line("i32.shr_s")
	case ir.OpNot:
		g.line("i32.eqz")
	case ir.OpEq:
		g.line("i32.eq")
	case ir.OpNe:
		g.line("i32.ne")
	case ir.OpLtS:
		g.line("i32.lt_s")
	case ir.OpLeS:
		g.line("i32.le_s")
	case ir.OpGtS:
		g.line("i32.gt_s")
	case ir.OpGeS:
		g.line("i32.ge_s")
	case ir.OpFAdd:
		g.line("f32.add")
	case ir.OpFSub:
		g.line("f32.sub")
	case ir.OpFMul:
		g.line("f32.mul")
	case ir.OpFDiv:
		g.line("f32.div")
	case ir.OpFNeg:
		g.line("f32.neg")
	case ir.OpFEq:
		g.line("f32.eq")
	case ir.OpFNe:
		g.line("f32.ne")
	case ir.OpFLt:
		g.line("f32.lt")
	case ir.OpFLe:
		g.line("f32.le")
	case ir.OpFGt:
		g.line("f32.gt")
	case ir.OpFGe:
		g.line("f32.ge")
	case ir.OpLoad:
		g.line("i32.load")
	case ir.OpStore:
		g.line("i32.store")
	case ir.OpFLoad:
		g.line("f32.load")
	case ir.OpFStore:
		g.line("f32.store")
	case ir.OpLoadByte:
		g.line("i32.load8_u")
	case ir.OpStoreI8:
		g.line("i32.store8")
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
		g.linef("call $%s", op.Str)
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
	case ir.OpMakeClosure:
		return g.emitMakeClosureFromIR(op)
	default:
		return fmt.Errorf("wasm/ir: unsupported op %s", op.Kind)
	}
	return nil
}

// emitMakeClosureFromIR consumes the N captures from the top of the
// stack (in reverse, into per-capture temps), allocates an env block
// and a closure pair `{fn_idx, env_ptr}`, and pushes the closure
// pointer. Per-capture types come from the hoisted FuncDecl's
// Captures list, which closureconv populated with the right ast.Type
// for each captured outer-scope variable.
func (g *generator) emitMakeClosureFromIR(op ir.Op) error {
	tIdx, ok := g.tableIndex[op.Str]
	if !ok {
		return fmt.Errorf("wasm/ir: closure target %q not in funcref table", op.Str)
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

	// Pop captures into temps so we can rebind them to env offsets in
	// declaration order. The top of stack is the LAST capture, so we
	// pop from N-1 down to 0.
	for i := n - 1; i >= 0; i-- {
		g.linef("local.set $__cap_%d", i)
	}

	// Allocate the env block when there are captures and stash its
	// pointer in $__env_scratch.
	if n > 0 {
		g.linef("i32.const %d", n*4)
		g.line("call $__lang_alloc")
		g.line("local.set $__env_scratch")
		for i, capParam := range hoisted.Captures {
			g.line("local.get $__env_scratch")
			if i > 0 {
				g.linef("i32.const %d", i*4)
				g.line("i32.add")
			}
			g.linef("local.get $__cap_%d", i)
			if _, isFloat := capParam.Type.(ast.FloatType); isFloat {
				g.line("f32.store")
			} else {
				g.line("i32.store")
			}
		}
	}

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
