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

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/treeshake"
)

// BuildOptions tweaks the module-level structure produced by
// Build. Defaults match what the raw `fern -target wasm-bin`
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
	// Either variant covers the auto-prelude case vs the
	// explicit-`import "core/int"` case; the emitter picks
	// whichever survives.
	var treeshakeExtras []string
	if opts.PrintMainResult {
		treeshakeExtras = []string{"int_to_string", "int__int_to_string"}
	}
	if opts.HttpHandler {
		// `handle` is called by the wrapper but the treeshake
		// walker doesn't see the call (the wrapper lives in
		// emit-time wasm bytes, not the AST). Same shape for
		// `__method_HeaderMap_append` — the wrapper calls it
		// per header entry from the canonical-ABI fields list.
		treeshakeExtras = append(treeshakeExtras, "handle", "__method_HeaderMap_append")
		// The auto-synthesised `main()` (in std/tcp's prelude)
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
	// The shared IR optimisation pipeline — the same passes every
	// backend runs, so a change here lights up on all of them at
	// once. See each pass's doc comment in internal/ir for the
	// rationale on the ordering.
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
	// the auto-injected stdlib drags in ~250 helpers most of
	// which never get called from user code.
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
	})
}
