// Package wasm emits WebAssembly text format (WAT) for a checked
// Program. Shares the optimisation pipeline (Inline, FuseTee,
// FlattenBranches, plus the PropagateCopies / ConstPropagate /
// Fold / ReduceStrength fixed-point cleanup) with the arm64
// backend — new language features land once at the IR layer and
// both backends pick them up.
//
// Run the output with `wasmtime run --invoke main prog.wat` or
// convert to binary first with `wat2wasm prog.wat`.
package wasm

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/langstring"
	"github.com/jakechampion/lang/internal/treeshake"
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

// codegenAliasMap collects the IR-level call-name rewrites the
// wasm emitter applies at OpCallDirect emit time. Two flavours
// coexist:
//
//   - prelude shims sharing one body (`__array_append_jsonvalue`
//     and `__array_append_string` both 4-byte-pointer-stride);
//   - generic-method dispatch on Map[K, V] / MapIter[K, V] where
//     the lang doesn't yet have generic methods on a generic
//     struct, so the prelude declares concrete `_impl`
//     counterparts and the call name routes through the alias.
//
// Used by the call-emit switch AND by IR-level dead-function
// elimination, which would otherwise drop the impls (their AST
// callers only reference the source name).
var codegenAliasMap = map[string]string{
	"map_new":                   "map_new_impl",
	"__method_Map_len":          "__map_len_impl",
	"__method_Map_has":          "__map_has_impl",
	"__method_Map_get":          "__map_get_impl",
	"__method_Map_get_or":       "__map_get_or_impl",
	"__method_Map_set":          "__map_set_impl",
	"__method_Map_delete":       "__map_delete_impl",
	"__method_Map_clear":        "__map_clear_impl",
	"__method_Map_keys":         "__map_keys_impl",
	"__method_Map_values":       "__map_values_impl",
	"__method_Map_iter":         "__map_iter_impl",
	"__method_MapIter_has_next": "__mapiter_has_next_impl",
	"__method_MapIter_key":      "__mapiter_key_impl",
	"__method_MapIter_value":    "__mapiter_value_impl",
	"__method_MapIter_advance":  "__mapiter_advance_impl",
}

func Emit(prog *ast.Program, info *checker.Info) (string, error) {
	return EmitWithOptions(prog, info, EmitOptions{})
}

// EmitOptions tunes the WAT emission. Each field is conservative —
// the zero value matches what the original `Emit` produced before
// options were introduced.
type EmitOptions struct {
	// HttpHandler emits a `wasi:http/incoming-handler@0.2.0#handle`
	// export wrapping the user's `function handle(req: HttpRequest):
	// HttpResponse`. No `_start` is exported (proxy-world components
	// have no entry point — the host invokes `handle` per inbound
	// request). When this is set, `HttpRequest` and `HttpResponse`
	// struct decls are auto-injected into the program.
	HttpHandler bool

	// PrintMainResult makes the `_start` wrapper format `main()`'s
	// integer return value through `int_to_string` and write it to
	// stdout (followed by a newline) before returning, instead of
	// dropping the value. Used by the WASM e2e test harness so it
	// can observe `main`'s i32 result through the component's
	// stdout — preview-2 components don't preserve arbitrary
	// integers through `wasi:cli/exit` and there's no `--invoke
	// main` equivalent for components, so stdout is the only
	// observation channel left. Has no effect when `main` returns
	// void; if `main` returns float, the flag is ignored (only the
	// integer path is wired) and the test should use a PASS/FAIL
	// shape instead.
	PrintMainResult bool
}

// EmitWithOptions is the option-aware sibling of Emit. The two share
// the same lowering pipeline (closure conversion → IR → IR opts →
// EmitFromIR); the options only influence the WAT layer.
func EmitWithOptions(prog *ast.Program, info *checker.Info, opts EmitOptions) (string, error) {
	// Tree-shake unreferenced functions before lowering so the
	// auto-injected lang prelude (internal/prelude) only pays
	// for what user code actually calls. Idempotent on
	// already-pruned input.
	//
	// `_start` (printMainResult test mode) calls
	// `int_to_string` from a codegen-emitted wat snippet that
	// the AST walker can't see, so list it as an extra entry
	// point to keep the prelude version alive.
	var extras []string
	if opts.PrintMainResult {
		extras = append(extras, "int_to_string")
	}
	// Wasi-http target uses the user's `handle()` directly via
	// the `wasi:http/incoming-handler.handle` export wrapper —
	// the auto-synthesised `main()` (which calls tcp_serve and
	// pulls in wasi:sockets imports the wasi-http preview-1
	// adapter doesn't satisfy) is irrelevant on this target,
	// so drop it before tree-shake runs. arm64 / wasm-CLI keep
	// the synth main as their entry point.
	if opts.HttpHandler {
		out := prog.Funcs[:0]
		for _, fn := range prog.Funcs {
			if fn.IsSynthesisedHandlerMain {
				continue
			}
			out = append(out, fn)
		}
		prog.Funcs = out
	}
	treeshake.Run(prog, extras...)
	// ir.Lower runs closure conversion as a precondition (hoisting
	// nested functions, rewriting captures), then produces an
	// ir.Program. ir.Fold runs constant folding on the lowered ops
	// — picking up the post-lowering shapes the AST optimiser can't
	// see (collapsed ternaries / short-circuits, etc.). EmitFromIR
	// turns the folded program into WAT, reusing the module-level
	// scaffolding (runtime helpers, function table, closure cells,
	// data segments, exports) defined alongside it.
	ip, err := ir.LowerWith(prog, info, 4)
	if err != nil {
		return "", err
	}
	// Tail-call optimisation — same one-liner backport
	// from x86-64 PR 6 / arm64 (this PR). Self-tail calls
	// become a parameter rebind + backward branch, so
	// self-recursive functions stay O(1) in wasm-side
	// call-stack depth (helpful when wasmtime's default
	// stack-limit knob actually matters in a real edge-
	// handler workload).
	ir.TailCallOptimize(ip)
	ir.Inline(ip)
	// Defunctionalise rewrites monomorphic-flow closure calls
	// (`var f = MakeClosure(...); ... f(args)`) to direct calls
	// to the hoisted target, eliminating the `call_indirect`
	// dispatch + the function-table type check. Runs after
	// Inline so the direct calls it produces are themselves
	// candidates for further inlining if the hoisted body
	// fits — and before FuseTee / Cleanup so the load+const+add
	// scratch arithmetic the rewrite synthesises gets folded /
	// peep-holed alongside the rest.
	// Wasm closure pair: 8 bytes total, env_ptr at offset 4.
	ir.Defunctionalise(ip, 4)
	// Drop the 8-byte closure-pair allocation when every reader
	// of the slot has been rewritten to a defunctionalised
	// direct call. The pair's fn_idx field is dead in that
	// case; the slot can hold env_ptr directly. Saves one
	// __lang_alloc per closure that fully defunctionalises.
	ir.ElideClosurePair(ip, 4)
	// Zero-capture closures escaping past ElideClosurePair (e.g.
	// passed as a function-typed argument — `tryThing(my_lambda)`)
	// rewrite to OpConstFunc so the value materialises as a
	// static `(closuresBase + 8*tableIdx)` pointer instead of a
	// heap-allocated `{fn_idx, env_ptr=0}` pair. Same shape as
	// any top-level fn reference.
	ir.InlineZeroCaptureClosures(ip)
	// Re-run Inline after Defunctionalise so the just-emitted
	// `OpCallClosureDirect` calls to small hoisted closures
	// also get substituted (the inliner recognises both
	// OpCallDirect and OpCallClosureDirect as inlineable
	// targets). Without this second pass, the defunctionalised
	// closure body still pays a call per use.
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
	// IR-level dead-function elimination: drop top-level
	// functions whose body the inliner / defunctionaliser
	// left without any remaining callers. Treeshake at the
	// AST level couldn't see this (it ran before IR rewrites).
	// Trims ip.Funcs only — prog.Funcs stays intact so the
	// AST-walking scan-for-uses passes (needsStrConcat /
	// needsArrays / etc.) still see the full program.
	deadFuncExtras := []string(nil)
	if opts.PrintMainResult {
		deadFuncExtras = append(deadFuncExtras, "int_to_string")
	}
	// `__state_init` is wired into the wasm `(start ...)`
	// section by the codegen layer below — that wiring isn't
	// visible to the IR-level DCE walker, so pin the function
	// as a live root manually whenever state{} blocks declared
	// any non-literal init expressions.
	if len(prog.States) > 0 {
		deadFuncExtras = append(deadFuncExtras, "__state_init")
	}
	if live := ir.LiveFunctionsWithAliases(ip, codegenAliasMap, deadFuncExtras...); live != nil {
		out := ip.Funcs[:0]
		for _, irFn := range ip.Funcs {
			if live[irFn.Name] {
				out = append(out, irFn)
			}
		}
		ip.Funcs = out
	}
	return EmitFromIRWithOptions(prog, info, ip, opts)
}

type generator struct {
	out      strings.Builder
	info     *checker.Info
	indent   int
	current  *ast.FuncDecl
	httpHandler     bool // emit the wasi:http/incoming-handler.handle export wrapping user's handle(HttpRequest) -> HttpResponse.
	printMainResult bool // _start prints main()'s i32 return value via int_to_string before returning (test-only).
	// origTopLevelCount records how many functions the source had
	// before closure conversion appended hoisted ones. The first
	// origTopLevelCount entries get static closure cells (env=0)
	// because they're the only ones a `var f = name` reference can
	// reach by name.
	origTopLevelCount int

	// stateDecls preserves the state-block declarations from the
	// source program so the module-emit pipeline can render them
	// as wasm globals after the memory declaration. Empty for
	// programs without state blocks.
	stateDecls []*ast.StateDecl
	// needsPersistent is true when the program has any state{}
	// block. Triggers the two-cursor allocator wiring: a
	// reserved persistent heap region between the strings and
	// the arena, a separate persistent cursor at mem[44], and
	// an active-mode flag at mem[48] that __lang_alloc consults
	// to decide which cursor to bump. Programs without state
	// keep the single-cursor layout for minimum overhead.
	needsPersistent bool

	// Runtime / strings / arrays.
	needsRuntime  bool
	needsArrays   bool
	needsStrEq       bool // any String == String / != comparison
	needsStrConcat   bool // any String + String concatenation
	needsStrSlice    bool // any `s[a:b]` on a string — emits $__str_slice
	needsStrMethods  bool // any `s.starts_with` / `.ends_with` / `.contains` call
	needsStrFromBytes bool // any `string_from_bytes(bs)` call
	// `base64_encode` / `base64_decode` migrated to lang
	// prelude; no flag needed.
	// `hex_encode` / `hex_decode` migrated to lang prelude;
	// no flag needed.
	needsBulkMemory         bool // any `__memcpy(dst, src, n)` / `__memset(dst, b, n)` call — emits bulk-memory wat shims
	// `__array_append_string` migrated to lang prelude;
	// no flag needed.
	// `url_parse(s)` migrated to lang prelude; no flag.
	// `url_encode` / `url_decode` migrated to the lang prelude
	// (PR 176); no flag needed.
	// `query_parse(s)` migrated to lang prelude; no flag.
	// `json_encode` / `json_parse` migrated to lang prelude;
	// no flags needed.
	needsStructs     bool
	needsBoundsCheck bool // any array or string Index expression appears
	needsClosures    bool // any FuncDecl was hoisted by closure conversion
	needsArgs        bool // any `args()` call appears — pulls in WASI args_*
	needsReadLine    bool // any `read_line()` call appears — pulls in WASI fd_read + helper + scratch slots
	needsEnv         bool // any `env(name)` call appears — pulls in WASI environ_* + helper + cache slots
	needsExit        bool // any `exit(code)` call appears — pulls in WASI proc_exit
	needsArena       bool // any `arena_save` / `arena_restore` call — emits the two heap-cursor helpers
	// Map runtime migrated to lang prelude; no flag needed.
	needsRandomBytes bool // any `random_bytes(n)` call — pulls in WASI random_get
	// `int_to_string` + the .to_string() family migrated
	// to lang prelude; no flags needed.
	needsTcp         bool // any tcp_* call — pulls in WASI sock_accept + fd_read/fd_write/fd_close on socket fds
	needsFileIO      bool // any `read_file` / `write_file` call — pulls in WASI path_open / fd_read / fd_close
	needsStreamingIO bool // any open_reader / open_writer / Reader|Writer method call — extends needsFileIO with the streaming helpers
	needsStdStreams  bool // any stdin() / stdout() / stderr() call — emits trivial constructors that wrap fd 0 / 1 / 2 in Reader / Writer
	stringPool    map[string]int // value → pointer in linear memory
	stringEntries []stringEntry  // emission order (data segments)
	stringOffset  int            // next free byte for a string entry
	closuresBase  int            // start of the per-function closure-cell region
	// enumSentinelOffsets maps a tag value to its linear-memory
	// offset for the shared 4-byte `[tag=N]` sentinel. Lazily
	// populated on each OpEnumSentinel emit via
	// internEnumSentinel; one slot per unique tag value gets
	// reserved. Programs that never construct payloadless enum
	// variants skip the static-memory cost entirely.
	enumSentinelOffsets map[int]int

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

	// emittedFuncs tracks which functions actually had a body
	// emitted. After IR-level DCE, ip.Funcs may be a subset of
	// prog.Funcs — names absent from emittedFuncs must NOT appear
	// in `(export ... (func $name))` declarations or any other
	// place that would dangle without a definition.
	emittedFuncs map[string]bool
	// pairForm is the set of functions lowered with the
	// register-based (tag, payload) return ABI. Wasm signs
	// these as `(result i32 i32)` instead of `(result i32)`,
	// pushes (tag, payload) directly on OpMakeSome/None/Ok/Err
	// without a heap-box alloc, and consumers either consume
	// the two stack values inline (OpCallDirectPair) or rebox
	// into a heap pointer (OpCallDirect to a pair-form callee
	// from a generic-position consumer).
	pairForm map[string]bool
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

// emitStrLenFromLocal emits a sequence that loads the i32 length
// of the string whose pointer-shaped value lives in WebAssembly
// local `local` (the leading `$` is part of the name).
//
// Routes through `$__lang_str_len`, which branches on the top-bit
// inline flag: inline form returns the 3-bit length from bits
// 24..26 of the operand-stack word; heap form returns the i32 at
// `[local - 4]` (the legacy length-prefix layout). Centralising
// every string-length read here means runtime helpers
// (__str_eq, __str_concat, __str_slice, tcp_send, write_file, …)
// gain inline-awareness for free as the SSO migration extends
// the producer side.
func (g *generator) emitStrLenFromLocal(local string) {
	g.linef("local.get %s", local)
	g.line("call $__lang_str_len")
}

// emitStrLenStoreToLocal writes the i32 length stored in WebAssembly
// local `lenLocal` to the 4-byte little-endian length prefix at
// `[dstLocal - 4]`, where dstLocal holds the new string's *data
// pointer* (one past the prefix). Inverse of emitStrLenFromLocal:
// string-producing runtime helpers (__str_concat, __str_slice,
// string_from_bytes, random_bytes, env, tcp_recv, Reader.read_chunk)
// all flow through this one site so future SSO encoding changes
// that affect string construction have a single seam. Array-length
// stores (`__alloc_u8`, `$args` outer array) stay open-coded since
// arrays may diverge.
func (g *generator) emitStrLenStoreToLocal(lenLocal, dstLocal string) {
	g.linef("local.get %s", dstLocal)
	g.line("i32.const 4")
	g.line("i32.sub")
	g.linef("local.get %s", lenLocal)
	g.line("i32.store")
}

// emitPromoteStrParam emits a three-line preamble at the head of
// a runtime helper that uses a string-typed parameter as a
// linear-memory address (I/O writes, byte-by-byte compares,
// `memory.copy` reads, etc.). It loads `local`, calls
// `$__lang_str_to_heap` (passthrough for heap-form pointers;
// inline → fresh alloc+copy with length prefix), and stores the
// promoted pointer back over the param slot. After this preamble
// `local` always names a heap-form pointer the existing logic
// can address with the legacy `[ptr - 4]` / `[ptr + i]` shapes.
//
// SSO seam paired with `$__lang_str_to_heap`. Any new wasm
// helper that handles user-supplied strings as memory should
// call this at entry to stay correct under inline-form inputs.
func (g *generator) emitPromoteStrParam(local string) {
	g.linef("local.get %s", local)
	g.line("call $__lang_str_to_heap")
	g.linef("local.set %s", local)
}

// emitStrEmpty pushes the canonical empty-string value onto the
// operand stack. With single-i32 SSO live, the empty string is the
// inline-encoded value `0x80000000` (flag bit set, length=0, no
// data bytes) rather than a data-segment pointer — that drops the
// 4-byte heap sentinel from any module that never references `""`
// as a literal, AND lets short-string runtime helpers
// (`$__str_concat`, `$__str_slice`) return the empty result
// without touching the heap. Downstream readers route through
// `$__lang_str_len` (returns 0) and `$__lang_str_byte` (never
// called for length-0 strings); helpers that need a real linear-
// memory address (I/O, $__str_idx, $__str_slice as a base)
// call `$__lang_str_to_heap` at entry which materialises the
// empty sentinel on demand.
func (g *generator) emitStrEmpty() {
	g.line("i32.const 0x80000000")
}

// emitArrayLenFromLocal emits a sequence that loads the i32 length
// of the length-prefixed array whose data pointer lives in `local`.
// Today this is a 4-byte little-endian load from `[local - 4]`.
// Centralised seam for arrays, parallel to emitStrLenFromLocal.
// Stays distinct because arrays may diverge from strings under
// future layout changes.
func (g *generator) emitArrayLenFromLocal(local string) {
	g.linef("local.get %s", local)
	g.line("i32.const 4")
	g.line("i32.sub")
	g.line("i32.load")
}

// emitArrayLenStoreToLocal writes the i32 length in `lenLocal` to
// the 4-byte little-endian length prefix at `[dstLocal - 4]`,
// where dstLocal holds the new array's *data pointer*. Inverse
// of emitArrayLenFromLocal.
func (g *generator) emitArrayLenStoreToLocal(lenLocal, dstLocal string) {
	g.linef("local.get %s", dstLocal)
	g.line("i32.const 4")
	g.line("i32.sub")
	g.linef("local.get %s", lenLocal)
	g.line("i32.store")
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
		case *ast.Arena:
			walk(x.Body)
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
		case *ast.Destructure:
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
		case *ast.IfExpr:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *ast.TryOp:
			walk(x.Inner)
		case *ast.MatchExpr:
			walk(x.Tag)
			for _, arm := range x.Arms {
				if arm.Guard != nil {
					walk(arm.Guard)
				}
				walk(arm.Body)
			}
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
				case "int_to_string":
					// Now lives in the lang prelude
					// (internal/prelude/prelude.lang).
					g.needsRuntime = true
					g.needsArrays = true
					g.needsRuntime = true
				case "tcp_listen", "tcp_accept", "tcp_recv", "tcp_send", "tcp_close":
					g.needsTcp = true
					g.needsArrays = true
					g.needsRuntime = true
				case "map_new":
					// Now lives in the lang prelude
					// (internal/prelude/prelude.lang); the IR
					// alias map rewrites the call to
					// `map_new_impl`.
					g.needsRuntime = true
					// String-keyed Map[string, V] still needs
					// the byte-level string-equality helper for
					// the prelude version's `(s == s)` compare.
					g.needsStrEq = true
				case "string_from_bytes":
					g.needsStrFromBytes = true
					g.needsRuntime = true
				case "base64_encode", "base64_decode":
					// Now lives in the lang prelude
					// (internal/prelude/prelude.lang).
				case "hex_encode", "hex_decode":
					// Now lives in the lang prelude
					// (internal/prelude/prelude.lang).
				case "__memcpy", "__memset", "__alloc_u8",
					"__alloc", "__load_i32", "__store_i32",
					"__load_ptr", "__store_ptr", "__ptr_width":
					// Wat shims that wrap wasm's bulk-memory
					// `memory.copy` / `memory.fill` plus a tiny
					// allocator + raw 4-byte poke set. Exposed
					// to the lang prelude (map runtime
					// migration, json buffer family) so
					// high-level helpers can build growable
					// buffers without dropping out of the
					// language. The wide / sub-i32 store ops
					// live in the IR directly (emitArrayPush
					// uses `i64.store` etc. via the typed
					// store ops); no wat shims needed.
					g.needsBulkMemory = true
					g.needsRuntime = true
				case "__method_Array_push":
					// `arr.push(v)` lowers inline at the IR
					// layer (emitArrayPush). The call calls
					// `__memcpy` so we still need bulk-memory
					// + runtime.
					g.needsBulkMemory = true
					g.needsRuntime = true
				case "url_parse":
					// Now lives in the lang prelude
					// (internal/prelude/prelude.lang).
				case "url_encode", "url_decode", "query_parse",
					"json_encode", "json_parse":
					// All live in the lang prelude
					// (internal/prelude/prelude.lang). No
					// wat-side gating needed.
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
				if strings.HasPrefix(id.Name, "__method_Map_") ||
					strings.HasPrefix(id.Name, "__method_MapIter_") {
					// Map runtime migrated to the lang prelude;
					// the IR alias map rewrites these calls to
					// the corresponding `_impl` functions.
					g.needsRuntime = true
					g.needsStrEq = true
				}
				if strings.HasPrefix(id.Name, "__method_string_") {
					g.needsStrMethods = true
					g.needsRuntime = true
					// `trim` allocates a fresh substring via
					// `$__str_slice`; we emit all string-method
					// helpers as a single block, so always pull
					// the slice helper in when any string method
					// is in use.
					g.needsStrSlice = true
					g.needsBoundsCheck = true
				}
				// `int_to_string` and the (i32 / u32 / i64 / u64)
				// `.to_string()` family migrated to the lang
				// prelude; no flag needed. They share the
				// `__int_to_string_u64` core, also in the
				// prelude. `f32.to_string()` / `f64.to_string()`
				// have lived in the prelude since earlier.
			}
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.MapLit:
			// `Map { k: v, ... }` desugars to map_new + set
			// calls during IR lowering. Map runtime lives in
			// the lang prelude; the runtime preamble pull
			// covers __lang_alloc + the bulk-memory shims that
			// the prelude versions use.
			g.needsRuntime = true
			g.needsStrEq = true
			for _, e := range x.Entries {
				walk(e.Key)
				walk(e.Value)
			}
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.Arena:
			// `arena { … }` lowers to arena_save → body →
			// arena_restore at IR time, so the helpers need
			// to be in the binary even if user code never
			// names them directly.
			g.needsArena = true
			walk(x.Body)
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
		case *ast.Destructure:
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
		case *ast.IfExpr:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *ast.TryOp:
			walk(x.Inner)
		case *ast.MatchExpr:
			walk(x.Tag)
			for _, arm := range x.Arms {
				if arm.Guard != nil {
					walk(arm.Guard)
				}
				walk(arm.Body)
			}
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
		case *ast.Arena:
			walk(x.Body)
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
		case *ast.Destructure:
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
		case *ast.IfExpr:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *ast.TryOp:
			walk(x.Inner)
		case *ast.MatchExpr:
			walk(x.Tag)
			for _, arm := range x.Arms {
				if arm.Guard != nil {
					walk(arm.Guard)
				}
				walk(arm.Body)
			}
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
		case *ast.TupleLit:
			// Tuples share the heap-record codegen with structs;
			// the same `needsStructs` flag pulls in __lang_alloc.
			g.needsStructs = true
			g.needsRuntime = true
			for _, e := range x.Elems {
				walk(e)
			}
		case *ast.FieldAccess:
			walk(x.Target)
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.Arena:
			walk(x.Body)
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
		case *ast.Destructure:
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
	case *ast.Arena:
		g.scanIndirectStmt(x.Body)
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
	case *ast.Destructure:
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
	case *ast.IfExpr:
		g.scanIndirectExpr(x.Cond, false)
		g.scanIndirectExpr(x.Then, false)
		g.scanIndirectExpr(x.Else, false)
	case *ast.TryOp:
		g.scanIndirectExpr(x.Inner, false)
	case *ast.MatchExpr:
		g.scanIndirectExpr(x.Tag, false)
		for _, arm := range x.Arms {
			if arm.Guard != nil {
				g.scanIndirectExpr(arm.Guard, false)
			}
			g.scanIndirectExpr(arm.Body, false)
		}
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
		case *ast.Arena:
			walk(x.Body)
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
		case *ast.Destructure:
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
				if arm.Guard != nil {
					walk(arm.Guard)
				}
				walk(arm.Body)
			}
		case *ast.IfLet:
			walk(x.Source)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.LetElse:
			walk(x.Source)
			walk(x.Else)
		case *ast.Defer:
			walk(x.Expr)
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
}

