# Typed ownership IR migration

Status: executable opt-in ARM64 pilot, not a completed migration. Highest development
priority by user direction. Related to #8920, #8278, #7989 and the existing
SSA cutover and typed-IR work. Initial integration base: `02ea66e91`.

## Current implementation checkpoint

Expression control flow now preserves live values versus terminated paths
explicitly. Value blocks and if-expressions join only live edges and identical
complete semantic types; coercing joins require a future checked coercion
contract rather than guessing from IsFloat or machine widths. Boolean &&/||
uses conditional edges, not eager operand evaluation, and ! is a verified
boolean operation. Every supported constructor, call, projection and scalar
operand list stops after an expression returns, breaks or continues. Never
does not acquire an invented SSA value. This is groundwork for typed match
arms/guards; it does not replace the preserved self-host typed-match work.

The compiler now exposes `-target arm64-linux -backend typed-ssa`. This route
uses the common frontend checking, monomorphization and capability enforcement,
then typed semantic ownership planning and concrete RC lowering directly to
fresh machine SSA. It bypasses `ir.LowerWith` and its AST ownership decisions;
the default route remains unchanged. `-o` links an executable; without `-o` it
writes assembly. Run/shared/export/external-linker/component options and
coverage/sanitizer instrumentation are explicitly unsupported, not ignored.

Executable regressions compare complete output with interpreter expectations,
before and after SSA optimization. They cover nested arrays and tuples, append,
own/borrow call interactions, early returns, an executed loop back edge,
escaped child arrays under allocator reuse, shared snapshots and borrowed
dynamic-string units. The runtime census checks matching allocations/frees and
zero live bytes for these fixtures. This does not fix the existing runtime's
final-string-release limitation or establish native/self-host parity.

Some inferred nested numeric literals still carry polymorphic type metadata
after the common checker. This pilot rejects that unresolved metadata; explicit
literal types work. Resolve the frontend's final type facts rather than dropping
the unresolved marker or reconstructing types from backend layouts. Match,
closures, cleanup, aggregate mutation and broader types remain unsupported.

The source producer now handles local replacement, while/unconditional loops,
break/continue (including labels), joins and early returns. A sealed-block
binding map creates typed phis on demand; it never keys ownership by source
names or scans AST syntax for an ownership decision. All bodies are built and
verified before existing identity-only trivial-phi simplification and dead
pure-value removal run. Types and source metadata are retained and reverified.
No low-level width-dependent optimization runs on this semantic graph.

The initial scalar induction surface is wrapping i32 addition, subtraction
and multiplication, i32 comparisons and boolean equality. Unsupported numeric
widths, overloaded operators and division are
explicit errors until their own contracts are implemented. Source/interpreter
and raw/optimized ARM64 tests exercise repeated append, retained snapshots,
projected borrows across allocator churn, simultaneous swaps, counted-parameter
loops, nested labeled exits, early returns and shadowed variables. Allocation
census and reference-count underflow checks remain part of executable tests.

The `internal/semir` pilot now uses a private existing-SSA graph, with full
checker types and source positions in phase-local value metadata. New explicit
array/tuple construction and projection operations put container/index operands
in ordinary SSA def-use edges. Semantic verification rejects missing types,
ABI-split values, foreign identities, invalid projections, incompatible phis
and unsupported operations before ownership analysis.

The checked-source producer handles local aliases, independent shadowed
bindings, array and tuple construction, nested tuple destructures and
conditional early returns. Unsupported aggregate mutation and cleanup
effects fail explicitly. Source loops now produce the same verified phis and
ownership edges as the hand-built loop tests. Struct/enum/callable/view types
remain outside this initial verified surface, not erased to scalar stand-ins.

Direct value-returning calls now use `BuildProgram`: a closed module declares
checked types and own/borrow contracts before building bodies, including
forward calls and recursion. Calls use module function identities, not runtime
helper names. Verification checks arguments, results and actual callee modes
against shared contracts. Missing, stale and unsupported callees are rejected.
Indirect/external/void calls remain outside this slice. `BuildFunc` is an
explicit single-function wrapper; calls to other functions need BuildProgram.

