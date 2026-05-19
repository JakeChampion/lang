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
// Build. Defaults match what the raw `lang -target wasm-bin`
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
}

// Build is BuildWithOptions with the default (zero-value) options.
func Build(prog *ast.Program, info *checker.Info) ([]byte, error) {
	return BuildWithOptions(prog, info, BuildOptions{})
}

// BuildWithOptions is the option-aware sibling of Build.
func BuildWithOptions(prog *ast.Program, info *checker.Info, opts BuildOptions) ([]byte, error) {
	treeshake.Run(prog)
	ip, err := ir.LowerWith(prog, info, 4)
	if err != nil {
		return nil, fmt.Errorf("wasmbin: lower: %w", err)
	}
	// Same IR optimisation pipeline the WAT path runs — see
	// internal/codegen/wasm/wasm.go EmitWithOptions for the
	// rationale on each step. Both paths share the pipeline so
	// changes light up on both backends at once.
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
	if live := ir.LiveFunctions(ip); live != nil {
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
	})
}