// emitStateGlobals writes one `(global $state_<name> (mut T)
// (T.const N))` declaration per state-block var. Called from
// the module-emit pipeline after the memory declaration so
// the globals are visible to every function that follows.
//
// Two paths cover the var's init expression:
//   - Literal scalar init (NumberLit / FloatLit / BoolLit or
//     `-LIT`) → bake the literal into the wasm global's init
//     expression directly. Zero runtime cost.
//   - Anything else (string init, computed scalar init like
//     `1 + 2 * 3`) → emit the global with a zero placeholder,
//     and the synthesised `__state_init` start function (see
//     emitStateInit) computes the real value at module-
//     instantiation time and `global.set`s it.
//
// State init runs while the bump cursor is still at its
// initial position, so any allocations the init expression
// performs (e.g. string concat) end up below the per-request
// `arena_save` point and are preserved across `arena_restore`.
func (g *generator) emitStateGlobals() {
	for _, sd := range g.stateDecls {
		for _, v := range sd.Vars {
			wasmTy := stateGlobalWasmType(v.Type)
			if isStateInitLiteralExpr(v.Init) {
				g.linef(`(global $state_%s (mut %s) (%s.const %s))`,
					v.Name, wasmTy, wasmTy, formatStateInit(v.Init))
			} else {
				// Zero / null placeholder; __state_init writes
				// the real value before any user code runs.
				zero := "0"
				if wasmTy == "f32" || wasmTy == "f64" {
					zero = "0.0"
				}
				g.linef(`(global $state_%s (mut %s) (%s.const %s))`,
					v.Name, wasmTy, wasmTy, zero)
			}
		}
	}
}

// stateGlobalWasmType picks the wasm scalar type for a state
// var's declared lang-level type. Pointers (today: string) are
// i32-shaped at the wasm layer; numeric / float types pick
// i32 / i64 / f32 / f64 by width.
func stateGlobalWasmType(t ast.Type) string {
	switch typ := t.(type) {
	case ast.NumberType:
		if typ.Width == 64 {
			return "i64"
		}
		return "i32"
	case ast.FloatType:
		if typ.Width == 64 {
			return "f64"
		}
		return "f32"
	case ast.StringType:
		return "i32"
	}
	return "i32"
}

// isStateInitLiteralExpr matches the scalar literal shapes
// that compile to a wasm `<type>.const N` global init
// expression directly — no runtime computation needed. Pairs
// with the runtime-init path in emitStateInit for everything
// else.
func isStateInitLiteralExpr(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.NumberLit, *ast.FloatLit, *ast.BoolLit:
		return true
	case *ast.Unary:
		if v.Op == "-" {
			return isStateInitLiteralExpr(v.Operand)
		}
	}
	return false
}

// formatStateInit serialises a literal initialiser to the text
// the wasm `<type>.const` opcode wants. Negation is flattened
// (the parser produces `Unary{"-", lit}` for `-1`; wat accepts
// `-1` as the const operand directly).
func formatStateInit(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.NumberLit:
		return fmt.Sprintf("%d", v.Value)
	case *ast.FloatLit:
		return formatFloatBits(v.Value)
	case *ast.BoolLit:
		if v.Value {
			return "1"
		}
		return "0"
	case *ast.Unary:
		if v.Op == "-" {
			return "-" + formatStateInit(v.Operand)
		}
	}
	return "0"
}

