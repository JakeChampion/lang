# Verify binding observations after SSA promotion

This migration slice preserves typed binding semantics across optional-place
promotion, then checks the resulting SSA independently. It does not enable
conditional cleanup execution or retire the native/self-host AST paths yet.

## Pass boundary and proof

Before promotion removes binding instructions, a function records writes and
observations for explicitly snapshotted BindingIDs only. Writes retain the
binding identity, block, exact payload value, initializer/replacement distinction
and source position. Observations retain their block, state value and original
position relative to local writes: the last preceding local write, or the
incoming state. A later assignment cannot silently change an earlier snapshot.

These are typed semantic events, not a cached successful-verification flag or
the promoter's inferred reaching-definition table. The post-promotion verifier
reconstructs obligations using the current CFG, actual predecessor slots and
existing typed lifetime-end records:

- A local or reaching write requires `Present` of its exact payload identity.
- An entry without a write requires `Absent`.
- A lifetime end requires `Absent` on that edge, even if an earlier iteration
  initialized the place. Previously observed values remain independent snapshots.
- A state phi selects the actual argument for each predecessor. A canonicalized
  state alias is checked across the corresponding incoming paths instead.

The iterative worklist memoizes value/binding/block-entry obligations. It has no
arbitrary recursion bound and does not call promotion to decide validity. Every
incoming path must discharge; backedges cannot supply an invented entry payload.
Record identities, payload types and observation dominance are checked using
the dominance tree already produced by successful SSA verification. Diagnostics
retain original observation or write locations.

Ordinary read aliases and trivial-phi aliases update the records using the same
substitutions as graph operands. `TrivialPhisWithAliases` optionally fills a
caller-owned map, cleared on each invocation. The ordinary SSA pass keeps its
scratch map local; functions without snapshot records do not request an output
map. Equal constants materialized under an existing phi identity remain under
that identity, rather than becoming invalid aliases to one predecessor.

Metadata does not add SSA consumers or RC uses. An unused state phi can disappear
under DCE, and its observation is pruned. An unobserved pure write payload can
disappear while its historical record remains. Existing availability operations
remain conservatively impure in shared SSA passes: this change does not make
generic DCE, CSE or LICM responsible for their ownership semantics.

The contract currently checks promoted snapshot equations. The retained
initializer marker is semantic history for subsequent cleanup integration, not
a claim that post-promotion registration/capture correlation is implemented.
Conditional source execution remains rejected until activation, late captures,
guarded writeback and their before/after-promotion proofs are integrated.

## Validation

New corruption tests isolate this contract: structural SSA and the pre-existing
semantic checks still accept the mutated graph, but the new verifier rejects
the wrong payload, reversed predecessor slots, stale iteration state, later
replacement of an old observation, malformed metadata or unavailable state.
Ordinary-read aliases, state/payload phi aliases, DCE pruning and source locations
have dedicated coverage. SSA tests cover alias chains, materialized constants
and clearing a reused output map.

The existing exact CFG presence oracle now also checks all 168 cases after
`finishFlow`, including nested and irreducible loops. Native raw/optimized
snapshot tests pass through that phase too, exercising borrowed/counted arrays,
saved replacements, allocator pressure and fresh iteration epochs.

Final local validation: full SSA 93.179 s, semantic IR 0.831 s, source lint
17.770 s and CLI 32.087 s. Strict native Linux ARM64 semantic/runtime tests pass
in 1.981 s and the typed CLI matrix in 9.529 s; the required-tooling flag prevents
missing native tooling from silently skipping this matrix. Targeted race checks
pass for semantic IR (1.466 s) and SSA (1.300 s); `make lint-all` passes.
Full integration CI remains a publication/merge gate. No expected outputs or
performance baselines are changed.

## Measured costs

Native Darwin ARM64, Apple M3 Pro, Go 1.26.0. Parent:
`431229c9167e35da31d958da207047c1ba91c871` (tree-identical to `9602a510b`).
Candidate: this change on that parent, measured before its documentation commit.
A one-iteration smoke verified the pipeline before five 100 ms samples per
size. `BenchmarkBindingSnapshotPromotion` includes construction, promotion,
verification and ARM64 SSA lowering, not just the new verifier. These are
compile-pipeline measurements, not generated-program speedup claims.

| Workload | Parent allocs | Candidate allocs | Parent bytes | Candidate bytes |
| --- | ---: | ---: | ---: | ---: |
| Snapshot, 1 write | 171 | 176 | 14,024-14,025 | 14,456-14,457 |
| Snapshot, 64 writes | 316 | 321 | 35,038-35,041 | 38,624-38,625 |
| Snapshot, 1,024 writes | 2,255 | 2,260 | 346,199-346,206 | 395,766-395,774 |
| Ordinary cleanup, 1 action | 1,208 | 1,208 | 98,413-98,415 | 98,460-98,462 |
| Ordinary cleanup, 8 actions | 3,666 | 3,666 | 327,905-327,911 | 328,061-328,070 |
| Ordinary cleanup, 64 actions | 19,720-19,721 | 19,720-19,721 | 2,436,454-2,436,467 | 2,437,505-2,437,687 |

Retaining typed history and verifying it is additional work. The 1,024-write
workload takes 176,605-183,710 ns at the parent and 277,059-282,639 ns with the
contract. This is a regression in that compile workload, accepted for the new
post-transformation correctness check, not described as an optimization.
Ordinary workloads retain allocation-count parity but use slightly more bytes
because each semantic function carries an optional contract pointer.

An initial returned-map API added two allocations even on ordinary cleanup
workloads. The caller-owned output API removes those. Exact event preallocation
and packed write records reduce the 1,024-write candidate from
469,356-469,379 bytes / 2,270 allocations to the final figures above. All original
write events remain present; no semantic history was discarded to obtain that
reduction. Timing samples on ordinary workloads are noisy, including high-tail
64-action candidate samples, so they do not establish a general speedup.
A repeat after local test jobs finished measured 2,339,100-2,431,790 ns for the
parent and 2,238,220-3,323,840 ns for the candidate, again with overlapping
central samples and a higher candidate tail. This does not establish ordinary
timing parity; raw repeats are retained as `{parent,final}-quiet.log`.

Comparable `go build -trimpath -buildvcs=false` compiler files grow from
28,970,834 to 29,005,698 bytes: **+34,864 bytes**. Mach-O `__text` grows from
10,138,452 to 10,148,596 bytes: **+10,144 instruction bytes**. New symbol sizes
include 6,128 bytes for the equation verifier, 1,488 for event recording and
880 for alias rewriting. The remaining instruction change includes pass
integration and map/type support. Debug/type/PC metadata and segment alignment
also contribute to the compiler-file delta. No generated runtime operation is
added by these records, and no size baseline is inflated.

Reproduce against the parent and candidate worktrees:

```sh
go test ./internal/semir -run '^$' \
  -bench 'BenchmarkBindingSnapshotPromotion|BenchmarkTypedCleanupActions' \
  -benchtime=100ms -count=5
go build -trimpath -buildvcs=false -o /tmp/fern-snapshot-contract ./cmd/fern
/usr/bin/size -m /tmp/fern-snapshot-contract
go tool nm -size /tmp/fern-snapshot-contract
```

Local raw logs: `/private/tmp/lang-snapshot-contract-{parent,candidate,refined,final}-bench.log`.
The `candidate` and `refined` logs retain the intermediate implementations;
`final` is the version described by the final table.