Call effects expose consuming-argument obligations but leave reference-bearing
return ownership unresolved pending unit certification. Return may-provenance
now uses a finite interprocedural worklist over explicit SSA uses and callers.
It separates parameter identity from contained values, preserving exact tuple
fields and summarizing array elements. Parameter-path substitution uses the
checked type shape, so recursive calls do not grow an unbounded path vocabulary.
Only reachable phi inputs contribute. Recursion may add possible sources;
an empty result is bottom, not a proof that a call terminates or returns owned.
Generated sources describe semantic producers, not physical freshness or
uniqueness. Solve these summaries once per module rather than repeating the
old whole-module scan in each lowering consumer.

Source array append is connected through explicit checker intrinsic metadata.
The checker records the registered function identity and instantiated checked
signature; the producer preserves argument evaluation order and emits the typed
append operation. Missing or stale metadata is rejected, never reconstructed
from a runtime helper spelling. This metadata is not an AST ownership heuristic.

The existing `ssa.ComputeLiveness` handles value uses and phi predecessor
edges, but is not itself an ownership lifetime proof. An array-get operand
keeps the container live through the read; a borrowed child used later still
needs a live ownership anchor, or an independently acquired unit. The next
analysis must preserve that obligation across subsequent calls and joins.
For branch-local containers, blindly unioning all possible parents into a join
would refer to values that do not dominate the join. Keep edge-conditional
anchors or secure the child's unit on the corresponding incoming edge, then
verify exact transfer/drop balance. Do not pass raw register liveness off as
borrow liveness, or equate containment with identity to get a positive answer.

Ownership contracts distinguish projected borrows from counted results,
immortals and joins. Constructors expose each stored value's lifetime
obligation; append separately exposes copied element relationships and the
newly stored value. Duplicate reference-bearing stores require separate units.
Returns expose an escape obligation, not a preselected retain or transfer.

The lifetime and counted-unit planner now implements the contract below over
the verified pilot graph. It extends existing SSA liveness with projection
dependencies, normalizes phi units on incoming edges, acquires each stored or
consumed occurrence, transfers dead units only once, and places last-use and
edge-specific drops. Borrowed call arguments keep their caller-side anchors
through the call, even when another argument consumes the same container.

An independent abstract-unit verifier replays each block and checks every edge
against the successor's unit invariant. It checks all returns in the complete
closed module, including recursive components, rather than certifying a caller
from a signature alone. It recomputes semantic lifetime facts, not trusting the
planner's cached last-use decisions. Corruption tests cover missing supplies,
duplicate transfers, early/missing drops, false immortality, lost copied-child
obligations and a callee that fails its counted-return ABI. Both hand-built and
source loop swaps verify simultaneous phi transfer.

This is opt-in, not the default pipeline. No legacy AST proof has been deleted;
the new route bypasses those decisions for its verified supported surface.
Physical RC lowering and executable lifetime checks now cover the pilot's
supported operations. Remaining work includes match/cleanup and broader source
coverage, broader runtime contracts, reuse proofs and native/self-host parity.
Abstract unit verification alone does not prove that a concrete runtime helper
implements its contract. The executable tests add runtime evidence for their
specific fixtures, not general runtime or performance acceptance. This useful
end-to-end connection meets the initial publication gate below; the complete
migration acceptance gates remain outstanding.

## Source loop validation, 2026-09-08

Complete semantic IR, CLI and source-lint packages, targeted race tests and
`make lint-all` pass. Native Linux ARM64 execution with required runtime
tooling passes the expanded raw/optimized lifetime matrix and actual source
loop CLI executable. Source loop support is a follow-up to the initial pilot;
it does not establish self-host or other-target parity.

At source-loop commit `bfa1572d7`, `BenchmarkTypedSourceLoops` measures checked-source production plus verified
physical lowering, excluding frontend checking, backend optimization and
assembly. Five 100 ms samples on native Darwin ARM64, Apple M3 Pro, measured
72,649 to 81,294 ns/op for one loop, 403,784 to 542,983 ns/op for eight, and
3,174,700 to 3,390,880 ns/op for 64. The corresponding allocation counts are
1,193, 4,752, and 29,958 to 29,959 per compilation. This is an initial scaling
baseline for the new phase, not a speedup against the existing compiler or a
generated-program runtime benchmark. Only the source loop count changes.

## Initial publication validation, 2026-09-08

