// Package wasmbin is a binary WebAssembly emitter that consumes the
// shared ir.Program and produces a core module's bytes directly,
// without going through WAT text. Sits next to internal/codegen/wasm
// (the WAT path) during the cutover — both share the lowering and IR
// optimisation pipeline, so a feature added to ir.Lower lights up
// here automatically once the corresponding op handler is wired.
//
// The aim is for this package to fully replace the WAT emitter and
// the `wasm-tools parse` shell-out it depends on. Each PR lands
// another slice of op coverage; the package is exercised via
// `lang -target wasm-bin -o prog.wasm prog.lang` end-to-end.
//
// Current op coverage:
//
//   - Constants: i32 / i64 / f32 / f64 / string (inline + heap).
//   - Integer arithmetic, bitwise, comparison (signed + unsigned).
//   - Logical not (i32.eqz).
//   - Float arithmetic + comparison.
//   - Type conversions: extend / wrap / trunc / convert / sign-
//     extend (post-MVP) / reinterpret / promote / demote.
//   - Locals: param + declared + scratch, load / store / tee.
//     String-typed slots fan out to two wasm slots via the
//     two-word (data, len) ABI.
//   - Control flow: block / loop / if / else / end / br / br_if.
//   - Memory: linear-memory section gating, load / store across
//     every width incl. sub-i32 signed / unsigned variants;
//     two-word OpLoad / OpStore WidthString.
//   - Function calls: direct, indirect (via funcref table),
//     closure-direct (env_ptr already on stack). Static
//     closure-pair pointers via OpConstFunc.
//   - String runtime: __lang_str_len, __lang_str_byte (SSO
//     seam), __str_eq, __str_concat.
//   - Bump allocator: __lang_alloc, cursor at memory[40], pages
//     grow on demand.
//   - WASI: wasi_snapshot_preview1.fd_write import + a
//     __lang_print helper that copies the string into a fresh
//     buffer (inline-form aware) and writes one iovec.
//   - Enum dispatch: OpEnumSentinel + OpMatchTag.
//   - Pair-return ABI: OpReturnPair, OpMakeSomeI32 /
//     OpMakeNoneI32 / OpMakeOkI32 / OpMakeErrI32, OpCallDirectPair.
//
// Still out of scope (returns an `unsupported op` error):
//
//   - OpMakeClosure / OpMakeEnv (heap-allocated closures with
//     captures — the per-capture type info isn't carried at the
//     IR layer, so wasmbin would need ast access).
//   - Preview-2 component wrapping for the wasm-bin CLI target
//     (currently emits raw core modules).
package wasmbin

import (
	"fmt"
	"math"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/langstring"
	"github.com/jakechampion/lang/internal/wasm/convert"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/imports"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/module"
	"github.com/jakechampion/lang/internal/wasm/numeric"
	"github.com/jakechampion/lang/internal/wasm/sections"
)

// EmitOptions tunes module-level structure. The zero value matches
// what Emit produced before these knobs were added.
type EmitOptions struct {
	// ForceMemorySection unconditionally emits the linear memory
	// + its export. Default behaviour gates memory on actual use
	// (anyMemoryOp || alloc-helper-needed). Callers that intend
	// to wrap the bytes in a preview-2 component set this true
	// so the WASI adapter's env::memory import is satisfied.
	ForceMemorySection bool
	// SynthStart synthesises a `_start` wrapper that calls
	// `main`, drops any i32 result, and is exported as
	// `_start`. Preview-1's `wasi:cli/run.run` glue in the
	// adapter dispatches to `_start` as the command entry, so
	// preview-2 wrapping needs this on.
	SynthStart bool
}

// Emit is EmitWithOptions with the zero-value options.
func Emit(prog *ir.Program) ([]byte, error) {
	return EmitWithOptions(prog, EmitOptions{})
}

