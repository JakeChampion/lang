// Package wasmbin is a binary WebAssembly emitter that consumes the
// shared ir.Program and produces a core module's bytes directly,
// without going through WAT text. It is the wasm backend: the older
// WAT-text emitter (internal/codegen/wasm) it replaced has been
// removed, along with the `wasm-tools parse` shell-out that depended
// on it. wasmbin shares the lowering and IR optimisation pipeline
// with every other backend, so a feature added to ir.Lower lights up
// here automatically once the corresponding op handler is wired. The
// package is exercised via `fern -target wasm-bin -o prog.wasm
// prog.fern` end-to-end.
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
//   - String runtime: __fern_str_len, __fern_str_byte (SSO
//     seam), __str_eq, __str_concat.
//   - Bump allocator: __fern_alloc, cursor at memory[40], pages
//     grow on demand.
//   - WASI: wasi_snapshot_preview1.fd_write import + a
//     __fern_print helper that copies the string into a fresh
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
	"github.com/jakechampion/lang/internal/fernstring"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/wasm/convert"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/imports"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/leb128"
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
	// PrintMainResult tunes the SynthStart wrapper to route
	// main's i32 return through `int_to_string` + `__fern_print`
	// instead of dropping it. Mirrors the WAT path's
	// `EmitOptions.PrintMainResult` so e2e tests can observe
	// main's value over stdout (preview-2 hosts only surface 0/1
	// through `wasi:cli/exit`). No-op for non-i32 / void returns.
	PrintMainResult bool
	// HttpHandler emits the `wasi:http/incoming-handler@0.2.0#handle`
	// export wrapping the user-defined `function handle(req:
	// HttpRequest, plat: Platform): HttpResponse`. The synthetic
	// `__http_entry` helper marshals the canonical-ABI incoming-
	// request into the user's HttpRequest struct, invokes `handle`,
	// then streams the HttpResponse back through outgoing-body.
	// Pulls in the wasi:http/types preview-2 imports + a
	// `cabi_realloc` export the host calls back for list<u8>
	// allocations. Mirrors the WAT path's `EmitOptions.HttpHandler`.
	HttpHandler bool
	// SynthCliRun emits a synthetic `_lang_run() -> i32` wrapper
	// that calls `main` and produces an i32 result regardless of
	// main's actual return shape (void → i32.const 0; i32 →
	// pass-through; other shapes are an error). Used by the
	// `-component-wrap-cli` driver path so the canon-lifted
	// wasi:cli/run::run sees a `() -> i32` core export even when
	// the user's main returns void.
	SynthCliRun bool
	// CliRunResult normalises the SynthCliRun wrapper's i32 return
	// to a valid `wasi:cli/run` `result<_, _>` discriminant (0 = ok,
	// 1 = err) when its value is fed to that canon lift. Only 0/1 are
	// valid discriminants, so without this a main returning >= 2 traps
	// the host. Off for the raw u32-export (`-component-wrap`) shape.
	CliRunResult bool
	// AsyncExportName, when non-empty, emits a WASI Preview-3
	// component-model-async export under this name: an
	// `("", "task-return")` import is added and a synthetic core
	// function calls `main`, hands its i32 result to task-return, and
	// returns void. The composer lifts it with the `async` canonical
	// option. `main` must return i32. See
	// docs/WASI-PREVIEW3-ASYNC-PLAN.md.
	AsyncExportName string
	// AsyncSourceFunc names the Fern function the async wrapper calls;
	// empty defaults to "main". Set to the `async function`'s name when
	// driven by the keyword (vs the `-async-export` flag, which wraps
	// main).
	AsyncSourceFunc string
	// Preview2WASI rewrites preview-1-shaped WASI imports to their
	// preview-2 component-model equivalents. Currently scoped to
	// `proc_exit` — the only import whose core-wasm signature is
	// identical across the two preview generations (i32 → ()). The
	// canonical-ABI `result<_, _>` that lifts to wasi:cli/exit::exit
	// also lowers to a single i32, so __fern_exit's call site is
	// untouched. Off by default; opt-in for the WrapWasiImported
	// pipeline that produces preview-2-native components without
	// the wasm-tools adapter shell-out.
	Preview2WASI bool
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
	// Synthetic runtime-helper functions (e.g. __fern_str_len)
	// get appended after the user functions in the same
	// numbering space; their entries land in this map once
	// the runtime-needs scan below has decided which helpers
	// the program actually uses.
	// Imports first: imported functions occupy funcidx 0..N-1
	// in the module's funcidx namespace; user functions and
	// synthetic helpers shift up by N. Today the only imports
	// come from runtime needs (e.g. WASI fd_write for print).
	helpers := scanRuntimeHelpers(prog, opts)
	if opts.PrintMainResult {
		// The synthesised `_start` calls __fern_print to flush
		// `int_to_string(main())` to stdout. If no user-emitted
		// op already required __fern_print, the scan above would
		// have left it out. Force-include it now (with its
		// transitive deps) so the funcIdx table covers the
		// SynthStart call site.
		helpers.add("__fern_str_len")
		helpers.add("__fern_str_byte")
		helpers.add("__fern_alloc")
		helpers.add("__fern_print")
	}
	if opts.HttpHandler {
		// __http_entry's body alloc()s the request struct, the
		// HeaderMap parallel arrays, the body accumulator, the
		// canonical-ABI retptr scratch, the Platform capability
		// bag, and the response outgoing-body. It also calls
		// __bytes_to_lang_string for the host-bytes →
		// lang-string round-trip, and emitStrNormalize for the
		// outgoing body SSO normalize. Per-request memory is
		// reclaimed by reference counting (RC), not a bump reset.
		helpers.add("__fern_alloc")
		helpers.add("__fern_str_len")
		helpers.add("__fern_str_byte")
		helpers.add("__bytes_to_lang_string")
		helpers.add("__http_entry")
	}
	// P6: a string-result `@export` function's wrapper SSO-normalizes the Fern
	// (data,len) pair into a heap return area, so pin emitStrNormalize's deps; a
	// numeric-array (`list<T>`) result's wrapper allocates the [ptr,len] return
	// area, so it needs __fern_alloc (docs/WIT-BRING-YOUR-OWN.md).
	for _, exp := range prog.Exports {
		if exp.ResultEnum != nil || exp.ResultTuple != nil {
			// The Option/Result / tuple RESULT wrapper allocates the canonical
			// return area via __fern_alloc.
			helpers.add("__fern_alloc")
		}
		for _, fn := range prog.Funcs {
			if fn.Name != exp.Name {
				continue
			}
			if isStringType(fn.ReturnType) {
				helpers.add("__fern_alloc")
				helpers.add("__fern_str_len")
				helpers.add("__fern_str_byte")
			}
			if isScalarArrayParamType(fn.ReturnType) {
				helpers.add("__fern_alloc")
			}
			if funcHasNumericArrayParam(fn) {
				// The list-PARAM wrapper builds a length-prefixed Fern array from
				// the canonical (ptr,len) via __fern_alloc + memory.copy.
				helpers.add("__fern_alloc")
			}
		}
	}
	// cabi_realloc is the canonical-ABI allocator the host
	// calls back into for `list<u8>` / variable-size return
	// materialisation. Preview-2 wrapping (signalled by
	// ForceMemorySection) needs it unconditionally — every
	// component that imports a variable-size canonical-ABI
	// return (wasi:sockets/tcp.accept, wasi:io/streams
	// blocking-read, wasi:http response bodies, etc.) has the
	// host write through it. Mirrors the WAT path's
	// always-export shape.
	if opts.ForceMemorySection {
		helpers.add("cabi_realloc")
	}
	importNeeds := scanImports(prog, helpers, opts)

	// Extern WIT imports (`@import` functions — bring-your-own WIT, P4,
	// docs/WIT-BRING-YOUR-OWN.md): each referenced extern becomes a core wasm
	// function import of (Iface, WITName) with a signature derived from its
	// Fern declaration. Only emit those actually called, so an unused
	// declaration costs nothing and the component composer isn't asked to wire
	// it. externSpecs overlays importSpecs in the import-section loop below;
	// the extern's funcidx (assigned in the import block) is what a call to its
	// name resolves to. A composite-result extern (P4c) instead registers a
	// generated wrapper helper (externWrappers) under its Fern name and its raw
	// import under "<name>$import".
	externSpecs, externWrappers, err := scanExternImports(prog, &importNeeds, &helpers)
	if err != nil {
		return nil, err
	}

	// WASI Preview-3 async export: pull in the ("", "task-return")
	// import (importSpecs["async_task_return"]) so the synthetic async
	// wrapper below can call it. Added before funcIdx is assigned so the
	// import takes its slot in the import index space. The task-return
	// intrinsic carries the source function's (scalar) result, so its
	// param valtype is width-matched to it via an externSpecs overlay —
	// an i64/f32/f64-returning async export hands task.return the wider
	// value (the default importSpecs entry is i32-only).
	if opts.AsyncExportName != "" {
		srcFn := opts.AsyncSourceFunc
		if srcFn == "" {
			srcFn = "main"
		}
		var srcRet ast.Type
		found := false
		for _, fn := range prog.Funcs {
			if fn.Name == srcFn {
				srcRet, found = fn.ReturnType, true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("wasmbin: AsyncExportName needs the source function %q", srcFn)
		}
		rv, err := resultValtypes(srcRet)
		if err != nil {
			return nil, fmt.Errorf("wasmbin: AsyncExportName: %q result: %w", srcFn, err)
		}
		if len(rv) != 1 {
			return nil, fmt.Errorf("wasmbin: AsyncExportName: %q must return a single scalar (i32/i64/f32/f64); a void/string/composite async export is not supported yet", srcFn)
		}
		externSpecs["async_task_return"] = importSpec{module: "", name: "task-return", params: rv, results: nil}
		importNeeds.add("async_task_return")
	}

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

	funcByName := make(map[string]*ir.Func, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		funcByName[fn.Name] = fn
	}

	// Type-section dedup: same param-list + result-list → same
	// typeidx. The key is the param count (one byte) followed by the
	// param then result valtype bytes — the count delimits params from
	// results unambiguously. A literal separator byte would NOT work: the
	// natural choice '|' is 0x7c, which is also the f64 valtype byte, so
	// `() -> (f64)` and `(f64) -> ()` would collide (and merge into one
	// wrong type). The count-prefix avoids any value-range overlap.
	typeIdx := map[string]uint32{}
	addType := func(params, results []byte) uint32 {
		key := string(append(append([]byte{byte(len(params))}, params...), results...))
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
	// Low-memory layout (0..closuresBase):
	//
	//	0..47   args / env / read_byte cache (see wasi.go)
	//	48..55  print iovec
	//	56..59  print ret
	//	60..63  random buf
	//	64..71  __str_idx scratch (data, len) for inline-form strings
	//	72..79  reserved for future per-helper scratch
	//	80..83  preview-2 stdout-handle init flag
	//	84..87  preview-2 stdout-handle cache
	//	88..91  preview-2 stderr-handle init flag
	//	92..95  preview-2 stderr-handle cache
	//	96..    closure pair cells (8 bytes each)
	const closuresBase = 96
	const maxClosureCells = (1024 - closuresBase) / 8
	closureTableIdx := map[string]int{}
	// progFuncTableIdx maps a user-function name to its position
	// in prog.Funcs. The element segment places prog.Funcs[i] at
	// table-index i (see anyTableOp branch below); call_indirect
	// dispatches by that table-index, not by the module-wide
	// funcidx (which has the import-count shift baked in).
	progFuncTableIdx := make(map[string]uint32, len(prog.Funcs))
	for i, fn := range prog.Funcs {
		progFuncTableIdx[fn.Name] = uint32(i)
	}

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
		// Cell bytes: fn_table_idx LE + env_ptr=0 LE.
		// call_indirect takes a TABLE index, not a funcidx —
		// table-index = position in prog.Funcs (the element
		// segment places prog.Funcs[i] at table-index i).
		fnTableIdx, ok := progFuncTableIdx[name]
		if !ok {
			return 0, fmt.Errorf("OpConstFunc: target %q not in prog.Funcs (treeshake / IR mismatch)", name)
		}
		closureBytes = append(closureBytes,
			byte(fnTableIdx), byte(fnTableIdx>>8), byte(fnTableIdx>>16), byte(fnTableIdx>>24),
			0, 0, 0, 0)
		return idx, nil
	}

	// String interning state for OpConstStr's heap-form path.
	// Inline-form strings (≤7 bytes via fernstring.FitsInlineWasm)
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
		// Prepend an 8-byte rc-sentinel header (rc=0x80000000 at
		// [data-8], pad at [data-4]) before the bytes — a heap-form
		// string LITERAL lives immortally in the data segment and must
		// never be rc-freed when it flows through the rc system (an
		// aliased / container-stored literal reaches __fern_str_inc /
		// __fern_str_dec, which read [data-8]). The high bit makes both
		// short-circuit, exactly like the enum-sentinel header and the
		// runtime's __fern_alloc_box boxes. Literals sit at 1024+, ABOVE
		// the low-address guard (1024), so without this header the dec
		// would misread mid-data-segment bytes as an rc. The returned
		// offset still points at the first content byte, so every reader
		// (__fern_str_len / __fern_str_byte / concat / slice) is
		// unchanged. Mirrors internEnumSentinel.
		dataBytes = append(dataBytes,
			0x00, 0x00, 0x00, 0x80, // rc = 0x80000000 (LE)
			0x00, 0x00, 0x00, 0x00) // pad
		off := stringNextOff + 8
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
		// Phase 1e-enums-ii: an 8-byte rc header precedes the tag
		// cell (rc=0x80000000 at [data-8], pad at [data-4]) so the
		// __fern_rc_inc/dec helpers short-circuit on the high bit
		// once the enum-ii predicate widening starts dec'ing enum
		// locals that hold a payloadless variant. The returned
		// offset still points at the tag, so OpMatchTag's
		// `[ptr + 0]` load is unchanged.
		dataBytes = append(dataBytes,
			0x00, 0x00, 0x00, 0x80, // rc = 0x80000000 (LE)
			0x00, 0x00, 0x00, 0x00) // pad
		off := stringNextOff + 8
		enumSentinels[tag] = off
		// Append 4 LE bytes for the tag value.
		dataBytes = append(dataBytes,
			byte(tag), byte(tag>>8), byte(tag>>16), byte(tag>>24))
		stringNextOff = off + 4
		return off
	}

	// `dyn Trait` vtable interning. Each (trait, concrete) pair gets one
	// table in the data segment: an array of i32 function-TABLE indices
	// (positions in prog.Funcs), one slot per non-associated trait method
	// in declaration order. No rc header — vtables are static and never
	// inc/dec'd. OpConstVtable emits `i32.const <returned address>` and
	// OpCallDyn loads slot k (`+ slot*4`) then call_indirect's through it.
	// See docs/DYN-TRAITS.md §4.2.1.
	vtablePool := map[string]int{}
	internVtable := func(trait, concrete string) (int, error) {
		key := trait + "\x00" + concrete
		if off, ok := vtablePool[key]; ok {
			return off, nil
		}
		var decl *ir.VtableDecl
		for i := range prog.Vtables {
			if prog.Vtables[i].Trait == trait && prog.Vtables[i].Concrete == concrete {
				decl = &prog.Vtables[i]
				break
			}
		}
		if decl == nil {
			return 0, fmt.Errorf("OpConstVtable: no vtable for (trait %q, concrete %q)", trait, concrete)
		}
		off := stringNextOff
		for _, mth := range decl.Methods {
			idx, ok := progFuncTableIdx[mth.Func]
			if !ok {
				return 0, fmt.Errorf("OpConstVtable: impl method %q not in prog.Funcs", mth.Func)
			}
			dataBytes = append(dataBytes,
				byte(idx), byte(idx>>8), byte(idx>>16), byte(idx>>24))
		}
		// Trailing drop slot at index len(Methods) (docs/DYN-TRAITS.md
		// §4.4): the concrete type's drop fn as a function-table index, or
		// a null sentinel (0) when it needs none. The __drop_dyn_<set>
		// helper reads this slot and call_indirects it to run the erased
		// concrete destructor. Appended trailing so the method slot indices
		// (0..n-1) are unchanged — OpCallDyn's slot math is untouched.
		dropIdx := uint32(0)
		if decl.Drop != "" {
			idx, ok := progFuncTableIdx[decl.Drop]
			if !ok {
				return 0, fmt.Errorf("OpConstVtable: drop fn %q not in prog.Funcs", decl.Drop)
			}
			dropIdx = idx
		}
		dataBytes = append(dataBytes,
			byte(dropIdx), byte(dropIdx>>8), byte(dropIdx>>16), byte(dropIdx>>24))
		stringNextOff = off + 4*(len(decl.Methods)+1)
		vtablePool[key] = off
		return off, nil
	}

	closureTargets := closureTargetSet(prog)
	ctx := &emitCtx{
		funcIdx:            funcIdx,
		progFuncTableIdx:   progFuncTableIdx,
		internString:       internString,
		internClosure:      internClosure,
		closuresBaseAddr:   closuresBase,
		internEnumSentinel: internEnumSentinel,
		internVtable:       internVtable,
		closureTargets:     closureTargets,
		funcByName:         funcByName,
		addRawType:         addType,
		addSigType: func(sig *ast.FuncType) (uint32, error) {
			params := make([]byte, 0, len(sig.Params))
			for _, pt := range sig.Params {
				// slotValtypes (not valtypeFor) so a two-word string param
				// fans out to [i32, i32] — matching paramValtypes, which
				// types the call_indirect target the same way. A `dyn`
				// method with a string arg dispatches through this seam
				// (docs/DYN-TRAITS.md §4.2.3).
				vts, err := slotValtypes(pt)
				if err != nil {
					return 0, err
				}
				params = append(params, vts...)
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
				// slotValtypes (not valtypeFor) so a two-word string param
				// fans out to [i32, i32] — matching the callee definition,
				// which types its params via paramValtypes → slotValtypes
				// (line ~636). valtypeFor rejected string outright, so any
				// `(string, …) => …` closure invoked through call_indirect
				// failed to compile for wasm (#4804). Mirrors addSigType's
				// OpCallDyn seam, which already fans strings this way.
				vts, err := slotValtypes(pt)
				if err != nil {
					return 0, err
				}
				params = append(params, vts...)
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
		spec, isExtern := externSpecs[name]
		if !isExtern {
			spec = importSpecs[name]
			if opts.Preview2WASI && name == "wasi_proc_exit" {
				spec.module = "wasi:cli/exit@0.2.0"
				spec.name = "exit"
			}
		}
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
	// pulled in (__fern_alloc grows memory; __fern_str_byte and
	// transitively __str_eq emit i32.load8_u even in branches
	// that don't execute at runtime — wasm validation still
	// requires memory 0 to exist). Memory layout matches the
	// WAT path: 1 page (64 KiB) with no upper bound.
	if opts.ForceMemorySection || anyMemoryOp(prog) || helpers.set["__fern_alloc"] || helpers.set["__fern_str_byte"] || helpers.set["__load_i32"] || helpers.set["__store_i32"] || helpers.set["__load_i64"] || helpers.set["__store_i64"] || helpers.set["__load_ptr"] || helpers.set["__store_ptr"] || helpers.set["__memcpy"] || helpers.set["__memset"] || len(importNeeds.order) > 0 {
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
		if closureTargets[fn.Name] && !hasEnvParam(fn.Params) {
			// Closure-target ABI: append an i32 (env_ptr) as the
			// last param. The body doesn't have to use it; the
			// indirect-call deref always pushes one so the wasm
			// signature must accept it.
			//
			// Skip the append when closureconv already added a
			// `__env` IR param — that param IS the env_ptr and
			// doubling up confuses every callee that does its
			// own slot-index arithmetic on params.
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
	// (e.g. __str_eq → __fern_str_len + __fern_str_byte) so the
	// call targets are resolved at module-assembly time. funcIdx
	// already has every helper's index installed up-front by the
	// pre-scan loop above.
	// helperIdxs carries every name visible from a helper body —
	// both imports and other helpers. Past versions only seeded
	// helpers here, so any helper that referenced multiple imports
	// (e.g. __fern_arg_at calling both wasi_args_sizes_get and
	// wasi_args_get) silently fell back to funcidx 0 for missing
	// keys, calling whatever import happened to land first. Always
	// include both.
	helperIdxs := make(map[string]uint32, len(helpers.order)+len(importNeeds.order)+len(prog.Funcs))
	for _, name := range importNeeds.order {
		helperIdxs[name] = funcIdx[name]
	}
	for _, name := range helpers.order {
		helperIdxs[name] = funcIdx[name]
	}
	// Also expose user-defined IR functions to helper bodies —
	// HttpHandler's __http_entry calls the user's `handle()`, and
	// `__method_HeaderMap_append` is reached from the same wrapper.
	// Without this, lookups fall back to funcidx 0 (the first
	// import) and the wrapper synthesises a 500 response.
	for _, fn := range prog.Funcs {
		if idx, ok := funcIdx[fn.Name]; ok {
			helperIdxs[fn.Name] = idx
		}
	}
	// __enum_sent(disc)->ptr maps a WIT-enum-result discriminant to the static
	// per-tag sentinel; its select-chain spans 0..maxEnumN-1 and is built here
	// because it needs internEnumSentinel (a data address, not a funcidx).
	maxEnumN := 0
	for _, ex := range prog.Externs {
		if ex.ResultPlainEnumN > maxEnumN {
			maxEnumN = ex.ResultPlainEnumN
		}
	}
	for _, name := range helpers.order {
		var spec runtimeHelperSpec
		isExtern := false
		if name == "__enum_sent" {
			spec = runtimeHelperSpec{
				params:  []byte{encode.ValtypeI32},
				results: []byte{encode.ValtypeI32},
				body:    buildEnumSentBody(internEnumSentinel, maxEnumN),
			}
		} else if s, ok := externWrappers[name]; ok {
			spec, isExtern = s, true
		} else {
			spec = runtimeHelperSpecs[name]
		}
		params, results := spec.params, spec.results
		tIdx := addType(params, results)
		m.FunctionTypeidxs = append(m.FunctionTypeidxs, tIdx)
		bodyFn := spec.body
		if !isExtern && opts.Preview2WASI {
			if alt, ok := preview2HelperBodyOverrides[name]; ok {
				bodyFn = alt
			}
		}
		m.CodeBodies = append(m.CodeBodies, bodyFn(helperIdxs))
	}

	// HttpHandler-specific export: `__http_entry` surfaced under
	// the canonical component-model name. Helpers are private by
	// default (the loop above doesn't add export entries for them).
	// Mirrors how SynthStart sets up `_start` below.
	if opts.HttpHandler {
		if idx, ok := funcIdx["__http_entry"]; ok {
			m.ExportNames = append(m.ExportNames, "wasi:http/incoming-handler@0.2.0#handle")
			m.ExportKinds = append(m.ExportKinds, sections.ExportFunc)
			m.ExportIdxs = append(m.ExportIdxs, idx)
		}
	}
	// cabi_realloc as a top-level export — see the helpers.add
	// note above. Any preview-2 component the host wraps wants
	// to call back for variable-size return materialisation, so
	// gate on ForceMemorySection just like the helper add does.
	// A composite-result `@import` extern (P4c) also needs the host
	// to call back into cabi_realloc to materialize its returned
	// bytes, so export it whenever such a wrapper is present.
	if opts.ForceMemorySection || len(externWrappers) > 0 {
		if idx, ok := funcIdx["cabi_realloc"]; ok {
			m.ExportNames = append(m.ExportNames, "cabi_realloc")
			m.ExportKinds = append(m.ExportKinds, sections.ExportFunc)
			m.ExportIdxs = append(m.ExportIdxs, idx)
		}
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
	if helpers.set["__fern_alloc"] {
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
		// Phase 6: record the cursor's seed at heapBaseAddr so
		// __fern_heap_bump_bytes can report (cursor − seed) — the bump
		// high-water mark, 0 at start (mirrors the natives' heap_base).
		m.DataOffsets = append(m.DataOffsets, heapBaseAddr)
		m.DataInits = append(m.DataInits, le32(int32(start)))
	}

	// Synth `_start` wrapper when requested. The body is just
	// `call $main` + optional `drop` (when main has a result)
	// + `end`. Appended after all user functions + helpers so
	// it lands at the highest funcidx; gets a fresh `() -> ()`
	// typeidx + export.
	//
	// PrintMainResult turns the trailing drop into a
	// `call $int_to_string` + `call $__fern_print` pair so
	// main's i32 value lands on stdout. Mirrors the WAT path's
	// PrintMainResult mode; same fallback shape (drop) when the
	// program doesn't include an int_to_string variant.
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
		printed := false
		if opts.PrintMainResult && len(mainResults) == 1 && mainResults[0] == encode.ValtypeI32 {
			// `int_to_string` resolves to the bare name (flat-load);
			// `int__int_to_string` covers an explicit
			// `import "core/int"` whose mangling pass appends
			// the module name. Pick whichever survived
			// tree-shake + dead-function elimination. If
			// neither is present (a program that doesn't import
			// core/int) fall back to drop so the wrapper still
			// links.
			intToStrName := ""
			if _, ok := funcIdx["int_to_string"]; ok {
				intToStrName = "int_to_string"
			} else if _, ok := funcIdx["int__int_to_string"]; ok {
				intToStrName = "int__int_to_string"
			}
			if intToStrName != "" {
				body = inst.InstCall(body, funcIdx[intToStrName])
				printIdx, ok := funcIdx["__fern_print"]
				if !ok {
					return nil, fmt.Errorf("wasmbin: PrintMainResult: __fern_print helper not registered (scanRuntimeHelpers gap)")
				}
				body = inst.InstCall(body, printIdx)
				printed = true
			}
		}
		if !printed {
			for range mainResults {
				body = inst.InstDrop(body)
			}
		}
		m.FunctionTypeidxs = append(m.FunctionTypeidxs, startTIdx)
		m.CodeBodies = append(m.CodeBodies, inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body))
		m.ExportNames = append(m.ExportNames, "_start")
		m.ExportKinds = append(m.ExportKinds, sections.ExportFunc)
		m.ExportIdxs = append(m.ExportIdxs, startFuncIdx)
	}
	// P6: surface a core export `<iface>#<wit-name>` for each `@export`
	// function so the world-driven composer can alias + lift it as the named
	// world export (docs/WIT-BRING-YOUR-OWN.md). The function is also exported
	// under its plain name above; this adds the WIT-id alias the composer keys
	// off. Additive — a program with no `@export` emits nothing here, so its
	// bytes are unchanged.
	exportFuncByName := map[string]*ir.Func{}
	for _, fn := range prog.Funcs {
		exportFuncByName[fn.Name] = fn
	}
	for _, exp := range prog.Exports {
		idx, ok := funcIdx[exp.Name]
		if !ok {
			return nil, fmt.Errorf("wasmbin: @export %q: function not found", exp.Name)
		}
		fn := exportFuncByName[exp.Name]
		// A numeric-array (`list<T>`) PARAMETER means the canonical ABI calls the
		// export with (ptr,len) per array, which doesn't line up with the Fern
		// function's single-pointer array param — so it needs a wrapper, and the
		// scalar/composite-RESULT branches below (which forward params straight to
		// the user func) would mis-call it. Reject the array-param + composite-result
		// combination (a later slice) and route the array-param + scalar/void case
		// to the dedicated param wrapper.
		arrParam := fn != nil && funcHasNumericArrayParam(fn)
		if arrParam && (isStringType(fn.ReturnType) || isScalarArrayParamType(fn.ReturnType) || exp.ResultEnum != nil || exp.ResultTuple != nil) {
			return nil, fmt.Errorf("wasmbin: @export %q: a numeric-array parameter with a composite result is not supported yet", exp.Name)
		}
		// A string-RESULT export needs the canonical return-area shape: the Fern
		// function returns a two-word (data,len) pair, but the world-driven
		// composer's memory lift expects a core func returning a single i32
		// pointer to [data,len]. Surface a wrapper that repacks it; scalar-result
		// exports surface the function directly.
		if fn != nil && !arrParam && isStringType(fn.ReturnType) {
			if _, ok := funcIdx["__fern_alloc"]; !ok {
				return nil, fmt.Errorf("wasmbin: @export %q: string result needs __fern_alloc (not pinned)", exp.Name)
			}
			pvts, err := paramValtypes(fn.Params)
			if err != nil {
				return nil, fmt.Errorf("wasmbin: @export %q: %w", exp.Name, err)
			}
			wrapTIdx := addType(pvts, []byte{encode.ValtypeI32})
			wrapFuncIdx := nextFuncIdx
			nextFuncIdx++
			body, locals := buildExportStringResultWrapper(funcIdx, idx, len(pvts))
			m.FunctionTypeidxs = append(m.FunctionTypeidxs, wrapTIdx)
			m.CodeBodies = append(m.CodeBodies, inst.PutFunctionBody(nil, locals, body))
			m.ExportNames = append(m.ExportNames, exp.Iface+"#"+exp.WITName)
			m.ExportKinds = append(m.ExportKinds, sections.ExportFunc)
			m.ExportIdxs = append(m.ExportIdxs, wrapFuncIdx)
			continue
		}
		// A numeric-array (`list<T>`) RESULT export: the Fern function returns a
		// single i32 element pointer, but the composer's memory lift expects a
		// core func returning a pointer to the [data,len] canonical return area.
		// Surface a wrapper that writes it (no copy — the Fern array is already
		// contiguous at the canonical stride). docs/WIT-BRING-YOUR-OWN.md.
		if fn != nil && isScalarArrayParamType(fn.ReturnType) {
			if _, ok := funcIdx["__fern_alloc"]; !ok {
				return nil, fmt.Errorf("wasmbin: @export %q: list result needs __fern_alloc (not pinned)", exp.Name)
			}
			pvts, err := paramValtypes(fn.Params)
			if err != nil {
				return nil, fmt.Errorf("wasmbin: @export %q: %w", exp.Name, err)
			}
			wrapTIdx := addType(pvts, []byte{encode.ValtypeI32})
			wrapFuncIdx := nextFuncIdx
			nextFuncIdx++
			body, locals := buildExportListResultWrapper(funcIdx, idx, len(pvts))
			m.FunctionTypeidxs = append(m.FunctionTypeidxs, wrapTIdx)
			m.CodeBodies = append(m.CodeBodies, inst.PutFunctionBody(nil, locals, body))
			m.ExportNames = append(m.ExportNames, exp.Iface+"#"+exp.WITName)
			m.ExportKinds = append(m.ExportKinds, sections.ExportFunc)
			m.ExportIdxs = append(m.ExportIdxs, wrapFuncIdx)
			continue
		}
		// An Option/Result (WIT `option` / `result`) RESULT export: the Fern
		// function returns an enum box pointer, but the canonical sum returns
		// indirectly through a (disc, payload) return area. Surface a wrapper that
		// writes it (docs/WIT-BRING-YOUR-OWN.md).
		if exp.ResultEnum != nil {
			if _, ok := funcIdx["__fern_alloc"]; !ok {
				return nil, fmt.Errorf("wasmbin: @export %q: sum-type result needs __fern_alloc (not pinned)", exp.Name)
			}
			pvts, err := paramValtypes(fn.Params)
			if err != nil {
				return nil, fmt.Errorf("wasmbin: @export %q: %w", exp.Name, err)
			}
			wrapTIdx := addType(pvts, []byte{encode.ValtypeI32})
			wrapFuncIdx := nextFuncIdx
			nextFuncIdx++
			body, locals := buildExportSumTypeResultWrapper(funcIdx, idx, len(pvts), exp.ResultEnum, prog.PairForm[exp.Name])
			m.FunctionTypeidxs = append(m.FunctionTypeidxs, wrapTIdx)
			m.CodeBodies = append(m.CodeBodies, inst.PutFunctionBody(nil, locals, body))
			m.ExportNames = append(m.ExportNames, exp.Iface+"#"+exp.WITName)
			m.ExportKinds = append(m.ExportKinds, sections.ExportFunc)
			m.ExportIdxs = append(m.ExportIdxs, wrapFuncIdx)
			continue
		}
		// A tuple `(A, B, …)` RESULT export: the Fern function returns a tuple
		// value pointer, but the canonical tuple returns indirectly through a
		// return area. Surface a wrapper that copies each element to it.
		if exp.ResultTuple != nil {
			if _, ok := funcIdx["__fern_alloc"]; !ok {
				return nil, fmt.Errorf("wasmbin: @export %q: tuple result needs __fern_alloc (not pinned)", exp.Name)
			}
			pvts, err := paramValtypes(fn.Params)
			if err != nil {
				return nil, fmt.Errorf("wasmbin: @export %q: %w", exp.Name, err)
			}
			wrapTIdx := addType(pvts, []byte{encode.ValtypeI32})
			wrapFuncIdx := nextFuncIdx
			nextFuncIdx++
			body, locals := buildExportTupleResultWrapper(funcIdx, idx, len(pvts), exp.ResultTuple)
			m.FunctionTypeidxs = append(m.FunctionTypeidxs, wrapTIdx)
			m.CodeBodies = append(m.CodeBodies, inst.PutFunctionBody(nil, locals, body))
			m.ExportNames = append(m.ExportNames, exp.Iface+"#"+exp.WITName)
			m.ExportKinds = append(m.ExportKinds, sections.ExportFunc)
			m.ExportIdxs = append(m.ExportIdxs, wrapFuncIdx)
			continue
		}
		// A numeric-array (`list<T>`) PARAMETER export (scalar/void result): the
		// canonical ABI hands the export (ptr,len) per array, so surface a wrapper
		// that rebuilds the length-prefixed Fern array and calls the user func.
		if arrParam {
			if _, ok := funcIdx["__fern_alloc"]; !ok {
				return nil, fmt.Errorf("wasmbin: @export %q: list param needs __fern_alloc (not pinned)", exp.Name)
			}
			pvts, err := canonicalExportParamVts(fn.Params)
			if err != nil {
				return nil, fmt.Errorf("wasmbin: @export %q: %w", exp.Name, err)
			}
			rvts, err := resultValtypes(fn.ReturnType)
			if err != nil {
				return nil, fmt.Errorf("wasmbin: @export %q: %w", exp.Name, err)
			}
			wrapTIdx := addType(pvts, rvts)
			wrapFuncIdx := nextFuncIdx
			nextFuncIdx++
			body, locals := buildExportListParamWrapper(funcIdx, idx, fn.Params)
			m.FunctionTypeidxs = append(m.FunctionTypeidxs, wrapTIdx)
			m.CodeBodies = append(m.CodeBodies, inst.PutFunctionBody(nil, locals, body))
			m.ExportNames = append(m.ExportNames, exp.Iface+"#"+exp.WITName)
			m.ExportKinds = append(m.ExportKinds, sections.ExportFunc)
			m.ExportIdxs = append(m.ExportIdxs, wrapFuncIdx)
			continue
		}
		m.ExportNames = append(m.ExportNames, exp.Iface+"#"+exp.WITName)
		m.ExportKinds = append(m.ExportKinds, sections.ExportFunc)
		m.ExportIdxs = append(m.ExportIdxs, idx)
	}
	if opts.SynthCliRun {
		mainIdx, ok := funcIdx["main"]
		if !ok {
			return nil, fmt.Errorf("wasmbin: SynthCliRun needs a `main` function")
		}
		mainPosInFnSection := mainIdx - uint32(len(importNeeds.order))
		mainResults := m.TypeResults[m.FunctionTypeidxs[mainPosInFnSection]]
		runTIdx := addType(nil, []byte{encode.ValtypeI32})
		runFuncIdx := nextFuncIdx
		nextFuncIdx++
		var body []byte
		body = inst.InstCall(body, mainIdx)
		switch {
		case len(mainResults) == 0:
			body = inst.InstI32Const(body, 0)
		case len(mainResults) == 1 && mainResults[0] == encode.ValtypeI32:
			// PrintMainResult routes main's i32 through int_to_string +
			// __fern_print (so e2e tests can observe it over stdout, the
			// same as the _start path) and returns 0 — preview-2's
			// wasi:cli/run only surfaces ok/err, not the value. Falls back
			// to normalising main's i32 to the run result when no
			// int_to_string variant survived tree-shake.
			printed := false
			if opts.PrintMainResult {
				intToStrName := ""
				if _, ok := funcIdx["int_to_string"]; ok {
					intToStrName = "int_to_string"
				} else if _, ok := funcIdx["int__int_to_string"]; ok {
					intToStrName = "int__int_to_string"
				}
				if intToStrName != "" {
					body = inst.InstCall(body, funcIdx[intToStrName])
					printIdx, ok := funcIdx["__fern_print"]
					if !ok {
						return nil, fmt.Errorf("wasmbin: PrintMainResult: __fern_print helper not registered (scanRuntimeHelpers gap)")
					}
					body = inst.InstCall(body, printIdx)
					body = inst.InstI32Const(body, 0)
					printed = true
				}
			}
			if !printed && opts.CliRunResult {
				// main's i32 becomes the run result, which the canon
				// lift treats as the discriminant of `result<_, _>`:
				// only 0 (ok) and 1 (err) are valid — any other value
				// traps the host with "invalid expected discriminant".
				// Normalise to the documented contract (0 = ok, non-zero
				// = err) with a double i32.eqz: eqz(eqz(v)) is 0 when v
				// is 0 and 1 for every non-zero v. Skipped for the raw
				// u32-export shape (CliRunResult off), where main's full
				// value is the legitimate result.
				body = numeric.InstI32Eqz(body)
				body = numeric.InstI32Eqz(body)
			}
		default:
			return nil, fmt.Errorf("wasmbin: SynthCliRun: `main` must return void or i32, got %v", mainResults)
		}
		m.FunctionTypeidxs = append(m.FunctionTypeidxs, runTIdx)
		m.CodeBodies = append(m.CodeBodies, inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body))
		m.ExportNames = append(m.ExportNames, "_lang_run")
		m.ExportKinds = append(m.ExportKinds, sections.ExportFunc)
		m.ExportIdxs = append(m.ExportIdxs, runFuncIdx)
	}
	if opts.AsyncExportName != "" {
		// WASI Preview-3 async export: a synthetic `() -> ()` core
		// function that calls `main` (which must return i32), hands the
		// result to the `("", "task-return")` import, then returns void
		// — the async ABI (result via task.return; function-return =
		// task done). The composer lifts it with the `async` canonical
		// option (component.BuildAsyncLiftedExportComponent).
		srcFn := opts.AsyncSourceFunc
		if srcFn == "" {
			srcFn = "main"
		}
		mainIdx, ok := funcIdx[srcFn]
		if !ok {
			return nil, fmt.Errorf("wasmbin: AsyncExportName needs the source function %q", srcFn)
		}
		mainPosInFnSection := mainIdx - uint32(len(importNeeds.order))
		mainResults := m.TypeResults[m.FunctionTypeidxs[mainPosInFnSection]]
		// A single scalar result (i32/i64/f32/f64) is delivered through
		// task.return, whose import param was width-matched to it above. A
		// void/multi result has no scalar to hand over.
		if len(mainResults) != 1 {
			return nil, fmt.Errorf("wasmbin: AsyncExportName: %q must return a single scalar, got %v", srcFn, mainResults)
		}
		trIdx, ok := funcIdx["async_task_return"]
		if !ok {
			return nil, fmt.Errorf("wasmbin: AsyncExportName: task-return import not registered")
		}
		// The wrapper mirrors the source function's (scalar) parameters and
		// forwards them, so `async function f(a, b): T` lifts as a component
		// export `f: async func(a, b) -> T` (not just the no-param main/run case).
		// Params are the wrapper's locals 0..n-1; void source params → `() -> ()`.
		srcParams := m.TypeParams[m.FunctionTypeidxs[mainPosInFnSection]]
		asyncTIdx := addType(srcParams, nil) // (srcParams) -> ()
		asyncFuncIdx := nextFuncIdx
		nextFuncIdx++
		var body []byte
		for i := range srcParams {
			body = inst.InstLocalGet(body, uint32(i)) // forward param i
		}
		body = inst.InstCall(body, mainIdx) // src(params…) -> scalar (result on stack)
		body = inst.InstCall(body, trIdx)   // task-return(scalar)
		// void return
		m.FunctionTypeidxs = append(m.FunctionTypeidxs, asyncTIdx)
		m.CodeBodies = append(m.CodeBodies, inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body))
		m.ExportNames = append(m.ExportNames, opts.AsyncExportName)
		m.ExportKinds = append(m.ExportKinds, sections.ExportFunc)
		m.ExportIdxs = append(m.ExportIdxs, asyncFuncIdx)
	}
	if opts.HttpHandler && !opts.SynthStart {
		// Proxy components don't run a `main()` — `wasmtime
		// serve` invokes the exported `wasi:http/incoming-
		// handler.handle` per request — but the wasi-preview1-
		// component-adapter (`command.wasm`) still wires its
		// own `wasi:cli/run.run` to a `_start` core export, so
		// we emit an empty stub. Switching to the dedicated
		// `proxy.wasm` adapter would drop this requirement;
		// the project doesn't ship that yet. Mirrors the WAT
		// path's empty-stub branch (wasm_ir.go ~302).
		startTIdx := addType(nil, nil) // () -> ()
		startFuncIdx := nextFuncIdx
		nextFuncIdx++
		m.FunctionTypeidxs = append(m.FunctionTypeidxs, startTIdx)
		m.CodeBodies = append(m.CodeBodies, inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), nil))
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
//
// Pointer-shaped composite types (struct, enum, array, slice,
// tuple, closure / *FuncType) reduce to a single i32 slot on
// wasm32 — the value is the heap pointer. The IR uses OpAlloc +
// OpStore / OpLoad to materialise these on the heap; codegen
// just needs to thread the pointer through param / local /
// return slots.
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
	case ast.ArrayType, ast.SliceType, ast.TupleType, ast.StructType, ast.EnumType:
		return encode.ValtypeI32, nil
	case ast.HandleType:
		// own/borrow R — a resource handle is an opaque i32 (P5).
		return encode.ValtypeI32, nil
	case ast.CharType:
		// A Unicode scalar rides an i32 slot, the same way a handle does.
		// ir.eraseSurfaceTypes turns `char` into i32 for the declaration
		// and Info slots it can reach, but a lowering-created scratch slot
		// can still be typed from an expression position the walk misses,
		// and the other three backends classify by width and never notice.
		// This seam classifies by TYPE, so it has to know the shape —
		// listing it here is the same call HandleType already makes.
		return encode.ValtypeI32, nil
	case *ast.FuncType:
		return encode.ValtypeI32, nil
	}
	return 0, fmt.Errorf("unsupported type %s (scalar i32/i64/f32/f64 + bool + pointer-shaped composites only at this seam)", t)
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