On base `0365298f474`, the complete checker, semantic IR, SSA and source-lint
packages pass, as do targeted CLI and race tests and `make lint-all`. Native
Linux ARM64 execution, with `FERN_REQUIRE_ARM64_SSA_DIFF=1`, passes the raw and
optimized lifetime matrix and the actual CLI compile/link/execute test without
runtime skips. Full repository integration and fixpoints remain CI gates.

With `go build -trimpath -buildvcs=false` on the same host, the Go compiler
grows from 28,433,874 to 28,693,618 bytes. The newly linked semantic package
accounts for 87,952 symbol bytes; total file growth also includes Go metadata,
debug information and layout. This is a new opt-in compiler pipeline, not a
generated-program speed or size improvement. No baseline has been changed.
A fixed nested-array/call fixture produces a byte-identical executable through
the existing SSA route before and after this change. Its typed-route ELF is
65,622 bytes versus 65,579 bytes through the legacy SSA route. These are one
fixture's artifact measurements, not representative performance acceptance.

## Objective

Make semantic value flow, rather than source syntax or runtime-helper pattern
matching, authoritative for ownership. Preserve immutable value semantics and
precise diagnostics while enabling sound borrowing, reference-count placement
and reuse. A separate backend cutover is not a prerequisite.

The target boundary is:

```text
parse and check
  -> typed semantic values and control flow
  -> ownership/dataflow analysis and semantic transforms
  -> explicit reference-count and reuse lowering
  -> existing low-level optimization and backends
```

The intermediate states of this migration must remain clearly distinguished
from that completed boundary. A typed annotation on an AST, a file extraction,
or analysis of an already reference-counted program is useful groundwork, but
none alone constitutes the migration.

## Verified starting point

- Native `ir.LowerWith` performs AST-based ownership analyses and emits most
  reference-count operations during expression/statement lowering. There is
  no existing option providing the proposed typed pre-RC stage.
- Self-host `irlower.fern` similarly combines type, ownership and control-flow
  lowering. Its type tags are not a replacement for full semantic types.
- Native SSA already provides value IDs, blocks, phis, dominance, def-use
  analysis, structural verification and source-op provenance. Reuse these
  facilities rather than implementing another control-flow analysis library.
- SSA's ownership return solver deliberately leaves ordinary memory loads
  unproven. Its borrowed-return anchors mean value identity, not containment.
  Relabeling a field load as an alias of its container would be unsound.
- Existing low-level SSA tracks widths and address-ness, not the complete
  semantic type and ownership contract needed by this phase. `SrcOp` already
  exists; historical documents saying it is missing are stale.
- The separate typed-match worktree preserves complete self-host `Type`
  values and typed binders/captures. Preserve that work and coordinate its
  integration; do not introduce a competing string-encoded type system.

## Required representation contracts

1. Every semantic value has an explicit identity and resolved semantic type.
   Source names and spans are diagnostic metadata, never ownership keys.
   Unknown type or unsupported operation is an explicit boundary failure,
   not an invented scalar type or positive ownership conclusion.
2. Reference-bearing projections explicitly identify their container and
   selection: array index, tuple field, struct field or enum payload. These
   dependencies must be visible to def-use and lifetime analysis, not hidden
   in an auxiliary table that code motion or dead-code elimination ignores.
3. Identity aliasing and containment are different relations. A projected
   element can outlive its container only when its lifetime is secured by the
   applicable ownership protocol. Retaining a container does not automatically
   grant an independently owned element reference.
4. Borrowed, counted-owned, immortal and unproven ownership are distinct from
   uniqueness. An `own` parameter transfers a counted unit but can still have
   aliases. Reuse requires a separate proof or runtime uniqueness guard.
5. Constructors, stores, calls, returns and closure captures expose which
   units they borrow, transfer or acquire. Container-buffer ownership and
   element ownership remain separate. Do not infer these effects from an
   arbitrary helper name or from whether a value was freshly allocated.
6. Branch joins and loops carry values explicitly. A release or transfer must
   be valid on each reachable edge. A syntactic last occurrence is not a
   dynamic last use, and a phi is not proof of a new ownership unit.
7. Cleanup is explicit at return, break, continue and defer boundaries. Keep
   expression evaluation order, guard side effects and source diagnostics.
8. Verification runs before ownership analysis and after transformations.
   Validate typing, dominance, projection shape, effect contracts and edge
   transfers. Existing structural SSA verification is necessary, not sufficient.