// EmitWithOptions produces a wasm core module's bytes for the
// given IR program. Every function in prog.Funcs is added to the
// module and exported under its IR name. Function order is
// preserved — callers downstream rely on funcidx being assigned
// in declaration order.
//
// Returns an error if any function uses an op or operand type the
// current slice doesn't support.
func EmitWithOptions(prog *ir.Program, opts EmitOptions) ([]byte, error) {
	m := module.New()

	// Build a name → funcidx map so OpCallDirect can resolve
	// its callee. Funcidx is the declaration index — which
	// matches FunctionTypeidxs / CodeBodies / ExportIdxs since
	// every function is also exported by name. Imports would
	// shift this offset; the binary path doesn't emit imports
	// yet (the WASI / preview-2 wiring lives in a later slice).
	//
	// Synthetic runtime-helper functions (e.g. __lang_str_len)
	// get appended after the user functions in the same
	// numbering space; their entries land in this map once
	// the runtime-needs scan below has decided which helpers
	// the program actually uses.
	// Imports first: imported functions occupy funcidx 0..N-1
	// in the module's funcidx namespace; user functions and
	// synthetic helpers shift up by N. Today the only imports
	// come from runtime needs (e.g. WASI fd_write for print).
	helpers := scanRuntimeHelpers(prog)
	importNeeds := scanImports(prog, helpers)

	funcIdx := make(map[string]uint32, len(prog.Funcs)+len(helpers.order)+len(importNeeds.order))
	nextFuncIdx := uint32(0)
	for _, name := range importNeeds.order {
		funcIdx[name] = nextFuncIdx
		nextFuncIdx++
	}
	for i, fn := range prog.Funcs {
		funcIdx[fn.Name] = uint32(int(nextFuncIdx) + i)
	}
	nextFuncIdx += uint32(len(prog.Funcs))
	for _, name := range helpers.order {
		funcIdx[name] = nextFuncIdx
		nextFuncIdx++
	}

	// Type-section dedup: same param-list + result-list → same
	// typeidx. The string key joins valtype bytes; collisions
	// are impossible since valtype bytes are in 0x7c..0x7f.
	typeIdx := map[string]uint32{}
	addType := func(params, results []byte) uint32 {
		key := string(params) + "|" + string(results)
		if idx, ok := typeIdx[key]; ok {
			return idx
		}
		idx := uint32(len(m.TypeParams))
		typeIdx[key] = idx
		m.TypeParams = append(m.TypeParams, params)
		m.TypeResults = append(m.TypeResults, results)
		return idx
	}

	// Static closure-pair pool for OpConstFunc. Each unique
	// function name referenced via OpConstFunc gets an 8-byte
	// cell at closuresBase + 8*tableIdx: { fn_idx (i32 LE),
	// env_ptr=0 (i32 LE) }. OpConstFunc emits an i32.const
	// pointing at the cell; OpCallIndirect on that pointer
	// would dereference to recover (fn_idx, env_ptr).
	//
	// Cells live in the reserved low-memory window 64..1024
	// (before stringStart). Programs with up to 120 unique
	// OpConstFunc targets fit without growing into the string
	// pool.
	const closuresBase = 64
	const maxClosureCells = (1024 - closuresBase) / 8
	closureTableIdx := map[string]int{}
	var closureBytes []byte
	internClosure := func(name string) (int, error) {
		if idx, ok := closureTableIdx[name]; ok {
			return idx, nil
		}
		idx := len(closureTableIdx)
		if idx >= maxClosureCells {
			return 0, fmt.Errorf("OpConstFunc: closure-cell pool exhausted (>%d unique targets)", maxClosureCells)
		}
		closureTableIdx[name] = idx
		// Cell bytes: fn_idx LE + env_ptr=0 LE. fn_idx is the
		// post-import-shifted funcidx, which equals funcIdx[name]
		// here since the import shift is uniform.
		fnIdx := funcIdx[name]
		closureBytes = append(closureBytes,
			byte(fnIdx), byte(fnIdx>>8), byte(fnIdx>>16), byte(fnIdx>>24),
			0, 0, 0, 0)
		return idx, nil
	}

	// String interning state for OpConstStr's heap-form path.
	// Inline-form strings (≤7 bytes via langstring.FitsInlineWasm)
	// pack into the two i32.consts directly and don't visit the
	// data section. Heap-form strings get a unique offset; the
	// data section's bytes are accumulated here in declaration
	// order, with the per-entry offset stored alongside the bytes.
	// stringStart matches the WAT path's choice of 1024 so the
	// data segment doesn't collide with the low-memory pair-cells
	// the closures slice will later allocate (the WAT path uses
	// the same convention).
	stringPool := map[string]int{}
	const stringStart = 1024
	stringNextOff := stringStart
	var dataBytes []byte
	internString := func(s string) int {
		if off, ok := stringPool[s]; ok {
			return off
		}
		off := stringNextOff
		stringPool[s] = off
		dataBytes = append(dataBytes, s...)
		stringNextOff = off + len(s)
		return off
	}

	// Per-tag enum-sentinel pool. Each unique tag value gets one
	// 4-byte cell in the data segment containing the tag as i32
	// LE. OpMatchTag (i32.load at offset 0) reads the value
	// directly, so the sentinel acts as a heap-allocated
	// `[tag=N]` box without an actual heap allocation.
	enumSentinels := map[int32]int{}
	internEnumSentinel := func(tag int32) int {
		if off, ok := enumSentinels[tag]; ok {
			return off
		}
		off := stringNextOff
		enumSentinels[tag] = off
		// Append 4 LE bytes for the tag value.
		dataBytes = append(dataBytes,
			byte(tag), byte(tag>>8), byte(tag>>16), byte(tag>>24))
		stringNextOff = off + 4
		return off
	}

	closureTargets := closureTargetSet(prog)
	ctx := &emitCtx{
		funcIdx:            funcIdx,
		internString:       internString,
		internClosure:      internClosure,
		closuresBaseAddr:   closuresBase,
		internEnumSentinel: internEnumSentinel,
		closureTargets:     closureTargets,
		addSigType: func(sig *ast.FuncType) (uint32, error) {
			params := make([]byte, 0, len(sig.Params))
			for _, pt := range sig.Params {
				vt, err := valtypeFor(pt)
				if err != nil {
					return 0, err
				}
				params = append(params, vt)
			}
			results, err := resultValtypes(sig.Result)
			if err != nil {
				return 0, err
			}
			return addType(params, results), nil
		},
		addClosureSigType: func(sig *ast.FuncType) (uint32, error) {
			params := make([]byte, 0, len(sig.Params)+1)
			for _, pt := range sig.Params {
				vt, err := valtypeFor(pt)
				if err != nil {
					return 0, err
				}
				params = append(params, vt)
			}
			// Closure-target ABI: env_ptr (i32) as last param.
			params = append(params, encode.ValtypeI32)
			results, err := resultValtypes(sig.Result)
			if err != nil {
				return 0, err
			}
			return addType(params, results), nil
		},
	}

	// Table section is emitted iff the program contains any
	// indirect-call op (OpCallIndirect / OpCallClosureDirect /
	// OpConstFunc). Slice 6 includes every function in the
	// program in the funcref table at its declaration index —
	// the simplest layout that lets OpCallIndirect dispatch by
	// funcidx.
	// Import section. Each entry already has its funcidx assigned
	// (0..len(importNeeds.order)-1 above); we just need to emit
	// the descriptors. Imported function types get added to the
	// type-section dedup map up-front so the descriptor's typeidx
	// is stable.
	for _, name := range importNeeds.order {
		spec := importSpecs[name]
		tIdx := addType(spec.params, spec.results)
		m.ImportModules = append(m.ImportModules, spec.module)
		m.ImportNames = append(m.ImportNames, spec.name)
		m.ImportKinds = append(m.ImportKinds, imports.ImportFunc)
		m.ImportDescs = append(m.ImportDescs, imports.ImportDescFunc(tIdx))
	}

	if anyTableOp(prog) {
		n := uint32(len(prog.Funcs))
		m.TablePresent = true
		m.TableMin = n
		m.TableMax = -1
		// Element segment funcidxs reference the function-table
		// AFTER the imports shift. funcIdx already encodes this
		// for each user function; reuse it.
		idxs := make([]uint32, n)
		for i, fn := range prog.Funcs {
			idxs[i] = funcIdx[fn.Name]
		}
		m.ElementOffsets = []int32{0}
		m.ElementFuncidxs = [][]uint32{idxs}
		// Export the table too — useful for hosts that want to
		// poke at the slot layout. Same canonical name the WAT
		// emitter uses.
		m.ExportNames = append(m.ExportNames, "__indirect_function_table")
		m.ExportKinds = append(m.ExportKinds, sections.ExportTable)
		m.ExportIdxs = append(m.ExportIdxs, 0)
	}

	// Memory section is emitted iff any function in the program
	// touches memory (load / store / sub-width variants / fN load
	// or store) OR if any runtime helper that touches memory is
	// pulled in (__lang_alloc grows memory; __lang_str_byte and
	// transitively __str_eq emit i32.load8_u even in branches
	// that don't execute at runtime — wasm validation still
	// requires memory 0 to exist). Memory layout matches the
	// WAT path: 1 page (64 KiB) with no upper bound.
	if opts.ForceMemorySection || anyMemoryOp(prog) || helpers.set["__lang_alloc"] || helpers.set["__lang_str_byte"] || len(importNeeds.order) > 0 {
		m.MemoryPresent = true
		m.MemoryMin = 1
		m.MemoryMax = -1
		// Export the memory under the canonical name so tests
		// and host tooling can poke at it.
		m.ExportNames = append(m.ExportNames, "memory")
		m.ExportKinds = append(m.ExportKinds, sections.ExportMemory)
		m.ExportIdxs = append(m.ExportIdxs, 0)
	}

	for fnIdx, fn := range prog.Funcs {
		params, err := paramValtypes(fn.Params)
		if err != nil {
			return nil, fmt.Errorf("wasmbin: %s: %w", fn.Name, err)
		}
		if closureTargets[fn.Name] {
			// Closure-target ABI: append an i32 (env_ptr) as the
			// last param. The body doesn't have to use it; the
			// indirect-call deref always pushes one so the wasm
			// signature must accept it.
			params = append(params, encode.ValtypeI32)
		}
		var results []byte
		if prog.PairForm[fn.Name] {
			// Pair-form ABI: the wasm-level return type is the
			// 2-i32 multi-value tuple (tag, payload), regardless
			// of what IR ReturnType says. Callers reach this via
			// OpCallDirectPair.
			results = []byte{encode.ValtypeI32, encode.ValtypeI32}
		} else {
			results, err = resultValtypes(fn.ReturnType)
			if err != nil {
				return nil, fmt.Errorf("wasmbin: %s: %w", fn.Name, err)
			}
		}

		tIdx := addType(params, results)
		m.FunctionTypeidxs = append(m.FunctionTypeidxs, tIdx)
		m.ExportNames = append(m.ExportNames, fn.Name)
		m.ExportKinds = append(m.ExportKinds, sections.ExportFunc)
		// Export funcidx must match the post-import shift —
		// funcIdx[fn.Name] already encodes it.
		m.ExportIdxs = append(m.ExportIdxs, funcIdx[fn.Name])
		_ = fnIdx

		body, locals, err := emitBody(fn, ctx)
		if err != nil {
			return nil, fmt.Errorf("wasmbin: %s: %w", fn.Name, err)
		}
		m.CodeBodies = append(m.CodeBodies, inst.PutFunctionBody(nil, locals, body))
	}

	// Append runtime-helper functions. These don't get exported
	// (no entry in m.ExportNames / Kinds / Idxs) so they stay
	// private to the module. Same type-section / function-section
	// / code-section assembly as user functions.
	//
	// Bodies receive a name → funcidx map for cross-helper calls
	// (e.g. __str_eq → __lang_str_len + __lang_str_byte) so the
	// call targets are resolved at module-assembly time. funcIdx
	// already has every helper's index installed up-front by the
	// pre-scan loop above.
	helperIdxs := make(map[string]uint32, len(helpers.order))
	for _, name := range helpers.order {
		helperIdxs[name] = funcIdx[name]
	}
	for _, name := range helpers.order {
		spec := runtimeHelperSpecs[name]
		params, results := spec.params, spec.results
		tIdx := addType(params, results)
		m.FunctionTypeidxs = append(m.FunctionTypeidxs, tIdx)
		m.CodeBodies = append(m.CodeBodies, spec.body(helperIdxs))
	}

	// OpConstFunc closure-pair cells → data segment at the
	// reserved low-memory window (closuresBase=64). When present,
	// also force the memory section so the data segment has a
	// target.
	if len(closureBytes) > 0 {
		if !m.MemoryPresent {
			m.MemoryPresent = true
			m.MemoryMin = 1
			m.MemoryMax = -1
			m.ExportNames = append(m.ExportNames, "memory")
			m.ExportKinds = append(m.ExportKinds, sections.ExportMemory)
			m.ExportIdxs = append(m.ExportIdxs, 0)
		}
		m.DataOffsets = append(m.DataOffsets, int32(closuresBase))
		m.DataInits = append(m.DataInits, closureBytes)
	}

	// Heap-form strings → data segment. Even if no other op used
	// memory, the data segment requires a memory; force one in
	// that case. The single segment lives at stringStart (1024)
	// matching the WAT path so subsequent heap allocations land
	// after the literals.
	if len(dataBytes) > 0 {
		if !m.MemoryPresent {
			m.MemoryPresent = true
			m.MemoryMin = 1
			m.MemoryMax = -1
			m.ExportNames = append(m.ExportNames, "memory")
			m.ExportKinds = append(m.ExportKinds, sections.ExportMemory)
			m.ExportIdxs = append(m.ExportIdxs, 0)
		}
		m.DataOffsets = append(m.DataOffsets, int32(stringStart))
		m.DataInits = append(m.DataInits, dataBytes)
	}

	// Seed the bump cursor at memory[40] when the allocator is in
	// use. Cursor value = max(allocMinStart, end-of-string-pool)
	// rounded up to 8 bytes — matches the WAT path's choice so
	// canonical-ABI alignment expectations stay satisfied.
	if helpers.set["__lang_alloc"] {
		if !m.MemoryPresent {
			m.MemoryPresent = true
			m.MemoryMin = 1
			m.MemoryMax = -1
			m.ExportNames = append(m.ExportNames, "memory")
			m.ExportKinds = append(m.ExportKinds, sections.ExportMemory)
			m.ExportIdxs = append(m.ExportIdxs, 0)
		}
		start := stringNextOff
		if start < allocMinStart {
			start = allocMinStart
		}
		if start%8 != 0 {
			start += 8 - (start % 8)
		}
		m.DataOffsets = append(m.DataOffsets, allocCursorAddr)
		m.DataInits = append(m.DataInits, le32(int32(start)))
	}

	// Synth `_start` wrapper when requested. The body is just
	// `call $main` + optional `drop` (when main has a result)
	// + `end`. Appended after all user functions + helpers so
	// it lands at the highest funcidx; gets a fresh `() -> ()`
	// typeidx + export.
	if opts.SynthStart {
		mainIdx, ok := funcIdx["main"]
		if !ok {
			return nil, fmt.Errorf("wasmbin: SynthStart needs a `main` function")
		}
		startTIdx := addType(nil, nil) // () -> ()
		startFuncIdx := nextFuncIdx
		nextFuncIdx++
		var body []byte
		body = inst.InstCall(body, mainIdx)
		// If main returns anything, drop it. Look up its result
		// shape via the typeidx; safer than re-inferring from
		// ip.PairForm / ReturnType here. The TypeResults slice
		// is in declaration order; main's typeidx is
		// FunctionTypeidxs[main_funcidx - len(imports)].
		mainPosInFnSection := mainIdx - uint32(len(importNeeds.order))
		mainResults := m.TypeResults[m.FunctionTypeidxs[mainPosInFnSection]]
		for range mainResults {
			body = inst.InstDrop(body)
		}
		m.FunctionTypeidxs = append(m.FunctionTypeidxs, startTIdx)
		m.CodeBodies = append(m.CodeBodies, inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body))
		m.ExportNames = append(m.ExportNames, "_start")
		m.ExportKinds = append(m.ExportKinds, sections.ExportFunc)
		m.ExportIdxs = append(m.ExportIdxs, startFuncIdx)
	}

	return module.Build(m), nil
}

