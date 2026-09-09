# Conditional cleanup execution in typed IR

The opt-in ARM64 typed pipeline now executes conditionally registered plain
function and iteration cleanup actions. This connects the previously separate
registration/capture proof, optional binding states, guarded replacement and
post-promotion snapshot contract. It does not retire production native or
self-host AST ownership paths, enable error-only cleanup or admit registration
inside loop conditions or deferred actions.

## Pass boundaries and executable proof

The source producer resolves capture BindingIDs and checks/types each private
action region once. The complete pending CFG then passes independent boundary,
registration-order and capture-initialization verification before expansion.
Expansion consumes typed action regions, binding identities and control-flow
edges, not AST syntax or parser-generated temporaries.

Functions with only dominating registrations retain ordinary direct expansion.
When registration is conditional, each action gets a private boolean activation
binding owned by its existing cleanup boundary. Its sole initializer is exactly
at registration. No replacement is legal. Activation is the presence of this
binding, not its boolean payload: the payload need not be extracted at runtime.
Iteration ends clear availability through the existing verified lifetime rules.

Each replay has a recorded dispatch site, ordered activation/capture guards,
execute entry, body exit and common continuation. The actual branch must test
`StateHas` of that guard's exact binding snapshot. Every false edge goes to the
common continuation; true edges form the unique chain into the action. Captures
are snapshotted late, after earlier actions publish their outputs. `StateGet`
occurs only behind the corresponding true presence edge. Body cloning is shared
with ordinary expansion, including internal branches and loops. All yielded
outputs are computed before guarded binding replacements publish them.

The common continuation, not merely the taken body exit, inherits successor
predecessor slots, cleanup finish points and boundary ends. Return values already
computed before cleanup remain ordinary SSA snapshots. No runtime environment
object, protocol tuple or AST action replay is introduced.

Verification checks the executable records against the current graph on each
invocation. Before promotion, guards require direct same-block snapshots of the
specified bindings. After promotion, they require matching typed observations;
the separate equation verifier reconstructs their meaning from retained writes,
current predecessor slots and lifetime ends. Alias rewriting updates guard state
identities and snapshot records together. A representation-mode flag makes a
missing action descriptor invalid; it is not a cached successful proof.

Correlated scope and action/capture projections consume actual initializers
before promotion and retained typed initializer events afterwards. They prove
that a registered action has all its captures, as well as LIFO order and exactly
once replay. Guard shape plus independently verified activation/snapshot equations
prove that it really runs, while an unregistered action skips without reading
absent locals. Merely adding a capture guard would be insufficient: without the
correlation proof, an incorrectly absent capture could silently suppress cleanup.

Logical replay remains at dispatch in the finite-history model. Both abstract
successors have the same post-dispatch history. The independent executable proof
justifies the real branch; the model does not prune CFG edges using unproven
activation metadata. Action bodies contain no nested cleanup events, which remains
an explicit admission restriction. All ordinary must-initialization, ownership
unit and source-diagnostic checks remain in force.

## Correctness evidence

Eleven shared source cases pass the interpreter oracle, typed compilation and
raw/optimized native Linux ARM64 execution with balanced allocation/free counts
and zero live bytes. Coverage includes taken/skipped actions, late array updates,
branch-local nested arrays, zero captures, saved return projections, alternating
iteration activation, labelled break/continue, mutually exclusive registration,
body branches/loops and nested-array lifetime under repeated allocator pressure.

One source enumerates all 16 subsets of four registrations. Expected replay
traces come from a separate Go stack model, not the compiler's projected proof
or the interpreter. A first-registered action checks the final trace, preventing
a saved return value from hiding missing or misordered replay. A taken zero-
capture fault must still abort in both raw and optimized code; its skipped case
must return normally. The CLI test compiles and links the actual typed backend,
preserving returned arrays while conditional iteration cleanup churns allocations.

Malformed-contract tests retain valid ordinary SSA while rejecting missing or
wrong activation/dispatch records, wrong binding/state/branch identities, work
before guards, early snapshots, missing initialization, false replacement-as-init
history, wrong promoted scope and absent/misplaced observations. Mutating actual
activation from Present to Absent, with all event and guard metadata unchanged,
is rejected by the independent snapshot equation verifier. Reversed activation
branches are rejected even with no capture extraction to expose the error.

Existing independent 11,520 single-scope and 960 nested-scope history comparisons
remain intact. The old fixture now marks its unpromoted phase explicitly and
provides a real initializer operand; its reference algorithm and outcomes are
unchanged. Existing snapshot, guarded-writeback and lifetime oracles also pass.