Use the existing semantic type definitions through a clean phase boundary;
do not create another approximate type vocabulary. Keep target layout and
machine widths separate from source-level type identity. Avoid increasing
every low-level operation's size for metadata only the semantic phase needs;
measure live memory as well as generated-code size.

## First end-to-end pilot

### Lifetime and counted-unit planning contract

Extend the existing SSA liveness solver with explicit additional use
dependencies. Projection borrows keep their transitive container anchors live
at every use, including returns and phi incoming edges. Do not mutate the
semantic graph to invent extra operands or expose it to low-level optimizers.

Normalize reference-bearing phis to one counted unit on each incoming edge:
acquire from a borrow or transfer a dead owned input, before releasing any
container anchor. Phi results then have independent lifetimes; they do not
carry branch-local parent identities into a block those parents do not dominate.
Loop edges use the same protocol, with simultaneous phi assignments.

Plan counted stores and consuming call arguments per occurrence. A single
existing unit can move only once, after other acquisitions, and only if no
subsequent use or active borrowed call argument needs its anchor. Borrowing
is not a consume. Releases occur at last lifetime use, with edge-specific
cleanup when a value is needed on only one successor. Append's copied child
units remain a separate bulk obligation, not a transfer of the original buffer.

The experimental closed-module ABI requires a counted reference result from
every reference-returning function. This is an obligation to enforce at every
return, not an inference from return provenance. Recursive calls can rely on
it only when the complete module's plans have been independently verified.
No plan may cross into backend lowering without that complete verification.

### Physical lowering boundary audit

First executable target: the existing ARM64 SSA backend's explicit 8-byte
pointer, single-word string ABI. This is not the separate flat ARM64 two-word
route. Lower verified semantic plans directly to fresh low-level SSA graphs,
with source-position metadata retained outside target operations. Split edges
for RC work and preserve predecessor/phi correspondence. Use existing SSA
verification/optimization and the existing ARM64 assembler/runtime.

Generate aggregate destruction by complete semantic type, with child release
only on the final owning reference. Append initially implements the plan's
copy-form contract: allocate a new buffer, acquire each copied child unit and
install the separately supplied new element. Do not activate destructive reuse
without its own proof. Check sizes and full-width indices before narrowing.
Array/box reclamation and borrowed-string reference balance are executable
acceptance evidence; this backend's documented final-string-release limitation
must not be represented as solved by the new pass. The x86 SSA bump-only heap
cannot provide reclamation evidence, so it is not the first lifetime target.

The existing CLI SSA routes still call `ir.LowerWith` before lifting, so they
cannot consume this pre-RC graph without a new verified driver boundary.
`ssa` imports `ir`; importing `semir` from `ir` would introduce a cycle. Keep
integration above that boundary or extract a genuinely shared representation.

The low-level `ir.Program` and `ir.Func` explicitly retain target pointer width
and the two-word string ABI. Preserve those facts in the new lowering; do not
query the temporarily overridden global ABI after lowering. Unit releases
must select complete typed destructors: releasing a shared container does not
release its child units, while the final buffer release must reclaim every
owned child. Do not replace nested typed cleanup with a buffer-only helper or
assume a flat pointer decrement recursively drops arbitrary nested values.
The existing typed-match worktree's complete self-host Type remains the
integration source; no duplicate approximate type vocabulary is introduced.

Start with checked string-array construction, element reads, append, direct
calls and returned arrays. Include local aliases, independent shadowed
bindings, branches and loops. Tuple destructures and match payloads are
projection cases, not ordinary local initializers.

1. Define semantic operations and verifier tests using existing SSA value and
   control-flow infrastructure. Pin invalid projections, missing types and
   stale or hidden dependencies as rejected cases. Do not feed these new
   operations to existing low-level optimizers or emitters before lowering them.
2. Produce this representation from actual checked source in the pilot, before
   ownership decisions. Compare source bindings, full types and control flow
   with independent expectations, including the four #8928 initializer-witness
   regressions. Do not infer facts by reversing emitted retains and drops.
3. Compute borrow/containment/transfer facts over this representation. Make
   counted element stores and returned-buffer cleanup a coherent protocol.
   Run ownership verification and lower explicit effects to existing backends.