// le32 returns the 4-byte little-endian representation of v —
// the on-disk byte order for an i32 stored at a known offset.
func le32(v int32) []byte {
	u := uint32(v)
	return []byte{byte(u), byte(u >> 8), byte(u >> 16), byte(u >> 24)}
}

// valtypeFor maps a single ast.Type to the wasm valtype byte used to
// hold it. Only single-slot types live here; strings (two-slot ABI)
// fan out through slotValtypes / slotIsString instead.
func valtypeFor(t ast.Type) (byte, error) {
	switch v := t.(type) {
	case ast.NumberType:
		if v.NormalWidth() == 64 {
			return encode.ValtypeI64, nil
		}
		return encode.ValtypeI32, nil
	case ast.BoolType:
		return encode.ValtypeI32, nil
	case ast.FloatType:
		if v.NormalWidth() == 64 {
			return encode.ValtypeF64, nil
		}
		return encode.ValtypeF32, nil
	}
	return 0, fmt.Errorf("unsupported type %s (scalar i32/i64/f32/f64 + bool only at this seam)", t)
}

// isStringType reports whether t uses the two-word `(data, len)`
// ABI — i.e. needs 2 wasm slots when used as a param / local /
// result. Currently only ast.StringType.
func isStringType(t ast.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.(ast.StringType)
	return ok
}

// slotValtypes returns the wasm valtype sequence for an ast.Type
// used as a slot (param / local / result). Strings fan out to
// `[i32, i32]` for the two-word ABI; everything else maps to a
// single valtype via valtypeFor.
func slotValtypes(t ast.Type) ([]byte, error) {
	if isStringType(t) {
		return []byte{encode.ValtypeI32, encode.ValtypeI32}, nil
	}
	vt, err := valtypeFor(t)
	if err != nil {
		return nil, err
	}
	return []byte{vt}, nil
}

// paramValtypes returns the wasm param valtype vector for an IR
// function's parameter list. String params fan out to two wasm
// slots; everything else maps 1:1.
func paramValtypes(params []ast.Param) ([]byte, error) {
	var out []byte
	for _, p := range params {
		vts, err := slotValtypes(p.Type)
		if err != nil {
			return nil, err
		}
		out = append(out, vts...)
	}
	return out, nil
}

// resultValtypes returns the wasm result valtype vector for an IR
// function's return type. Void → empty; scalar → one slot;
// string → two slots (multi-value return for the (data, len) pair).
func resultValtypes(t ast.Type) ([]byte, error) {
	if t == nil {
		return nil, nil
	}
	if _, isVoid := t.(ast.VoidType); isVoid {
		return nil, nil
	}
	return slotValtypes(t)
}

// localValtypes returns the wasm valtype vector for an IR function's
// declared locals + scratch slots — exactly what the local-section
// preamble of the function body needs. String-typed slots fan out
// to two i32 slots `(data, len)`. Three additional i32 scratch
// slots are appended for functions that load/store strings to
// heap memory (OpLoad/OpStore with WidthString) — used by the
// two-word ABI fan-out.
func localValtypes(fn *ir.Func) ([]byte, error) {
	var out []byte
	for _, l := range fn.Locals {
		vts, err := slotValtypes(l.Type)
		if err != nil {
			return nil, err
		}
		out = append(out, vts...)
	}
	for _, s := range fn.ScratchTypes {
		vts, err := slotValtypes(s)
		if err != nil {
			return nil, err
		}
		out = append(out, vts...)
	}
	if fnNeedsStrPairScratch(fn) {
		for i := 0; i < strPairScratchSlots; i++ {
			out = append(out, encode.ValtypeI32)
		}
	}
	if fnNeedsPairMakeScratch(fn) {
		for i := 0; i < pairMakeScratchSlots; i++ {
			out = append(out, encode.ValtypeI32)
		}
	}
	for i := 0; i < closureMakeScratchSlots(fn); i++ {
		out = append(out, encode.ValtypeI32)
	}
	if fnNeedsCallIndirectScratch(fn) {
		for i := 0; i < callIndirectScratchSlots; i++ {
			out = append(out, encode.ValtypeI32)
		}
	}
	return out, nil
}