// isTwoWordType reports whether t occupies two adjacent i32 wasm slots:
// a string `(data, len)` or a `dyn Trait` fat pointer `[data, vtable]`.
// Both fan out identically at the param / local / result / load / store
// seams — the second word is just another i32 — so the two-word string
// machinery serves dyn values unchanged. See docs/DYN-TRAITS.md §4.2.1.
func isTwoWordType(t ast.Type) bool {
	if isStringType(t) {
		return true
	}
	_, ok := t.(ast.DynTraitType)
	return ok
}

// slotValtypes returns the wasm valtype sequence for an ast.Type
// used as a slot (param / local / result). Strings fan out to
// `[i32, i32]` for the two-word ABI; everything else maps to a
// single valtype via valtypeFor.
func slotValtypes(t ast.Type) ([]byte, error) {
	if isTwoWordType(t) {
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

// canonicalExportParamVts flattens an `@export` function's parameters to the
// core valtypes the canonical ABI passes the lifted export (the wrapper-facing
// shape, vs paramValtypes's Fern-side flattening): a string or numeric array is
// `(ptr, len)` — two i32s — and a scalar keeps its slot. Used to type the
// export PARAM wrapper that materialises arrays before calling the user func.
func canonicalExportParamVts(params []ast.Param) ([]byte, error) {
	var out []byte
	for _, p := range params {
		switch {
		case isStringType(p.Type) || isScalarArrayParamType(p.Type):
			out = append(out, encode.ValtypeI32, encode.ValtypeI32)
		default:
			vts, err := slotValtypes(p.Type)
			if err != nil {
				return nil, err
			}
			out = append(out, vts...)
		}
	}
	return out, nil
}

// canonicalExternParamValtypes flattens an `@import` extern's parameters to the
// core valtypes the *raw* component import carries (the host-facing canonical
// ABI), as opposed to paramValtypes, which gives the Fern-side flattening that
// a param wrapper consumes. They differ for `u8[]` (a Fern array is a single
// element pointer, but a canonical `list<u8>` is `(ptr, len)` — two i32s) and
// records (a Fern struct is one pointer, but a canonical `record` flattens to
// its fields' core types). A `string` is two slots either way (its Fern
// (data, len) pair already lines up with ptr+len once normalized).
func canonicalExternParamValtypes(ex *ir.ExternFunc) ([]byte, error) {
	var out []byte
	for i, p := range ex.Params {
		switch {
		case isStringType(p.Type) || isScalarArrayParamType(p.Type) || isBoolArrayParamType(p.Type):
			out = append(out, encode.ValtypeI32, encode.ValtypeI32)
		case ex.ParamRecords[i] != nil:
			for _, f := range ex.ParamRecords[i] {
				out = append(out, externRecordFieldValtype(f.Type))
			}
		case ex.ParamEnums[i] != nil:
			// variant flattens to (disc:i32, payload-join). A multi-field variant
			// joins to SlotCount slots, each its canonical join valtype (i32/i64/f32/
			// f64); a single-field one to one payload slot.
			ep := ex.ParamEnums[i]
			out = append(out, encode.ValtypeI32)
			if ep.SlotCount > 0 {
				for s := int32(0); s < ep.SlotCount; s++ {
					out = append(out, externRecordFieldValtype(ep.SlotTypes[s]))
				}
			} else {
				out = append(out, externRecordFieldValtype(ep.PayloadType))
			}
		case ex.ParamPlainEnums[i]:
			// plain enum → WIT enum: a single i32 discriminant.
			out = append(out, encode.ValtypeI32)
		default:
			vt, err := valtypeFor(p.Type)
			if err != nil {
				return nil, err
			}
			out = append(out, vt)
		}
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

// externScalarType reports whether t is a scalar an `@import` extern's
// signature can carry today (bring-your-own WIT P4b — docs/WIT-BRING-YOUR-OWN.md):
// integers, floats, and bool, all of which lower to a single i32/i64/f64 slot
// with no canonical-ABI marshalling. Composite types (string, list, record,
// tuple, variant, option, result) need the canonical lift/lower that's P4c, so
// they're rejected up front rather than silently miscompiled into raw pointer
// slots that don't match the host's ABI. void is allowed only as a return type
// (handled by the caller).
func externScalarType(t ast.Type) bool {
	switch t.(type) {
	case ast.NumberType, ast.FloatType, ast.BoolType:
		return true
	}
	return false
}

// isScalarArrayParamType reports whether t is an array of a fixed-width numeric
// element (`u8[]`, `i32[]`, `f32[]`, `i64[]`, `f64[]`, …) — the arrays that
// lower to a canonical `list<T>` (ptr, len) as an `@import` parameter (and lift
// the same way as a result). A Fern array value is the element pointer, with
// the element count in the i32 prefix at `ptr-4` and elements packed at native
// stride, so it's already a valid canonical payload (the count is the element
// count, matching the canonical list length). bool arrays (whose Fern stride
// differs from the canonical 1-byte `list<bool>`) are left to a later slice.
func isScalarArrayParamType(t ast.Type) bool {
	at, ok := t.(ast.ArrayType)
	if !ok {
		return false
	}
	switch at.Elem.(type) {
	case ast.NumberType, ast.FloatType:
		return true
	}
	return false
}

// isBoolArrayParamType reports whether t is a `bool[]`. A Fern bool is a 4-byte
// i32 (0/1), but the canonical `list<bool>` element is a single byte, so —
// unlike the numeric arrays, whose native stride already matches the canonical
// element size — a bool array can't be passed/lifted zero-copy: it needs a
// byte-repacking copy (params: contract each 4-byte bool to one byte; results:
// expand each canonical byte back to a 4-byte slot). Handled by dedicated
// wrappers rather than the zero-copy `isScalarArrayParamType` path.
func isBoolArrayParamType(t ast.Type) bool {
	at, ok := t.(ast.ArrayType)
	if !ok {
		return false
	}
	_, ok = at.Elem.(ast.BoolType)
	return ok
}

// externRecordFieldValtype returns the flat core valtype a record field
// flattens to in the canonical ABI: i64 for a 64-bit integer, f64 for a 64-bit
// float, f32 for a 32-bit float, i32 for everything else (32-bit integers). It
// matches the load op the record-param wrapper uses and the field's storage
// slot, so the flattened import signature and the wrapper agree.
func externRecordFieldValtype(t ast.Type) byte {
	switch x := t.(type) {
	case ast.NumberType:
		if x.NormalWidth() == 64 {
			return encode.ValtypeI64
		}
		return encode.ValtypeI32
	case ast.FloatType:
		if x.NormalWidth() == 64 {
			return encode.ValtypeF64
		}
		return encode.ValtypeF32
	}
	return encode.ValtypeI32
}

// scalarArrayElemStride returns the element size in bytes of a scalar array
// type accepted by isScalarArrayParamType (1 for u8, 4 for i32/u32/f32, 8 for
// i64/u64/f64). Used to size the materialized Fern array and the host-byte
// copy for a `list<T>` result.
func scalarArrayElemStride(t ast.Type) uint32 {
	at, ok := t.(ast.ArrayType)
	if !ok {
		return 1
	}
	return uint32(ast.ElemSizeBytesFor(at.Elem, 4))
}

// localValtypes returns the wasm valtype vector for an IR function's
// declared locals + scratch slots — exactly what the local-section
// preamble of the function body needs. String-typed slots fan out
// to two i32 slots `(data, len)`. Three additional i32 scratch
// slots are appended for functions that load/store strings to
// heap memory (OpLoad/OpStore with WidthString) — used by the
// two-word ABI fan-out.
// localValtypes returns the wasm valtype vector for fn's
// declared locals + scratch slots + closure-make scratch
// derived from the program's funcByName map. The closure
// scratch needs per-capture types so it's threaded through
// funcByName rather than reading from fn alone.
func localValtypes(fn *ir.Func, funcByName map[string]*ir.Func) ([]byte, error) {
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
	// Closure-make scratch is typed per-capture; compute it via
	// closureMakeSites so layout matches what emitMakeEnv /
	// emitMakeClosure will use.
	_, closureScratch, err := closureMakeSites(fn, funcByName, 0)
	if err != nil {
		return nil, err
	}
	out = append(out, closureScratch...)
	if fnNeedsCallIndirectScratch(fn) {
		for i := 0; i < callIndirectScratchSlots; i++ {
			out = append(out, encode.ValtypeI32)
		}
	}
	if fnNeedsCallDynScratch(fn) {
		for i := 0; i < callDynScratchSlots; i++ {
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

// callDynScratchSlots is the count of extra wasm locals appended when a
// function uses OpCallDyn. The single scratch holds the vtable address
// while the receiver's vtable word is popped off the top of the stack
// (so the i32.load of slot k can fetch the function-table index).
const callDynScratchSlots = 1

func fnNeedsCallDynScratch(fn *ir.Func) bool {
	for _, op := range fn.Ops {
		if op.Kind == ir.OpCallDyn {
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
	// progFuncTableIdx maps an IR function name to its position in
	// prog.Funcs — i.e. its slot in the element segment. Closure
	// pair cells (OpMakeClosure, OpConstFunc) store this value
	// because call_indirect indexes by table-idx, not funcidx
	// (the import shift would skew the lookup otherwise).
	progFuncTableIdx map[string]uint32
	// addSigType resolves a function-type signature to its
	// typeidx, lazily inserting into the type section.
	addSigType func(*ast.FuncType) (uint32, error)
	// addClosureSigType is like addSigType but appends an i32
	// (env_ptr) to params, matching the closure-target ABI
	// every OpCallIndirect dispatches through.
	addClosureSigType func(*ast.FuncType) (uint32, error)
	// addRawType registers a wasm-level (params, results) type
	// directly and returns its typeidx. Used for multi-value
	// block types where the IR has the valtype bytes already
	// and bypasses the AST-level addSigType seam.
	addRawType func(params, results []byte) uint32
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
	// callDynScratchIdx is the wasm-slot index of the scratch i32
	// used by OpCallDyn to stash the vtable address while it loads
	// the function-table index of the dispatched method slot.
	callDynScratchIdx uint32
	// internVtable returns the data-segment address of the (trait,
	// concrete) vtable, interning per pair. Used by OpConstVtable.
	internVtable func(trait, concrete string) (int, error)
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
	// closureSites is the per-function precomputed scratch
	// layout for each OpMakeEnv / OpMakeClosure site, in the
	// order they appear in fn.Ops. emitMakeEnv / emitMakeClosure
	// step through this slice via closureSiteCursor as they
	// encounter their respective ops.
	closureSites      []closureMakeSite
	closureSiteCursor int
	// funcByName looks up an IR function by name (across the
	// whole program). emitClosure-pre-pass uses this to read
	// per-capture types from the target function.
	funcByName map[string]*ir.Func
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
		if isTwoWordType(slotType(fn, j)) {
			wasm++
		}
	}
	return uint32(wasm)
}

// bodySlotIdx maps an IR slot to its wasm local index INSIDE a
// function body, accounting for the closure-target env_ptr param the
// type-section append inserts between the declared params and the
// body locals (emitFuncSection). Without the shift, a no-capture
// named function used as a closure VALUE (OpConstFunc — closureconv
// never adds its `__env` IR param because there is nothing to
// capture) reads/writes its body locals one slot low: local 0..P-1
// are the params, wasm slot P is the appended env, and the first body
// local lives at P+1 — but the unshifted mapping pointed it at P.
// Latent until such a function had body locals at all (tiny adapter
// fns don't); first hit by core/map's default hash adapters (#5042).
func bodySlotIdx(ctx *emitCtx, fn *ir.Func, irIdx int32) uint32 {
	idx := wasmSlotIdx(fn, irIdx)
	if irIdx >= int32(len(fn.Params)) && ctx.closureTargets[fn.Name] && !hasEnvParam(fn.Params) {
		idx++
	}
	return idx
}

// emitBody walks fn.Ops and returns the function's body bytes plus
// its locals-preamble bytes (the latter pre-wrapped by
// inst.PutLocalsOneGroup-equivalent encoding for the declared local
// valtypes).
func emitBody(fn *ir.Func, ctx *emitCtx) (body, locals []byte, err error) {
	lvts, err := localValtypes(fn, ctx.funcByName)
	if err != nil {
		return nil, nil, err
	}
	locals = encodeLocals(lvts)

	ctx.fn = fn
	// The scratch base is the wasm-slot index just past every IR
	// slot (params + locals + scratch types, accounting for string-
	// typed slots taking 2 wasm slots each).
	lastIR := int32(len(fn.Params) + len(fn.Locals) + len(fn.ScratchTypes))
	ctx.strPairScratchBase = wasmSlotIdx(fn, lastIR)
	// Closure-target functions get an extra wasm slot for the
	// env_ptr param appended by the closure-target ABI, BUT only
	// when the IR didn't already include __env as a param (post
	// closureconv). With __env in IR.Params, lastIR + wasmSlotIdx
	// already covers it.
	if ctx.closureTargets[fn.Name] && !hasEnvParam(fn.Params) {
		ctx.strPairScratchBase++
	}
	// pairMake scratch sits AFTER any str-pair scratch slots.
	pairBase := ctx.strPairScratchBase
	if fnNeedsStrPairScratch(fn) {
		pairBase += strPairScratchSlots
	}
	ctx.pairMakeScratchIdx = pairBase
	// closureMake scratch sits AFTER pairMake — populate per-
	// site layout via closureMakeSites.
	closureBase := pairBase
	if fnNeedsPairMakeScratch(fn) {
		closureBase += pairMakeScratchSlots
	}
	sites, closureScratch, err := closureMakeSites(fn, ctx.funcByName, closureBase)
	if err != nil {
		return nil, nil, err
	}
	ctx.closureSites = sites
	ctx.closureSiteCursor = 0
	// callIndirect scratch sits AFTER closureMake.
	callIndBase := closureBase + uint32(len(closureScratch))
	ctx.callIndirectScratchIdx = callIndBase
	// callDyn scratch sits AFTER callIndirect.
	callDynBase := callIndBase
	if fnNeedsCallIndirectScratch(fn) {
		callDynBase += callIndirectScratchSlots
	}
	ctx.callDynScratchIdx = callDynBase
	defer func() {
		ctx.fn = nil
		ctx.strPairScratchBase = 0
		ctx.pairMakeScratchIdx = 0
		ctx.closureSites = nil
		ctx.closureSiteCursor = 0
		ctx.callIndirectScratchIdx = 0
		ctx.callDynScratchIdx = 0
	}()

	for opIdx, op := range fn.Ops {
		body, err = emitOp(body, op, ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("op[%d] %v: %w", opIdx, op.Kind, err)
		}
	}
	// TCO sentinel: when the function body is the synthetic
	// outer loop from `TailCallOptimize` (first op OpLoop, last
	// op OpEnd, every iteration ends in OpReturn / `br 0`), the
	// loop-end is unreachable at runtime but the wasm validator
	// still expects the operand stack at function exit to match
	// the result type. An `unreachable` after the loop's end
	// makes the validator treat the exit point as stack-poly.
	// No-op for functions that legitimately fall through a
	// trailing OpEnd (e.g. an if-as-expression with both arms
	// pushing the result and the function returning the if's
	// value) — those need to actually push the result, not trap.
	if len(fn.Ops) > 1 &&
		fn.Ops[0].Kind == ir.OpLoop &&
		fn.Ops[len(fn.Ops)-1].Kind == ir.OpEnd {
		if _, isVoid := fn.ReturnType.(ast.VoidType); !isVoid && fn.ReturnType != nil {
			body = inst.InstUnreachable(body)
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
		if fernstring.FitsInlineWasm(len(op.Str)) {
			data, length := fernstring.PackInlineWasm([]byte(op.Str))
			body = inst.InstI32Const(body, int32(data))
			body = inst.InstI32Const(body, int32(length))
			return body, nil
		}
		off := ctx.internString(op.Str)
		body = inst.InstI32Const(body, int32(off))
		body = inst.InstI32Const(body, int32(len(op.Str)))
		return body, nil

	case ir.OpLoadLocal:
		idx := bodySlotIdx(ctx, ctx.fn, op.I32)
		if isTwoWordType(slotType(ctx.fn, op.I32)) {
			// Two-word ABI: push (data, len) in low-to-high
			// order so the stack mirrors a fresh OpConstStr.
			body = inst.InstLocalGet(body, idx)
			body = inst.InstLocalGet(body, idx+1)
			return body, nil
		}
		return inst.InstLocalGet(body, idx), nil
	case ir.OpStoreLocal:
		idx := bodySlotIdx(ctx, ctx.fn, op.I32)
		if isTwoWordType(slotType(ctx.fn, op.I32)) {
			// Stack: [..., data, len]. Pop len first (top of
			// stack), then data, into adjacent locals.
			body = inst.InstLocalSet(body, idx+1)
			body = inst.InstLocalSet(body, idx)
			return body, nil
		}
		return inst.InstLocalSet(body, idx), nil
	case ir.OpTeeLocal:
		idx := bodySlotIdx(ctx, ctx.fn, op.I32)
		if isTwoWordType(slotType(ctx.fn, op.I32)) {
			// Same as store-then-load: pop len, tee data
			// (leaves data on stack), push len back.
			body = inst.InstLocalSet(body, idx+1)
			body = inst.InstLocalTee(body, idx)
			body = inst.InstLocalGet(body, idx+1)
			return body, nil
		}
		return inst.InstLocalTee(body, idx), nil

	case ir.OpDrop:
		// Width=WidthString means the dropped value is a two-slot
		// (data, len) pair from the SSO string ABI — needs two
		// wasm `drop`s. Set by copyprop when it rewrites a dead
		// OpStoreLocal on a string slot. Default width drops one
		// slot.
		if op.Width == ir.WidthString {
			body = inst.InstDrop(body)
		}
		return inst.InstDrop(body), nil
	case ir.OpReturn:
		return inst.InstReturn(body), nil
	case ir.OpReturnVoid:
		return inst.InstReturn(body), nil

	case ir.OpBlock:
		btEnc, err := blocktypeEnc(op.I32, ctx)
		if err != nil {
			return nil, err
		}
		body = append(body, 0x02)
		return append(body, btEnc...), nil
	case ir.OpLoop:
		btEnc, err := blocktypeEnc(op.I32, ctx)
		if err != nil {
			return nil, err
		}
		body = append(body, 0x03)
		return append(body, btEnc...), nil
	case ir.OpIf:
		btEnc, err := blocktypeEnc(op.I32, ctx)
		if err != nil {
			return nil, err
		}
		body = append(body, 0x04)
		return append(body, btEnc...), nil
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
	case ir.OpDivS, ir.OpRemS:
		// Route through the guarded runtime helper so division
		// follows the never-trap contract (x/0 = 0, x%0 = x,
		// INT_MIN/-1 = INT_MIN, INT_MIN%-1 = 0) instead of the raw
		// div/rem instruction, which traps on those edges.
		name := intDivRemHelperName(op.Width == 64, op.Unsigned, op.Kind == ir.OpRemS)
		idx, ok := ctx.funcIdx[name]
		if !ok {
			return nil, fmt.Errorf("%s: %s helper not registered (scanRuntimeHelpers gap)", op.Kind, name)
		}
		return inst.InstCall(body, idx), nil
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
	case ir.OpReinterpretI32F32:
		return convert.InstI32ReinterpretF32(body), nil
	case ir.OpReinterpretF32I32:
		return convert.InstF32ReinterpretI32(body), nil
	case ir.OpReinterpretI64F64:
		return convert.InstI64ReinterpretF64(body), nil
	case ir.OpReinterpretF64I64:
		return convert.InstF64ReinterpretI64(body), nil

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
		// Saturating (trunc_sat): NaN → 0, out-of-range clamps to
		// the destination's min/max instead of trapping — matches
		// the native backends' saturating contract.
		if op.Width == 64 {
			if op.Unsigned {
				return convert.InstI64TruncSatF32U(body), nil
			}
			return convert.InstI64TruncSatF32S(body), nil
		}
		if op.Unsigned {
			return convert.InstI32TruncSatF32U(body), nil
		}
		return convert.InstI32TruncSatF32S(body), nil
	case ir.OpITruncF64:
		if op.Width == 64 {
			if op.Unsigned {
				return convert.InstI64TruncSatF64U(body), nil
			}
			return convert.InstI64TruncSatF64S(body), nil
		}
		if op.Unsigned {
			return convert.InstI32TruncSatF64U(body), nil
		}
		return convert.InstI32TruncSatF64S(body), nil

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
			body = numeric.InstI32Add(body)       // [data, addr+4]
			body = memory.InstI32Load(body, 2, 0) // [data, len]
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
	case ir.OpStoreI8:
		return memory.InstI32Store8(body, 0, 0), nil

	// ---- Calls (slice 5) ----
	case ir.OpCallDirect, ir.OpRcInc, ir.OpRcDec, ir.OpRcIsUnique:
		// Source-language built-ins (e.g. `print(s)`) get lowered
		// to OpCallDirect with the source name. Map those names
		// onto the synthetic runtime helpers that implement them.
		// User functions and helpers without an alias map 1:1.
		// The dedicated rc ops (#4402 opt 2) carry the helper name
		// in Str and lower to the same plain call; opt 2b replaces
		// this shared path with inline fast-path bodies.
		name := callDirectAlias(op.Str)
		idx, ok := ctx.funcIdx[name]
		if !ok {
			return nil, fmt.Errorf("%s: unknown callee %q", op.Kind, op.Str)
		}
		// A function that is BOTH direct-called and used as a closure
		// value (e.g. `sort_by(arr, string_cmp)` while string_cmp is
		// also called directly) got an env_ptr appended to its wasm
		// signature by the closure-target ABI. The direct-call IR site
		// supplies only the natural args, so push a dummy env_ptr (0)
		// after them — env is the last param and such a target never
		// reads it (only captured closures do, and those aren't
		// direct-called by name). See closureTargetSet's invariant
		// note; a bare comparator passed to sort_by breaks it (#4829).
		if op.Kind == ir.OpCallDirect && calleeEnvAppended(ctx, op.Str) {
			body = inst.InstI32Const(body, 0)
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
		if op.Sig() == nil {
			return nil, fmt.Errorf("OpCallIndirect: missing op.Sig()")
		}
		// Closure-target ABI: callee signature has env_ptr (i32)
		// appended. The typeidx we dispatch through must match —
		// derive it from op.Sig() + env_ptr.
		tIdx, err := ctx.addClosureSigType(op.Sig())
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

	// ---- `dyn Trait` runtime dispatch ----
	case ir.OpConstVtable:
		// Push the static address of the (trait, concrete) vtable —
		// an array of i32 function-table indices in the data segment.
		off, err := ctx.internVtable(op.Str, op.Str2())
		if err != nil {
			return nil, err
		}
		return inst.InstI32Const(body, int32(off)), nil
	case ir.OpCallDyn:
		if op.Sig() == nil {
			return nil, fmt.Errorf("OpCallDyn: missing op.Sig()")
		}
		// The dyn receiver ABI is a plain i32 pointer (no env append),
		// so dispatch through the no-env signature.
		tIdx, err := ctx.addSigType(op.Sig())
		if err != nil {
			return nil, fmt.Errorf("OpCallDyn: resolving signature: %w", err)
		}
		// Stack: [data, args..., vtable]. Pop the vtable into a scratch
		// local, load slot `op.I32` (`+ slot*4`) to get the function-
		// table index, push it, and call_indirect — leaving [data,
		// args...] as the callee's arguments under the table index.
		idx := ctx.callDynScratchIdx
		body = inst.InstLocalSet(body, idx) // pop vtable → scratch
		body = inst.InstLocalGet(body, idx)
		if op.I32 != 0 {
			body = inst.InstI32Const(body, op.I32*4)
			body = numeric.InstI32Add(body)
		}
		body = memory.InstI32Load(body, 2, 0) // push fn_table_idx
		return inst.InstCallIndirect(body, tIdx, 0), nil

	case ir.OpBoxDyn:
		// OpBoxDyn is the BOXED native (`dyn Trait`) representation
		// (docs/DYN-TRAITS.md §4.2.2); wasm uses the inline two-word
		// fat pointer and never emits it. Reaching here is an IR bug.
		return nil, fmt.Errorf("OpBoxDyn is native-only; wasm uses the inline two-word dyn representation")

	// ---- String runtime helpers ----
	case ir.OpStrLen:
		// Stack: (data, len). The synthetic __fern_str_len helper
		// consumes both and returns the SSO-aware byte length.
		idx, ok := ctx.funcIdx["__fern_str_len"]
		if !ok {
			return nil, fmt.Errorf("OpStrLen: __fern_str_len helper not registered (scanRuntimeHelpers gap)")
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
		// Same env-append reconciliation as OpCallDirect (#4829): a
		// pair-returning function that is also a closure target needs a
		// dummy env_ptr pushed after its natural args on a direct call.
		if calleeEnvAppended(ctx, op.Str) {
			body = inst.InstI32Const(body, 0)
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
		// Stack: (size). __fern_alloc bumps memory[40] and returns
		// the OLD value as the i32 pointer.
		idx, ok := ctx.funcIdx["__fern_alloc"]
		if !ok {
			return nil, fmt.Errorf("OpAlloc: __fern_alloc helper not registered (scanRuntimeHelpers gap)")
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

// CallDirectAliases maps source-language built-in names that the
// IR lowering emits as OpCallDirect targets onto the runtime
// helpers or stdlib impls that actually implement them in wasmbin.
//
// Two flavours of entry:
//   - Synthetic helper aliases (e.g. `print` → `__fern_print`)
//     route to runtime-emitted wasm helpers in runtime.go / wasi.go.
//   - Codegen aliases (e.g. `map_new` → `map_new_impl`) route to
//     stdlib `.fern` functions that exist as regular IR functions
//     but only get referenced by their alias-target name at emit
//     time. The IR-level reachability walker has to know about
//     these so the impls survive dead-function elimination —
//     wasmbin.Build threads this map into LiveFunctionsWithAliases
//     for that reason.
//
// Names not in the map pass through unchanged.
var CallDirectAliases = map[string]string{
	// Synthetic runtime helpers.
	"exit":         "__fern_exit",
	"print":        "__fern_print",
	"eprint":       "__fern_eprint",
	"write":        "__fern_write",
	"putchar":      "__fern_putchar",
	"random_i32":   "__fern_random_i32",
	"random_bytes": "__fern_random_bytes",
	"now_ns":       "__fern_now_ns",
	"now_unix_ms":  "__fern_now_unix_ms",
	"monotonic_ns": "__fern_monotonic_ns",

	// wasm reactor primitives (Preview-2 pollables): a timer
	// pollable from monotonic-clock.subscribe-duration, a blocking
	// wait on a pollable, the poll(list<pollable>) multiplexer, and
	// the pollable drop. See docs/WASM-REACTOR-PLAN.md.
	"wasm_timer_pollable": "__fern_wasm_timer_pollable",
	"wasm_block":          "__fern_wasm_block",
	"wasm_poll":           "__fern_wasm_poll",
	"wasm_pollable_drop":  "__fern_wasm_pollable_drop",

	// f64 math primitives that map to native wasm ops. sin /
	// cos / log / exp / pow / round have no wasm-native shape
	// and stay unimplemented in wasmbin for now (the WAT path
	// is in the same state).
	"__sqrt_f64":  "__fern_sqrt_f64",
	"__abs_f64":   "__fern_abs_f64",
	"__floor_f64": "__fern_floor_f64",
	"__ceil_f64":  "__fern_ceil_f64",
	"__trunc_f64": "__fern_trunc_f64",
	"env_count":   "__fern_env_count",
	"arg_count":   "__fern_arg_count",
	"arg_at":      "__fern_arg_at",
	"env_at":      "__fern_env_at",
	"args":        "__fern_args",
	"env":         "__fern_env",
	"read_byte":   "__fern_read_byte",
	"read_line":   "__fern_read_line",

	// Reader / Writer API. `stdin()` returns a real Reader
	// struct (`{ fd: 0 }`); the `__method_Reader_*` helpers
	// dispatch on `r.fd` so the same lowering covers stdin
	// and file Readers identically. `open_writer` /
	// `open_appender` produce Writers similarly.
	"stdin":                      "__fern_stdin",
	"__method_Reader_read_line":  "__fern_reader_read_line_fd",
	"__method_Reader_read_chunk": "__fern_reader_read_chunk",
	"__method_Reader_close":      "__fern_reader_close_fd",
	"__method_Writer_write":      "__fern_writer_write",
	"__method_Writer_close":      "__fern_writer_close",

	// String / bytes round-trip.
	"string_from_bytes_unchecked": "__fern_string_from_bytes",

	// File I/O. `read_file` / `write_file` read or truncate-write
	// in one shot; `open_reader` / `open_writer` / `open_appender`
	// return Reader / Writer values backed by a preview-1 fd.
	"read_file":     "__fern_read_file",
	"write_file":    "__fern_write_file",
	"open_reader":   "__fern_open_reader",
	"open_writer":   "__fern_open_writer",
	"open_appender": "__fern_open_appender",

	// stdio Writers, mirrors of stdin for consistency. fd=1 / 2.
	"stdout": "__fern_stdout",
	"stderr": "__fern_stderr",

	// TCP. Each builtin maps to a runtime helper in wasi_tcp.go;
	// the helpers wrap wasi:sockets + wasi:io directly. See
	// `scanRuntimeHelpers` / `scanImports` for the dep wiring.
	"tcp_listen":   "__fern_tcp_listen",
	"tcp_accept":   "__fern_tcp_accept",
	"tcp_connect":  "__fern_tcp_connect",
	"tcp_pollable": "__fern_tcp_pollable",
	"tcp_recv":     "__fern_tcp_recv",
	"tcp_send":     "__fern_tcp_send",
	"tcp_close":    "__fern_tcp_close",
	"udp_send":     "__fern_udp_send",

	// Map / MapIter generic-method dispatch — the lang doesn't yet
	// support generic methods on a generic struct, so the stdlib
	// declares concrete `_impl` counterparts and call sites route
	// through these aliases. Mirrors codegenAliasMap in the WAT
	// path verbatim.
	"map_new":             "map_new_impl",
	"__method_Map_len":    "__map_len_impl",
	"__method_Map_has":    "__map_has_impl",
	"__method_Map_get":    "__map_get_impl",
	"__method_Map_get_or": "__map_get_or_impl",
	"__method_Map_set":    "__map_set_impl",
	"__method_Map_delete": "__map_delete_impl",
	// Struct/enum (keyKind-3) keys: `_keyed` variants take the key
	// type's derived hash/eq as trailing fn-value args (#2671).
	"__method_Map_has_keyed":    "__map_has_keyed_impl",
	"__method_Map_get_keyed":    "__map_get_keyed_impl",
	"__method_Map_get_or_keyed": "__map_get_or_keyed_impl",
	"__method_Map_set_keyed":    "__map_set_keyed_impl",
	"__method_Map_delete_keyed": "__map_delete_keyed_impl",
	"__method_Map_clear":        "__map_clear_impl",
	"__method_Map_keys":         "__map_keys_impl",
	"__method_Map_values":       "__map_values_impl",
	"__method_Map_iter":         "__map_iter_impl",
	"__method_MapIter_has_next": "__mapiter_has_next_impl",
	"__method_MapIter_key":      "__mapiter_key_impl",
	"__method_MapIter_value":    "__mapiter_value_impl",
	"__method_MapIter_advance":  "__mapiter_advance_impl",
}

// callDirectAlias is the function-form lookup over CallDirectAliases.
// Inlined at every OpCallDirect emit site.
func callDirectAlias(name string) string {
	if dst, ok := CallDirectAliases[name]; ok {
		return dst
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
// hasEnvParam reports whether the IR params end with the
// closureconv-added `__env` slot. When true, the closure-target
// ABI's wasm-level env_ptr append is already covered by the IR
// param and the codegen seam mustn't double it.
func hasEnvParam(params []ast.Param) bool {
	if len(params) == 0 {
		return false
	}
	return params[len(params)-1].Name == "__env"
}

// calleeEnvAppended reports whether the wasm codegen appended an
// env_ptr param to callee `name`'s signature — i.e. it's a closure
// target whose IR params don't already carry the closureconv `__env`
// slot. This is exactly the condition under which emitBody bumps the
// scratch base for the extra env slot (see the closureTargets check
// there). A *direct* call to such a function must push a dummy
// env_ptr (0) after its natural args, because the direct-call IR site
// supplies only the natural args. Normally a function is either
// direct-called or a closure target (closureTargetSet's invariant),
// but a bare named comparator passed to a generic — `sort_by(arr,
// string_cmp)` where string_cmp is also called directly — is both,
// which is what surfaced this on the wasm backend (#4829).
func calleeEnvAppended(ctx *emitCtx, name string) bool {
	if !ctx.closureTargets[name] {
		return false
	}
	callee, ok := ctx.funcByName[name]
	if !ok {
		return false
	}
	return !hasEnvParam(callee.Params)
}

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
			case ir.OpConstVtable, ir.OpCallDyn:
				// `dyn Trait` dispatch loads function-table indices
				// from a vtable and call_indirect's through them, so
				// the table + element segment must be present (the
				// impl methods are already in prog.Funcs → the table).
				return true
			}
		}
	}
	return false
}

// captureSlotSize returns the env-block byte stride for a capture
// of type t. Matches what closureconv assumes when computing
// CaptureRef offsets in the body — keep the two in sync.
//
//   - i64 / u64 / f64 → 8 bytes
//   - string         → 8 bytes (data + len, two i32 slots)
//   - everything else → 4 bytes (i32 / f32 / bool / heap-pointer-
//     shaped types, plus sub-i32 widths that pad to 4)
func captureSlotSize(t ast.Type) int {
	switch v := t.(type) {
	case ast.NumberType:
		if v.NormalWidth() == 64 {
			return 8
		}
		return 4
	case ast.FloatType:
		if v.NormalWidth() == 64 {
			return 8
		}
		return 4
	case ast.StringType:
		return 8
	}
	// Pointer-shaped: struct / enum / array / slice / func value /
	// tuple — all i32 on wasm32.
	return 4
}

// captureScratchValtypes returns the wasm-level valtype sequence
// for one capture's tmp scratch slots. Each scalar capture takes
// one slot of the matching wasm type; string captures fan out to
// two i32 slots (data + len).
func captureScratchValtypes(t ast.Type) []byte {
	if _, isString := t.(ast.StringType); isString {
		return []byte{encode.ValtypeI32, encode.ValtypeI32}
	}
	switch v := t.(type) {
	case ast.NumberType:
		if v.NormalWidth() == 64 {
			return []byte{encode.ValtypeI64}
		}
		return []byte{encode.ValtypeI32}
	case ast.FloatType:
		if v.NormalWidth() == 64 {
			return []byte{encode.ValtypeF64}
		}
		return []byte{encode.ValtypeF32}
	}
	return []byte{encode.ValtypeI32}
}

// captureStoreOp emits the wasm instruction to store one capture
// value from the operand stack into memory[addr]. For string
// captures the caller must have arranged TWO stores (data + len)
// separately — this helper covers the scalar case.
func captureStoreOp(body []byte, t ast.Type, offset uint32) []byte {
	switch v := t.(type) {
	case ast.NumberType:
		if v.NormalWidth() == 64 {
			return memory.InstI64Store(body, 3, offset)
		}
		return memory.InstI32Store(body, 2, offset)
	case ast.FloatType:
		if v.NormalWidth() == 64 {
			return memory.InstF64Store(body, 3, offset)
		}
		return memory.InstF32Store(body, 2, offset)
	}
	return memory.InstI32Store(body, 2, offset)
}

// closureMakeSite collects the per-site scratch layout used by
// emitMakeEnv / emitMakeClosure. Built once at function-emit time
// (a pre-pass over fn.Ops) so each site knows its scratch base
// without re-scanning.
type closureMakeSite struct {
	// captures is the target function's Captures (the source of
	// per-capture types). May be nil for sites where no target
	// is registered — those error out at emit time.
	captures []ast.Param
	// scratchBase is the wasm-slot index of this site's first
	// scratch local. Slot count = sum(captureScratchValtypes)
	// over captures; +1 env_ptr; +1 pair_ptr for closure.
	scratchBase uint32
}

// closureMakeSites scans fn.Ops in order, looks up each
// OpMakeEnv / OpMakeClosure target in funcByName, and produces
// a slice of per-site layout descriptors plus the total scratch
// valtype list to append to fn's locals.
func closureMakeSites(fn *ir.Func, funcByName map[string]*ir.Func, base uint32) ([]closureMakeSite, []byte, error) {
	var sites []closureMakeSite
	var scratchValtypes []byte
	for _, op := range fn.Ops {
		if op.Kind != ir.OpMakeEnv && op.Kind != ir.OpMakeClosure {
			continue
		}
		// Resolve per-capture types from the target function's
		// Captures list. Two fallbacks for partially-typed IR
		// (synthetic tests, legacy IR that pre-dates the Captures
		// field): missing target or empty Captures list with a
		// positive op.I32 → synthesize all-i32 captures, matching
		// the legacy uniform-stride behavior.
		target := funcByName[op.Str]
		var caps []ast.Param
		if target != nil {
			caps = target.Captures
		}
		if len(caps) == 0 && op.I32 > 0 {
			caps = make([]ast.Param, op.I32)
			for i := range caps {
				caps[i] = ast.Param{Type: ast.NumberType{Width: 32, Signed: true}}
			}
		} else if int(op.I32) != len(caps) {
			return nil, nil, fmt.Errorf("%v %q: op.I32=%d but target has %d captures", op.Kind, op.Str, op.I32, len(caps))
		}
		site := closureMakeSite{
			captures:    caps,
			scratchBase: base + uint32(len(scratchValtypes)),
		}
		// One scratch slot tower per capture (variable size by
		// type) plus 1 env_ptr + 1 pair_ptr (i32 each).
		for _, c := range caps {
			scratchValtypes = append(scratchValtypes, captureScratchValtypes(c.Type)...)
		}
		scratchValtypes = append(scratchValtypes, encode.ValtypeI32, encode.ValtypeI32)
		sites = append(sites, site)
	}
	return sites, scratchValtypes, nil
}

// emitClosureMakeAlloc handles the common alloc-and-store body of
// OpMakeEnv / OpMakeClosure. Pops captures (in reverse), allocs
// env_size bytes, stores each capture at its type-aware offset,
// and returns the env_ptr slot index for the caller to use.
//
// site is this op's pre-computed scratch layout (see
// closureMakeSites). siteIdx is its sequential index in the
// function (0, 1, 2, …) — used to step through ctx.closureSites.
func emitClosureMakeAlloc(body []byte, site closureMakeSite, ctx *emitCtx) ([]byte, uint32, uint32, error) {
	allocIdx, ok := ctx.funcIdx["__fern_alloc_rc1"]
	if !ok {
		return nil, 0, 0, fmt.Errorf("__fern_alloc_rc1 helper not registered")
	}
	caps := site.captures
	n := len(caps)
	// Compute per-capture wasm-slot offsets and the total
	// env-block byte size.
	wasmSlotOffsets := make([]uint32, n)
	envOffsets := make([]uint32, n)
	envSize := uint32(0)
	scratchOff := uint32(0)
	for i, c := range caps {
		wasmSlotOffsets[i] = site.scratchBase + scratchOff
		// Apply the shared capture alignment (a no-op on wasm32,
		// where ptrW=4 keeps every capture packed) so the env layout
		// stays in lockstep with the canonical closureconv offsets.
		envSize = uint32(ast.CaptureAlign(int32(envSize), c.Type, 4))
		envOffsets[i] = envSize
		scratchOff += uint32(len(captureScratchValtypes(c.Type)))
		envSize += uint32(captureSlotSize(c.Type))
	}
	envSlot := site.scratchBase + scratchOff
	pairSlot := envSlot + 1
	// Pop captures into typed scratch slots, in reverse so the
	// stack drains top-down.
	for i := n - 1; i >= 0; i-- {
		c := caps[i]
		if _, isString := c.Type.(ast.StringType); isString {
			// Top of stack is len; below is data. Pop len then
			// data into the two scratch slots in that order:
			// scratch[off] = data, scratch[off+1] = len.
			body = inst.InstLocalSet(body, wasmSlotOffsets[i]+1) // len
			body = inst.InstLocalSet(body, wasmSlotOffsets[i])   // data
		} else {
			body = inst.InstLocalSet(body, wasmSlotOffsets[i])
		}
	}
	// Allocate env_size bytes (0 is fine — bump allocator just
	// returns the current cursor).
	body = inst.InstI32Const(body, int32(envSize))
	body = inst.InstCall(body, allocIdx)
	body = inst.InstLocalSet(body, envSlot)
	// Store each capture at env_ptr + envOffsets[i]. Address
	// goes BEFORE value on the stack (store consumes both),
	// hence the load-env_ptr then value pattern.
	for i, c := range caps {
		off := envOffsets[i]
		if _, isString := c.Type.(ast.StringType); isString {
			// Two i32.stores: data at off+0, len at off+4.
			body = inst.InstLocalGet(body, envSlot)
			body = inst.InstLocalGet(body, wasmSlotOffsets[i]) // data
			body = memory.InstI32Store(body, 2, off)
			body = inst.InstLocalGet(body, envSlot)
			body = inst.InstLocalGet(body, wasmSlotOffsets[i]+1) // len
			body = memory.InstI32Store(body, 2, off+4)
			continue
		}
		body = inst.InstLocalGet(body, envSlot)
		body = inst.InstLocalGet(body, wasmSlotOffsets[i])
		body = captureStoreOp(body, c.Type, off)
	}
	return body, envSlot, pairSlot, nil
}

// emitMakeEnv emits the wasm bytes for OpMakeEnv. Reads per-
// capture types from the target function (looked up via
// ctx.closureSites[ctx.closureSiteCursor]) so int / float / 64-
// bit / string captures all land at the correct env-block offset.
// Returns the env_ptr.
func emitMakeEnv(body []byte, op ir.Op, ctx *emitCtx) ([]byte, error) {
	if ctx.closureSiteCursor >= len(ctx.closureSites) {
		return nil, fmt.Errorf("OpMakeEnv: site cursor exhausted (pre-pass mismatch)")
	}
	site := ctx.closureSites[ctx.closureSiteCursor]
	ctx.closureSiteCursor++
	body, envSlot, _, err := emitClosureMakeAlloc(body, site, ctx)
	if err != nil {
		return nil, fmt.Errorf("OpMakeEnv: %w", err)
	}
	body = inst.InstLocalGet(body, envSlot)
	return body, nil
}

// emitMakeClosure is OpMakeEnv plus a 16-byte closure pair cell
// {fn_idx, env_ptr, drop_fn_idx, env_ptr}. Returns the pair pointer.
//
// The trailing two slots carry the per-closure drop-fn pointer (the
// table index of __closure_drop_<name>, or 0 for a zero-capture
// closure with no env to free) and a DUPLICATE of env_ptr. The
// duplication makes {drop_fn_idx@8, env_ptr@12} a self-contained
// callable sub-pair: a generic holder (e.g. __drop_arr_closure) can
// reclaim the env without static closure identity by dispatching
// call_indirect through (pair + 8) — reading fn at +0 and env at +4
// of the sub-pair, exactly as a normal closure call does.
func emitMakeClosure(body []byte, op ir.Op, ctx *emitCtx) ([]byte, error) {
	allocIdx, ok := ctx.funcIdx["__fern_alloc_rc1"]
	if !ok {
		return nil, fmt.Errorf("OpMakeClosure: __fern_alloc_rc1 helper not registered")
	}
	if op.Str == "" {
		return nil, fmt.Errorf("OpMakeClosure: missing target name")
	}
	// call_indirect dispatches by table-idx, not funcidx, so the
	// pair cell stores the function's slot in the element segment
	// (= its position in prog.Funcs) rather than the
	// post-import-shifted funcidx.
	fnTableIdx, ok := ctx.progFuncTableIdx[op.Str]
	if !ok {
		return nil, fmt.Errorf("OpMakeClosure: target %q not in prog.Funcs", op.Str)
	}
	if ctx.closureSiteCursor >= len(ctx.closureSites) {
		return nil, fmt.Errorf("OpMakeClosure: site cursor exhausted")
	}
	site := ctx.closureSites[ctx.closureSiteCursor]
	ctx.closureSiteCursor++
	// drop_fn table index: the per-closure env-drop thunk. Zero-capture
	// closures have env_ptr==0 and no thunk, so store 0 (the generic
	// drop guards drop_fn!=0 before dispatching).
	dropFnTableIdx := int32(0)
	if len(site.captures) > 0 {
		if idx, ok := ctx.progFuncTableIdx["__closure_drop_"+op.Str]; ok {
			dropFnTableIdx = int32(idx)
		}
	}
	body, envSlot, pairSlot, err := emitClosureMakeAlloc(body, site, ctx)
	if err != nil {
		return nil, fmt.Errorf("OpMakeClosure: %w", err)
	}
	// Pair cell: 16 bytes {fn_table_idx, env_ptr, drop_fn_idx, env_ptr}.
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, allocIdx)
	body = inst.InstLocalSet(body, pairSlot)
	body = inst.InstLocalGet(body, pairSlot)
	body = inst.InstI32Const(body, int32(fnTableIdx))
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, pairSlot)
	body = inst.InstLocalGet(body, envSlot)
	body = memory.InstI32Store(body, 2, 4)
	body = inst.InstLocalGet(body, pairSlot)
	body = inst.InstI32Const(body, dropFnTableIdx)
	body = memory.InstI32Store(body, 2, 8)
	body = inst.InstLocalGet(body, pairSlot)
	body = inst.InstLocalGet(body, envSlot)
	body = memory.InstI32Store(body, 2, 12)
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
				ir.OpLoadByte, ir.OpStoreI8:
				return true
			}
		}
	}
	return false
}

// blocktypeEnc maps an ir.BlockType* constant to the encoded
// bytes for the wasm 1.0 blocktype immediate on `block` /
// `loop` / `if`. Single-valtype blocks emit a 1-byte short
// form (0x40 for void, or the valtype byte). Multi-value
// blocks emit the SLEB128 of a typeidx — wasm 1.0 reuses the
// function-type space for block types and references it by
// signed leb (non-negative typeidxs are encoded the same as
// their unsigned value when ≤ 63, longer otherwise).
//
// Today the only multi-value blocktype is BlockTypeStringPair
// (two i32s: data + len). Adding more (struct unpacks, enum
// destructures) is just another case here registering the
// right (params, results) pair via ctx.addRawType.
func blocktypeEnc(bt int32, ctx *emitCtx) ([]byte, error) {
	switch bt {
	case ir.BlockTypeVoid:
		return []byte{inst.BlocktypeEmpty}, nil
	case ir.BlockTypeI32:
		return []byte{encode.ValtypeI32}, nil
	case ir.BlockTypeI64:
		return []byte{encode.ValtypeI64}, nil
	case ir.BlockTypeF32:
		return []byte{encode.ValtypeF32}, nil
	case ir.BlockTypeF64:
		return []byte{encode.ValtypeF64}, nil
	case ir.BlockTypeStringPair:
		tIdx := ctx.addRawType(nil, []byte{encode.ValtypeI32, encode.ValtypeI32})
		return leb128.SlebI32(nil, int32(tIdx)), nil
	}
	return nil, fmt.Errorf("unknown blocktype %d", bt)
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
