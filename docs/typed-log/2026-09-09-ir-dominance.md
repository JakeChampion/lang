# Dominance cost in typed ownership verification

The guarded-writeback profile identified repeated parent-chain dominance queries
as a deep-graph verification bottleneck. This change keeps the existing immediate
dominator algorithm and its answers, but indexes the completed dominator tree
with DFS subtree intervals. Queries use block identity and interval containment,
not source names, mutable Block.ID numbers or CFG RPO interval assumptions.

The index takes linear construction work and storage. Its traversal is iterative
and child/sibling links share a scratch arena. Single-block trees need no index.
Non-nil reflexivity, including dead/foreign blocks, and nil rejection are unchanged.
The tree, Idom and RPO are read-only analysis snapshots, valid until CFG mutation.

Successful SSA verification can return its already-built tree through
`VerifyWithDomTree`. Typed availability and cleanup checks reuse that snapshot
within the same read-only verification call. No result is returned after a failed
verification, stored on semantic IR, or carried across graph mutation. Private
cleanup action functions still receive their own independent verification.

An interval-only prototype added avoidable allocations to ordinary compilation.
Singleton elision and sharing the verified analysis remove those repeated costs.
The remaining construction tradeoff is explicit, not a universal speedup claim.

## Correctness evidence

An independent oracle deletes each candidate dominator from the CFG and tests
reachability. All 14,641 four-block CFGs with zero, one or two distinct successors
per block agree on 234,256 queries. The corpus includes loops, irreducible flow,
joins, self edges and dead predecessor cycles. It shares no idom, RPO or interval
algorithm with the implementation. Additional tests cover a 4,096-block chain,
concurrent read-only queries, nil/dead/foreign identities and rejecting analysis
results when use-dominance verification fails.

Full SSA, semantic IR, source-lint and CLI packages, targeted race checks and lint
pass locally. Strict native Linux ARM64 typed-runtime and CLI checks also pass.
Full integration CI remains the merge gate. No source conditional-cleanup gate,
ownership condition, size baseline or production AST fallback is changed here.

## Measurements

Parent `88b7b0c21`, Go 1.26.0, native Darwin ARM64 on Apple M3 Pro. Five 100 ms
samples per case followed a small smoke run. Only graph/action count changes
within each family. These are observed ranges, not confidence intervals.

| Complete semantic verification | Parent ns/op | Indexed/shared ns/op | Parent allocations | Indexed/shared allocations |
| --- | --- | --- | --- | --- |
| 1 guarded write | 2,982-4,142 | 2,671-3,477 | 42 | 37 |
| 64 guarded writes | 101,973-105,560 | 36,458-40,097 | 149 | 113 |
| 1,024 guarded writes | 17,727,090-18,438,965 | 621,533-655,443 | 269-271 | 197 |
| 64 conditional actions | 12,730,859-13,451,375 | 12,722,255-13,423,380 | 1,882 | 1,771 |

The 64-action conditional projection workload remains dominated by its other
work: these measurements do not establish a timing improvement there. At 1,024
writes, allocated bytes fall from 950,956-952,244 to 735,421-735,448. The one-write
case uses 1,240 bytes instead of 1,200 despite fewer allocations.

Ordinary source-to-physical cleanup compilation uses 1,208 / 3,666 / 19,720-19,721
allocations for 1 / 8 / 64 actions, versus 1,242 / 3,799 / 20,042-20,043. The
64-action time changes from 2,482,423-2,598,348 to 2,133,936-2,227,515 ns/op.
One-action allocated bytes increase from 98,023-98,026 to 98,414-98,418; larger
cases allocate fewer bytes. The retained interval index is not free.

Isolated chain queries show the expected depth independence: 64 blocks change
from 884.4-931.4 to 7.733-7.907 ns, and 1,024 blocks from 16,710-17,377 to
8.832-9.912 ns, with zero query allocations. Two-block queries instead increase
from 3.183-4.528 to 5.017-5.142 ns. Building the two-block tree increases from
153.5-196.7 ns, 248 bytes and 5 allocations to 216.8-225.0 ns, 504 bytes and 8
allocations. At 1,024 blocks, construction allocates 285,824-285,834 bytes instead
of 252,833-252,839 and uses 79 rather than 75 allocations. Query-heavy deep graphs
benefit substantially; callers that only need immediate dominators pay indexing
overhead. The existing minimal-main lift/optimize benchmark retains 44 allocations
but increases from 2,361 to 2,425 bytes; its timing samples overlap.

Identical `go build -trimpath -buildvcs=false` builds change from 28,970,658 to
28,970,834 file bytes, an increase of 176 bytes. Mach-O instruction bytes increase
by 1,264. The new subtree-index routine contributes 688 symbol bytes; verification
plumbing and inlined query sites account for other code changes. Debug/data and
segment alignment explain why file growth differs from instruction growth. No
baseline is raised.

Raw local logs: `/private/tmp/lang-dominance-{parent,shared}-{dom,semir,prelude}-bench.log`.
The interval-only intermediate measurements use `index` instead of `shared`.
These are compiler-verification measurements, not coreutils runtime comparisons
or evidence of completed native/self-host AST ownership retirement.