const callIndirectScratchSlots = 1

func fnNeedsCallIndirectScratch(fn *ir.Func) bool {
	for _, op := range fn.Ops {
		if op.Kind == ir.OpCallIndirect {
			return true
		}
	}
	return false
}

// strPairScratchSlots is the count of extra wasm locals appended
// to a function body when it uses OpLoad/OpStore with WidthString.
// Layout (relative to strPairScratchBase): +0 addr, +1 data, +2 len.
const strPairScratchSlots = 3

// pairMakeScratchSlots is the count of extra wasm locals appended
// when a function uses OpMakeSomeI32 / OpMakeOkI32 / OpMakeErrI32.
// The single scratch holds the payload while we push the tag
// "under" it (wasm has no swap instruction).
const pairMakeScratchSlots = 1

// fnNeedsStrPairScratch reports whether fn has any OpLoad / OpStore
// op with Width == ir.WidthString — the two-word string ABI load/
// store fan-out needs three i32 scratch locals to juggle stack
// values without losing data.
func fnNeedsStrPairScratch(fn *ir.Func) bool {
	for _, op := range fn.Ops {
		if (op.Kind == ir.OpLoad || op.Kind == ir.OpStore) && op.Width == ir.WidthString {
			return true
		}
	}
	return false
}

// fnNeedsPairMakeScratch reports whether fn has any OpMakeSomeI32 /
// OpMakeOkI32 / OpMakeErrI32. OpMakeNoneI32 doesn't need scratch
// (no input value to preserve under the tag).
func fnNeedsPairMakeScratch(fn *ir.Func) bool {
	for _, op := range fn.Ops {
		switch op.Kind {
		case ir.OpMakeSomeI32, ir.OpMakeOkI32, ir.OpMakeErrI32:
			return true
		}
	}
	return false
}

// emitCtx bundles per-program lookups shared across every op
// emitted in this build. Growing this struct is preferable to
// growing emitOp's signature for each new slice.
type emitCtx struct {
	// funcIdx maps an IR function name to its funcidx in the
	// emitted module. OpCallDirect / OpCallClosureDirect use it.
	funcIdx map[string]uint32
	// addSigType resolves a function-type signature to its
	// typeidx, lazily inserting into the type section.
	addSigType func(*ast.FuncType) (uint32, error)
	// addClosureSigType is like addSigType but appends an i32
	// (env_ptr) to params, matching the closure-target ABI
	// every OpCallIndirect dispatches through.
	addClosureSigType func(*ast.FuncType) (uint32, error)
	// internString returns the data-segment offset for the
	// heap-form bytes of s, interning so repeats share an
	// address. Used by OpConstStr.
	internString func(string) int
	// internClosure reserves a closure-pair cell for the
	// named function and returns its table-slot index. The
	// cell address is closuresBaseAddr + 8*idx.
	internClosure    func(string) (int, error)
	closuresBaseAddr int
	// internEnumSentinel reserves a 4-byte data-segment cell
	// containing tag as i32 LE, interning per unique tag.
	// Used by OpEnumSentinel — the returned offset is the
	// "heap pointer" for a payloadless enum variant.
	internEnumSentinel func(int32) int
	// fn is the current function being walked. emitBody sets and
	// clears it. Slot-aware ops (OpLoadLocal / OpStoreLocal /
	// OpTeeLocal) consult slotType(fn, op.I32) to decide whether
	// to fan out into the two-word string ABI.
	fn *ir.Func
	// strPairScratchBase is the wasm-slot index of the first of
	// three scratch i32 locals appended to fn's locals when fn
	// uses OpLoad/OpStore WidthString. Layout: +0 addr, +1 data,
	// +2 len. Zero when fnNeedsStrPairScratch(fn) is false.
	strPairScratchBase uint32
	// callIndirectScratchIdx is the wasm-slot index of the
	// scratch i32 used by OpCallIndirect to stash closure_ptr
	// while loading env_ptr + fn_idx from the pair cell.
	callIndirectScratchIdx uint32
	// closureTargets is the set of function names whose wasm
	// signature has env_ptr (i32) appended as the last param.
	// emitBody consults this when computing wasm-slot indices
	// for scratch locals.
	closureTargets map[string]bool
	// pairMakeScratchIdx is the wasm-slot index of the single
	// i32 scratch local appended when fn uses OpMakeSomeI32 /
	// OpMakeOkI32 / OpMakeErrI32. The scratch holds the payload
	// while we push the tag underneath it. Zero when not needed.
	pairMakeScratchIdx uint32
	// closureMakeScratchBase is the wasm-slot index of the
	// first of N+2 scratch i32 locals appended when fn uses
	// OpMakeClosure / OpMakeEnv. Layout: N capture stashes,
	// then env_ptr, then pair_ptr.
	closureMakeScratchBase uint32
}

// slotType returns the ast.Type of an IR slot. Layout follows the
// IR convention: params first, then declared locals, then the
// scratch slots the lowering pass conjured.
func slotType(fn *ir.Func, irIdx int32) ast.Type {
	i := int(irIdx)
	if i < len(fn.Params) {
		return fn.Params[i].Type
	}
	i -= len(fn.Params)
	if i < len(fn.Locals) {
		return fn.Locals[i].Type
	}
	i -= len(fn.Locals)
	if i < len(fn.ScratchTypes) {
		return fn.ScratchTypes[i]
	}
	return nil
}

// wasmSlotIdx translates an IR slot index to the wasm local index.
// String slots fan out to two adjacent wasm locals (data at the
// computed index, len at +1), so every preceding string slot
// shifts the result by an extra +1.
func wasmSlotIdx(fn *ir.Func, irIdx int32) uint32 {
	wasm := 0
	for j := int32(0); j < irIdx; j++ {
		wasm++
		if isStringType(slotType(fn, j)) {
			wasm++
		}
	}
	return uint32(wasm)
}

// emitBody walks fn.Ops and returns the function's body bytes plus
// its locals-preamble bytes (the latter pre-wrapped by
// inst.PutLocalsOneGroup-equivalent encoding for the declared local
// valtypes).
func emitBody(fn *ir.Func, ctx *emitCtx) (body, locals []byte, err error) {
	lvts, err := localValtypes(fn)
	if err != nil {
		return nil, nil, err
	}
	locals = encodeLocals(lvts)

	ctx.fn = fn
	// The scratch base is the wasm-slot index just past every IR
	// slot (params + locals + scratch types, accounting for string-
	// typed slots taking 2 wasm slots each). Only meaningful when
	// fnNeedsStrPairScratch(fn) is true; otherwise it's unused.
	lastIR := int32(len(fn.Params) + len(fn.Locals) + len(fn.ScratchTypes))
	ctx.strPairScratchBase = wasmSlotIdx(fn, lastIR)
	// Closure-target functions get an extra wasm slot for the
	// env_ptr param appended by the closure-target ABI. That
	// shifts everything below.
	if ctx.closureTargets[fn.Name] {
		ctx.strPairScratchBase++
	}
	// pairMake scratch sits AFTER any str-pair scratch slots.
	pairBase := ctx.strPairScratchBase
	if fnNeedsStrPairScratch(fn) {
		pairBase += strPairScratchSlots
	}
	ctx.pairMakeScratchIdx = pairBase
	// closureMake scratch sits AFTER pairMake.
	closureBase := pairBase
	if fnNeedsPairMakeScratch(fn) {
		closureBase += pairMakeScratchSlots
	}
	ctx.closureMakeScratchBase = closureBase
	// callIndirect scratch sits AFTER closureMake.
	callIndBase := closureBase + uint32(closureMakeScratchSlots(fn))
	ctx.callIndirectScratchIdx = callIndBase
	defer func() {
		ctx.fn = nil
		ctx.strPairScratchBase = 0
		ctx.pairMakeScratchIdx = 0
		ctx.closureMakeScratchBase = 0
		ctx.callIndirectScratchIdx = 0
	}()

	for opIdx, op := range fn.Ops {
		body, err = emitOp(body, op, ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("op[%d] %v: %w", opIdx, op.Kind, err)
		}
	}
	return body, locals, nil
}