// formatFloatBits formats a float literal so wasmtime accepts
// it. Fractional values use Go's default %g; integers get a
// trailing `.0` so they parse as floats not ints.
func formatFloatBits(v float64) string {
	s := fmt.Sprintf("%g", v)
	for _, c := range s {
		if c == '.' || c == 'e' || c == 'E' || c == 'n' || c == 'N' {
			return s
		}
	}
	return s + ".0"
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
		case *ast.FString:
			// f-strings lower to a `+`-chain via Desugared; the
			// scan walks the original AST so it has to descend
			// explicitly to see the IsStringConcat=true Binaries.
			walk(x.Desugared)
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
		case *ast.IfExpr:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *ast.TryOp:
			walk(x.Inner)
		case *ast.MatchExpr:
			walk(x.Tag)
			for _, arm := range x.Arms {
				if arm.Guard != nil {
					walk(arm.Guard)
				}
				walk(arm.Body)
			}
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
		case *ast.Arena:
			walk(x.Body)
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
		case *ast.Destructure:
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
		// Don't short-circuit on `g.needsBoundsCheck` —
		// SliceExpr sets BOTH `needsBoundsCheck` and
		// `needsStrSlice` (when on a string), so an Index
		// earlier in the walk shouldn't prevent a later
		// SliceExpr from setting `needsStrSlice`. Each
		// flag-set is idempotent.
		switch x := n.(type) {
		case *ast.Index:
			g.needsBoundsCheck = true
			g.needsRuntime = true
		case *ast.SliceExpr:
			// Slicing pulls in the same heap-record machinery
			// as bounds-checked indexing — the slice constructor
			// is alloc + 2× store, plus indexing on slice values
			// goes through `$__slice_idx`. String slicing has
			// its own dedicated copy-into-fresh-string helper.
			g.needsBoundsCheck = true
			g.needsRuntime = true
			g.needsStructs = true
			if x.IsString {
				g.needsStrSlice = true
			}
			walk(x.Source)
			if x.Low != nil {
				walk(x.Low)
			}
			if x.High != nil {
				walk(x.High)
			}
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
		case *ast.IfExpr:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *ast.TryOp:
			walk(x.Inner)
		case *ast.MatchExpr:
			walk(x.Tag)
			for _, arm := range x.Arms {
				if arm.Guard != nil {
					walk(arm.Guard)
				}
				walk(arm.Body)
			}
		case *ast.StructLit:
			for _, f := range x.Fields {
				walk(f.Value)
			}
		case *ast.FieldAccess:
			walk(x.Target)
		case *ast.CastExpr:
			walk(x.Inner)
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.Arena:
			walk(x.Body)
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
		case *ast.Destructure:
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
		case *ast.Arena:
			walk(x.Body)
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
		case *ast.Destructure:
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
		case *ast.IfExpr:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *ast.TryOp:
			walk(x.Inner)
		case *ast.MatchExpr:
			walk(x.Tag)
			for _, arm := range x.Arms {
				if arm.Guard != nil {
					walk(arm.Guard)
				}
				walk(arm.Body)
			}
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

// internOrPackMethod returns the operand-stack i32 value for an HTTP
// method name (or other short interned string), picking the same
// encoding `OpConstStr` would pick for the same source literal:
//
//   - len(s) ≤ 3: the single-i32 inline pack (`langstring.PackTinyWasm`).
//     User code's `"GET" == req.method` compares two inline-encoded
//     i32s; `$__str_eq`'s pointer-eq short-circuit fires before the
//     byte loop.
//   - len(s) >  3: the heap-form `internString` entry, deduped across
//     the module. User code's `"OPTIONS" == req.method` likewise gets
//     a heap pointer that matches the dedup-shared offset.
//
// Keeping the wrapper-side encoding in lockstep with the
// user-source-side encoding is what restores the pointer-eq fast
// path lost when `OpConstStr` started inline-packing short literals.
func (g *generator) internOrPackMethod(s string) int {
	if packed, ok := langstring.PackTinyWasm([]byte(s)); ok {
		return int(packed)
	}
	return g.internString(s)
}

// internEnumSentinel reserves and returns the linear-memory offset
// of a shared static 4-byte sentinel containing the i32 value
// `tag` (used as a payloadless enum variant's heap stand-in).
// Lazily allocated per unique tag value so programs that never
// construct payloadless variants pay no memory cost. Match / try
// sites' `i32.load` at offset 0 reads the tag from this address
// as if it were a heap-allocated `[tag=N]` box.
func (g *generator) internEnumSentinel(tag int) int {
	if off, ok := g.enumSentinelOffsets[tag]; ok {
		return off
	}
	g.needsRuntime = true
	if g.enumSentinelOffsets == nil {
		g.enumSentinelOffsets = map[int]int{}
	}
	off := g.stringOffset
	g.enumSentinelOffsets[tag] = off
	g.stringOffset += 4
	return off
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
//	          bit 2 = stdin cached, bit 3 = preopen cached,
//	          bit 4 = network cached)
//	116..119 preview-2 stdin input-stream resource handle (cached on
//	         first $read_line call)
//	120..123 preview-2 preopen descriptor handle (cached on first
//	         file open; resolves paths relative to it via open-at)
//	124..127 preview-2 network resource handle (cached on first
//	         tcp_listen / tcp_accept; from instance-network())
//	96+     string data, each entry: 4-byte length prefix then bytes
//	  (string data starts at 128 instead of 96 when preview-2 streams
//	  are in play; see EmitFromIRWithOptions.)
func (g *generator) emitRuntimePreamble() {
	if g.needsArgs {
		// args / env still come in via preview-1; the adapter
		// translates these to `wasi:cli/environment.get-arguments`
		// / `get-environment` at component-new time. A native
		// migration of these two pulls in canonical-ABI list<string>
		// machinery and is deferred to a follow-up.
		g.line(`(import "wasi_snapshot_preview1" "args_sizes_get" (func $__wasi_args_sizes_get (param i32 i32) (result i32)))`)
		g.line(`(import "wasi_snapshot_preview1" "args_get" (func $__wasi_args_get (param i32 i32) (result i32)))`)
	}
	if g.needsEnv {
		g.line(`(import "wasi_snapshot_preview1" "environ_sizes_get" (func $__wasi_environ_sizes_get (param i32 i32) (result i32)))`)
		g.line(`(import "wasi_snapshot_preview1" "environ_get" (func $__wasi_environ_get (param i32 i32) (result i32)))`)
	}
	if g.needsExit {
		// proc_exit is wrapped by the adapter into
		// `wasi:cli/exit.exit(result<_, _>)`; non-zero codes are
		// flattened to Err(()), so the host process sees exit 1
		// for any non-zero code. Documented limitation under
		// preview-2 0.2.0 — a `wasi:cli/exit.exit-with-code` wrapper
		// would land in 0.2.1+.
		g.line(`(import "wasi_snapshot_preview1" "proc_exit" (func $__wasi_proc_exit (param i32)))`)
	}
	// Preview-2 stdio: get-stdout / get-stderr / get-stdin return
	// resource handles; output-stream.blocking-write-and-flush
	// takes (handle, ptr, len, retptr) and flushes synchronously
	// — drop-in for $print / $write / $eprint / $putchar.
	// input-stream.blocking-read takes (handle, len_u64, retptr)
	// and writes `result<list<u8>, stream-error>` through the
	// retptr — see the read_line / read_chunk / tcp_recv call
	// sites.
	g.line(`(import "wasi:cli/stdout@0.2.0" "get-stdout" (func $__wasi_get_stdout (result i32)))`)
	g.line(`(import "wasi:cli/stderr@0.2.0" "get-stderr" (func $__wasi_get_stderr (result i32)))`)
	g.line(`(import "wasi:io/streams@0.2.0" "[method]output-stream.blocking-write-and-flush" (func $__wasi_blocking_write_and_flush (param i32 i32 i32 i32)))`)
	if g.needsReadLine || g.needsStreamingIO || g.needsFileIO || g.needsStdStreams || g.needsTcp || g.httpHandler {
		g.line(`(import "wasi:cli/stdin@0.2.0" "get-stdin" (func $__wasi_get_stdin (result i32)))`)
		g.line(`(import "wasi:io/streams@0.2.0" "[method]input-stream.blocking-read" (func $__wasi_blocking_read (param i32 i64 i32)))`)
	}
	if g.needsFileIO || g.needsStreamingIO {
		// File I/O imports. preopens.get-directories returns the
		// host's preopen list; we take the first entry as the
		// working directory descriptor and call descriptor.open-at
		// against it. read/write/append-via-stream return a stream
		// resource the rest of the runtime feeds through
		// wasi:io/streams.
		g.line(`(import "wasi:filesystem/preopens@0.2.0" "get-directories" (func $__wasi_get_directories (param i32)))`)
		g.line(`(import "wasi:filesystem/types@0.2.0" "[method]descriptor.open-at" (func $__wasi_descriptor_open_at (param i32 i32 i32 i32 i32 i32 i32)))`)
		g.line(`(import "wasi:filesystem/types@0.2.0" "[method]descriptor.read-via-stream" (func $__wasi_descriptor_read_via_stream (param i32 i64 i32)))`)
		g.line(`(import "wasi:filesystem/types@0.2.0" "[method]descriptor.write-via-stream" (func $__wasi_descriptor_write_via_stream (param i32 i64 i32)))`)
		g.line(`(import "wasi:filesystem/types@0.2.0" "[method]descriptor.append-via-stream" (func $__wasi_descriptor_append_via_stream (param i32 i32)))`)
	}
	if g.needsFileIO || g.needsStreamingIO || g.needsTcp || g.httpHandler {
		// Stream resource drops — closing a file Reader/Writer
		// (step 3c) and closing a TCP connection (step 4) both
		// drop input-stream / output-stream resources to keep
		// them from leaking on the host. The underlying
		// descriptor / tcp-socket handles still leak because the
		// bump allocator can't free the wrapper struct either;
		// bounded opens per program lifetime keeps that
		// acceptable.
		g.line(`(import "wasi:io/streams@0.2.0" "[resource-drop]input-stream" (func $__wasi_input_stream_drop (param i32)))`)
		g.line(`(import "wasi:io/streams@0.2.0" "[resource-drop]output-stream" (func $__wasi_output_stream_drop (param i32)))`)
	}
	if g.needsRandomBytes {
		// Component-model lowered signature for
		//   get-random-bytes: func(len: u64) -> list<u8>
		// is `(param i64 i32)` where i32 is the return-area
		// pointer. The host calls our exported `cabi_realloc` to
		// allocate a buffer in our linear memory, fills it with
		// the random bytes, then writes the (ptr, len) pair to
		// the return area.
		g.line(`(import "wasi:random/random@0.2.0" "get-random-bytes" (func $__wasi_random_get_p2 (param i64 i32)))`)
	}
	if g.needsTcp {
		// Native wasi:sockets. The user-facing API (tcp_listen /
		// tcp_accept / tcp_recv / tcp_send / tcp_close) returns a
		// heap pointer to a 12-byte struct `(tcp_socket,
		// input_stream, output_stream)`; for listening sockets
		// the stream slots stay 0. The host doesn't need
		// `wasmtime --tcp-listen=…` — the guest binds the port
		// itself.
		g.line(`(import "wasi:sockets/instance-network@0.2.0" "instance-network" (func $__wasi_instance_network (result i32)))`)
		g.line(`(import "wasi:sockets/network@0.2.0" "[resource-drop]network" (func $__wasi_network_drop (param i32)))`)
		g.line(`(import "wasi:sockets/tcp-create-socket@0.2.0" "create-tcp-socket" (func $__wasi_create_tcp_socket (param i32 i32)))`)
		// start-bind takes the canonical-ABI flattening of
		// `ip-socket-address` — a 1-i32 discriminant plus an
		// 11-i32 max payload (ipv4 uses 5 slots, ipv6 needs 11,
		// the variant joins them). Total params: self,
		// borrow<network>, disc, 11 flat slots, retptr = 15 i32.
		g.line(`(import "wasi:sockets/tcp@0.2.0" "[method]tcp-socket.start-bind" (func $__wasi_tcp_start_bind (param i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32)))`)
		g.line(`(import "wasi:sockets/tcp@0.2.0" "[method]tcp-socket.finish-bind" (func $__wasi_tcp_finish_bind (param i32 i32)))`)
		g.line(`(import "wasi:sockets/tcp@0.2.0" "[method]tcp-socket.start-listen" (func $__wasi_tcp_start_listen (param i32 i32)))`)
		g.line(`(import "wasi:sockets/tcp@0.2.0" "[method]tcp-socket.finish-listen" (func $__wasi_tcp_finish_listen (param i32 i32)))`)
		g.line(`(import "wasi:sockets/tcp@0.2.0" "[method]tcp-socket.accept" (func $__wasi_tcp_accept (param i32 i32)))`)
		g.line(`(import "wasi:sockets/tcp@0.2.0" "[method]tcp-socket.subscribe" (func $__wasi_tcp_subscribe (param i32) (result i32)))`)
		g.line(`(import "wasi:sockets/tcp@0.2.0" "[resource-drop]tcp-socket" (func $__wasi_tcp_socket_drop (param i32)))`)
		g.line(`(import "wasi:io/poll@0.2.0" "[method]pollable.block" (func $__wasi_pollable_block (param i32)))`)
		g.line(`(import "wasi:io/poll@0.2.0" "[resource-drop]pollable" (func $__wasi_pollable_drop (param i32)))`)
	}
	if g.httpHandler {
		// wasi:http imports for the `wasi:http/incoming-handler.handle`
		// export wrapper. The wrapper unpacks the incoming request
		// into a lang HttpRequest, calls the user's `handle`, and
		// streams the response back through outgoing-body. See
		// emitHttpHandlerWrapper for the call ordering.
		g.line(`(import "wasi:http/types@0.2.0" "[method]incoming-request.method" (func $__wasi_http_request_method (param i32 i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[method]incoming-request.path-with-query" (func $__wasi_http_request_path_with_query (param i32 i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[method]incoming-request.consume" (func $__wasi_http_request_consume (param i32 i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[resource-drop]incoming-request" (func $__wasi_http_request_drop (param i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[method]incoming-body.stream" (func $__wasi_http_incoming_body_stream (param i32 i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[static]incoming-body.finish" (func $__wasi_http_incoming_body_finish (param i32) (result i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[resource-drop]future-trailers" (func $__wasi_http_future_trailers_drop (param i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[constructor]fields" (func $__wasi_http_fields_new (result i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[constructor]outgoing-response" (func $__wasi_http_response_new (param i32) (result i32)))`)
		// set-status-code's `result<_, _>` returns the disc inline as
		// a single i32 (no retptr, since both Ok and Err carry no
		// payload — the canonical-ABI flat representation collapses
		// to a single discriminant slot).
		g.line(`(import "wasi:http/types@0.2.0" "[method]outgoing-response.set-status-code" (func $__wasi_http_response_set_status (param i32 i32) (result i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[method]outgoing-response.body" (func $__wasi_http_response_body (param i32 i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[method]outgoing-body.write" (func $__wasi_http_outgoing_body_write (param i32 i32)))`)
		// outgoing-body.finish: (param this option-disc option-trailers retptr).
		// Result <_, error-code> can be large; we ignore the result
		// payload but the host still needs ≥64 bytes of retptr to
		// scratch the error variant. We pass our shared 16-byte
		// retptr at memory[92] — the static area was sized for
		// smaller variants — so allocate a wider one inline.
		g.line(`(import "wasi:http/types@0.2.0" "[static]outgoing-body.finish" (func $__wasi_http_outgoing_body_finish (param i32 i32 i32 i32)))`)
		// response-outparam.set: param + flattened
		// `result<outgoing-response, error-code>`. Total 9 i32-or-i64
		// params: 1 outparam, 1 disc, 7 payload slots. Slot 2 of
		// the payload is i64 because an error-code case carries
		// `option<u64>` (HTTP-{request,response}-body-size) and
		// the canonical-ABI joins the variant width up to the
		// wider type.
		g.line(`(import "wasi:http/types@0.2.0" "[static]response-outparam.set" (func $__wasi_http_response_outparam_set (param i32 i32 i32 i32 i64 i32 i32 i32 i32)))`)
		// pollable.block + drop already imported when needsTcp; we
		// might need them for the future-trailers (no — future-trailers.drop
		// is what we use; we don't subscribe). Skip.
	}
	// Memory layout with state{}-block support:
	//   [0..63]                     reserved scratch
	//   [64..stringEnd]             string entries + closure cells
	//   [stringEnd..arenaStart]     persistent heap (state allocations)
	//   [arenaStart..]              arena heap (per-request)
	// where `arenaStart = stringEnd + persistentRegionBytes`.
	//
	// When the program has any state{} block we reserve
	// `persistentRegionBytes` (= 4 MiB) up front for state-init
	// allocations + future state-rooted mutations. Programs
	// without state keep the original single-cursor layout
	// (mem[40] starts at stringEnd, mem[44] / mem[48] / mem[52]
	// are unused). Memory pages are sized accordingly so the
	// data-segment writes for the cursor globals always land
	// in addressable memory.
	if g.needsPersistent {
		// 64 pages * 64 KiB = 4 MiB persistent + ~1 page for static
		// + arena startup. memory.grow extends arena as it fills.
		g.line(`(memory $mem 64)`)
	} else {
		g.line(`(memory $mem 1)`)
	}

	// state{}-block module-globals. Each `state { var NAME: T = LIT; }`
	// becomes a mutable wasm global named `$state_<NAME>` whose
	// init expression is the literal directly. The wasm runtime
	// initialises globals once at module instantiation, before
	// any user code runs — so `main()` / `handle()` see the
	// declared values on first read, and writes persist until
	// the module is unloaded. First-PR scalar-only scope means
	// every init expression is a single `<type>.const N`; richer
	// shapes would need a runtime `__state_init` start function.
	g.emitStateGlobals()

	{
		// $__lang_alloc bumps the allocator pointer at memory[40] and
		// returns the address that was there before the bump. There's
		// no free — arrays in lang are immutable but not GC'd.
		//
		// Always emit because `$cabi_realloc` (the canonical-ABI
		// allocator the host calls into) defers to it
		// unconditionally; an alloc-free program would otherwise
		// fail to compile at the wasm-tools step with "unknown func
		// $__lang_alloc".
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
		if g.needsPersistent {
			// Two-cursor allocator: mem[48] selects which cursor
			// to bump. 0 = arena (mem[40]), non-zero = persistent
			// (mem[44]). Persistent allocations stay below the
			// per-request `arena_save` boundary even when bumped
			// during handle() bodies, so state mutations survive
			// `arena_restore` cleanly. Persistent OOMs trap on
			// crossing mem[52] (the persistent floor); arena OOMs
			// extend memory via memory.grow as before.
			g.line(`i32.const 48`)
			g.line(`i32.load`)
			g.line(`if`)
			g.indent++
			// Persistent path: ptr = mem[44]; mem[44] = ptr + size;
			// trap if mem[44] > mem[52] (cap).
			g.line(`i32.const 44`)
			g.line(`i32.load`)
			g.line(`local.set $ptr`)
			g.line(`local.get $ptr`)
			g.line(`local.get $size`)
			g.line(`i32.add`)
			g.line(`local.set $end`)
			g.line(`local.get $end`)
			g.line(`i32.const 52`)
			g.line(`i32.load`)
			g.line(`i32.gt_u`)
			g.line(`if`)
			g.indent++
			// Persistent OOM: 4 MiB region exhausted. Trap.
			g.line(`unreachable`)
			g.indent--
			g.line(`end`)
			g.line(`i32.const 44`)
			g.line(`local.get $end`)
			g.line(`i32.store`)
			g.line(`local.get $ptr`)
			g.line(`return`)
			g.indent--
			g.line(`end`)
		}
		// Arena path: ptr = mem[40]; end = ptr + size; grow memory
		// if end > current pages * 64KB; mem[40] = end; return ptr.
		g.line(`i32.const 40`)
		g.line(`i32.load`)
		g.line(`local.set $ptr`)
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

		if g.needsPersistent {
			// $__lang_set_persistent_mode(flag) — save the previous
			// mode, set the new one, return the old. Codegen wraps
			// state-init bodies and state-rooted call sites as:
			//   prev = __lang_set_persistent_mode(1)
			//   <work>
			//   __lang_set_persistent_mode(prev)
			// The save/restore shape composes through nested calls
			// without the inner reset clobbering an outer "we're
			// already in persistent mode" frame.
			g.line(`(func $__lang_set_persistent_mode (param $flag i32) (result i32)`)
			g.indent++
			g.line(`(local $prev i32)`)
			g.line(`i32.const 48`)
			g.line(`i32.load`)
			g.line(`local.set $prev`)
			g.line(`i32.const 48`)
			g.line(`local.get $flag`)
			g.line(`i32.store`)
			g.line(`local.get $prev`)
			g.indent--
			g.line(`)`)
		}

		// SSO seams. These three helpers are the runtime side of the
		// small-string-optimisation migration: any string value on
		// the operand stack can now be either a heap-form pointer
		// (top bit clear; bytes live at `[p..p+len]` with the i32
		// length prefix at `[p-4]` — the historical layout) OR a
		// single-i32 inline form (top bit set; bytes packed into
		// bits 0..23, 3-bit length in bits 24..26; see
		// `langstring.PackTinyWasm` for the authoritative encoding).
		// All length and byte readers route through the helpers
		// below so callers don't need to branch on the flag bit
		// open-coded; helpers that need a real linear-memory
		// address (I/O, $__str_idx, $__str_slice) call
		// $__lang_str_to_heap at entry to normalise.
		//
		// Today only $__str_concat / $__str_slice / emitStrEmpty
		// produce inline values (via the empty-string sentinel
		// path); future PRs extend the producer side as the
		// helpers grow native inline output for ≤3-byte results.
		//
		// $__lang_str_len(p) → i32: branches on the top bit.
		// Inline form: bits 24..26 carry the length.
		// Heap form: i32.load at `[p - 4]`.
		g.line(`(func $__lang_str_len (param $p i32) (result i32)`)
		g.indent++
		g.line(`local.get $p`)
		g.line(`i32.const 0x80000000`)
		g.line(`i32.and`)
		g.line(`if (result i32)`)
		g.indent++
		g.line(`local.get $p`)
		g.line(`i32.const 24`)
		g.line(`i32.shr_u`)
		g.line(`i32.const 0x7`)
		g.line(`i32.and`)
		g.indent--
		g.line(`else`)
		g.indent++
		g.line(`local.get $p`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`)`)

		// $__lang_str_byte(p, i) → i32 byte. Inline: extract via
		// shift+mask. Heap: i32.load8_u at `[p + i]`. Caller is
		// expected to have already bounds-checked.
		g.line(`(func $__lang_str_byte (param $p i32) (param $i i32) (result i32)`)
		g.indent++
		g.line(`local.get $p`)
		g.line(`i32.const 0x80000000`)
		g.line(`i32.and`)
		g.line(`if (result i32)`)
		g.indent++
		g.line(`local.get $p`)
		g.line(`local.get $i`)
		g.line(`i32.const 8`)
		g.line(`i32.mul`)
		g.line(`i32.shr_u`)
		g.line(`i32.const 0xff`)
		g.line(`i32.and`)
		g.indent--
		g.line(`else`)
		g.indent++
		g.line(`local.get $p`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`i32.load8_u`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`)`)

		// $__lang_str_to_heap(p) → i32 heap-form pointer. Inline
		// inputs allocate a fresh heap entry (length prefix +
		// bytes), heap inputs pass through. Used at every site
		// that needs a real linear-memory pointer into the bytes
		// — I/O helpers, $__str_idx (which returns an address),
		// $__str_slice (which copies a substring from a known
		// base address).
		g.line(`(func $__lang_str_to_heap (param $p i32) (result i32)`)
		g.indent++
		g.line(`(local $len i32) (local $dst i32) (local $i i32)`)
		g.line(`local.get $p`)
		g.line(`i32.const 0x80000000`)
		g.line(`i32.and`)
		g.line(`if (result i32)`)
		g.indent++
		// $len = (p >> 24) & 7
		g.line(`local.get $p`)
		g.line(`i32.const 24`)
		g.line(`i32.shr_u`)
		g.line(`i32.const 0x7`)
		g.line(`i32.and`)
		g.line(`local.set $len`)
		// $dst = __lang_alloc($len + 4) + 4
		g.line(`local.get $len`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`call $__lang_alloc`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`local.set $dst`)
		// mem[$dst - 4] = $len  (length prefix)
		g.line(`local.get $dst`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`local.get $len`)
		g.line(`i32.store`)
		// for (i=0; i<len; i++) mem[$dst+i] = (p >> (i*8)) & 0xff
		g.line(`i32.const 0`)
		g.line(`local.set $i`)
		g.line(`block $end`)
		g.indent++
		g.line(`loop $loop`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`local.get $len`)
		g.line(`i32.eq`)
		g.line(`br_if $end`)
		g.line(`local.get $dst`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`local.get $p`)
		g.line(`local.get $i`)
		g.line(`i32.const 8`)
		g.line(`i32.mul`)
		g.line(`i32.shr_u`)
		g.line(`i32.const 0xff`)
		g.line(`i32.and`)
		g.line(`i32.store8`)
		g.line(`local.get $i`)
		g.line(`i32.const 1`)
		g.line(`i32.add`)
		g.line(`local.set $i`)
		g.line(`br $loop`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $dst`)
		g.indent--
		g.line(`else`)
		g.indent++
		// Heap form: pass through unchanged.
		g.line(`local.get $p`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`)`)

		// $__lang_str_data_ptr(p) → i32 linear-memory pointer to
		// the string's bytes. Heap-form values pass through; for
		// inline-form values, the helper spills the operand-word
		// bytes into a fixed scratch slot at mem[0..3] (shared
		// with the putchar buffer — safe because the slot is
		// overwritten on each use and wasm is single-threaded)
		// and returns mem[0]. Caller is responsible for reading
		// the bytes synchronously before any other code path
		// touches mem[0..3]; the helper is only used by stream-
		// write paths ($print / $write / $eprint / $tcp_send /
		// $__method_Writer_write / the HTTP body write) whose
		// $__streams_write call reads synchronously, so the
		// contract holds.
		//
		// Strictly an alloc-saving alternative to $__lang_str_to_heap
		// for the I/O case. The wasi:io/streams blocking-write call
		// only reads `len` bytes, so the high byte of the inline
		// word (length nibble + flag) written into mem[3] is never
		// observed; the address arithmetic users ($__str_idx,
		// $__str_slice, $env, etc.) still go through
		// $__lang_str_to_heap because they need durable storage.
		g.line(`(func $__lang_str_data_ptr (param $p i32) (result i32)`)
		g.indent++
		g.line(`local.get $p`)
		g.line(`i32.const 0x80000000`)
		g.line(`i32.and`)
		g.line(`if (result i32)`)
		g.indent++
		// Inline: spill 4 bytes (3 inline + 1 flag/length byte)
		// at mem[0]; return the scratch address.
		g.line(`i32.const 0`)
		g.line(`local.get $p`)
		g.line(`i32.store`)
		g.line(`i32.const 0`)
		g.indent--
		g.line(`else`)
		g.indent++
		// Heap: pass through.
		g.line(`local.get $p`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`)`)
	}

	if g.needsStrEq {
		// $__str_eq compares two strings byte-by-byte. Returns 1
		// if equal, 0 otherwise. Identical pointer-shaped values
		// short-circuit to true — works for both heap-form
		// pointers and inline-form values, since two inline
		// strings with identical bytes pack to the same i32. The
		// length seam ($__lang_str_len) and the byte seam
		// ($__lang_str_byte) handle the inline/heap branch
		// internally, so this helper stays agnostic about
		// either input's encoding.
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
		// la = len(a); lb = len(b) via the centralised helper.
		g.emitStrLenFromLocal("$a")
		g.line(`local.set $la`)
		g.emitStrLenFromLocal("$b")
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
		g.line(`call $__lang_str_byte`)
		g.line(`local.get $b`)
		g.line(`local.get $i`)
		g.line(`call $__lang_str_byte`)
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
		// $__str_concat returns the concatenation of `a` and `b`.
		//
		// Two output shapes, chosen by total length:
		//   - total == 0:  inline empty (0x80000000) via emitStrEmpty
		//   - total <= 3:  inline-packed value (top-bit flag + 3-bit
		//                  length + up-to-3 inline bytes), no alloc
		//   - total >  3:  heap-form (alloc length-prefix + copy)
		//
		// Inputs may be either inline or heap-form; all byte reads
		// route through `$__lang_str_byte`, which branches on the
		// flag bit. Length reads route through `$__lang_str_len`.
		// This is the first runtime helper that produces inline
		// outputs for non-empty strings — downstream consumers
		// (operand-stack values via OpStrLen, comparisons through
		// $__str_eq, more concatenation through this same helper)
		// already understand the inline form by the time they see
		// the value here.
		g.line(`(func $__str_concat (param $a i32) (param $b i32) (result i32)`)
		g.indent++
		g.line(`(local $la i32) (local $lb i32) (local $total i32) (local $dst i32) (local $i i32) (local $inline i32) (local $byte i32)`)
		g.emitStrLenFromLocal("$a")
		g.line(`local.set $la`)
		g.emitStrLenFromLocal("$b")
		g.line(`local.set $lb`)
		g.line(`local.get $la`)
		g.line(`local.get $lb`)
		g.line(`i32.add`)
		g.line(`local.set $total`)
		// total == 0: return the inline-encoded empty value. No
		// alloc, no data-segment touch.
		g.line(`local.get $total`)
		g.line(`i32.eqz`)
		g.line(`if`)
		g.indent++
		g.emitStrEmpty()
		g.line(`return`)
		g.indent--
		g.line(`end`)
		// total <= 3: build the inline encoding via shifts + ORs.
		// `$inline = InlineFlagWasm | (total << 24)` to start,
		// then OR in each byte at its `(i*8)`-bit slot, fetched
		// through `$__lang_str_byte` (which knows whether `$a` /
		// `$b` are inline or heap). Bytes from `$a` cover indices
		// `0 .. la-1`; bytes from `$b` cover `la .. total-1`.
		g.line(`local.get $total`)
		g.line(`i32.const 3`)
		g.line(`i32.le_u`)
		g.line(`if`)
		g.indent++
		g.line(`i32.const 0x80000000`)
		g.line(`local.get $total`)
		g.line(`i32.const 24`)
		g.line(`i32.shl`)
		g.line(`i32.or`)
		g.line(`local.set $inline`)
		g.line(`i32.const 0`)
		g.line(`local.set $i`)
		g.line(`block $iend`)
		g.indent++
		g.line(`loop $iloop`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`local.get $total`)
		g.line(`i32.eq`)
		g.line(`br_if $iend`)
		// byte = (i < la) ? $__lang_str_byte($a, i) : $__lang_str_byte($b, i - la)
		g.line(`local.get $i`)
		g.line(`local.get $la`)
		g.line(`i32.lt_u`)
		g.line(`if (result i32)`)
		g.indent++
		g.line(`local.get $a`)
		g.line(`local.get $i`)
		g.line(`call $__lang_str_byte`)
		g.indent--
		g.line(`else`)
		g.indent++
		g.line(`local.get $b`)
		g.line(`local.get $i`)
		g.line(`local.get $la`)
		g.line(`i32.sub`)
		g.line(`call $__lang_str_byte`)
		g.indent--
		g.line(`end`)
		g.line(`local.set $byte`)
		// $inline |= byte << (i * 8)
		g.line(`local.get $inline`)
		g.line(`local.get $byte`)
		g.line(`local.get $i`)
		g.line(`i32.const 8`)
		g.line(`i32.mul`)
		g.line(`i32.shl`)
		g.line(`i32.or`)
		g.line(`local.set $inline`)
		g.line(`local.get $i`)
		g.line(`i32.const 1`)
		g.line(`i32.add`)
		g.line(`local.set $i`)
		g.line(`br $iloop`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $inline`)
		g.line(`return`)
		g.indent--
		g.line(`end`)
		// Heap-form output path: total > 3.
		// dst = __lang_alloc(total + 4) + 4
		g.line(`local.get $total`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`call $__lang_alloc`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`local.set $dst`)
		g.emitStrLenStoreToLocal("$total", "$dst")
		// Copy a's bytes: for (i=0; i<la; i++) dst[i] = a[i]
		// Reads route through $__lang_str_byte so an inline `a`
		// (e.g. a previous $__str_concat output of length 1..3
		// concatenated with a longer string) still works.
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
		g.line(`call $__lang_str_byte`)
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
		g.line(`call $__lang_str_byte`)
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
		// Array bounds-check length reads go through
		// emitArrayLenFromLocal — the array seam parallels the
		// string seam (emitStrLenFromLocal) but stays distinct
		// so arrays can diverge from strings under future layout
		// changes (inline u8[] / typed-element headers / etc).
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
		g.emitArrayLenFromLocal("$base")
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
		// Inline-form bases don't have a linear-memory address;
		// promote-to-heap at entry so the byte-address arithmetic
		// below is always against a heap-form pointer.
		g.emitPromoteStrParam("$base")
		g.line(`local.get $i`)
		g.line(`i32.const 0`)
		g.line(`i32.lt_s`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $i`)
		g.emitStrLenFromLocal("$base")
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

		// Stride-aware variants for sub-i32 (`__str_idx` already
		// covers stride=1) and i64/f64 arrays. Same bounds-check
		// shape as `__arr_idx`; the only delta is the multiplier
		// applied to the index when computing the byte address.
		g.line(`(func $__arr_idx_2 (param $base i32) (param $i i32) (result i32)`)
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
		g.emitArrayLenFromLocal("$base")
		g.line(`i32.ge_u`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $base`)
		g.line(`local.get $i`)
		g.line(`i32.const 2`)
		g.line(`i32.mul`)
		g.line(`i32.add`)
		g.indent--
		g.line(`)`)

		g.line(`(func $__arr_idx_8 (param $base i32) (param $i i32) (result i32)`)
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
		g.emitArrayLenFromLocal("$base")
		g.line(`i32.ge_u`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $base`)
		g.line(`local.get $i`)
		g.line(`i32.const 8`)
		g.line(`i32.mul`)
		g.line(`i32.add`)
		g.indent--
		g.line(`)`)

		// Slice helpers — gated on the same flag as bounds-check
		// since slice creation, indexing, and len all need to
		// access the (data_ptr, len) heap pair.
		//
		// Slice memory layout: 8 bytes at slice_ptr —
		//   [+0..+3] data_ptr (i32, aliases the parent array's
		//            element-0 address shifted by `low * 4`)
		//   [+4..+7] len (i32)
		g.line(`(func $__slice_make (param $data i32) (param $len i32) (result i32)`)
		g.indent++
		g.line(`(local $s i32)`)
		g.line(`i32.const 8`)
		g.line(`call $__lang_alloc`)
		g.line(`local.tee $s`)
		g.line(`local.get $data`)
		g.line(`i32.store`)
		g.line(`local.get $s`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`local.get $len`)
		g.line(`i32.store`)
		g.line(`local.get $s`)
		g.indent--
		g.line(`)`)

		// $__slice_idx_N: same shape, scaled stride. The IR
		// picks the helper to call from the slice's element
		// type (1 for `[u8]` / `[i8]`, 2 for `[u16]` /
		// `[i16]`, 4 for the historical i32-stride layout, 8
		// for `[i64]` / `[u64]` / `[f64]`).
		emitSliceIdx := func(name string, stride int) {
			g.linef(`(func %s (param $slice i32) (param $i i32) (result i32)`, name)
			g.indent++
			g.line(`(local $data i32) (local $len i32)`)
			g.line(`local.get $slice`)
			g.line(`i32.load`)
			g.line(`local.set $data`)
			g.line(`local.get $slice`)
			g.line(`i32.const 4`)
			g.line(`i32.add`)
			g.line(`i32.load`)
			g.line(`local.set $len`)
			g.line(`local.get $i`)
			g.line(`i32.const 0`)
			g.line(`i32.lt_s`)
			g.line(`if`)
			g.indent++
			g.line(`unreachable`)
			g.indent--
			g.line(`end`)
			g.line(`local.get $i`)
			g.line(`local.get $len`)
			g.line(`i32.ge_u`)
			g.line(`if`)
			g.indent++
			g.line(`unreachable`)
			g.indent--
			g.line(`end`)
			g.line(`local.get $data`)
			if stride == 1 {
				g.line(`local.get $i`)
				g.line(`i32.add`)
			} else {
				g.line(`local.get $i`)
				g.linef(`i32.const %d`, stride)
				g.line(`i32.mul`)
				g.line(`i32.add`)
			}
			g.indent--
			g.line(`)`)
		}
		emitSliceIdx("$__slice_idx", 4)
		emitSliceIdx("$__slice_idx_1", 1)
		emitSliceIdx("$__slice_idx_2", 2)
		emitSliceIdx("$__slice_idx_8", 8)
	}

	if g.needsStrSlice {
		// $__str_slice(base, low, high) — copy bytes
		// `base[low..high]` into a fresh length-prefixed string
		// and return the new string's content pointer. Bounds
		// check matches the rest of the family: low >= 0, high
		// <= source length, low <= high.
		g.line(`(func $__str_slice (param $base i32) (param $low i32) (param $high i32) (result i32)`)
		g.indent++
		g.line(`(local $src_len i32) (local $new_len i32) (local $out i32) (local $i i32) (local $inline i32) (local $byte i32)`)
		// Inline-form bases don't have a linear-memory address;
		// promote-to-heap at entry so the `memory.copy(base+low,
		// new_len)` below reads from a real source pointer.
		g.emitPromoteStrParam("$base")
		g.emitStrLenFromLocal("$base")
		g.line(`local.set $src_len`)
		// low < 0 → trap
		g.line(`local.get $low`)
		g.line(`i32.const 0`)
		g.line(`i32.lt_s`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		// high > src_len → trap
		g.line(`local.get $high`)
		g.line(`local.get $src_len`)
		g.line(`i32.gt_u`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		// low > high → trap
		g.line(`local.get $low`)
		g.line(`local.get $high`)
		g.line(`i32.gt_s`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		// new_len = high - low
		g.line(`local.get $high`)
		g.line(`local.get $low`)
		g.line(`i32.sub`)
		g.line(`local.set $new_len`)
		// Short-circuit on new_len == 0: return the shared empty-
		// string sentinel without allocating.
		g.line(`local.get $new_len`)
		g.line(`i32.eqz`)
		g.line(`if`)
		g.indent++
		g.emitStrEmpty()
		g.line(`return`)
		g.indent--
		g.line(`end`)
		// new_len ≤ 3 → build the single-i32 inline output via
		// `$base[low..low+new_len]` byte reads (heap-form by now;
		// the promote-at-entry above guarantees it). Mirrors the
		// $__str_concat inline-output fast path so any slice
		// result that fits the SSO tiny cap skips the alloc +
		// `memory.copy` shape entirely.
		g.line(`local.get $new_len`)
		g.line(`i32.const 3`)
		g.line(`i32.le_u`)
		g.line(`if`)
		g.indent++
		g.line(`i32.const 0x80000000`)
		g.line(`local.get $new_len`)
		g.line(`i32.const 24`)
		g.line(`i32.shl`)
		g.line(`i32.or`)
		g.line(`local.set $inline`)
		g.line(`i32.const 0`)
		g.line(`local.set $i`)
		g.line(`block $iend`)
		g.indent++
		g.line(`loop $iloop`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`local.get $new_len`)
		g.line(`i32.eq`)
		g.line(`br_if $iend`)
		// byte = mem[base + low + i]  (base is heap-form here)
		g.line(`local.get $base`)
		g.line(`local.get $low`)
		g.line(`i32.add`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`i32.load8_u`)
		g.line(`local.set $byte`)
		// inline |= byte << (i * 8)
		g.line(`local.get $inline`)
		g.line(`local.get $byte`)
		g.line(`local.get $i`)
		g.line(`i32.const 8`)
		g.line(`i32.mul`)
		g.line(`i32.shl`)
		g.line(`i32.or`)
		g.line(`local.set $inline`)
		g.line(`local.get $i`)
		g.line(`i32.const 1`)
		g.line(`i32.add`)
		g.line(`local.set $i`)
		g.line(`br $iloop`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $inline`)
		g.line(`return`)
		g.indent--
		g.line(`end`)
		// Heap-form output path: new_len > 3.
		// out = alloc(4 + new_len) + 4   (out is the data ptr)
		g.line(`local.get $new_len`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`call $__lang_alloc`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`local.set $out`)
		// Length prefix via the centralised store helper.
		g.emitStrLenStoreToLocal("$new_len", "$out")
		// memory.copy(out, base + low, new_len)
		g.line(`local.get $out`)
		g.line(`local.get $base`)
		g.line(`local.get $low`)
		g.line(`i32.add`)
		g.line(`local.get $new_len`)
		g.line(`memory.copy`)
		// Return content pointer.
		g.line(`local.get $out`)
		g.indent--
		g.line(`)`)
	}

	// `base64_*` migrated to the lang prelude; no wat helper.

	if g.needsStrMethods {
		g.emitStringMethodHelpers()
	}
	if g.needsStrFromBytes {
		// $string_from_bytes(bs: u8[]): string — copies the
		// byte-array's payload into a fresh string. Round-trip
		// companion to s.bytes().
		//
		// Two output shapes, chosen by bLen:
		//   - bLen == 0:  inline empty (0x80000000) via emitStrEmpty
		//   - bLen <= 3:  inline-packed i32 (top-bit flag + 3-bit
		//                 length + up-to-3 inline bytes), no alloc
		//   - bLen >  3:  heap-form (alloc length-prefix + copy)
		g.line(`(func $string_from_bytes (param $bs i32) (result i32)`)
		g.indent++
		g.line(`(local $bLen i32) (local $out i32) (local $i i32) (local $inline i32) (local $byte i32)`)
		// Input u8[] length via the array seam — distinct from
		// the string-side emitStrLenFromLocal so the two layouts
		// can diverge under future array-only changes.
		g.emitArrayLenFromLocal("$bs")
		g.line(`local.set $bLen`)
		// Short-circuit on bLen == 0: return the inline-encoded
		// empty value rather than allocating a 0-byte buffer.
		g.line(`local.get $bLen`)
		g.line(`i32.eqz`)
		g.line(`if`)
		g.indent++
		g.emitStrEmpty()
		g.line(`return`)
		g.indent--
		g.line(`end`)
		// bLen ≤ 3 → build the single-i32 inline output by
		// reading bytes straight out of the u8[] payload. The
		// array's bytes live at `$bs .. $bs + bLen`; pack them
		// into bits 0..23, with bits 24..26 carrying the length
		// and bit 31 the inline flag. Mirrors the
		// $__str_concat / $__str_slice fast-path shape.
		g.line(`local.get $bLen`)
		g.line(`i32.const 3`)
		g.line(`i32.le_u`)
		g.line(`if`)
		g.indent++
		g.line(`i32.const 0x80000000`)
		g.line(`local.get $bLen`)
		g.line(`i32.const 24`)
		g.line(`i32.shl`)
		g.line(`i32.or`)
		g.line(`local.set $inline`)
		g.line(`i32.const 0`)
		g.line(`local.set $i`)
		g.line(`block $iend`)
		g.indent++
		g.line(`loop $iloop`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`local.get $bLen`)
		g.line(`i32.eq`)
		g.line(`br_if $iend`)
		// byte = mem[bs + i]
		g.line(`local.get $bs`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`i32.load8_u`)
		g.line(`local.set $byte`)
		// inline |= byte << (i * 8)
		g.line(`local.get $inline`)
		g.line(`local.get $byte`)
		g.line(`local.get $i`)
		g.line(`i32.const 8`)
		g.line(`i32.mul`)
		g.line(`i32.shl`)
		g.line(`i32.or`)
		g.line(`local.set $inline`)
		g.line(`local.get $i`)
		g.line(`i32.const 1`)
		g.line(`i32.add`)
		g.line(`local.set $i`)
		g.line(`br $iloop`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $inline`)
		g.line(`return`)
		g.indent--
		g.line(`end`)
		// Heap-form output: bLen > 3.
		// out = __lang_alloc(bLen + 4) + 4   (data ptr)
		g.line(`local.get $bLen`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`call $__lang_alloc`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`local.set $out`)
		// Length prefix via the centralised store helper.
		g.emitStrLenStoreToLocal("$bLen", "$out")
		// memory.copy(out, bs, bLen)
		g.line(`local.get $out`)
		g.line(`local.get $bs`)
		g.line(`local.get $bLen`)
		g.line(`memory.copy`)
		g.line(`local.get $out`)
		g.indent--
		g.line(`)`)
	}

	// `hex_*` migrated to lang prelude; no wat helper.
	if g.needsBulkMemory {
		g.emitBulkMemoryHelpers()
	}
	// `__array_append_string` migrated to lang prelude;
	// no wat helper.
	// `url_parse`, `url_encode`, `url_decode`, `query_parse`,
	// `json_encode`, and `json_parse` all migrated to the lang
	// prelude (internal/prelude/prelude.lang); no wat-side
	// emission for any of them.

	g.emitStreamsStdioHelpers()

	// putchar(n) — blocking-write-and-flush(stdout, ptr=0, len=1).
	// memory[0] holds the byte we just stored.
	g.line(`(func $putchar (param $n i32)`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`local.get $n`)
	g.line(`i32.store8`)
	g.line(`call $__stdout_handle`)
	g.line(`i32.const 0`)
	g.line(`i32.const 1`)
	g.line(`call $__streams_write`)
	g.indent--
	g.line(`)`)

	// print(s) — writes the string and a newline. Two single-iovec
	// blocking-write-and-flush calls (rather than one with two
	// iovecs) because some wasmtime versions silently drop all but
	// the first iovec when iovs_len > 1.
	g.line(`(func $print (param $s i32)`)
	g.indent++
	g.emitStreamsWriteString("$__stdout_handle", "$s")
	g.emitStreamsWriteNewline("$__stdout_handle")
	g.indent--
	g.line(`)`)

	// write(s) — stdout without a trailing newline. Same shape as
	// $print's first half; users compose their own newlines / field
	// separators when they want.
	g.line(`(func $write (param $s i32)`)
	g.indent++
	g.emitStreamsWriteString("$__stdout_handle", "$s")
	g.indent--
	g.line(`)`)

	// eprint(s) — `print` shape but routed to stderr.
	g.line(`(func $eprint (param $s i32)`)
	g.indent++
	g.emitStreamsWriteString("$__stderr_handle", "$s")
	g.emitStreamsWriteNewline("$__stderr_handle")
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
	// Map runtime migrated to lang prelude; no wat helper.
	if g.needsRandomBytes {
		g.emitRandomBytesHelper()
	}
	// `int_to_string` and the `.to_string()` family migrated
	// to lang prelude; no wat helpers.
	// `f32.to_string()` / `f64.to_string()` are in the lang
	// prelude; no wat-side helper.
	// `cabi_realloc` is the canonical-ABI allocator the host
	// invokes to materialise dynamically-sized return values
	// (e.g. `list<u8>` from `get-random-bytes` or
	// `input-stream.blocking-read`) in our linear memory.
	// Always emit it — the cost is one trivially-tiny function;
	// tracking individual import dependencies isn't worth the
	// gating complexity.
	g.emitCabiRealloc()
	if g.needsTcp {
		g.emitTcpHelpers()
	}
	if g.httpHandler {
		g.emitHttpHandlerWrapper()
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
// struct holding the cached stdin / stdout / stderr stream
// resource handle. Called repeatedly each yields a fresh struct;
// that's a small allocation cost for a usually-once-per-program
// lookup, in exchange for not needing static memory slots or a
// cached-once flag for the *struct* itself — the underlying
// stream handle is cached elsewhere (see
// emitStreamHandleAccessor).
func (g *generator) emitStdStreamHelpers() {
	g.emitStdStream("$stdin", "$__stdin_handle")
	g.emitStdStream("$stdout", "$__stdout_handle")
	g.emitStdStream("$stderr", "$__stderr_handle")
}

func (g *generator) emitStdStream(name, handleAccessor string) {
	g.linef(`(func %s (result i32)`, name)
	g.indent++
	g.line(`(local $r i32)`)
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $r`)
	g.line(`local.get $r`)
	g.linef(`call %s`, handleAccessor)
	g.line(`i32.store`)
	g.line(`local.get $r`)
	g.indent--
	g.line(`)`)
}

// emitFdWriteString emits one fd_write call that writes a single
// length-prefixed string to `fd`. local is the wasm local holding
// the string's data pointer (e.g. "$s"); the string's length is
// fetched through the central emitStrLenFromLocal helper. Reuses
// iovec[0] at offset 16.
//
// Stream-write semantics: routes through $__lang_str_data_ptr to
// stage inline strings into a fixed scratch slot without alloc;
// length comes from $__lang_str_len.
func (g *generator) emitFdWriteString(fd int, local string) {
	g.line(`i32.const 16`)
	g.linef(`local.get %s`, local)
	g.line(`call $__lang_str_data_ptr`)
	g.line(`i32.store`)
	g.line(`i32.const 20`)
	g.emitStrLenFromLocal(local)
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
	if g.needsReadLine || g.needsStreamingIO || g.needsStdStreams || g.needsFileIO {
		g.emitStreamHandleAccessor("$__stdin_handle", "$__wasi_get_stdin", 116, 4)
	}
	if g.needsReadLine || g.needsStreamingIO || g.needsFileIO {
		g.emitStdinStreamsReadLine()
	}
	if g.needsFileIO || g.needsStreamingIO {
		g.emitPreopenDirHelper()
	}

	// $__streams_write(handle, ptr, len) — wrap blocking-write-and-flush.
	// `blocking-write-and-flush` has a per-call 4 KiB buffer cap
	// in canonical implementations (wasmtime enforces it
	// strictly), so chunk the write. We don't surface
	// stream-error here — print / write failures aren't recoverable
	// for a CLI program and the helpers' callers don't propagate
	// errors anyway.
	g.line(`(func $__streams_write (param $handle i32) (param $ptr i32) (param $len i32)`)
	g.indent++
	g.line(`(local $off i32) (local $chunk i32)`)
	g.line(`block $end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $off`)
	g.line(`local.get $len`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end`)
	// chunk = min(len - off, 4096)
	g.line(`local.get $len`)
	g.line(`local.get $off`)
	g.line(`i32.sub`)
	g.line(`local.tee $chunk`)
	g.line(`i32.const 4096`)
	g.line(`i32.gt_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4096`)
	g.line(`local.set $chunk`)
	g.indent--
	g.line(`end`)
	// blocking-write-and-flush(handle, ptr+off, chunk, retptr=92)
	g.line(`local.get $handle`)
	g.line(`local.get $ptr`)
	g.line(`local.get $off`)
	g.line(`i32.add`)
	g.line(`local.get $chunk`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_blocking_write_and_flush`)
	// On Err (disc != 0), bail — we can't make progress.
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`br_if $end`)
	g.line(`local.get $off`)
	g.line(`local.get $chunk`)
	g.line(`i32.add`)
	g.line(`local.set $off`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
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
// string's bytes through the preview-2 streams API: load the
// cached handle, push (data_ptr, len), call $__streams_write.
//
// `local`'s value may be either a heap-form pointer (top bit
// clear, bytes at `local..local+len`) or a single-i32 inline
// value (top bit set, bytes packed into the operand word — see
// `langstring.PackTinyWasm`). Both forms route through
// `$__lang_str_data_ptr`, which passes heap pointers through
// and spills inline bytes into a shared scratch slot — no alloc
// in either case. The synchronous `blocking-write-and-flush`
// call reads `len` bytes from the returned pointer before
// returning, so the scratch slot is safe to clobber on the
// next use. Pair with `$__lang_str_len` for the length seam.
func (g *generator) emitStreamsWriteString(handleAccessor, local string) {
	g.linef(`call %s`, handleAccessor)
	g.linef(`local.get %s`, local)
	g.line(`call $__lang_str_data_ptr`)
	g.linef(`local.get %s`, local)
	g.line(`call $__lang_str_len`)
	g.line(`call $__streams_write`)
}

// emitStreamsWriteNewline emits one $__streams_write call against
// the pre-initialised newline byte at memory[32]. Used by $print
// / $eprint after the string body to give the `\n`-terminated
// write semantics print() exposes.
func (g *generator) emitStreamsWriteNewline(handleAccessor string) {
	g.linef(`call %s`, handleAccessor)
	g.line(`i32.const 32`) // newline byte
	g.line(`i32.const 1`)
	g.line(`call $__streams_write`)
}

// emitPreopenDirHelper writes `$__preopen_dir (result i32)` — a
// lazily-initialising accessor over the host's first preopen
// directory descriptor. Mirrors the preview-1 convention of
// resolving paths against `fd=3`. Called by the preview-2
// open_reader / open_writer / open_appender / read_file /
// write_file helpers; the descriptor handle gets cached at
// memory[120] (init bit 3 in the flag byte at memory[112]).
//
// The `get-directories` import returns `list<tuple<descriptor,
// string>>`; canonical-ABI lowering passes us a retptr we read
// (host_list_ptr, host_list_len) from. Each tuple is 12 bytes
// `(descriptor i32, name_ptr i32, name_len i32)` (4-byte aligned),
// so the first descriptor lives at `host_list_ptr + 0`. We
// ignore the name and any subsequent preopens.
func (g *generator) emitPreopenDirHelper() {
	g.line(`(func $__preopen_dir (result i32)`)
	g.indent++
	g.line(`(local $h i32) (local $list_ptr i32)`)
	// Already cached?
	g.line(`i32.const 112`)
	g.line(`i32.load`)
	g.line(`i32.const 8`) // bit 3
	g.line(`i32.and`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const 120`)
	g.line(`i32.load`)
	g.indent--
	g.line(`else`)
	g.indent++
	// get-directories(retptr=92) → mem[92]=ptr, mem[96]=len.
	g.line(`i32.const 92`)
	g.line(`call $__wasi_get_directories`)
	g.line(`i32.const 92`)
	g.line(`i32.load`)
	g.line(`local.set $list_ptr`)
	// First tuple's descriptor handle at list_ptr + 0.
	g.line(`local.get $list_ptr`)
	g.line(`i32.load`)
	g.line(`local.tee $h`)
	g.line(`i32.const 120`)
	g.line(`local.get $h`)
	g.line(`i32.store`)
	g.line(`i32.const 112`)
	g.line(`i32.const 112`)
	g.line(`i32.load`)
	g.line(`i32.const 8`)
	g.line(`i32.or`)
	g.line(`i32.store`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)
}

// emitOpenViaStreamHelper writes a private
// `$<name>(path) -> result<stream, errno>` helper that opens a
// path under the cached preopen directory and returns the
// requested stream type. It packages errors as a 2-element slot:
// retptr+0 is the stream handle on success / errno on failure;
// retptr+4 is the errno (0 on success). Callers (open_reader /
// open_writer / open_appender) wrap that into the Result[Reader|
// Writer, IoError] shape.
//
// `openFlags` and `descFlags` are the WIT-level `open-flags` and
// `descriptor-flags` immediates. `viaStreamImport` is the
// canonical-ABI lowered name of the via-stream call to use after
// the open succeeds (`$__wasi_descriptor_read_via_stream`,
// `$__wasi_descriptor_write_via_stream`, or
// `$__wasi_descriptor_append_via_stream`). Append-via-stream has
// a shorter signature `(param i32 i32)` (no offset), so callers
// pass `appendMode=true` to skip the offset push.
func (g *generator) emitOpenViaStreamHelper(name string, openFlags, descFlags int, viaStreamImport string, appendMode bool) {
	g.linef(`(func %s (param $path i32) (result i32)`, name)
	g.indent++
	g.line(`(local $errno i32) (local $desc i32) (local $stream i32) (local $result i32)`)
	// `$path` may arrive in single-i32 inline form (a short
	// path built via `$__str_concat`); the host's `open-at`
	// reads (ptr, len) from linear memory, so promote-to-heap
	// before passing on.
	g.emitPromoteStrParam("$path")

	// open-at(preopen, path-flags=1=symlink-follow, path_ptr,
	// path_len, open-flags, descriptor-flags, retptr=92).
	g.line(`call $__preopen_dir`)
	g.line(`i32.const 1`) // path-flags = symlink-follow
	g.line(`local.get $path`)
	g.emitStrLenFromLocal("$path")
	g.linef(`i32.const %d`, openFlags)
	g.linef(`i32.const %d`, descFlags)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_descriptor_open_at`)

	// Outer disc at retptr+0; non-zero = Err(error-code).
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	// Build Err(IoError) — Result.Err = tag 1, payload via
	// __build_io_error. The error-code at retptr+4 is the
	// `wasi:filesystem/types.error-code` enum index; translate it
	// to the preview-1 errno space before handing it to the
	// shared $__build_io_error table (which keys on preview-1
	// values).
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`call $__filesystem_error_translate`)
	g.line(`local.set $errno`)
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

	// Ok: descriptor at retptr+4. Fetch a stream from it. The
	// descriptor itself is intentionally leaked (the bump
	// allocator can't free, and the program lifetime is bounded).
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $desc`)
	if appendMode {
		// append-via-stream(self, retptr=92).
		g.line(`local.get $desc`)
		g.line(`i32.const 92`)
		g.linef(`call %s`, viaStreamImport)
	} else {
		// read/write-via-stream(self, offset=0, retptr=92).
		g.line(`local.get $desc`)
		g.line(`i64.const 0`)
		g.line(`i32.const 92`)
		g.linef(`call %s`, viaStreamImport)
	}
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	// Stream open failed. Treat as IoError with the inner errno.
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $errno`)
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

	// Ok: stream at retptr+4. Allocate Reader/Writer struct
	// holding the stream handle, return Ok wrapper.
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $stream`)
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $desc`) // reuse $desc as the struct ptr
	g.line(`local.get $stream`)
	g.line(`i32.store`)
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 0`) // Result.Ok = tag 0
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $desc`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`)`)
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
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $result`) // $result = data ptr (one past prefix)
	// Outer string[] container length via the array seam (per-
	// entry string stores in the loop below use emitStrLenStoreToLocal).
	g.emitArrayLenStoreToLocal("$argc", "$result")

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

	// SSO inline-output fast path: when strlen ≤ 3 the argv
	// string fits the single-i32 tiny inline form. Pack bytes
	// straight out of the host's NUL-terminated buffer into
	// `$sbase` (reusing it as the inline value holder; the
	// downstream `result[i] = $sbase` store doesn't care whether
	// $sbase is a heap data pointer or an inline-encoded value
	// because both round-trip through the SSO seams). Common
	// for short CLI flags like "-h", "-v", "go", "rm".
	g.line(`local.get $strlen`)
	g.line(`i32.const 3`)
	g.line(`i32.le_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0x80000000`)
	g.line(`local.get $strlen`)
	g.line(`i32.const 24`)
	g.line(`i32.shl`)
	g.line(`i32.or`)
	g.line(`local.set $sbase`)
	g.line(`i32.const 0`)
	g.line(`local.set $j`)
	g.line(`block $end_inline_pack`)
	g.indent++
	g.line(`loop $inline_pack`)
	g.indent++
	g.line(`local.get $j`)
	g.line(`local.get $strlen`)
	g.line(`i32.eq`)
	g.line(`br_if $end_inline_pack`)
	// $sbase |= mem[$cstr + $j] << ($j * 8)
	g.line(`local.get $sbase`)
	g.line(`local.get $cstr`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.get $j`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.shl`)
	g.line(`i32.or`)
	g.line(`local.set $sbase`)
	g.line(`local.get $j`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $j`)
	g.line(`br $inline_pack`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Store inline value at result[i] and advance the outer
	// loop. Mirrors the heap-path tail at the end of the body.
	g.line(`local.get $result`)
	g.line(`local.get $i`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $sbase`)
	g.line(`i32.store`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $outer`)
	g.indent--
	g.line(`end`)

	// Heap-form output path: strlen > 3.
	// Allocate strlen+4 bytes; write length prefix.
	g.line(`local.get $strlen`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $sbase`) // $sbase = data ptr (one past length prefix)
	g.emitStrLenStoreToLocal("$strlen", "$sbase")

	// Byte-copy cstr[0..strlen) into sbase.
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

	// result[i] = sbase (already the data pointer; length lives at -4)
	g.line(`local.get $result`)
	g.line(`local.get $i`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $sbase`)
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
	g.line(`(func $read_line (result i32)`)
	g.indent++
	g.line(`call $__stdin_handle`)
	g.line(`call $__stream_read_line`)
	g.indent--
	g.line(`)`)
}

// emitStdinStreamsReadLine writes the private
// `$__stream_read_line(handle) -> Option[string]` helper — the
// streams-flavoured line reader shared between the bare
// `$read_line` global, `__method_Reader_read_line` (preview-2),
// and any other path that reads a line from an `input-stream`
// resource. Reads one byte at a time via
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
	g.line(`(func $__stream_read_line (param $handle i32) (result i32)`)
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
	g.line(`local.get $handle`)
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

	// Materialise as a length-prefixed string. $sptr is the
	// data pointer (one past the length prefix).
	g.line(`local.get $cur_offset`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $sptr`)
	g.emitStrLenStoreToLocal("$cur_offset", "$sptr")

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
	// `$name` is compared byte-by-byte against host env entries via
	// `i32.load8_u`; promote-to-heap so the address arithmetic
	// against `$name + j` reads real memory. All `(local …)`
	// declarations must precede the first instruction in WAT, so
	// this preamble lands after the locals block above.
	g.emitPromoteStrParam("$name")

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

	g.emitStrLenFromLocal("$name")
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
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $sptr`) // $sptr = data ptr
	g.emitStrLenStoreToLocal("$vlen", "$sptr")
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
// arm64 helpers.
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

// emitStringMethodHelpers writes the wat-side `as_bytes` slice-
// header helper. Most string methods migrated to the lang
// prelude; the only one left here is `as_bytes`, which builds
// an aliasing `[u8]` slice header rather than copying.
func (g *generator) emitStringMethodHelpers() {
	// All string methods that took an allocation-free shape
	// (`is_empty`, `starts_with`, `ends_with`, `contains`,
	// `index_of`, `trim`) and the allocating ones built on
	// `__alloc_u8` (`to_lower`, `to_upper`, `bytes`, `split`,
	// `replace`) live in the lang prelude
	// (internal/prelude/prelude.lang). Only `as_bytes` —
	// which builds an aliasing slice header rather than
	// copying — still needs a wat-side helper since the
	// prelude doesn't yet have a way to allocate the 8-byte
	// `{data_ptr, len}` slice header on its own.

	// $__method_string_as_bytes(s): [u8] — non-copying view.
	// Allocates only the 8-byte slice header `{data_ptr=s,
	// len=len(s)}`; the data_ptr aliases the string's
	// payload, sharing its lifetime. Reads/writes through the
	// view route through `__slice_idx_1` (1-byte stride),
	// which is the same helper byte-arrays use, so the read
	// path produces ergonomic-equivalent values to a copying
	// `bytes()` call without the alloc. Mutations through the
	// view do propagate to the underlying string memory —
	// callers that don't want this should use `bytes()`.
	g.line(`(func $__method_string_as_bytes (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $hdr i32)`)
	// The returned slice header records the source string's
	// `data_ptr`; promote inline-form strings to heap first so
	// the byte-array view reads real memory through it.
	g.emitPromoteStrParam("$s")
	g.emitStrLenFromLocal("$s")
	g.line(`local.set $sLen`)
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $hdr`)
	g.line(`local.get $s`)
	g.line(`i32.store`) // data_ptr = s
	g.line(`local.get $hdr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $sLen`)
	g.line(`i32.store`) // len = sLen
	g.line(`local.get $hdr`)
	g.indent--
	g.line(`)`)
}




// emitBulkMemoryHelpers writes thin wat shims around wasm's
// bulk-memory `memory.copy` / `memory.fill` instructions so
// the lang prelude can call them as regular functions.
//
// Three helpers are exposed under the `__memcpy` / `__memset`
// / `__alloc_u8` naming the doc roadmap calls out (see
// "Stdlib implementation strategy → Bridge functions for
// wasm intrinsics"). They're the unlock for migrating
// helpers that build / scan growable byte buffers — today
// the map runtime + remaining string methods live as
// 1000+-line wat blocks because their memcpy / memset /
// alloc access can't yet be expressed in lang.
//
// Args:
//
//	__memcpy(dst, src, n) — copy n bytes from src to dst.
//	__memset(dst, b, n)   — write byte b to each of n bytes
//	                        starting at dst.
//	__alloc_u8(n)         — allocate a fresh `u8[]` of length
//	                        n, zero-initialised. The lang
//	                        array layout is [len:i32, data...]
//	                        with the user-visible pointer
//	                        addressing the data; `__lang_alloc`
//	                        returns the buffer base, we write
//	                        the length prefix, and return
//	                        base + 4. `string_from_bytes(buf)`
//	                        is the natural way to seal it
//	                        back into a string.
//
// memcpy / memset treat overlapping ranges per the wasm spec:
// `memory.copy` is required to behave correctly regardless
// of overlap (the engine picks the right copy direction).
func (g *generator) emitBulkMemoryHelpers() {
	g.line(`(func $__memcpy (param $dst i32) (param $src i32) (param $n i32)`)
	g.indent++
	g.line(`local.get $dst`)
	g.line(`local.get $src`)
	g.line(`local.get $n`)
	g.line(`memory.copy`)
	g.indent--
	g.line(`)`)

	g.line(`(func $__memset (param $dst i32) (param $b i32) (param $n i32)`)
	g.indent++
	g.line(`local.get $dst`)
	g.line(`local.get $b`)
	g.line(`local.get $n`)
	g.line(`memory.fill`)
	g.indent--
	g.line(`)`)

	g.line(`(func $__alloc_u8 (param $n i32) (result i32)`)
	g.indent++
	g.line(`(local $base i32)`)
	// Short-circuit on n == 0: return the shared static empty-
	// buffer sentinel (`[length=0]` at internString("") offset)
	// rather than allocating a fresh 4-byte length-only block.
	// The empty-string sentinel from PR #299 is byte-identical
	// (4 bytes of zero followed by an empty data area) so the
	// two seams share storage on wasm; the native targets keep
	// distinct `.LStr_Empty` / `.LArr_Empty` symbols to leave
	// room for future array-only encoding changes.
	g.line(`local.get $n`)
	g.line(`i32.eqz`)
	g.line(`if (result i32)`)
	g.indent++
	g.linef("i32.const %d", g.internString(""))
	g.indent--
	g.line(`else`)
	g.indent++
	// Allocate n + 4 bytes (length prefix + payload). $base is
	// the data pointer (one past the length prefix).
	g.line(`local.get $n`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $base`)
	g.emitArrayLenStoreToLocal("$n", "$base")
	// __lang_alloc memory comes from a freshly bumped region
	// that wasm initialises to zero, so the payload bytes are
	// already 0; no explicit memset needed.
	g.line(`local.get $base`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)

	// $__alloc(n): i32 — raw allocation of n bytes,
	// no length prefix. Returns the base pointer. Lower-
	// level than `__alloc_u8` — use this when the caller
	// owns the memory layout entirely (eg the Map
	// runtime's mixed bucket-index + entries buffer).
	g.line(`(func $__alloc (param $n i32) (result i32)`)
	g.indent++
	g.line(`local.get $n`)
	g.line(`call $__lang_alloc`)
	g.indent--
	g.line(`)`)

	// $__load_i32(addr): i32 — read a 4-byte word from
	// linear memory. Pairs with `__store_i32`. Both are
	// raw-memory escape hatches the prelude uses to
	// build typed-array structures (the `string[]` /
	// `JsonValue[]` pointer-stride append, the Map
	// runtime's bucket / entry pokes). Out-of-bounds
	// addresses produce wasm traps; the prelude is
	// expected to bounds-check at the lang level.
	g.line(`(func $__load_i32 (param $addr i32) (result i32)`)
	g.indent++
	g.line(`local.get $addr`)
	g.line(`i32.load`)
	g.indent--
	g.line(`)`)

	g.line(`(func $__store_i32 (param $addr i32) (param $v i32)`)
	g.indent++
	g.line(`local.get $addr`)
	g.line(`local.get $v`)
	g.line(`i32.store`)
	g.indent--
	g.line(`)`)

	// $__load_ptr / $__store_ptr — pointer-width counterparts.
	// On wasm32 a pointer IS i32, so these are i32 round-trips.
	// arm64 backends ship 8-byte versions to avoid heap-pointer
	// truncation. The Map runtime uses these for its `m → buf`
	// handle indirection so the same prelude works on both.
	g.line(`(func $__load_ptr (param $addr i32) (result i32)`)
	g.indent++
	g.line(`local.get $addr`)
	g.line(`i32.load`)
	g.indent--
	g.line(`)`)
	g.line(`(func $__store_ptr (param $addr i32) (param $v i32)`)
	g.indent++
	g.line(`local.get $addr`)
	g.line(`local.get $v`)
	g.line(`i32.store`)
	g.indent--
	g.line(`)`)

	// $__ptr_width returns the pointer width in bytes: 4 on
	// wasm32, 8 on arm64. The Map runtime uses this constant
	// to size per-entry key/value slots.
	g.line(`(func $__ptr_width (result i32)`)
	g.indent++
	g.line(`i32.const 4`)
	g.indent--
	g.line(`)`)

	// Wide / sub-i32 wat shims (`__store_i64`, `__store_f64`,
	// `__store_u8`, `__store_u16`, plus the matching loads)
	// were removed when `arr.push(v)` moved to inline IR
	// lowering — the IR emits the typed wasm store ops
	// directly, no callable wat shim needed. If a future
	// lang-prelude helper needs raw wide / sub-i32 pokes,
	// reintroduce them here next to `__store_i32`.
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
// emitTcpHelpers writes the wasi:sockets-flavoured TCP builtins.
// `tcp_listen` drives the bind+listen pipeline directly so the
// program is self-contained (no `wasmtime --tcp-listen=…` host
// flag needed), and `tcp_accept` returns a heap struct holding
// the per-connection (tcp-socket, input-stream, output-stream)
// triple instead of a bare fd.
func (g *generator) emitTcpHelpers() {
	g.emitTcpHelpersPreview2()
}


// emitTcpHelpersPreview2 writes the wasi:sockets-flavoured TCP
// builtins. The user-facing API stays "fd-shaped" — every
// tcp_listen / tcp_accept return value is a 12-byte heap struct
// `(tcp_socket, input_stream, output_stream)`; for listening
// sockets the two stream slots are 0 (no streams attached until a
// connection is accepted). tcp_recv / tcp_send / tcp_close all
// take that pointer back and read the appropriate slot.
//
// The (tcp-socket, input-stream, output-stream) triple is the
// shape `tcp-socket.accept` returns natively, so the
// per-connection struct is a direct lift of the canonical-ABI
// payload. For tcp_close to be a single function the listener and
// connection structs share the same shape — listeners just zero
// the stream slots, and tcp_close skips drops on zero-handle
// slots. The bump allocator can't free the struct itself, so it
// leaks; same trade-off step 3c made for descriptors.
func (g *generator) emitTcpHelpersPreview2() {
	g.emitNetworkHandleAccessor()
	g.emitTcpListenPreview2()
	g.emitTcpAcceptPreview2()
	g.emitTcpRecvPreview2()
	g.emitTcpSendPreview2()
	g.emitTcpClosePreview2()
}

// emitNetworkHandleAccessor writes
// `$__network_handle (result i32)` — the lazily-initialising
// accessor over `wasi:sockets/instance-network.instance-network`.
// Mirrors the stdin/stdout/stderr cached-handle pattern: the
// result lives at memory[124], with bit 4 of the init-flags byte
// at memory[112] tracking whether the slot is populated. Resource
// handles are opaque ints where 0 is a valid value, so the
// flag-bit indirection is necessary.
func (g *generator) emitNetworkHandleAccessor() {
	g.line(`(func $__network_handle (result i32)`)
	g.indent++
	g.line(`(local $h i32)`)
	g.line(`i32.const 112`)
	g.line(`i32.load`)
	g.line(`i32.const 16`) // bit 4
	g.line(`i32.and`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const 124`)
	g.line(`i32.load`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`call $__wasi_instance_network`)
	g.line(`local.tee $h`)
	g.line(`i32.const 124`)
	g.line(`local.get $h`)
	g.line(`i32.store`)
	g.line(`i32.const 112`)
	g.line(`i32.const 112`)
	g.line(`i32.load`)
	g.line(`i32.const 16`)
	g.line(`i32.or`)
	g.line(`i32.store`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)
}

// emitTcpListenPreview2 writes `$tcp_listen(port) -> i32` for the
// wasi:sockets path. Pipeline: create-tcp-socket(ipv4) →
// start-bind + finish-bind (0.0.0.0:port) → start-listen +
// finish-listen → return a 12-byte heap struct
// `(tcp_socket, 0, 0)`. On any error returns -errno (the inner
// error-code byte) so callers can treat negative as failure —
// matches the preview-1 contract for backward compatibility.
//
// `start-bind` takes 15 i32 params: self + borrow<network> +
// `ip-socket-address` flattened (1 disc + 11 payload) + retptr.
// We always emit the IPv4 case bound to 0.0.0.0:port; the unused
// payload slots beyond the IPv4 5-slot prefix are zero-padded
// because the variant joins the wider IPv6 layout.
func (g *generator) emitTcpListenPreview2() {
	g.line(`(func $tcp_listen (param $port i32) (result i32)`)
	g.indent++
	g.line(`(local $sock i32) (local $struct i32)`)

	// create-tcp-socket(0=ipv4, retptr=92) -> result<tcp-socket, error-code>
	g.line(`i32.const 0`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_create_tcp_socket`)
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	// errno from retptr+4 (i8 enum), return -errno.
	g.line(`i32.const 0`)
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.sub`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $sock`)

	// start-bind(self, borrow<network>, ip-socket-address ipv4(0.0.0.0:port), retptr=92)
	g.line(`local.get $sock`)
	g.line(`call $__network_handle`)
	g.line(`i32.const 0`)         // disc = 0 (ipv4)
	g.line(`local.get $port`)     // ipv4-socket-address.port
	g.line(`i32.const 0`)         // ipv4 byte 0
	g.line(`i32.const 0`)         // ipv4 byte 1
	g.line(`i32.const 0`)         // ipv4 byte 2
	g.line(`i32.const 0`)         // ipv4 byte 3
	g.line(`i32.const 0`)         // pad: ipv6 flow-info / ipv4 unused
	g.line(`i32.const 0`)         // pad
	g.line(`i32.const 0`)         // pad
	g.line(`i32.const 0`)         // pad
	g.line(`i32.const 0`)         // pad
	g.line(`i32.const 0`)         // pad — total 6 padding slots beyond the ipv4 5-slot prefix
	g.line(`i32.const 92`)
	g.line(`call $__wasi_tcp_start_bind`)
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.sub`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// finish-bind(self, retptr).
	g.line(`local.get $sock`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_tcp_finish_bind`)
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.sub`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// start-listen(self, retptr).
	g.line(`local.get $sock`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_tcp_start_listen`)
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.sub`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// finish-listen(self, retptr).
	g.line(`local.get $sock`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_tcp_finish_listen`)
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.sub`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// Allocate 12-byte struct (sock, 0, 0).
	g.line(`i32.const 12`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $struct`)
	g.line(`local.get $sock`)
	g.line(`i32.store`)
	g.line(`local.get $struct`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $struct`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $struct`)
	g.indent--
	g.line(`)`)
}

// emitTcpAcceptPreview2 writes `$tcp_accept(listener) -> i32`.
// Pipeline: subscribe(sock) → pollable.block → accept. The
// `accept` result is `result<tuple<tcp-socket, input-stream,
// output-stream>, error-code>`, lowered to 16 bytes (1 disc + 3
// pad + 12 payload). Our shared retptr at memory[92] is only 12
// bytes wide, so we allocate a fresh 16-byte scratch via
// __lang_alloc. The pointer leaks (bump allocator) — that's a
// 16-byte cost per accept, acceptable for the connection
// lifetime.
func (g *generator) emitTcpAcceptPreview2() {
	g.line(`(func $tcp_accept (param $listener i32) (result i32)`)
	g.indent++
	g.line(`(local $sock i32) (local $pollable i32) (local $retptr i32)`)
	g.line(`(local $newsock i32) (local $instream i32) (local $outstream i32) (local $struct i32)`)
	// Subscribe + block until a connection is ready. accept is
	// non-blocking on the wasi:sockets API; without the poll we'd
	// just get would-block on the first call.
	g.line(`local.get $listener`)
	g.line(`i32.load`)
	g.line(`local.tee $sock`)
	g.line(`call $__wasi_tcp_subscribe`)
	g.line(`local.tee $pollable`)
	g.line(`call $__wasi_pollable_block`)
	g.line(`local.get $pollable`)
	g.line(`call $__wasi_pollable_drop`)

	// Allocate a 16-byte retptr scratch.
	g.line(`i32.const 16`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $retptr`)
	g.line(`local.get $sock`)
	g.line(`local.get $retptr`)
	g.line(`call $__wasi_tcp_accept`)
	g.line(`local.get $retptr`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	// errno at retptr+4.
	g.line(`i32.const 0`)
	g.line(`local.get $retptr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.sub`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// Ok payload at retptr+4: tuple<i32, i32, i32>.
	g.line(`local.get $retptr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $newsock`)
	g.line(`local.get $retptr`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $instream`)
	g.line(`local.get $retptr`)
	g.line(`i32.const 12`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $outstream`)

	// Allocate 12-byte connection struct.
	g.line(`i32.const 12`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $struct`)
	g.line(`local.get $newsock`)
	g.line(`i32.store`)
	g.line(`local.get $struct`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $instream`)
	g.line(`i32.store`)
	g.line(`local.get $struct`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`local.get $outstream`)
	g.line(`i32.store`)
	g.line(`local.get $struct`)
	g.indent--
	g.line(`)`)
}

// emitTcpRecvPreview2 writes `$tcp_recv(conn, max) -> string`.
// Loads input_stream from the connection struct, calls
// blocking-read, materialises a length-prefixed string from the
// host buffer. On stream errors / EOF we return an empty string
// — same shape preview-1 tcp_recv produces on fd_read errno.
func (g *generator) emitTcpRecvPreview2() {
	g.line(`(func $tcp_recv (param $conn i32) (param $max i32) (result i32)`)
	g.indent++
	g.line(`(local $stream i32) (local $list_ptr i32) (local $n i32)`)
	g.line(`(local $sbase i32) (local $sptr i32) (local $j i32)`)
	// stream = mem[conn + 4]
	g.line(`local.get $conn`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $stream`)
	// blocking-read(stream, max, retptr=92)
	g.line(`local.get $stream`)
	g.line(`local.get $max`)
	g.line(`i64.extend_i32_u`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_blocking_read`)
	// On Err, treat as empty.
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const 0`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`i32.const 100`)
	g.line(`i32.load`)
	g.indent--
	g.line(`end`)
	g.line(`local.set $n`)
	g.line(`i32.const 96`)
	g.line(`i32.load`)
	g.line(`local.set $list_ptr`)
	// SSO inline-output fast path: small protocol tokens like
	// "OK", "OK\n", "GO" (≤ 3 bytes) fit the single-i32 tiny
	// cap and skip the alloc + length-prefix + memcpy + NUL
	// detour.
	g.line(`local.get $n`)
	g.line(`i32.const 3`)
	g.line(`i32.le_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0x80000000`)
	g.line(`local.get $n`)
	g.line(`i32.const 24`)
	g.line(`i32.shl`)
	g.line(`i32.or`)
	g.line(`local.set $sptr`)
	g.line(`i32.const 0`)
	g.line(`local.set $j`)
	g.line(`block $end_pack`)
	g.indent++
	g.line(`loop $pack_loop`)
	g.indent++
	g.line(`local.get $j`)
	g.line(`local.get $n`)
	g.line(`i32.eq`)
	g.line(`br_if $end_pack`)
	g.line(`local.get $sptr`)
	g.line(`local.get $list_ptr`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.get $j`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.shl`)
	g.line(`i32.or`)
	g.line(`local.set $sptr`)
	g.line(`local.get $j`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $j`)
	g.line(`br $pack_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $sptr`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Heap-form output: allocate length-prefixed string of size
	// $n + 5 (4 prefix + NUL), matching the existing string-from-
	// bytes allocation pattern.
	g.line(`local.get $n`)
	g.line(`i32.const 5`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $sptr`) // $sptr = data ptr
	g.emitStrLenStoreToLocal("$n", "$sptr")
	// memcpy host buffer into our string body.
	g.line(`local.get $sptr`)
	g.line(`local.get $list_ptr`)
	g.line(`local.get $n`)
	g.line(`memory.copy`)
	g.line(`local.get $sptr`)
	g.indent--
	g.line(`)`)
}

// emitTcpSendPreview2 writes `$tcp_send(conn, data) -> i32`.
// Loads output_stream from the connection struct, sends the
// string via the chunked $__streams_write helper. Returns the
// content length on success or -1 on failure. The preview-1
// version surfaced -errno; we don't have a meaningful errno here
// since stream errors get translated upstream, so -1 is the best
// negative sentinel.
func (g *generator) emitTcpSendPreview2() {
	g.line(`(func $tcp_send (param $conn i32) (param $data i32) (result i32)`)
	g.indent++
	g.line(`(local $stream i32) (local $len i32)`)
	// `$data` flows through `$__streams_write` synchronously, so
	// route it via `$__lang_str_data_ptr` — heap pointers pass
	// through, inline values spill into the shared scratch slot.
	// Avoids the promote-to-heap alloc on inline payloads (e.g.
	// short `$__str_concat` results piped straight to a socket).
	g.line(`local.get $conn`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $stream`)
	g.emitStrLenFromLocal("$data")
	g.line(`local.set $len`)
	g.line(`local.get $stream`)
	g.line(`local.get $data`)
	g.line(`call $__lang_str_data_ptr`)
	g.line(`local.get $len`)
	g.line(`call $__streams_write`)
	g.line(`local.get $len`)
	g.indent--
	g.line(`)`)
}

// emitTcpClosePreview2 writes `$tcp_close(conn) -> i32`. Drops
// the resources the struct holds: input + output streams first
// (only if non-zero — listener structs zero those slots), then
// the parent tcp-socket. The order matters: the canonical-ABI
// resource layer rejects dropping a parent that still has live
// children with "resource has children", so streams must go
// before the socket. Resource drops are infallible, so the
// return value is always 0.
func (g *generator) emitTcpClosePreview2() {
	g.line(`(func $tcp_close (param $struct i32) (result i32)`)
	g.indent++
	g.line(`(local $h i32)`)
	// input-stream first (child of tcp-socket).
	g.line(`local.get $struct`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.tee $h`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $h`)
	g.line(`call $__wasi_input_stream_drop`)
	g.indent--
	g.line(`end`)
	// output-stream (also a child).
	g.line(`local.get $struct`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.tee $h`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $h`)
	g.line(`call $__wasi_output_stream_drop`)
	g.indent--
	g.line(`end`)
	// Now the socket itself.
	g.line(`local.get $struct`)
	g.line(`i32.load`)
	g.line(`call $__wasi_tcp_socket_drop`)
	g.line(`i32.const 0`)
	g.indent--
	g.line(`)`)
}

// emitHttpHandlerWrapper writes the WAT function exported as
// `wasi:http/incoming-handler@0.2.0#handle` for the wasi-http
// target. The wrapper marshals the canonical-ABI incoming request
// into a 12-byte HttpRequest struct `(method, path, body)` (all
// length-prefixed lang strings), invokes the user's
// `handle(req: HttpRequest): HttpResponse`, then streams the
// response back through outgoing-body and hands an
// `Ok(outgoing-response)` to response-outparam.set.
//
// Resource lifetime, in order:
//   - `req` (incoming-request) is borrowed for `.method` /
//     `.path-with-query` / `.consume`, then explicitly dropped
//     once the body has been finished.
//   - `consume()` returns ownership of the incoming-body. After
//     reading the stream we hand the body to `[static]finish`
//     which transfers ownership and produces a future-trailers we
//     drop.
//   - `fields` constructed for the response are owned by the
//     outgoing-response constructor — no manual drop.
//   - `outgoing-body` from `outgoing-response.body()` is consumed
//     by `[static]finish`, again no manual drop.
//   - `output-stream` from `outgoing-body.write()` IS dropped
//     manually before we finish the body (the canonical-ABI
//     rejects parent drops with live children, same as TCP).
//   - `outgoing-response` and `response-outparam` are both
//     consumed by `[static]response-outparam.set`.
//
// Retptr scratch: each canonical-ABI return area we touch fits in
// 16 bytes (the static area at memory[92]) except
// `outgoing-body.finish`, whose result<_, error-code> can be 30+
// bytes once the variant payload joins are accounted for. We
// allocate a 64-byte scratch via `__lang_alloc(64)` for those
// calls. Eaten by the bump allocator, but the cost is per-request
// and the host serialises requests through `wasmtime serve`
// anyway — measurable budgets for fastly-style handlers don't
// notice.
func (g *generator) emitHttpHandlerWrapper() {
	// Sanity-check the user's `handle` signature. The checker
	// can't enforce this (HttpRequest / HttpResponse are
	// always-available structs, not http-target-only), so do
	// it here to keep error messages precise and the runtime
	// emit well-typed.
	handleFn := g.funcDecls["handle"]
	if handleFn == nil {
		// Fall back to emitting a stub that returns 500. The
		// real error comes from wasm-tools / wasmtime when the
		// component doesn't satisfy the export — a clearer
		// message would need a checker hook gated on
		// EmitOptions, which we haven't plumbed. Acceptable
		// for now: the lang error will be "no `handle` defined
		// for -target wasi-http" once we add the checker hook.
	}

	// Pre-pack the static method names. ASCII case matches
	// what most frameworks want (exposed as the lang string
	// `req.method`); upper-cased here so the user gets the
	// HTTP/1.1 canonical form regardless of wire format.
	//
	// Short method names ("GET", "PUT") use the same single-i32
	// SSO inline encoding as `OpConstStr` literal-pack — so a
	// user's `req.method == "GET"` compare sees identical
	// inline-encoded i32s on both sides and `$__str_eq`'s
	// pointer-eq fast path short-circuits instead of running
	// the byte loop. Longer methods (HEAD, POST, DELETE,
	// CONNECT, OPTIONS, TRACE, PATCH) keep their heap-form
	// `internString` entry; the user-side `"HEAD"` etc. also
	// stay heap-form (>3 bytes), so pointer-eq dedup via the
	// shared interned offset still works for them.
	methodGet := g.internOrPackMethod("GET")
	methodHead := g.internOrPackMethod("HEAD")
	methodPost := g.internOrPackMethod("POST")
	methodPut := g.internOrPackMethod("PUT")
	methodDelete := g.internOrPackMethod("DELETE")
	methodConnect := g.internOrPackMethod("CONNECT")
	methodOptions := g.internOrPackMethod("OPTIONS")
	methodTrace := g.internOrPackMethod("TRACE")
	methodPatch := g.internOrPackMethod("PATCH")
	emptyStr := g.internOrPackMethod("")

	g.line(`(func $__http_entry (param $req i32) (param $out i32)`)
	g.indent++
	g.line(`(local $retptr i32)`)
	g.line(`(local $method_str i32) (local $path_str i32) (local $body_str i32)`)
	g.line(`(local $body_handle i32) (local $body_stream i32)`)
	g.line(`(local $host_ptr i32) (local $host_len i32)`)
	g.line(`(local $body_buf i32) (local $body_size i32) (local $body_cur i32) (local $body_new_buf i32)`)
	g.line(`(local $disc i32)`)
	g.line(`(local $req_struct i32) (local $resp_struct i32) (local $status i32)`)
	g.line(`(local $resp_handle i32) (local $headers i32)`)
	g.line(`(local $out_body i32) (local $out_stream i32)`)
	g.line(`(local $__arena_handle i32)`)

	// Implicit per-request arena: save the bump cursor at
	// entry, restore it before returning so every allocation
	// the handler made (request struct, body string, response
	// body, intermediate string concat results, Map snapshots,
	// etc.) gets reclaimed in one pointer-store. The host has
	// already consumed the response body via the outgoing-body
	// writes by the time we restore — no live data references
	// the freed region.
	//
	// Same shape as the user-controlled `arena { … }` block,
	// just hoisted to the request-handler boundary so callers
	// don't have to wrap their bodies manually. Pairs with the
	// doc-roadmap "Implicit per-handler / per-CLI-invocation
	// arena defaults still TBD" item — settled here.
	g.line(`call $arena_save`)
	g.line(`local.set $__arena_handle`)

	// 64-byte scratch for canonical-ABI returns; large enough for
	// outgoing-body.finish's result<_, error-code> joined variant.
	g.line(`i32.const 64`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $retptr`)

	// ============================================================
	// Read method.
	// ============================================================
	g.line(`local.get $req`)
	g.line(`local.get $retptr`)
	g.line(`call $__wasi_http_request_method`)
	// Method variant: disc at retptr+0, payload (string ptr/len)
	// at retptr+4 / +8. Stash the disc in a local — the surrounding
	// `block`s introduce stack fences (default block type is empty
	// → empty, so the value stack inside is independent of the
	// outer one), so we can't carry the loaded byte across the
	// nesting and have to reload it inside.
	g.line(`local.get $retptr`)
	g.line(`i32.load8_u`)
	g.line(`local.set $disc`)
	g.line(`block $m_done`)
	g.indent++
	g.line(`block $m_other`)
	g.indent++
	g.line(`block $m_patch`)
	g.indent++
	g.line(`block $m_trace`)
	g.indent++
	g.line(`block $m_options`)
	g.indent++
	g.line(`block $m_connect`)
	g.indent++
	g.line(`block $m_delete`)
	g.indent++
	g.line(`block $m_put`)
	g.indent++
	g.line(`block $m_post`)
	g.indent++
	g.line(`block $m_head`)
	g.indent++
	g.line(`block $m_get`)
	g.indent++
	g.line(`local.get $disc`)
	// br_table over disc; out-of-range falls into "other" too (cap at 9).
	g.line(`br_table $m_get $m_head $m_post $m_put $m_delete $m_connect $m_options $m_trace $m_patch $m_other $m_other`)
	g.indent--
	g.line(`end`) // m_get
	g.linef(`i32.const %d`, methodGet)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_head
	g.linef(`i32.const %d`, methodHead)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_post
	g.linef(`i32.const %d`, methodPost)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_put
	g.linef(`i32.const %d`, methodPut)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_delete
	g.linef(`i32.const %d`, methodDelete)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_connect
	g.linef(`i32.const %d`, methodConnect)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_options
	g.linef(`i32.const %d`, methodOptions)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_trace
	g.linef(`i32.const %d`, methodTrace)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_patch
	g.linef(`i32.const %d`, methodPatch)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_other
	// other(s): ptr at retptr+4, len at retptr+8. Materialise.
	g.line(`local.get $retptr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $host_ptr`)
	g.line(`local.get $retptr`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $host_len`)
	g.line(`local.get $host_ptr`)
	g.line(`local.get $host_len`)
	g.line(`call $__bytes_to_lang_string`)
	g.line(`local.set $method_str`)
	g.indent--
	g.line(`end`) // m_done

	// ============================================================
	// Read path-with-query (option<string>).
	// ============================================================
	g.line(`local.get $req`)
	g.line(`local.get $retptr`)
	g.line(`call $__wasi_http_request_path_with_query`)
	g.line(`local.get $retptr`)
	g.line(`i32.load8_u`)
	g.line(`if (result i32)`)
	g.indent++
	// Some(string): ptr at retptr+4, len at retptr+8.
	g.line(`local.get $retptr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $host_ptr`)
	g.line(`local.get $retptr`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $host_len`)
	g.line(`local.get $host_ptr`)
	g.line(`local.get $host_len`)
	g.line(`call $__bytes_to_lang_string`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.linef(`i32.const %d`, emptyStr)
	g.indent--
	g.line(`end`)
	g.line(`local.set $path_str`)

	// ============================================================
	// Read body via consume + stream + bulk-read accumulator.
	// ============================================================
	g.line(`local.get $req`)
	g.line(`local.get $retptr`)
	g.line(`call $__wasi_http_request_consume`)
	g.line(`local.get $retptr`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	// Err: empty body, no body resource to drop.
	g.linef(`i32.const %d`, emptyStr)
	g.line(`local.set $body_str`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $retptr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $body_handle`)
	// stream() -> result<input-stream>
	g.line(`local.get $body_handle`)
	g.line(`local.get $retptr`)
	g.line(`call $__wasi_http_incoming_body_stream`)
	g.line(`local.get $retptr`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	g.linef(`i32.const %d`, emptyStr)
	g.line(`local.set $body_str`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $retptr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $body_stream`)
	// Accumulator: 4 KiB doubling buffer, same as $read_file.
	g.line(`i32.const 4096`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $body_buf`)
	g.line(`i32.const 4096`)
	g.line(`local.set $body_size`)
	g.line(`i32.const 0`)
	g.line(`local.set $body_cur`)
	g.line(`block $body_end`)
	g.indent++
	g.line(`loop $body_loop`)
	g.indent++
	// blocking-read(stream, 4096, retptr=92) — we use the static
	// retptr here, not our 64-byte one, because list<u8>'s ret
	// area is just (ptr, len) = 8 bytes.
	g.line(`local.get $body_stream`)
	g.line(`i64.const 4096`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_blocking_read`)
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`br_if $body_end`)
	g.line(`i32.const 100`)
	g.line(`i32.load`)
	g.line(`local.tee $host_len`)
	g.line(`i32.eqz`)
	g.line(`br_if $body_end`)
	g.line(`i32.const 96`)
	g.line(`i32.load`)
	g.line(`local.set $host_ptr`)
	// Grow the buffer until cur + host_len fits. Doubling.
	g.line(`block $grow_done`)
	g.indent++
	g.line(`loop $grow`)
	g.indent++
	g.line(`local.get $body_cur`)
	g.line(`local.get $host_len`)
	g.line(`i32.add`)
	g.line(`local.get $body_size`)
	g.line(`i32.le_u`)
	g.line(`br_if $grow_done`)
	g.line(`local.get $body_size`)
	g.line(`i32.const 1`)
	g.line(`i32.shl`)
	g.line(`local.set $body_size`)
	g.line(`local.get $body_size`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $body_new_buf`)
	// memcpy(new_buf, old_buf, body_cur).
	g.line(`local.get $body_new_buf`)
	g.line(`local.get $body_buf`)
	g.line(`local.get $body_cur`)
	g.line(`memory.copy`)
	g.line(`local.get $body_new_buf`)
	g.line(`local.set $body_buf`)
	g.line(`br $grow`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Append host bytes: memcpy(body_buf + body_cur, host_ptr, host_len)
	g.line(`local.get $body_buf`)
	g.line(`local.get $body_cur`)
	g.line(`i32.add`)
	g.line(`local.get $host_ptr`)
	g.line(`local.get $host_len`)
	g.line(`memory.copy`)
	g.line(`local.get $body_cur`)
	g.line(`local.get $host_len`)
	g.line(`i32.add`)
	g.line(`local.set $body_cur`)
	g.line(`br $body_loop`)
	g.indent--
	g.line(`end`) // body_loop
	g.indent--
	g.line(`end`) // body_end
	// Materialise lang string from (body_buf, body_cur).
	g.line(`local.get $body_buf`)
	g.line(`local.get $body_cur`)
	g.line(`call $__bytes_to_lang_string`)
	g.line(`local.set $body_str`)
	// Drop input-stream.
	g.line(`local.get $body_stream`)
	g.line(`call $__wasi_input_stream_drop`)
	g.indent--
	g.line(`end`) // stream-result branch

	// finish(body_handle) -> future-trailers; drop the trailers.
	g.line(`local.get $body_handle`)
	g.line(`call $__wasi_http_incoming_body_finish`)
	g.line(`call $__wasi_http_future_trailers_drop`)
	g.indent--
	g.line(`end`) // consume-result branch

	// Drop incoming-request.
	g.line(`local.get $req`)
	g.line(`call $__wasi_http_request_drop`)

	// ============================================================
	// Build HttpRequest struct (12 bytes: method, path, body).
	// ============================================================
	g.line(`i32.const 12`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $req_struct`)
	g.line(`local.get $method_str`)
	g.line(`i32.store`)
	g.line(`local.get $req_struct`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $path_str`)
	g.line(`i32.store`)
	g.line(`local.get $req_struct`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`local.get $body_str`)
	g.line(`i32.store`)

	// ============================================================
	// Call user-defined `handle(req): HttpResponse`.
	// ============================================================
	g.line(`local.get $req_struct`)
	g.line(`call $handle`)
	g.line(`local.set $resp_struct`)

	// HttpResponse layout: [status:i32][body:i32 (string ptr)] = 8 bytes.
	g.line(`local.get $resp_struct`)
	g.line(`i32.load`)
	g.line(`local.set $status`)
	g.line(`local.get $resp_struct`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $body_str`) // reuse local; now holds response body

	// ============================================================
	// Build outgoing-response.
	// ============================================================
	g.line(`call $__wasi_http_fields_new`)
	g.line(`local.set $headers`)
	g.line(`local.get $headers`)
	g.line(`call $__wasi_http_response_new`)
	g.line(`local.set $resp_handle`)

	// set-status-code(resp, status) — returns the result-disc inline; ignore it.
	g.line(`local.get $resp_handle`)
	g.line(`local.get $status`)
	g.line(`call $__wasi_http_response_set_status`)
	g.line(`drop`)

	// body() -> result<outgoing-body>.
	g.line(`local.get $resp_handle`)
	g.line(`local.get $retptr`)
	g.line(`call $__wasi_http_response_body`)
	// Assume Ok (the spec says it can only fail if called twice).
	g.line(`local.get $retptr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $out_body`)

	// outgoing-body.write() -> result<output-stream>. Take the
	// output-stream BEFORE calling response-outparam.set, since
	// `set` consumes the response and the host treats the
	// response-outparam handing-off as the "headers are sealed,
	// start streaming the body" cue.
	g.line(`local.get $out_body`)
	g.line(`local.get $retptr`)
	g.line(`call $__wasi_http_outgoing_body_write`)
	g.line(`local.get $retptr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $out_stream`)

	// ============================================================
	// response-outparam.set(out, Ok(resp_handle)). Has to happen
	// BEFORE we write the body bytes — the host won't accept body
	// chunks for a response whose headers haven't been finalised
	// via `set`. Takes ownership of `out` and `resp_handle`.
	// 9-param call: (outparam, disc, payload[7 slots, slot 2=i64]).
	// ============================================================
	g.line(`local.get $out`)
	g.line(`i32.const 0`)            // result disc = 0 (Ok)
	g.line(`local.get $resp_handle`) // Ok payload slot 0 (the response handle)
	g.line(`i32.const 0`)            // payload slot 1
	g.line(`i64.const 0`)            // payload slot 2 (i64, joined-variant width)
	g.line(`i32.const 0`)            // payload slot 3
	g.line(`i32.const 0`)            // payload slot 4
	g.line(`i32.const 0`)            // payload slot 5
	g.line(`i32.const 0`)            // payload slot 6
	g.line(`call $__wasi_http_response_outparam_set`)

	// blocking-write-and-flush via the chunked $__streams_write
	// helper. Now that the response is "in flight", body bytes
	// stream out to the client as we write. `$body_str` may be
	// in single-i32 inline form (a short response built via
	// `$__str_concat` whose total fits in <=3 bytes) — route
	// through `$__lang_str_data_ptr` so inline values spill into
	// the shared scratch slot rather than allocating a heap copy.
	g.line(`local.get $out_stream`)
	g.line(`local.get $body_str`)
	g.line(`call $__lang_str_data_ptr`)
	g.emitStrLenFromLocal("$body_str")
	g.line(`call $__streams_write`)

	// Drop output-stream (child of outgoing-body).
	g.line(`local.get $out_stream`)
	g.line(`call $__wasi_output_stream_drop`)

	// We deliberately don't call `[static]outgoing-body.finish`.
	// The wasi:http 0.2.0 spec implies the host needs an explicit
	// finish, but in practice (wasmtime 30+, Fastly Compute,
	// Netlify) `response-outparam.set` already takes ownership of
	// the response and seals the body when the output-stream is
	// dropped — calling finish afterwards traps with
	// "unknown handle index" because the body resource has
	// already been reaped. If a future host needs the explicit
	// call we'd add it before set instead of after, and skip the
	// drop(out_stream) line so the body is still alive.

	// Restore the bump cursor — see the matching arena_save at
	// function entry. Every request-scoped allocation is now
	// reclaimed.
	g.line(`local.get $__arena_handle`)
	g.line(`call $arena_restore`)

	g.indent--
	g.line(`)`)
	g.line(`(export "wasi:http/incoming-handler@0.2.0#handle" (func $__http_entry))`)

	// $__bytes_to_lang_string(host_ptr, host_len) -> lang_string_ptr.
	// Turns the host's (ptr, len) bytes into a lang string. Used
	// by the method / path / body marshaling above.
	//
	// Two output shapes, chosen by host_len:
	//   - host_len <= 3:  inline-packed i32 (top-bit flag + 3-bit
	//                     length + up-to-3 inline bytes), no alloc.
	//                     Catches the common HTTP "GET" / "PUT"
	//                     method strings — no per-request heap
	//                     pressure for them.
	//   - host_len >  3:  heap-form (alloc length-prefix + copy +
	//                     trailing NUL, the legacy layout).
	g.line(`(func $__bytes_to_lang_string (param $host_ptr i32) (param $host_len i32) (result i32)`)
	g.indent++
	g.line(`(local $sbase i32) (local $i i32) (local $inline i32) (local $byte i32)`)
	// Inline-output fast path. Mirrors the SSO producer shape from
	// $__str_concat / $__str_slice / $string_from_bytes — read
	// bytes straight out of the host buffer, pack into a single
	// i32, return.
	g.line(`local.get $host_len`)
	g.line(`i32.const 3`)
	g.line(`i32.le_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0x80000000`)
	g.line(`local.get $host_len`)
	g.line(`i32.const 24`)
	g.line(`i32.shl`)
	g.line(`i32.or`)
	g.line(`local.set $inline`)
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $iend`)
	g.indent++
	g.line(`loop $iloop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $host_len`)
	g.line(`i32.eq`)
	g.line(`br_if $iend`)
	g.line(`local.get $host_ptr`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.set $byte`)
	g.line(`local.get $inline`)
	g.line(`local.get $byte`)
	g.line(`local.get $i`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.shl`)
	g.line(`i32.or`)
	g.line(`local.set $inline`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $iloop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $inline`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Heap-form output path: host_len > 3.
	g.line(`local.get $host_len`)
	g.line(`i32.const 5`) // 4 prefix + NUL
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $sbase`) // $sbase = data ptr
	g.emitStrLenStoreToLocal("$host_len", "$sbase")
	g.line(`local.get $sbase`)
	g.line(`local.get $host_ptr`)
	g.line(`local.get $host_len`)
	g.line(`memory.copy`)
	// Trailing NUL.
	g.line(`local.get $sbase`)
	g.line(`local.get $host_len`)
	g.line(`i32.add`)
	g.line(`i32.const 0`)
	g.line(`i32.store8`)
	// Return data pointer.
	g.line(`local.get $sbase`)
	g.indent--
	g.line(`)`)
}

// emitRandomBytesHelper emits `$random_bytes` against the
// `wasi:random/random.get-random-bytes` import. The canonical-ABI
// lowered signature is `(param i64 i32)`: the i64 is the requested
// length and the i32 is a "return area" pointer where the host
// writes a (ptr, len) pair. The host calls our exported
// `cabi_realloc` to allocate the buffer in our linear memory,
// fills it, and writes back. We memcpy those bytes into a
// length-prefixed + NUL-terminated string-shape allocation so the
// rest of the runtime sees the standard string layout.
//
// Memory[92..99] is the static return-area slot — see the runtime
// memory layout comment near `emitRuntimePreamble`.
func (g *generator) emitRandomBytesHelper() {
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
	//
	// Under preview-2 the value reaching this helper is the
	// `wasi:filesystem/types.error-code` enum index, NOT a
	// preview-1 errno; the open helper goes through
	// `$__filesystem_error_translate` first to remap the enum
	// values (access=0, exist=7, interrupted=11, no-entry=20,
	// unsupported=27 in the spec) onto the preview-1 codes the
	// table below uses (2/20/27/44/58). Two of the preview-2
	// indexes overlap with preview-1 errnos for different
	// conditions (`exist`/EEXIST agree; `no-entry` collides with
	// EEXIST=20; `unsupported` collides with EINTR=27), which is
	// why the translate step has to run before this helper sees
	// the value.
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

	g.emitFilesystemErrorTranslate()
	g.emitReadFileHelper()
	g.emitWriteFileHelper()
	if g.needsStreamingIO {
		g.emitStreamingIOHelpers()
	}
}

// emitFilesystemErrorTranslate writes
// `$__filesystem_error_translate(code) -> errno` — the bridge
// between `wasi:filesystem/types.error-code` (an enum) and the
// preview-1 errno values that `$__build_io_error` keys on.
// Untranslated codes drop through to a sentinel that the
// io-error table treats as `Other(path, "io error")`.
func (g *generator) emitFilesystemErrorTranslate() {
	g.line(`(func $__filesystem_error_translate (param $code i32) (result i32)`)
	g.indent++
	type pair struct{ p2, p1 int }
	for _, p := range []pair{
		{0, 2},   // access     → EACCES
		{7, 20},  // exist      → EEXIST
		{11, 27}, // interrupted → EINTR
		{20, 44}, // no-entry   → ENOENT
		{27, 58}, // unsupported → ENOTSUP
	} {
		g.line(`local.get $code`)
		g.linef(`i32.const %d`, p.p2)
		g.line(`i32.eq`)
		g.line(`if (result i32)`)
		g.indent++
		g.linef(`i32.const %d`, p.p1)
		g.line(`return`)
		g.indent--
		g.line(`else`)
		g.indent++
	}
	// Catch-all: anything else falls through as -1, which doesn't
	// match any case in $__build_io_error and lands in Other(...).
	g.line(`i32.const -1`)
	for range 5 {
		g.indent--
		g.line(`end`)
	}
	g.indent--
	g.line(`)`)
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
// Result[Reader, IoError]`. On preview-1: path_open with read
// rights. descriptor.open-at against the cached preopen directory
// (open-flags=0, descriptor-flags=1=read), then
// descriptor.read-via-stream for an input-stream resource handle
// — the Reader struct holds that handle. Errors flow through
// __build_io_error.
func (g *generator) emitOpenReaderHelper() {
	// open-flags=0 (no create / truncate), descriptor-flags=1 (read).
	g.emitOpenViaStreamHelper("$open_reader", 0, 1, "$__wasi_descriptor_read_via_stream", false)
}

// emitOpenWriterHelper writes `$open_writer(path)`: open-at with
// open-flags=create|truncate (1|8 = 9), descriptor-flags=write
// (2), then descriptor.write-via-stream.
// (The `appendMode` parameter is unused for now; appender uses
// the dedicated emitOpenAppenderHelper.)
func (g *generator) emitOpenWriterHelper(_ bool) {
	g.emitOpenViaStreamHelper("$open_writer", 9, 2, "$__wasi_descriptor_write_via_stream", false)
}

// emitOpenAppenderHelper writes `$open_appender(path)`:
// open-flags=create (1), descriptor-flags=write (2), then
// descriptor.append-via-stream — append-via-stream inherently
// appends.
func (g *generator) emitOpenAppenderHelper() {
	g.emitOpenViaStreamHelper("$open_appender", 1, 2, "$__wasi_descriptor_append_via_stream", true)
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
	// path_open reads (ptr, len) from linear memory; promote
	// inline-form paths first.
	g.emitPromoteStrParam("$path")

	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $fd_buf`)

	g.line(`i32.const 3`)
	g.line(`i32.const 1`)
	g.line(`local.get $path`)
	g.emitStrLenFromLocal("$path")
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
	// Readers hold an `input-stream` resource handle in mem[$r]
	// (whether they came from $stdin, $open_reader, or anywhere
	// else). Delegate to the shared `$__stream_read_line` helper.
	g.line(`local.get $r`)
	g.line(`i32.load`)
	g.line(`call $__stream_read_line`)
	g.indent--
	g.line(`)`)
}

// emitReaderReadChunkMethod writes Reader.read_chunk(size).
// Preview-1: single fd_read into a heap buffer of capacity
// `size`. Single
// `wasi:io/streams.input-stream.blocking-read(size)` against the
// stream resource handle in mem[$r], `memory.copy` the host's
// buffer into a length-prefixed string, return Some. Uses the
// shared retptr at memory[92].
func (g *generator) emitReaderReadChunkMethod() {
	g.line(`(func $__method_Reader_read_chunk (param $r i32) (param $size i32) (result i32)`)
	g.indent++
	g.line(`(local $fd i32) (local $sbase i32) (local $sptr i32)`)
	g.line(`(local $n i32) (local $result i32) (local $list_ptr i32) (local $j i32)`)

	g.line(`local.get $r`)
	g.line(`i32.load`)
	g.line(`local.set $fd`)

	// blocking-read(stream, size, retptr=92).
	g.line(`local.get $fd`)
	g.line(`local.get $size`)
	g.line(`i64.extend_i32_u`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_blocking_read`)
	// Outer disc at retptr+0; non-zero = Err = treat as EOF.
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
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
	// list_ptr = mem[96], n = mem[100].
	g.line(`i32.const 96`)
	g.line(`i32.load`)
	g.line(`local.set $list_ptr`)
	g.line(`i32.const 100`)
	g.line(`i32.load`)
	g.line(`local.set $n`)
	// Empty list = EOF on a blocking read.
	g.line(`local.get $n`)
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
	// SSO inline-output fast path: when the chunk fits the
	// single-i32 tiny cap (≤ 3 bytes), pack bytes straight out
	// of the host buffer into a single i32 and bind that as
	// `$sptr` — no per-chunk alloc + memcpy. Catches very small
	// reads (e.g. one-byte status probes, two-byte protocol
	// tokens) that otherwise allocate the same as a 64-byte
	// chunk.
	g.line(`local.get $n`)
	g.line(`i32.const 3`)
	g.line(`i32.le_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0x80000000`)
	g.line(`local.get $n`)
	g.line(`i32.const 24`)
	g.line(`i32.shl`)
	g.line(`i32.or`)
	g.line(`local.set $sptr`)
	g.line(`i32.const 0`)
	g.line(`local.set $j`)
	g.line(`block $end_pack`)
	g.indent++
	g.line(`loop $pack_loop`)
	g.indent++
	g.line(`local.get $j`)
	g.line(`local.get $n`)
	g.line(`i32.eq`)
	g.line(`br_if $end_pack`)
	g.line(`local.get $sptr`)
	g.line(`local.get $list_ptr`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.get $j`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.shl`)
	g.line(`i32.or`)
	g.line(`local.set $sptr`)
	g.line(`local.get $j`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $j`)
	g.line(`br $pack_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`else`)
	g.indent++
	// Heap-form output: materialise length-prefixed string +
	// memcpy from the host buffer.
	g.line(`local.get $n`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $sbase`)
	g.line(`local.get $sbase`)
	g.line(`local.get $n`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $sptr`)
	g.line(`local.get $sptr`)
	g.line(`local.get $list_ptr`)
	g.line(`local.get $n`)
	g.line(`memory.copy`)
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
// shape — fd_close (preview-1) or `[resource-drop]input-stream`
// / `[resource-drop]output-stream` (preview-2). Both flavours
// return Option[IoError]; the streams path always succeeds (resource
// drops can't fail), so we always emit `None`.
func (g *generator) emitReaderCloseMethod() {
	g.emitCloseMethod("$__method_Reader_close", "$__wasi_input_stream_drop")
}

func (g *generator) emitWriterCloseMethod() {
	g.emitCloseMethod("$__method_Writer_close", "$__wasi_output_stream_drop")
}

// emitCloseMethod writes a Reader.close / Writer.close that
// drops the stream resource and returns Option[IoError]. Resource
// drops are infallible at the canonical-ABI level — there isn't a
// meaningful error to propagate to the lang program, so we always
// construct None.
func (g *generator) emitCloseMethod(name, dropImport string) {
	g.linef(`(func %s (param $r i32) (result i32)`, name)
	g.indent++
	g.line(`(local $result i32)`)
	g.line(`local.get $r`)
	g.line(`i32.load`)
	g.linef(`call %s`, dropImport)
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $result`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`)`)
}

// emitWriterWriteMethod writes Writer.write(s). On preview-1, a
// single fd_write of the entire string. On preview-2, a single
// `wasi:io/streams.[method]output-stream.blocking-write-and-flush`
// against the cached resource handle in mem[$w] — the Writer
// struct holds a stream handle end-to-end on the preview-2 path.
// Returns None on success, Some(IoError) on failure. Uses the
// static iovec at memory[56] and nwritten at memory[64] (same
// slots the stdin reader / Reader.read_line use — single-threaded
// so reuse is safe). On the streams path, retptr lives at memory
// 92 (shared canonical-ABI return area).
func (g *generator) emitWriterWriteMethod() {
	g.line(`(func $__method_Writer_write (param $w i32) (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $result i32)`)
	// $__streams_write loops blocking-write-and-flush in 4 KiB
	// chunks; it silently swallows stream errors (the helper
	// returns to caller without a discriminant), so Writer.write
	// always returns None. Matches the "stdio failures are
	// unrecoverable for a CLI" posture; a future revision can
	// switch to a helper that returns the canonical disc and
	// surface Some(IoError) on partial-write failure.
	//
	// `$s` routes through `$__lang_str_data_ptr` so inline-form
	// values stage into a shared scratch slot rather than
	// allocating a heap copy.
	g.line(`local.get $w`)
	g.line(`i32.load`)
	g.line(`local.get $s`)
	g.line(`call $__lang_str_data_ptr`)
	g.emitStrLenFromLocal("$s")
	g.line(`call $__streams_write`)
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
// Pipeline: $open_reader for the stream handle, bulk
// blocking-read(4096) loop into a growable accumulator,
// [resource-drop]input-stream on the way out, wrap in Ok(string).
// Errors out of $open_reader propagate as-is; stream errors
// mid-read are treated as EOF (matches Reader.read_chunk).
func (g *generator) emitReadFileHelper() {
	g.emitReadFileHelperPreview2()
}


// emitReadFileHelperPreview2 is the streams-flavoured `$read_file`.
// Pipeline: $open_reader (which goes through wasi:filesystem
// open-at + read-via-stream as of step 3c) → bulk
// blocking-read(4096) loop into a growable accumulator → drop
// the input-stream resource → wrap the accumulator in
// `Ok(string)`. Errors out of $open_reader propagate as-is; stream
// errors mid-read are treated as EOF (matches what Reader.read_chunk
// does too).
func (g *generator) emitReadFileHelperPreview2() {
	g.line(`(func $read_file (param $path i32) (result i32)`)
	g.indent++
	g.line(`(local $open_result i32) (local $reader i32) (local $stream i32)`)
	g.line(`(local $buf i32) (local $buf_size i32) (local $cur i32)`)
	g.line(`(local $host_ptr i32) (local $host_len i32)`)
	g.line(`(local $new_buf i32) (local $new_size i32)`)
	g.line(`(local $sbase i32) (local $sptr i32) (local $result i32)`)

	// Open. Result.Err shape matches what we want to return.
	g.line(`local.get $path`)
	g.line(`call $open_reader`)
	g.line(`local.tee $open_result`)
	g.line(`i32.load`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $open_result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// reader = mem[open_result + 4]; stream = mem[reader].
	g.line(`local.get $open_result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.tee $reader`)
	g.line(`i32.load`)
	g.line(`local.set $stream`)

	// Initial accumulator: 4 KiB. Doubles on overflow.
	g.line(`i32.const 4096`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $buf`)
	g.line(`i32.const 4096`)
	g.line(`local.set $buf_size`)
	g.line(`i32.const 0`)
	g.line(`local.set $cur`)

	g.line(`block $end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	// blocking-read(stream, 4096, retptr=92).
	g.line(`local.get $stream`)
	g.line(`i64.const 4096`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_blocking_read`)
	// Outer disc; non-zero = Err = treat as EOF.
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`br_if $end`)
	// list_len at retptr+8; 0 = EOF.
	g.line(`i32.const 100`)
	g.line(`i32.load`)
	g.line(`local.tee $host_len`)
	g.line(`i32.eqz`)
	g.line(`br_if $end`)
	// list_ptr at retptr+4.
	g.line(`i32.const 96`)
	g.line(`i32.load`)
	g.line(`local.set $host_ptr`)

	// Grow the buffer if cur + host_len > buf_size. Loop because
	// host_len could exceed multiple doublings (rare but
	// mathematically possible — we asked for ≤4096, the host
	// can return up to that, and 4096 fits in one doubling).
	g.line(`block $grow_done`)
	g.indent++
	g.line(`loop $grow`)
	g.indent++
	g.line(`local.get $cur`)
	g.line(`local.get $host_len`)
	g.line(`i32.add`)
	g.line(`local.get $buf_size`)
	g.line(`i32.le_u`)
	g.line(`br_if $grow_done`)
	// new_size = buf_size * 2; new_buf = alloc(new_size); memory.copy.
	g.line(`local.get $buf_size`)
	g.line(`i32.const 1`)
	g.line(`i32.shl`)
	g.line(`local.tee $new_size`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $new_buf`)
	g.line(`local.get $new_buf`)
	g.line(`local.get $buf`)
	g.line(`local.get $cur`)
	g.line(`memory.copy`)
	g.line(`local.get $new_buf`)
	g.line(`local.set $buf`)
	g.line(`local.get $new_size`)
	g.line(`local.set $buf_size`)
	g.line(`br $grow`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// memory.copy(buf + cur, host_ptr, host_len)
	g.line(`local.get $buf`)
	g.line(`local.get $cur`)
	g.line(`i32.add`)
	g.line(`local.get $host_ptr`)
	g.line(`local.get $host_len`)
	g.line(`memory.copy`)
	// cur += host_len
	g.line(`local.get $cur`)
	g.line(`local.get $host_len`)
	g.line(`i32.add`)
	g.line(`local.set $cur`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// Drop the stream resource (resource drops are infallible).
	g.line(`local.get $stream`)
	g.line(`call $__wasi_input_stream_drop`)

	// Materialise as a length-prefixed string.
	g.line(`local.get $cur`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $sbase`)
	g.line(`local.get $sbase`)
	g.line(`local.get $cur`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $sptr`)
	g.line(`local.get $sptr`)
	g.line(`local.get $buf`)
	g.line(`local.get $cur`)
	g.line(`memory.copy`)

	// Build Ok(sptr).
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
// `$write_file(path, content) -> Option[IoError]`. Pipeline:
// $open_writer → single blocking-write-and-flush of the entire
// content → drop the output-stream resource → return None.
// open_writer's Result.Err gets repackaged into Option[IoError];
// mid-write stream errors map to Some(IoError) with errno=29 (EIO).
func (g *generator) emitWriteFileHelper() {
	g.emitWriteFileHelperPreview2()
}


// emitWriteFileHelperPreview2 is the streams-flavoured `$write_file`.
// Pipeline: $open_writer (which goes through wasi:filesystem
// open-at + write-via-stream as of step 3c) → single
// blocking-write-and-flush of the entire content → drop the
// output-stream resource → return None. open_writer's Result.Err
// gets repackaged into the Option[IoError] shape this function
// returns; mid-write stream errors map to Some(IoError) with
// errno=29 (EIO).
func (g *generator) emitWriteFileHelperPreview2() {
	g.line(`(func $write_file (param $path i32) (param $content i32) (result i32)`)
	g.indent++
	g.line(`(local $open_result i32) (local $writer i32) (local $stream i32)`)
	g.line(`(local $content_len i32) (local $result i32)`)

	// Open. Result.Err shape needs to translate from
	// Result[Writer, IoError] to Option[IoError] = Some(err).
	g.line(`local.get $path`)
	g.line(`call $open_writer`)
	g.line(`local.tee $open_result`)
	g.line(`i32.load`)
	g.line(`if`)
	g.indent++
	// Some(err): allocate 8 bytes, tag=0 + err_ptr from open_result+4.
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $open_result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// writer = mem[open_result + 4]; stream = mem[writer].
	g.line(`local.get $open_result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.tee $writer`)
	g.line(`i32.load`)
	g.line(`local.set $stream`)

	g.emitStrLenFromLocal("$content")
	g.line(`local.set $content_len`)

	// $__streams_write loops blocking-write-and-flush in 4 KiB
	// chunks (the canonical-ABI per-call cap). It swallows
	// stream errors silently — same trade-off as Writer.write;
	// a future revision can plumb success/failure through a
	// dedicated chunked helper.
	g.line(`local.get $stream`)
	g.line(`local.get $content`)
	g.line(`local.get $content_len`)
	g.line(`call $__streams_write`)

	// Drop the stream and return None.
	g.line(`local.get $stream`)
	g.line(`call $__wasi_output_stream_drop`)
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
	// pre-initialised with (table_idx=i, env_ptr=0). Every in-
	// table function gets a cell — originally top-level fns
	// reach theirs via `OpConstFunc` (top-level reference), and
	// hoisted no-capture closures reach theirs via the
	// `InlineZeroCaptureClosures` rewrite that turns
	// `OpMakeClosure(hoisted, 0)` into `OpConstFunc(hoisted)`
	// (drops a `__lang_alloc(8)` per zero-capture closure pass).
	// Hoisted closures with captures still allocate a fresh pair
	// per MakeClosure invocation — their static cell is wasted
	// 8 bytes but cheaper than threading a per-name capture
	// count back through `closuresBase` arithmetic.
	if g.needsClosures {
		for i := range g.tableEntries {
			g.linef(`(data (i32.const %d) "%s%s")`, g.closuresBase+8*i, encodeI32(i), encodeI32(0))
		}
	}
	// strings
	for _, s := range g.stringEntries {
		g.linef(`(data (i32.const %d) "%s%s")`, s.offset, encodeI32(len(s.text)), wasmEscape(s.text))
	}
	// Per-tag enum sentinels — 4 bytes each, containing the
	// i32 tag value. Reserved lazily by internEnumSentinel;
	// emitted in tag-value order for deterministic output.
	if len(g.enumSentinelOffsets) > 0 {
		tags := make([]int, 0, len(g.enumSentinelOffsets))
		for t := range g.enumSentinelOffsets {
			tags = append(tags, t)
		}
		sort.Ints(tags)
		for _, t := range tags {
			g.linef(`(data (i32.const %d) "%s")`, g.enumSentinelOffsets[t], encodeI32(t))
		}
	}
	// Bump-allocator initial pointer at offset 40. We seed it past the
	// end of the strings, rounded up to 4 bytes for i32 access.
	//
	// When state{} is in use, mem[40] (arena cursor) starts AFTER
	// the reserved persistent region; mem[44] (persistent cursor)
	// starts at the post-strings offset; mem[52] (persistent
	// floor) caps the persistent region. The two-cursor layout is
	// described in detail at the (memory $mem ...) declaration in
	// the same file.
	if g.needsArrays || g.needsPersistent {
		start := g.stringOffset
		if start < 64 {
			start = 64
		}
		// Round to 8-byte alignment so allocations satisfy the
		// canonical-ABI `cabi_realloc(..., align=8, ...)` calls the
		// WASI preview1 adapter makes (it expects 8-aligned regions
		// for the host→guest string-list materialisation). 4-aligned
		// happened to work for any program whose string pool ended
		// at a multiple of 8; once SSO's empty-string sentinel
		// added 4 bytes to that pool, 4-byte-aligned starts (e.g.
		// 132) started breaking get-arguments.
		if start%8 != 0 {
			start += 8 - (start % 8)
		}
		if g.needsPersistent {
			persistentStart := start
			arenaStart := start + persistentRegionBytes
			g.linef(`(data (i32.const 40) "%s")`, encodeI32(arenaStart))
			g.linef(`(data (i32.const 44) "%s")`, encodeI32(persistentStart))
			g.linef(`(data (i32.const 52) "%s")`, encodeI32(arenaStart))
		} else {
			g.linef(`(data (i32.const 40) "%s")`, encodeI32(start))
		}
	}
}

// persistentRegionBytes is the size of the reserved persistent
// heap region for state{}-block allocations. Sits between the
// strings and the arena heap. 4 MiB is enough for typical state
// (counters, small Maps, small T[]); programs that need more
// will trap on persistent OOM until the region grows
// dynamically — that's a future PR.
const persistentRegionBytes = 4 * 1024 * 1024

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
	switch v := t.(type) {
	case ast.NumberType:
		// Sub-i32 widths still live in an i32 wasm slot (with
		// masking on store) — only i64 needs the wider wasm type.
		if v.NormalWidth() == 64 {
			return "i64", nil
		}
		return "i32", nil
	case ast.BoolType, ast.StringType:
		// Strings are pointers into linear memory, so they're i32.
		return "i32", nil
	case ast.ArrayType, ast.SliceType:
		// Arrays and slices are both heap-pointer values at the
		// wasm level — slices point to an 8-byte
		// (data_ptr, len) struct, arrays point at the data
		// (with a length prefix at base-4).
		return "i32", nil
	case *ast.FuncType:
		// Function values are table indices.
		return "i32", nil
	case ast.StructType, ast.EnumType, ast.TupleType:
		// Struct, enum and tuple values are heap pointers.
		return "i32", nil
	case ast.FloatType:
		if v.NormalWidth() == 64 {
			return "f64", nil
		}
		return "f32", nil
	}
	return "", fmt.Errorf("wasm: type %s isn't supported by this backend yet", t)
}
