# Self-hosted typed ownership cutover

The destination is the Fern-written compiler. Retiring the Go bootstrap compiler
is a primary project goal, distinct from retaining native-code generation.
Go-side work must serve a concrete bootstrap, migration or validation need, not
become a second permanent semantic pipeline.

## Verified production boundary

`examples/self_host/ircore.fern:lower_gated` lowers and caches each function through
`irlower.lower_func`. The resulting `ir.Op[]` already contains reference-count
calls, releases and reuse decisions. The three stack-IR backends consume it.
Consequently, downstream SSA lifting or `irverifyrc` alone cannot replace the
AST ownership authority: that would analyze decisions already made by the AST
lowerer, not supply their semantic justification.

The necessary boundary is checked semantic values and control flow, followed by
ownership analysis and explicit RC/reuse lowering, followed by the existing
physical IR and backends. Parsing, resolution and initial checking stay in the
frontend. Reuse the existing SSA dataflow infrastructure where it is sound; do
not restore a second independent AST-to-SSA frontend.

## First integrated prerequisite

`typeinfo.fern` now owns the production checker's existing recursive `Type` union.
The checker and resolver drivers consume that exact shared definition. The module
has no AST, parser, checker or physical-IR dependency, allowing future checked
syntax and semantic IR to hold its values without introducing an import cycle.
This uses the boundary designed in the existing, unfinished self-hosted typed-match
work, rather than adding a parallel type vocabulary.

This extraction preserves all 13 members, their order and fields. It deliberately
does not change type inference or diagnostics. Existing limitations, including
the production float type's lack of a concrete-width field and legacy lossy IR
tags, are not solved merely by relocating the type definitions. A complete
semantic consumer must preserve those distinctions before making ownership or
layout decisions. No AST ownership analysis is retired by this prerequisite.

## Next production steps

1. Integrate the existing self-hosted typed-match/result/binding/capture work.
   Complete its source activation, semantic type precision, scope-preserving
   rewrites, defer handling and actual IR joins. Preserve its tests and separate
   arm yields from function return, break and continue.
2. Split typed value/control-flow import from RC emission in the production
   lowerer. Use binding/value identities, exact types, explicit projections and
   containment, resolved call effects and cleanup edges. Do not reconstruct a
   projected child's identity from its container or a runtime helper name.
3. Move a complete ownership slice through this boundary, including borrowed
   inputs, counted transfers, shared containers and escaping match payloads.
   Verify edge-sensitive lifetime and counted-unit plans before physical lowering.
   `own` transfers a counted reference, not proof of uniqueness.
4. Replace the corresponding production AST consumers, then delete the obsolete
   analysis and fallback. Extend coverage until all ownership decisions use the
   semantic representation. A permanent optional path is not the endpoint.
5. Demonstrate self-host bootstrap reproducibility, required targets and diagnostic
   parity before retiring the Go implementation. Measure compiler/coreutils runtime,
   memory, allocations and size on the production route.

Starting deletion inventory: `str_producer_ownership`, `str_binding_ownership`,
`str_expr_ownership`, syntax-based container escape checks and `param_consumed_*`
in `irlower.fern`. Their production callers still exist. Track each replacement
and deletion with its executable regression and independently verified contracts.

## Validation for the shared-type extraction

`TestSelfHostSemanticTypeBoundary` pins the dependency boundary, full field shapes,
member order, absence of duplicate checker declarations and `CheckResult`'s actual
use of the shared type. Existing checker, scope-parity and type-resolution runtime
tests exercise the moved representation. The two resolver goldens now also run
under the configured execution runner; their expected output is unchanged.

Production annotation, diagnostic, compiler-size and self-host fixpoint checks
remain required. Type relocation is not permission to weaken a gate or increase
a size baseline without attribution. Validation results belong in the PR and
measurement record, not speculative performance claims.

The initial extraction's [measurement record](measurements/selfhost-shared-types-2026-09-09.txt)
contains executable-size attribution, raw compile observations and local validation.