// encodeLocals emits the run-length-encoded local-vec for a
// function body: groups consecutive identical valtypes into a single
// (count, valtype) record. Empty → the "no locals" encoding.
func encodeLocals(vts []byte) []byte {
	if len(vts) == 0 {
		return inst.PutLocalsEmpty(nil)
	}
	var out []byte
	// vec length prefix is the number of runs; we don't know it
	// ahead of time, so build the run-list first then prepend.
	var runs [][2]uint32 // {count, valtype}
	i := 0
	for i < len(vts) {
		j := i + 1
		for j < len(vts) && vts[j] == vts[i] {
			j++
		}
		runs = append(runs, [2]uint32{uint32(j - i), uint32(vts[i])})
		i = j
	}
	// vec(locals)
	out = appendUleb(out, uint32(len(runs)))
	for _, r := range runs {
		out = appendUleb(out, r[0])
		out = append(out, byte(r[1]))
	}
	return out
}

// emitOp translates one IR op into its wasm bytes and appends them
// to body. Op coverage grows slice-by-slice; unsupported ops
// return an error rather than emitting invalid bytes.
func emitOp(body []byte, op ir.Op, ctx *emitCtx) ([]byte, error) {
	switch op.Kind {
	case ir.OpConstI32:
		return inst.InstI32Const(body, op.I32), nil
	case ir.OpConstI64:
		return inst.InstI64Const(body, op.I64), nil
	case ir.OpConstF32:
		return inst.InstF32Const(body, math.Float32bits(op.F32)), nil
	case ir.OpConstF64:
		return inst.InstF64Const(body, math.Float64bits(op.F64)), nil

	case ir.OpConstFunc:
		// Push the address of the static closure-pair cell for
		// op.Str. The cell contains {fn_idx, env_ptr=0}.
		tableIdx, err := ctx.internClosure(op.Str)
		if err != nil {
			return nil, err
		}
		return inst.InstI32Const(body, int32(ctx.closuresBaseAddr+8*tableIdx)), nil

	case ir.OpConstStr:
		// Two-word string ABI: every OpConstStr pushes `(data, len)`
		// onto the operand stack as two i32 values.
		// Inline-form (≤7 bytes, ASCII-only via FitsInlineWasm)
		// packs into the two i32.consts directly and doesn't touch
		// the data section. Heap-form interns into the data segment
		// and emits (data_offset, length).
		if langstring.FitsInlineWasm(len(op.Str)) {
			data, length := langstring.PackInlineWasm([]byte(op.Str))
			body = inst.InstI32Const(body, int32(data))
			body = inst.InstI32Const(body, int32(length))
			return body, nil
		}
		off := ctx.internString(op.Str)
		body = inst.InstI32Const(body, int32(off))
		body = inst.InstI32Const(body, int32(len(op.Str)))
		return body, nil

	case ir.OpLoadLocal:
		idx := wasmSlotIdx(ctx.fn, op.I32)
		if isStringType(slotType(ctx.fn, op.I32)) {
			// Two-word ABI: push (data, len) in low-to-high
			// order so the stack mirrors a fresh OpConstStr.
			body = inst.InstLocalGet(body, idx)
			body = inst.InstLocalGet(body, idx+1)
			return body, nil
		}
		return inst.InstLocalGet(body, idx), nil
	case ir.OpStoreLocal:
		idx := wasmSlotIdx(ctx.fn, op.I32)
		if isStringType(slotType(ctx.fn, op.I32)) {
			// Stack: [..., data, len]. Pop len first (top of
			// stack), then data, into adjacent locals.
			body = inst.InstLocalSet(body, idx+1)
			body = inst.InstLocalSet(body, idx)
			return body, nil
		}
		return inst.InstLocalSet(body, idx), nil
	case ir.OpTeeLocal:
		idx := wasmSlotIdx(ctx.fn, op.I32)
		if isStringType(slotType(ctx.fn, op.I32)) {
			// Same as store-then-load: pop len, tee data
			// (leaves data on stack), push len back.
			body = inst.InstLocalSet(body, idx+1)
			body = inst.InstLocalTee(body, idx)
			body = inst.InstLocalGet(body, idx+1)
			return body, nil
		}
		return inst.InstLocalTee(body, idx), nil

	case ir.OpDrop:
		return inst.InstDrop(body), nil
	case ir.OpReturn:
		return inst.InstReturn(body), nil
	case ir.OpReturnVoid:
		return inst.InstReturn(body), nil

	case ir.OpBlock:
		bt, err := blocktypeByte(op.I32)
		if err != nil {
			return nil, err
		}
		return inst.InstBlockStart(body, bt), nil
	case ir.OpLoop:
		bt, err := blocktypeByte(op.I32)
		if err != nil {
			return nil, err
		}
		return inst.InstLoopStart(body, bt), nil
	case ir.OpIf:
		bt, err := blocktypeByte(op.I32)
		if err != nil {
			return nil, err
		}
		return inst.InstIfStart(body, bt), nil
	case ir.OpElse:
		return inst.InstElse(body), nil
	case ir.OpEnd:
		return inst.InstEnd(body), nil
	case ir.OpBr:
		return inst.InstBr(body, uint32(op.I32)), nil
	case ir.OpBrIf:
		return inst.InstBrIf(body, uint32(op.I32)), nil

	case ir.OpAdd:
		if op.Width == 64 {
			return numeric.InstI64Add(body), nil
		}
		return numeric.InstI32Add(body), nil
	case ir.OpSub:
		if op.Width == 64 {
			return numeric.InstI64Sub(body), nil
		}
		return numeric.InstI32Sub(body), nil
	case ir.OpMul:
		if op.Width == 64 {
			return numeric.InstI64Mul(body), nil
		}
		return numeric.InstI32Mul(body), nil
	case ir.OpDivS:
		if op.Width == 64 {
			if op.Unsigned {
				return numeric.InstI64DivU(body), nil
			}
			return numeric.InstI64DivS(body), nil
		}
		if op.Unsigned {
			return numeric.InstI32DivU(body), nil
		}
		return numeric.InstI32DivS(body), nil
	case ir.OpRemS:
		if op.Width == 64 {
			if op.Unsigned {
				return numeric.InstI64RemU(body), nil
			}
			return numeric.InstI64RemS(body), nil
		}
		if op.Unsigned {
			return numeric.InstI32RemU(body), nil
		}
		return numeric.InstI32RemS(body), nil
	case ir.OpAnd:
		if op.Width == 64 {
			return numeric.InstI64And(body), nil
		}
		return numeric.InstI32And(body), nil
	case ir.OpOr:
		if op.Width == 64 {
			return numeric.InstI64Or(body), nil
		}
		return numeric.InstI32Or(body), nil
	case ir.OpXor:
		if op.Width == 64 {
			return numeric.InstI64Xor(body), nil
		}
		return numeric.InstI32Xor(body), nil
	case ir.OpShl:
		if op.Width == 64 {
			return numeric.InstI64Shl(body), nil
		}
		return numeric.InstI32Shl(body), nil
	case ir.OpShrS:
		if op.Width == 64 {
			if op.Unsigned {
				return numeric.InstI64ShrU(body), nil
			}
			return numeric.InstI64ShrS(body), nil
		}
		if op.Unsigned {
			return numeric.InstI32ShrU(body), nil
		}
		return numeric.InstI32ShrS(body), nil
	case ir.OpNot:
		// logical not — i32.eqz; only meaningful on i32.
		return numeric.InstI32Eqz(body), nil

	case ir.OpEq:
		if op.Width == 64 {
			return numeric.InstI64Eq(body), nil
		}
		return numeric.InstI32Eq(body), nil
	case ir.OpNe:
		if op.Width == 64 {
			return numeric.InstI64Ne(body), nil
		}
		return numeric.InstI32Ne(body), nil
	case ir.OpLtS:
		if op.Width == 64 {
			if op.Unsigned {
				return numeric.InstI64LtU(body), nil
			}
			return numeric.InstI64LtS(body), nil
		}
		if op.Unsigned {
			return numeric.InstI32LtU(body), nil
		}
		return numeric.InstI32LtS(body), nil
	case ir.OpLeS:
		if op.Width == 64 {
			if op.Unsigned {
				return numeric.InstI64LeU(body), nil
			}
			return numeric.InstI64LeS(body), nil
		}
		if op.Unsigned {
			return numeric.InstI32LeU(body), nil
		}
		return numeric.InstI32LeS(body), nil
	case ir.OpGtS:
		if op.Width == 64 {
			if op.Unsigned {
				return numeric.InstI64GtU(body), nil
			}
			return numeric.InstI64GtS(body), nil
		}
		if op.Unsigned {
			return numeric.InstI32GtU(body), nil
		}
		return numeric.InstI32GtS(body), nil
	case ir.OpGeS:
		if op.Width == 64 {
			if op.Unsigned {
				return numeric.InstI64GeU(body), nil
			}
			return numeric.InstI64GeS(body), nil
		}
		if op.Unsigned {
			return numeric.InstI32GeU(body), nil
		}
		return numeric.InstI32GeS(body), nil

	case ir.OpFAdd:
		if op.Width == 64 {
			return numeric.InstF64Add(body), nil
		}
		return numeric.InstF32Add(body), nil
	case ir.OpFSub:
		if op.Width == 64 {
			return numeric.InstF64Sub(body), nil
		}
		return numeric.InstF32Sub(body), nil
	case ir.OpFMul:
		if op.Width == 64 {
			return numeric.InstF64Mul(body), nil
		}
		return numeric.InstF32Mul(body), nil
	case ir.OpFDiv:
		if op.Width == 64 {
			return numeric.InstF64Div(body), nil
		}
		return numeric.InstF32Div(body), nil
	case ir.OpFNeg:
		if op.Width == 64 {
			return numeric.InstF64Neg(body), nil
		}
		return numeric.InstF32Neg(body), nil
	case ir.OpFEq:
		if op.Width == 64 {
			return numeric.InstF64Eq(body), nil
		}
		return numeric.InstF32Eq(body), nil
	case ir.OpFNe:
		if op.Width == 64 {
			return numeric.InstF64Ne(body), nil
		}
		return numeric.InstF32Ne(body), nil
	case ir.OpFLt:
		if op.Width == 64 {
			return numeric.InstF64Lt(body), nil
		}
		return numeric.InstF32Lt(body), nil
	case ir.OpFLe:
		if op.Width == 64 {
			return numeric.InstF64Le(body), nil
		}
		return numeric.InstF32Le(body), nil
	case ir.OpFGt:
		if op.Width == 64 {
			return numeric.InstF64Gt(body), nil
		}
		return numeric.InstF32Gt(body), nil
	case ir.OpFGe:
		if op.Width == 64 {
			return numeric.InstF64Ge(body), nil
		}
		return numeric.InstF32Ge(body), nil

	// ---- Conversions (slice 3) ----
	case ir.OpExtendI32S:
		return convert.InstI64ExtendI32S(body), nil
	case ir.OpExtendI32U:
		return convert.InstI64ExtendI32U(body), nil
	case ir.OpWrapI64:
		return convert.InstI32WrapI64(body), nil
	case ir.OpFPromoteF32:
		return convert.InstF64PromoteF32(body), nil
	case ir.OpFDemoteF64:
		return convert.InstF32DemoteF64(body), nil
	case ir.OpSignExtend8:
		return convert.InstI32Extend8S(body), nil
	case ir.OpSignExtend16:
		return convert.InstI32Extend16S(body), nil
	case ir.OpReinterpretI32F32:
		return convert.InstI32ReinterpretF32(body), nil
	case ir.OpReinterpretF32I32:
		return convert.InstF32ReinterpretI32(body), nil

	case ir.OpFConvertI32:
		if op.Width == 64 {
			if op.Unsigned {
				return convert.InstF64ConvertI32U(body), nil
			}
			return convert.InstF64ConvertI32S(body), nil
		}
		if op.Unsigned {
			return convert.InstF32ConvertI32U(body), nil
		}
		return convert.InstF32ConvertI32S(body), nil
	case ir.OpFConvertI64:
		if op.Width == 64 {
			if op.Unsigned {
				return convert.InstF64ConvertI64U(body), nil
			}
			return convert.InstF64ConvertI64S(body), nil
		}
		if op.Unsigned {
			return convert.InstF32ConvertI64U(body), nil
		}
		return convert.InstF32ConvertI64S(body), nil
	case ir.OpITruncF32:
		if op.Width == 64 {
			if op.Unsigned {
				return convert.InstI64TruncF32U(body), nil
			}
			return convert.InstI64TruncF32S(body), nil
		}
		if op.Unsigned {
			return convert.InstI32TruncF32U(body), nil
		}
		return convert.InstI32TruncF32S(body), nil
	case ir.OpITruncF64:
		if op.Width == 64 {
			if op.Unsigned {
				return convert.InstI64TruncF64U(body), nil
			}
			return convert.InstI64TruncF64S(body), nil
		}
		if op.Unsigned {
			return convert.InstI32TruncF64U(body), nil
		}
		return convert.InstI32TruncF64S(body), nil

	// ---- Memory (slice 4) ----
	// Alignment is the *natural* alignment of the access — wasm
	// uleb-encodes it as log2(bytes). Offset is always 0 here;
	// the IR doesn't carry per-op offset (callers fold the base
	// + delta with OpAdd before the load/store).
	case ir.OpLoad:
		if op.Width == 64 {
			return memory.InstI64Load(body, 3, 0), nil
		}
		// Width=0 / 32 / WidthPtr (-1) all collapse to i32 on
		// wasm32 — WidthPtr is only meaningful on 64-bit native
		// targets. WidthString (-2) fans out into two i32 loads
		// (data at +0, len at +4) via a scratch local for the
		// shared base address.
		if op.Width == ir.WidthString {
			// Stack: [..., addr]. Need to keep addr around to
			// read both data (+0) and len (+4) without losing it.
			addrIdx := ctx.strPairScratchBase + 0
			body = inst.InstLocalTee(body, addrIdx) // stack: [addr], scratch=addr
			body = memory.InstI32Load(body, 2, 0)   // load data; stack: [data]
			body = inst.InstLocalGet(body, addrIdx) // [data, addr]
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Add(body)         // [data, addr+4]
			body = memory.InstI32Load(body, 2, 0)   // [data, len]
			return body, nil
		}
		return memory.InstI32Load(body, 2, 0), nil
	case ir.OpStore:
		if op.Width == 64 {
			return memory.InstI64Store(body, 3, 0), nil
		}
		if op.Width == ir.WidthString {
			// Stack: [..., addr, data, len]. Pop into scratch
			// then re-emit two i32.stores at +0 and +4.
			addrIdx := ctx.strPairScratchBase + 0
			dataIdx := ctx.strPairScratchBase + 1
			lenIdx := ctx.strPairScratchBase + 2
			body = inst.InstLocalSet(body, lenIdx)  // pop len
			body = inst.InstLocalSet(body, dataIdx) // pop data
			body = inst.InstLocalSet(body, addrIdx) // pop addr
			// Write data at addr+0.
			body = inst.InstLocalGet(body, addrIdx)
			body = inst.InstLocalGet(body, dataIdx)
			body = memory.InstI32Store(body, 2, 0)
			// Write len at addr+4.
			body = inst.InstLocalGet(body, addrIdx)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalGet(body, lenIdx)
			body = memory.InstI32Store(body, 2, 0)
			return body, nil
		}
		return memory.InstI32Store(body, 2, 0), nil
	case ir.OpFLoad:
		if op.Width == 64 {
			return memory.InstF64Load(body, 3, 0), nil
		}
		return memory.InstF32Load(body, 2, 0), nil
	case ir.OpFStore:
		if op.Width == 64 {
			return memory.InstF64Store(body, 3, 0), nil
		}
		return memory.InstF32Store(body, 2, 0), nil
	case ir.OpLoadByte:
		return memory.InstI32Load8U(body, 0, 0), nil
	case ir.OpLoadI8S:
		return memory.InstI32Load8S(body, 0, 0), nil
	case ir.OpStoreI8:
		return memory.InstI32Store8(body, 0, 0), nil
	case ir.OpLoadI16U:
		return memory.InstI32Load16U(body, 1, 0), nil
	case ir.OpLoadI16S:
		return memory.InstI32Load16S(body, 1, 0), nil
	case ir.OpStoreI16:
		return memory.InstI32Store16(body, 1, 0), nil

	// ---- Calls (slice 5) ----
	case ir.OpCallDirect:
		// Source-language built-ins (e.g. `print(s)`) get lowered
		// to OpCallDirect with the source name. Map those names
		// onto the synthetic runtime helpers that implement them.
		// User functions and helpers without an alias map 1:1.
		name := callDirectAlias(op.Str)
		idx, ok := ctx.funcIdx[name]
		if !ok {
			return nil, fmt.Errorf("OpCallDirect: unknown callee %q", op.Str)
		}
		return inst.InstCall(body, idx), nil

	// ---- Indirect calls (slice 6) ----
	case ir.OpCallClosureDirect:
		// Defunctionalised closure call: env_ptr is already on the
		// stack as the last arg, so this is just a direct call to
		// the hoisted target name.
		idx, ok := ctx.funcIdx[op.Str]
		if !ok {
			return nil, fmt.Errorf("OpCallClosureDirect: unknown callee %q", op.Str)
		}
		return inst.InstCall(body, idx), nil
	case ir.OpCallIndirect:
		if op.Sig == nil {
			return nil, fmt.Errorf("OpCallIndirect: missing op.Sig")
		}
		// Closure-target ABI: callee signature has env_ptr (i32)
		// appended. The typeidx we dispatch through must match —
		// derive it from op.Sig + env_ptr.
		tIdx, err := ctx.addClosureSigType(op.Sig)
		if err != nil {
			return nil, fmt.Errorf("OpCallIndirect: resolving signature: %w", err)
		}
		// Stack: [args..., closure_ptr]. Deref the closure pair
		// into [args..., env_ptr, fn_idx] for call_indirect.
		// env_ptr lives at offset 4; fn_idx at offset 0.
		idx := ctx.callIndirectScratchIdx
		body = inst.InstLocalSet(body, idx) // pop closure_ptr → scratch
		body = inst.InstLocalGet(body, idx)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 0) // push env_ptr
		body = inst.InstLocalGet(body, idx)
		body = memory.InstI32Load(body, 2, 0) // push fn_idx
		return inst.InstCallIndirect(body, tIdx, 0), nil

	// ---- String runtime helpers ----
	case ir.OpStrLen:
		// Stack: (data, len). The synthetic __lang_str_len helper
		// consumes both and returns the SSO-aware byte length.
		idx, ok := ctx.funcIdx["__lang_str_len"]
		if !ok {
			return nil, fmt.Errorf("OpStrLen: __lang_str_len helper not registered (scanRuntimeHelpers gap)")
		}
		return inst.InstCall(body, idx), nil

	// ---- String equality ----
	case ir.OpStrEq:
		// Stack: (a_data, a_len, b_data, b_len). The __str_eq
		// helper consumes all four and returns 0/1.
		idx, ok := ctx.funcIdx["__str_eq"]
		if !ok {
			return nil, fmt.Errorf("OpStrEq: __str_eq helper not registered (scanRuntimeHelpers gap)")
		}
		return inst.InstCall(body, idx), nil

	// ---- Enum tag dispatch ----
	case ir.OpMatchTag:
		// Stack: [ptr]. Tag is at offset 0. Just an i32.load.
		return memory.InstI32Load(body, 2, 0), nil
	case ir.OpEnumSentinel:
		// Push the address of the shared 4-byte cell holding
		// this tag value. OpMatchTag's i32.load reads the tag
		// back from there. op.I32 carries the tag value.
		off := ctx.internEnumSentinel(op.I32)
		return inst.InstI32Const(body, int32(off)), nil
	// ---- Pair-return ABI ----
	case ir.OpMakeSomeI32, ir.OpMakeOkI32:
		// Stack: [..., payload]. Want: [..., 0 (tag), payload].
		// Wasm has no swap; stash payload then push (0, payload).
		body = inst.InstLocalSet(body, ctx.pairMakeScratchIdx)
		body = inst.InstI32Const(body, 0) // tag
		body = inst.InstLocalGet(body, ctx.pairMakeScratchIdx)
		return body, nil
	case ir.OpMakeErrI32:
		// Stack: [..., payload]. Want: [..., 1 (tag), payload].
		body = inst.InstLocalSet(body, ctx.pairMakeScratchIdx)
		body = inst.InstI32Const(body, 1) // tag
		body = inst.InstLocalGet(body, ctx.pairMakeScratchIdx)
		return body, nil
	case ir.OpMakeNoneI32:
		// Stack: [...]. Want: [..., 1 (tag), 0 (payload)].
		body = inst.InstI32Const(body, 1)
		body = inst.InstI32Const(body, 0)
		return body, nil
	case ir.OpReturnPair:
		// Stack: [..., tag, payload]. wasm `return` unwinds with
		// the multi-value pair on the stack matching the function's
		// (i32, i32) result type.
		return inst.InstReturn(body), nil
	case ir.OpCallDirectPair:
		// Same as OpCallDirect; the callee's wasm signature has
		// (i32, i32) multi-value return per ip.PairForm gating.
		idx, ok := ctx.funcIdx[op.Str]
		if !ok {
			return nil, fmt.Errorf("OpCallDirectPair: unknown callee %q", op.Str)
		}
		return inst.InstCall(body, idx), nil

	// ---- String concatenation ----
	case ir.OpStrConcat:
		// Stack: (a_data, a_len, b_data, b_len). The __str_concat
		// helper consumes all four and returns a new (data, len)
		// heap-form pair via wasm multi-value return.
		idx, ok := ctx.funcIdx["__str_concat"]
		if !ok {
			return nil, fmt.Errorf("OpStrConcat: __str_concat helper not registered (scanRuntimeHelpers gap)")
		}
		return inst.InstCall(body, idx), nil

	// ---- Heap allocator ----
	case ir.OpAlloc:
		// Stack: (size). __lang_alloc bumps memory[40] and returns
		// the OLD value as the i32 pointer.
		idx, ok := ctx.funcIdx["__lang_alloc"]
		if !ok {
			return nil, fmt.Errorf("OpAlloc: __lang_alloc helper not registered (scanRuntimeHelpers gap)")
		}
		return inst.InstCall(body, idx), nil

	// ---- Heap-allocated closures ----
	case ir.OpMakeEnv:
		return emitMakeEnv(body, op, ctx)
	case ir.OpMakeClosure:
		return emitMakeClosure(body, op, ctx)
	}
	return nil, fmt.Errorf("unsupported op %v", op.Kind)
}

