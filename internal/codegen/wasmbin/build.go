// Build is the high-level entry point that mirrors what the WAT
// path's EmitWithOptions does at the IR level: tree-shake, lower
// to IR, run the standard IR optimisation pipeline, then call
// Emit to produce module bytes.
//
// Callers that already have an ir.Program (synthetic tests, or
// future callers that want to plug in their own pipeline) should
// use Emit directly. Build is the convenience entry the CLI uses
// when handed a checked AST.
//
// Today the binary backend doesn't yet cover every op the IR
// pipeline can produce (the runtime allocator, closure pair-cell
// initialisation, runtime string helpers, and the preview-2
// component wrapping are all not yet wired). Build will surface
// those gaps as "unsupported op X in function Y" — which is the
// signal a follow-up slice is needed. As coverage grows, more
// real programs compile through this path.
package wasmbin

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/treeshake"
)

// BuildOptions tweaks the module-level structure produced by
// Build. Defaults match what the raw `fern -target wasm32-wasi -emit core-module`
// CLI flow emits.
type BuildOptions struct {
	// ForceMemorySection makes Emit unconditionally include a
	// linear memory + export it under the canonical name
	// "memory". Needed when the resulting bytes will be wrapped
	// in a preview-2 component — the WASI preview-1 adapter
	// imports `env::memory` and refuses to compose if no memory
	// is present.
	ForceMemorySection bool
	// SynthStart emits a `_start` wrapper that calls `main` and
	// drops any return value, then exports it. The preview-1
	// adapter's `wasi:cli/run.run` glue dispatches to `_start`
	// as the command entry point.
	SynthStart bool
	// PrintMainResult routes main's i32 return through
	// `int_to_string` + `__fern_print` inside the synthesised
	// `_start` wrapper instead of dropping it. Used by e2e
	// tests that observe main's value over the component's
	// stdout — preview-2 hosts only surface 0/1 through
	// `wasi:cli/exit`, so the printed decimal is the channel
	// left for arbitrary-width result checks. Implies
	// SynthStart and pins `int_to_string` (or the
	// `int__int_to_string` modload-qualified variant) past
	// every dead-function-elimination step so the wrapper's
	// call resolves. No-op when main returns void; non-i32
	// returns fall back to the plain SynthStart drop path.
	PrintMainResult bool
	// HttpHandler emits the wasi:http/incoming-handler@0.2.0
	// component-model export wrapping the user-defined
	// `function handle(req: HttpRequest, plat: Platform):
	// HttpResponse`. The synthetic `__http_entry` helper does
	// the canonical-ABI marshalling. Pins `handle` +
	// `__method_HeaderMap_append` past every dead-function-
	// elimination step so the wrapper's calls resolve.
	HttpHandler bool
	// SynthCliRun emits a synthetic `_lang_run() -> i32` wrapper
	// that normalises main's signature (void → i32.const 0; i32
	// → pass-through). Used by `-component-wrap-cli` so the
	// canon-lifted wasi:cli/run::run sees a `() -> i32` core
	// export even when the user's main returns void. Forwarded
	// to EmitOptions.SynthCliRun.
	SynthCliRun bool
	// Preview2WASI emits preview-2-named WASI imports in place
	// of preview-1. Currently scoped to proc_exit
	// (wasi_snapshot_preview1.proc_exit → wasi:cli/exit@0.2.0.exit);
	// other imports stay on preview-1 until their own migrations
	// land. Forwarded to EmitOptions.Preview2WASI.
	Preview2WASI bool
	// AsyncExportName, when non-empty, emits a WASI Preview-3
	// component-model-async export: an `("", "task-return")` import is
	// added and a synthetic core function under this name calls `main`,
	// passes its i32 result to task-return, and returns void (the
	// async ABI: result via task.return, function-return = task done).
	// The composer lifts it with the `async` canonical option
	// (component.BuildAsyncLiftedExportComponent). `main` must return
	// i32. Forwarded to EmitOptions.AsyncExportName.
	AsyncExportName string
	// AsyncSourceFunc names the Fern function the async wrapper calls
	// (its i32 result is handed to task.return). Empty defaults to
	// "main" — the `-async-export` flag shape; `async function foo`
	// sets it to "foo". Forwarded to EmitOptions.AsyncSourceFunc.
	AsyncSourceFunc string
	// CliRunResult tells the SynthCliRun wrapper that its i32
	// return will be canon-lifted as a `wasi:cli/run` `result<_, _>`
	// (0 = ok, non-zero = err) rather than surfaced raw. Only 0 and
	// 1 are valid result discriminants, so the wrapper normalises
	// main's value to 0/1; without this, a main returning >= 2 traps
	// the host with "invalid expected discriminant". Left off for the
	// `-component-wrap` u32-export shape, where the raw value is the
	// legitimate result (`--invoke main()` → 42). Forwarded to
	// EmitOptions.CliRunResult.
	CliRunResult bool
}