Final local validation: full SSA 107.575 s; semantic IR 0.729 s; source lint
15.200 s; CLI 29.805 s. Strict native Linux ARM64 typed tests pass in 2.374 s and
the typed CLI matrix in 12.694 s. Required native tooling prevents silently
skipping that matrix. Targeted race tests pass for semantic IR (1.527 s) and SSA
(1.438 s), and `make lint-all` passes. Full integration CI remains the merge gate.

## Measured cost and allocation refinements

Measurements use native Darwin ARM64, Apple M3 Pro, Go 1.26.0. Parent is
`a580775c49dcebe1f023f4d0ec99c34e6a74bd28`, tree-identical to the published
snapshot-contract feature `76b6ea447`. Conditional execution is unsupported at
that parent, so conditional measurements describe a new capability, not a
before/after generated-program speedup. GNU/uutils timings are not relevant to
this compiler proof workload; broader coreutils benchmarking remains deferred
until the typed-IR migration acceptance criteria are met.

A one-iteration 1/8/64-action smoke preceded profiling. At 64 actions, the first
implementation allocated 117,553,440 bytes in 410,336 allocations. Profiling found
two avoidable costs, fixed without dropping or caching successful verification:

- Snapshot verification retained all bindings' value/CFG obligations in one
  global scratch map. Grouping observation indices by binding shares proofs for
  that binding, then clears and reuses the scratch. The semantic record stream
  stays unchanged; every observation still discharges its equations.
- Trivial-phi substitution repeatedly traversed the same alias chains per
  operand, allocating cycle-detection storage. Canonicalizing each chain once
  removes that repetition. Each cycle member still resolves to itself and a
  prefix to its first cycle entry, preserving the original resolver exactly.
  Exhaustive comparison covers all 1,296 four-node alias maps with live/invalid
  terminals, six start values each: 7,776 comparisons including cycles and fan-in.

The refined 64-action smoke allocated 42,155,696 bytes in 239,478 allocations.
These single-iteration samples are allocation evidence, not stable timing claims.
Correlated pair/capture projections still require O((A squared + C) G) work;
no proof pass or malformed-graph test is removed to conceal that cost.

Five subsequent quiet 100 ms samples, after local test jobs finished:

| Conditional actions | ns/op range | bytes/op range | allocs/op range |
| --- | ---: | ---: | ---: |
| 1 | 156,856-237,740 | 188,056-188,064 | 2,255 |
| 8 | 2,883,591-3,058,002 | 2,181,793-2,181,961 | 15,229-15,230 |
| 64 | 428,173,083-481,632,208 | 42,118,184-42,135,664 | 239,446-239,468 |

Each 64-action sample completes one iteration because it exceeds 100 ms. These
include typed construction, expansion, verification, ownership and ARM64 SSA
lowering, not parsing/checking or generated-program runtime. Scaling remains
costly and is explicitly not a claim of fastest compiler performance.

Ordinary action allocation counts remain unchanged at 1,208 / 3,666 /
19,720-19,721 for 1/8/64 actions. Bytes grow slightly with optional descriptors:
98,462-98,464 to 98,476-98,483; 328,058-328,074 to 328,189-328,209; and
2,437,480-2,437,540 to 2,438,543-2,438,630. Ordinary timings overlap: the
64-action parent spans 2,179,981-2,521,942 ns and candidate 2,134,695-2,244,905 ns;
this does not establish a general speedup. The 1,024-write single-binding
snapshot workload keeps 2,260 allocations and approximately 395,768 bytes, with
parent 261,702-284,219 ns and candidate 260,207-266,297 ns. The new grouping keeps
its single-observation fast path. Raw quiet logs are retained as
`/private/tmp/lang-guarded-cleanup-{parent,final}-quiet.log`.

Comparable `go build -trimpath -buildvcs=false` compiler files grow from
29,005,698 to 29,057,698 bytes: +52,000 bytes. Mach-O `__text` grows from
10,148,596 to 10,165,236 bytes: +16,640 instruction bytes. New executable guard
verification contributes 5,568 bytes including its closures; guarded expansion
4,560, activation preparation 1,504, initializer event indexing 784 and phi-alias
canonicalization 1,280. Shared body cloning replaces the old embedded clone,
so its symbol is not wholly new cost. Other changes include integration, snapshot
scratch grouping, map/type support, debug/PC metadata and segment alignment.
No binary-size, performance or expected-output baseline is changed.

Reproduction:

```sh
go test ./internal/semir -run '^$' \
  -bench 'BenchmarkGuardedCleanupBuild|BenchmarkBindingSnapshotPromotion|BenchmarkTypedCleanupActions' \
  -benchtime=100ms -count=5
go build -trimpath -buildvcs=false -o /tmp/fern-guarded-cleanup ./cmd/fern
/usr/bin/size -m /tmp/fern-guarded-cleanup
go tool nm -size /tmp/fern-guarded-cleanup
```

Profiles and validation logs are retained locally under
`/private/tmp/lang-guarded-cleanup-*`.