// callDirectAlias maps source-language built-in names that the IR
// lowering emits as OpCallDirect targets onto the synthetic runtime
// helpers that actually implement them in wasmbin. Names not in
// the map pass through unchanged.
//
// Today: `print(s)` → `__lang_print` (fd_write), `exit(code)` →
// `__lang_exit` (proc_exit). Future entries cover `putchar`,
// `read_line`, etc.
func callDirectAlias(name string) string {
	switch name {
	case "exit":
		return "__lang_exit"
	case "print":
		return "__lang_print"
	case "random_i32":
		return "__lang_random_i32"
	case "now_ns":
		return "__lang_now_ns"
	case "env_count":
		return "__lang_env_count"
	case "arg_count":
		return "__lang_arg_count"
	}
	return name
}

// closureTargetSet returns the set of function names that act as
// closure-call targets — appearing as op.Str of OpConstFunc,
// OpMakeClosure, OpMakeEnv, or OpCallClosureDirect. These
// functions get an extra `i32` (env_ptr) appended to their wasm
// signature so the indirect-call seam can pass env through
// uniformly.
//
// The lowering invariant is that a single function is EITHER
// directly-called OR closure-target — never both — so plain
// user functions called only via OpCallDirect stay unchanged.
func closureTargetSet(prog *ir.Program) map[string]bool {
	out := map[string]bool{}
	for _, fn := range prog.Funcs {
		for _, op := range fn.Ops {
			switch op.Kind {
			case ir.OpConstFunc, ir.OpMakeClosure, ir.OpMakeEnv, ir.OpCallClosureDirect:
				if op.Str != "" {
					out[op.Str] = true
				}
			}
		}
	}
	return out
}