4. Execute the pilot against the interpreter and native/self-host paths on
   ARM64, x86-64 and Wasm. Exercise complete bytes after allocator pressure,
   unique and shared inputs, early exits and repeated calls. Check balances,
   underflows, sanitizer results and bounded heap behavior where supported.
5. Replace the corresponding AST proof only after this production-connected
   pilot is verified. Expand by semantic operation, not by adding exceptions
   to source-name scans. Unsupported cases during development must be visible;
   they cannot silently qualify for ownership transformations.

A partial experimental builder is not publication-ready architecture. The
first architectural PR must connect useful semantics through the pipeline and
make its remaining coverage explicit. Preserve the existing production route
until the replacement's acceptance gates hold, without changing its answers
to make differential tests agree with a new bug.

## Acceptance gates

### Retirement endpoint

User direction: retire the old AST-based ownership approach. The opt-in pilot
is an intermediate delivery, not the endpoint. Parsing, name resolution and
initial type checking remain frontend responsibilities. Ownership, lifetime,
escape/containment and reuse decisions must become typed-IR consumers in both
the native and self-host compilers.

For each legacy analysis, record its production callers, replacement semantic
operations/contracts, outstanding feature/target gaps, differential tests and
the commit that removes it. An analysis is not retired merely because the
experimental backend bypasses it. Cut over its production consumers only after
the replacement covers their supported semantics, then delete the obsolete
implementation, its redundant metadata and fallback path. Keep behavioral
regressions and independent interpreter/runtime oracles after deleting the old
implementation; do not retain two permanent ownership decision systems.

Initial retirement inventory (not an exhaustive list):

| Legacy production analysis | Replacement responsibility | Current retirement blocker |
| --- | --- | --- |
| Native `ir.LowerWith` inputs: `inferParamEscapes`, `inferParamCountedRetain`, `findReturnsParamProjection` and related return facts | Typed interprocedural identity/containment, call effects and verified counted-unit plans | Pilot does not yet cover all calls, types, cleanup or production targets |
| Native `ast.ExprResultOwnershipWith` and self-host `str_producer_ownership` / `str_binding_ownership` / `str_expr_ownership` | Explicit typed producer effects and value/binding identities | Full string/runtime contracts and self-host producer parity remain incomplete |
| Self-host syntax-based container/alias escape checks in `irlower.fern` | Projection-aware lifetime, control-flow joins and explicit cleanup | Match payloads, aggregate updates, closures and self-host IR ownership integration remain incomplete |

None of these families is retired yet. Extend this inventory as their callers
are migrated, retaining exact deletion and regression-test references.

The concrete sequence is:

1. Complete typed source and effect contracts, including match projections,
   closures, cleanup, external/indirect calls and existing aggregate semantics.
   Preserve the existing self-host typed-match work and resolved source types.
2. Establish target ABI/runtime coverage and native/self-host parity, including
   diagnostics, allocator-pressure lifetime tests and compiler fixpoints.
3. Switch production ownership consumers to verified typed IR. Unsupported
   pilot features must be implemented before this cutover, not silently routed
   back to AST ownership heuristics.
4. Delete the replaced AST analyses and fallback routing, and add architecture
   checks preventing ownership consumers from depending on those old helpers.
5. Measure representative compiler/coreutils performance, memory, RC work and
   size on the production route. Attribute and fix avoidable regressions.

The migration is complete only when all production ownership consumers have
crossed this boundary and the old approach can be removed. The next checklist
item below is a minimum intermediate milestone, not full retirement.

### Validation

- Correct values, diagnostics, evaluation order and cleanup across targets.
- No use-after-free, over-release or falsely admitted ownership, including
  non-var binders, shared containers and retained snapshots.
- Native and self-host agreement on the semantic representation and ownership
  contracts, with complete compiler fixpoints and integration tests passing.
- At least one corresponding AST ownership analysis removed from the active
  path, rather than indefinitely running two independent decision systems.
- Measured compiler time, peak memory, emitted size, reference-count work and
  runtime on representative inputs. No universal speed claim from one pilot.
- All current-head CI checks complete without failure before merging. No
  weakened tests, hidden coverage gaps or inflated size baselines.

After the pilot, continue migrating remaining ownership and semantic analyses
before resuming unrelated utility/backend expansion. Coreutils and compiler
workloads supply the correctness and performance evidence for the migration.