// Build is BuildWithOptions with the default (zero-value) options.
func Build(prog *ast.Program, info *checker.Info) ([]byte, error) {
	return BuildWithOptions(prog, info, BuildOptions{})
}

// BuildWithOptions is the option-aware sibling of Build.
func BuildWithOptions(prog *ast.Program, info *checker.Info, opts BuildOptions) ([]byte, error) {
	// PrintMainResult's _start wrapper calls int_to_string from
	// a synthesised position that isn't an AST reference, so
	// pin it (and its modload-qualified twin) past tree-shake.
	// Either variant covers the flat-load case vs the
	// explicit-`import "core/int"` case; the emitter picks
	// whichever survives.
	var treeshakeExtras []string
	if opts.PrintMainResult {
		treeshakeExtras = []string{"int_to_string", "int__int_to_string"}
	}
	// P6: an `@export` function is a world-export root — keep it (and don't let
	// it be inlined away) even when no Fern code calls it, so the composer can
	// lift the surfaced core export. Collected from the AST here because
	// prog.Exports is only populated later, inside ir.LowerWith.
	var exportRoots []string
	for _, fn := range prog.Funcs {
		if fn.ExportIface != "" {
			exportRoots = append(exportRoots, fn.Name)
		}
	}
	treeshakeExtras = append(treeshakeExtras, exportRoots...)
	// A lazily-iterated stream import (`for x in body()`) is reached only through
	// its synthesised `body$open` companion (the checker desugar), never a bare
	// `body()` call the tree-shaker can see — so the extern decl would be culled
	// before scanExternImports could register the `$open` / per-element helpers.
	// Pin every async stream import so the extern survives; an unused one costs
	// nothing (scanExternImports only emits an import that is actually called).
	// See docs/STREAM-TYPE-SURFACE.md (L2).
	for _, fn := range prog.Funcs {
		if fn.ImportIface != "" && fn.Async && fn.StreamResultElem != nil {
			treeshakeExtras = append(treeshakeExtras, fn.Name)
		}
	}
	// `dyn Trait` impl methods are reached only through the runtime
	// vtable (OpConstVtable names them by string), never via a static
	// call the AST walker / IR reachability can see. Pin every impl
	// method of a concrete type that coerces to a `dyn Trait` so it
	// survives tree-shake and IR dead-function elimination. See
	// docs/DYN-TRAITS.md §4.2.1.
	dynImplMethods := treeshake.DynCoercionImplMethods(info)
	treeshakeExtras = append(treeshakeExtras, dynImplMethods...)
	// Same rooting for `e as? T` downcast targets: the (Trait,T) vtable
	// the compare references holds those impl methods, and a downcast-only
	// target (never coerced) is absent from DynCoercions
	// (docs/DYN-TRAITS.md §9). Kept in its own slice so it can also be fed
	// to the IR-level dead-function elimination below (LiveFunctions),
	// which culls separately from the AST tree-shaker — without that, a
	// downcast-only target's __method_* would survive tree-shake but be
	// dropped at the IR layer and the vtable cell would reference a
	// missing func (OpConstVtable: impl method not in prog.Funcs).
	downcastImplMethods := treeshake.DowncastImplMethods(prog, info)
	treeshakeExtras = append(treeshakeExtras, downcastImplMethods...)
	if opts.AsyncExportName != "" {
		// The async export's source function is called only by the
		// synthetic async wrapper (emit-time wasm bytes, invisible to the
		// AST/IR reachability walkers), so pin it past tree-shake — same
		// shape as `handle` for HttpHandler. An `async function foo` is
		// otherwise unreferenced and would be culled before funcIdx.
		src := opts.AsyncSourceFunc
		if src == "" {
			src = "main"
		}
		treeshakeExtras = append(treeshakeExtras, src)
	}
	if opts.HttpHandler {
		// `handle` is called by the wrapper but the treeshake
		// walker doesn't see the call (the wrapper lives in
		// emit-time wasm bytes, not the AST). Same shape for
		// `__method_HeaderMap_append` — the wrapper calls it
		// per header entry from the canonical-ABI fields list.
		treeshakeExtras = append(treeshakeExtras, "handle", "__method_HeaderMap_append")
		// The auto-synthesised `main()` (synthesised by the checker)
		// calls `tcp_serve` and pulls in wasi:sockets imports
		// the http world's WIT doesn't have. Drop it before
		// tree-shake so it doesn't hold tcp_serve / tcp_listen
		// alive on this target. This is the
		// `IsSynthesisedHandlerMain` pre-shake.
		out := prog.Funcs[:0]
		for _, fn := range prog.Funcs {
			if fn.IsSynthesisedHandlerMain {
				continue
			}
			out = append(out, fn)
		}
		prog.Funcs = out
	}
	treeshake.Run(prog, treeshakeExtras...)
	ip, err := ir.LowerWith(prog, info, 4)
	if err != nil {
		return nil, fmt.Errorf("wasmbin: lower: %w", err)
	}
	// The IR optimisation pipeline. MOSTLY shared with the native backends —
	// TailCallOptimize, Defunctionalise, ElideClosurePair,
	// InlineZeroCaptureClosures, FuseTee, FlattenBranches, OptimizeCleanup and
	// EliminateDeadCode all run there too, so a change to one of those lights
	// up everywhere. The exceptions are `ir.Inline` (twice, around
	// Defunctionalise) and the IR-level dead-function cull below, which are
	// wasm-only: both add or remove whole functions, and the natives still walk
	// their AST and IR functions by PARALLEL INDEX (`ip.Funcs[i]`), which that
	// invalidates. #4377 slice 2 is the name-keyed walk that would let them run
	// there as well.
	//
	// This comment used to claim the whole list was "the same passes every
	// backend runs" while the natives ran almost none of it, which is how the
	// gap #4377 was filed for stayed invisible. If you add a pass here, either
	// wire it into internal/codegen/{x86_64,arm64} too or say here why it
	// cannot be. See each pass's doc comment in internal/ir for the ordering
	// rationale.
	ir.TailCallOptimize(ip)
	ir.Inline(ip)
	// Wasm closure pair: 8 bytes total, env_ptr at offset 4.
	ir.Defunctionalise(ip, 4)
	ir.ElideClosurePair(ip, 4)
	ir.InlineZeroCaptureClosures(ip)
	ir.Inline(ip)
	ir.FuseTee(ip)
	ir.FlattenBranches(ip)
	ir.OptimizeCleanup(ip)
	ir.EliminateDeadCode(ip)
	// IR-level dead-function elimination: drop top-level
	// functions whose body the optimiser left without any
	// remaining callers. Critical for the binary path since
	// the stdlib a program imports drags in ~250 helpers most
	// of which never get called from user code.
	//
	// Pass CallDirectAliases so the reachability walker knows
	// about emit-time rewrites — without this, a user-code
	// `map_new` call wouldn't keep `map_new_impl` alive, and
	// the call site would resolve to a culled function.
	var liveExtras []string
	if opts.PrintMainResult {
		liveExtras = []string{"int_to_string", "int__int_to_string"}
	}
	if opts.HttpHandler {
		liveExtras = append(liveExtras, "handle", "__method_HeaderMap_append")
	}
	if opts.AsyncExportName != "" {
		// Root the async export's source function for the IR-level cull
		// too (it culls separately from the AST tree-shaker); the
		// synthetic async wrapper that calls it is emit-time only.
		src := opts.AsyncSourceFunc
		if src == "" {
			src = "main"
		}
		liveExtras = append(liveExtras, src)
	}
	liveExtras = append(liveExtras, exportRoots...)
	liveExtras = append(liveExtras, dynImplMethods...)
	liveExtras = append(liveExtras, downcastImplMethods...)
	// `dyn Trait` RC drop fns (docs/DYN-TRAITS.md §4.4) are reached only
	// through the vtable's trailing drop slot (an indirect call_indirect
	// the IR reachability walker can't follow), so root them explicitly so
	// they survive IR dead-function elimination. The vtable cell embeds
	// each Drop fn by table index; a culled drop fn would make
	// OpConstVtable reference a missing func. The per-set __drop_dyn_<set>
	// helpers are called by name (OpCallDirect) from the exit sweep, but
	// rooting them too is harmless and keeps a no-coercion-but-typed-dyn
	// program linking.
	for _, vt := range ip.Vtables {
		if vt.Drop != "" {
			liveExtras = append(liveExtras, vt.Drop)
		}
	}
	for _, fn := range ip.Funcs {
		if strings.HasPrefix(fn.Name, "__drop_dyn_") {
			liveExtras = append(liveExtras, fn.Name)
		}
	}
	if live := ir.LiveFunctionsWithAliases(ip, CallDirectAliases, liveExtras...); live != nil {
		out := ip.Funcs[:0]
		for _, irFn := range ip.Funcs {
			if live[irFn.Name] {
				out = append(out, irFn)
			}
		}
		ip.Funcs = out
	}
	return EmitWithOptions(ip, EmitOptions{
		ForceMemorySection: opts.ForceMemorySection,
		SynthStart:         opts.SynthStart,
		PrintMainResult:    opts.PrintMainResult,
		HttpHandler:        opts.HttpHandler,
		Preview2WASI:       opts.Preview2WASI,
		SynthCliRun:        opts.SynthCliRun,
		CliRunResult:       opts.CliRunResult,
		AsyncExportName:    opts.AsyncExportName,
		AsyncSourceFunc:    opts.AsyncSourceFunc,
	})
}