// anyTableOp reports whether prog needs a table + element section.
// Indirect calls (OpCallIndirect) dispatch through the funcref
// table; OpConstFunc materialises a static table-slot pointer.
// OpCallClosureDirect doesn't dispatch through the table — its
// callee is hoisted by closure conversion — so it isn't listed
// here.
func anyTableOp(prog *ir.Program) bool {
	for _, fn := range prog.Funcs {
		for _, op := range fn.Ops {
			switch op.Kind {
			case ir.OpCallIndirect, ir.OpConstFunc:
				return true
			}
		}
	}
	return false
}

// maxClosureCaptures returns the highest op.I32 (capture count)
// across every OpMakeClosure / OpMakeEnv in fn. Used to size the
// per-function scratch pool that holds captures during the
// pop → store sequence.
func maxClosureCaptures(fn *ir.Func) int {
	max := 0
	for _, op := range fn.Ops {
		switch op.Kind {
		case ir.OpMakeClosure, ir.OpMakeEnv:
			if int(op.I32) > max {
				max = int(op.I32)
			}
		}
	}
	return max
}

// fnNeedsClosureMakeScratch reports whether fn has any
// OpMakeClosure / OpMakeEnv. Used to gate scratch-slot allocation
// for the env_ptr + pair_ptr + N capture stash.
func fnNeedsClosureMakeScratch(fn *ir.Func) bool {
	for _, op := range fn.Ops {
		switch op.Kind {
		case ir.OpMakeClosure, ir.OpMakeEnv:
			return true
		}
	}
	return false
}

// closureMakeScratchSlots returns the count of extra wasm locals
// the closure-make path needs for fn:
//   - N slots for stashing the popped captures
//   - 1 slot for env_ptr
//   - 1 slot for pair_ptr (OpMakeClosure-only)
//
// Returns 0 if fn doesn't use either op.
func closureMakeScratchSlots(fn *ir.Func) int {
	n := maxClosureCaptures(fn)
	if n == 0 && !fnNeedsClosureMakeScratch(fn) {
		return 0
	}
	return n + 2
}

// emitMakeEnv emits the wasm bytes for OpMakeEnv:
//   - pop the N captures from the operand stack (in reverse) into
//     scratch slots [closureCapBase .. closureCapBase+N-1];
//   - alloc N*4 bytes;
//   - store each capture into mem[env_ptr + 4*i];
//   - push env_ptr (the result).
//
// All captures are treated as i32 (single-slot scalar). Programs
// with string / i64 / f64 captures get wrong layout — the per-
// capture type info isn't carried at the IR layer and would
// require ast access to consult.
func emitMakeEnv(body []byte, op ir.Op, ctx *emitCtx) ([]byte, error) {
	allocIdx, ok := ctx.funcIdx["__lang_alloc"]
	if !ok {
		return nil, fmt.Errorf("OpMakeEnv: __lang_alloc helper not registered")
	}
	n := int(op.I32)
	capBase := ctx.closureMakeScratchBase
	envSlot := capBase + uint32(n)
	for i := n - 1; i >= 0; i-- {
		body = inst.InstLocalSet(body, capBase+uint32(i))
	}
	body = inst.InstI32Const(body, int32(n*4))
	body = inst.InstCall(body, allocIdx)
	body = inst.InstLocalSet(body, envSlot)
	for i := 0; i < n; i++ {
		body = inst.InstLocalGet(body, envSlot)
		if i > 0 {
			body = inst.InstI32Const(body, int32(4*i))
			body = numeric.InstI32Add(body)
		}
		body = inst.InstLocalGet(body, capBase+uint32(i))
		body = memory.InstI32Store(body, 2, 0)
	}
	body = inst.InstLocalGet(body, envSlot)
	return body, nil
}

// emitMakeClosure is OpMakeEnv plus an 8-byte closure pair cell
// {fn_idx, env_ptr}. Returns the pair pointer.
func emitMakeClosure(body []byte, op ir.Op, ctx *emitCtx) ([]byte, error) {
	allocIdx, ok := ctx.funcIdx["__lang_alloc"]
	if !ok {
		return nil, fmt.Errorf("OpMakeClosure: __lang_alloc helper not registered")
	}
	if op.Str == "" {
		return nil, fmt.Errorf("OpMakeClosure: missing target name")
	}
	fnIdx, ok := ctx.funcIdx[op.Str]
	if !ok {
		return nil, fmt.Errorf("OpMakeClosure: unknown target %q", op.Str)
	}
	n := int(op.I32)
	capBase := ctx.closureMakeScratchBase
	envSlot := capBase + uint32(n)
	pairSlot := capBase + uint32(n) + 1
	// Pop captures into scratch slots in reverse order.
	for i := n - 1; i >= 0; i-- {
		body = inst.InstLocalSet(body, capBase+uint32(i))
	}
	// envSize bytes. When n=0 we still alloc 0 bytes — the bump
	// allocator handles that case by returning the current
	// cursor. The pair's env_ptr field then equals whatever the
	// next allocation would have returned, but it's never read
	// for empty-env closures.
	body = inst.InstI32Const(body, int32(n*4))
	body = inst.InstCall(body, allocIdx)
	body = inst.InstLocalSet(body, envSlot)
	// Store each capture at env_ptr + 4*i.
	for i := 0; i < n; i++ {
		body = inst.InstLocalGet(body, envSlot)
		if i > 0 {
			body = inst.InstI32Const(body, int32(4*i))
			body = numeric.InstI32Add(body)
		}
		body = inst.InstLocalGet(body, capBase+uint32(i))
		body = memory.InstI32Store(body, 2, 0)
	}
	// Pair cell: 8 bytes containing {fn_idx, env_ptr}.
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, allocIdx)
	body = inst.InstLocalSet(body, pairSlot)
	body = inst.InstLocalGet(body, pairSlot)
	body = inst.InstI32Const(body, int32(fnIdx))
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, pairSlot)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, envSlot)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, pairSlot)
	return body, nil
}

// anyMemoryOp reports whether prog needs a memory section. Any
// load / store (including sub-width and float variants) qualifies;
// pure arithmetic / control-flow programs stay memory-free so the
// output binary is one fewer section.
func anyMemoryOp(prog *ir.Program) bool {
	for _, fn := range prog.Funcs {
		for _, op := range fn.Ops {
			switch op.Kind {
			case ir.OpLoad, ir.OpStore,
				ir.OpFLoad, ir.OpFStore,
				ir.OpLoadByte, ir.OpStoreI8, ir.OpLoadI8S,
				ir.OpLoadI16U, ir.OpLoadI16S, ir.OpStoreI16:
				return true
			}
		}
	}
	return false
}

// blocktypeByte maps an ir.BlockType* constant to the single-byte
// blocktype encoding wasm 1.0 uses for `block` / `loop` / `if`
// when the block's result is empty or a single valtype.
//
// Multi-value blocks (string-pair, struct unpacks, etc.) need a
// typeidx reference here instead — they're out of scope for the
// control-flow slice and return an error so the missing case is
// loud.
func blocktypeByte(bt int32) (byte, error) {
	switch bt {
	case ir.BlockTypeVoid:
		return inst.BlocktypeEmpty, nil
	case ir.BlockTypeI32:
		return encode.ValtypeI32, nil
	case ir.BlockTypeI64:
		return encode.ValtypeI64, nil
	case ir.BlockTypeF32:
		return encode.ValtypeF32, nil
	case ir.BlockTypeF64:
		return encode.ValtypeF64, nil
	case ir.BlockTypeStringPair:
		return 0, fmt.Errorf("blocktype string-pair (multi-value) not yet supported")
	}
	return 0, fmt.Errorf("unknown blocktype %d", bt)
}

// appendUleb appends `v` as a uleb128 to `buf`. Duplicated from
// internal/wasm/leb128.UlebU32 to avoid a thin pass-through;
// keeping the local helper means the locals-vec assembly stays
// readable in one file.
func appendUleb(buf []byte, v uint32) []byte {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			buf = append(buf, b|0x80)
			continue
		}
		return append(buf, b)
	}
}

