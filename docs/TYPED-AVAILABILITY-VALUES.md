# Typed availability values

This executable slice supplies an internal representation prerequisite
for [conditional cleanup availability](TYPED-CLEANUP-REGIONS.md). It does not
enable source conditional registration,
and does not establish native/self-host production ownership retirement.

## Semantic type and operation contracts

Value types now distinguish ordinary checked source values from the internal
sum `Absent | Present(T)`. The source type or payload type remains fully resolved;
the form is part of semantic type equality, including phi inputs. No compiler
state is added to the AST or disguised as a user enum. Parameters, source returns,
ordinary operations and bindings still accept only their checked source types.

| Operation | Semantic input | Result |
| --- | --- | --- |
| `state_absent` | No payload | Availability of T |
| `state_present` | An ordinary T value | Availability of T |
| `state_has` | Availability of T | Boolean presence |
| `state_get` | Availability of T plus a verified control-flow proof | Ordinary T |
| State phi | The same complete availability type on every predecessor | Availability of T |

Booleans, settled integer widths and the pilot's resolved reference-bearing types
are supported. Zero and false are ordinary present payloads, never absence
sentinels. Reference-bearing payloads use the independently verified conditional
unit protocol below, not unconditional payload owners.

Extraction requires either a directly known `state_present` definition, or a
dominating true edge from `state_has` of the exact same immutable SSA state.
The guarded successor must not have another incoming edge. This rejects false
branches, bypasses and checks of a different state version. Ordinary SSA dominance
and semantic type verification run first. This is an explicit sufficient proof
for the slice, not a claim to recognize every logically equivalent predicate.

These operations are confined to the pre-RC phase and conservatively excluded
from ordinary machine optimizations until lowered. Scalar states have no counted
units. The existing independent unit verifier and whole-module gates still run;
the representation does not bypass ownership analysis for enclosing functions.

## Physical lowering

After semantic and unit verification, each state can use a presence flag and a
payload word in machine SSA. The absent semantic operation has no payload; an
inactive machine lane can be initialized only at this later boundary. It is never
exposed as a valid semantic T value. There is no tuple/environment heap allocation.

Lane demand propagates through state phis to a fixed point, once per component
and edge. Presence-only uses omit payload phis and inactive payload words. Direct
Present/Get chains omit unused flags, preserving payload identity without new
machine instructions. Lane demand also includes guarded retains and drops from
the verified unit plan; it does not itself prove ownership. Presence-only
reference states can still need payload lanes for reclamation.

Multi-lane phis are indexed by their actual physical identities and retain
predecessor order. Forward aliases are canonicalized before rewriting machine
operands. Ordinary single-lane functions retain their original direct phi/fixup
path. Full source locations and payload widths survive lowering.

## Conditional payload ownership

For each reference-bearing state `s`, the unit ledger records a distinct
obligation: one payload unit if `has(s)`, zero otherwise. This is not an
unconditional owner and does not assert uniqueness. `Absent` creates no payload
or acquisition. `Present(v)` requires one ordinary unit for `v`, satisfied by a
verified move or retain. State phis require an incoming conditional obligation;
their presence and payload lanes select the same predecessor together.

The independent unit verifier rebuilds lifetimes and distinguishes ordinary from
conditional obligations at every block. Conditional supplies name the exact state
as their presence condition. Retains precede all moves, so duplicated phi inputs
receive independent obligations and a source obligation cannot move twice.
Phi assignment renames the selected obligation to the new state identity. Loop
edges must establish the same typed invariant as every other incoming edge,
which is an inductive proof rather than bounded execution enumeration.

`Get` is a borrow through the state. Its references and nested projections extend
that state's lifetime; they do not acquire a payload unit automatically. Returns,
stores and consuming calls still require their own ordinary units. Provenance
passes propagate the payload's identity and children through Present/Get, while
Absent contributes no payload origins. Neither provenance nor counted ownership
is a uniqueness certificate.

After verification, conditional retains and drops branch on that same state's
presence flag. No RC helper observes an absent payload lane or uses its pointer
bits as a substitute discriminant. Cleanup can split machine blocks, so all phis
stay at the source block entry and successor operands use the actual
post-cleanup predecessor edges. Scalar lane-elision tests remain unchanged.

## Verification evidence

Malformed tests retain valid SSA while rejecting absent payload arguments,
missing or mistyped present operands, ordinary/state substitution, mixed phis,
invalid type forms, unsupported reference state, parameter/return escape,
unguarded extraction, wrong-state tests, false edges and bypass predecessors.
Additional tests cover direct Present extraction, loop-carried state, reordered
blocks, effect-only call arguments and a 1,024-state alias chain. Unit plans for
the scalar graph contain no supplies, drops or element-copy obligations.

Raw and optimized Linux ARM64 execution covers false/zero payloads, signed and
unsigned 8/16/32/64-bit values, absence, state joins, loop initialization and
presence-only lane elimination. The existing allocator-balance checks remain
unchanged. Full semantic IR, source-lint and CLI packages, the full SSA suite,
targeted race tests and lint pass. Integration and self-host CI remain separate
publication gates; no source feature or production cutover is claimed here.

Conditional-unit regression tests additionally cover independently corrupted
plans, duplicate conditional moves, shared and unused state phis, borrowed and
consumed arrays, nested tuple projection escape, snapshot-preserving append,
and repeated replacement through absent/present loop states. Native raw and
optimized tests exercise allocator pressure at 1 and 64 rounds. A poisoned,
non-null inactive payload lane proves that absence tests the discriminant before
RC. Borrowed-string tests keep an independent final fixture owner, as in the
existing string-unit tests; they do not repair or claim coverage of the runtime's
known final-string-free limitation.

## Scalar representation baseline

Measured against parent `f5b39c1ce` with Go 1.26.0 on native Darwin ARM64,
Apple M3 Pro. Each benchmark uses five 100 ms samples, after a one-state smoke
run. Source construction is excluded from the availability benchmark, which
includes verification, ownership planning and ARM64 SSA lowering. The existing
cleanup benchmarks include their source pipeline. These short samples describe
this pilot, not a general compiler throughput or coreutils speed claim.

| Ordinary source pipeline | Parent median ns/op | Availability median ns/op |
| --- | ---: | ---: |
| Iteration cleanup, 1 loop | 74,514 | 70,804 |
| Iteration cleanup, 8 loops | 404,377 | 435,048 |
| Iteration cleanup, 64 loops | 3,077,970 | 3,211,792 |
| Function cleanup, 1 action | 76,925 | 73,496 |
| Function cleanup, 8 actions | 296,930 | 311,664 |
| Function cleanup, 64 actions | 2,382,289 | 2,444,949 |

The form tag enlarges typed value metadata and verification checks it at ordinary
operation boundaries as well as state operations. At 64 loops, allocation bytes
change from 3,662,092-3,662,292 to 3,681,893-3,682,120 per operation; at 64 actions,
from 2,510,817-2,510,849 to 2,530,512-2,530,543. Allocation counts are unchanged
apart from one allocation of sample variation in the loop benchmark. Ordinary
functions skip state guard analysis, lane-demand propagation, multi-lane phi
indexing and alias-chain canonicalization. The larger ordinary timings are a
measured cost of this representation pilot, not a speed improvement.

Availability chains at 1/64/1,024 states take 6,476-6,922 / 233,566-316,521 /
4,033,275-4,085,771 ns/op. At 1,024 states they allocate 6,234,472-6,234,821 bytes
and 19,338-19,340 allocations. Before demand-driven lane omission, the same
1,024-state benchmark used 6,699,862-6,700,476 bytes and 21,427-21,430 allocations
in three 100 ms samples. The final chain lowers to one block, no machine
operations and the original payload parameter. A presence-only join emits one
presence phi and no payload phi or inactive payload constant. These structural
tests pin removal of avoidable generated work independently of timing noise.

Identical `go build -trimpath -buildvcs=false ./cmd/fern` builds grow from
28,883,426 to 28,900,850 bytes: 17,424 bytes. Mach-O instruction text grows by
9,744 bytes. Symbol attribution includes 1,056 bytes for state operation checks,
2,064 for exact-identity presence proof, 1,328 for lane demand and 752 for the
shared phi builder. The function lowerer grows by 2,784 bytes while its ordinary
operation helper shrinks by 720 bytes after removing duplicate phi handling.
The remaining file growth includes type/debug/PC metadata and alignment. These
are compiler representation, verification and lowering costs, not a generated
program heap environment. No binary-size or performance baseline is changed.

## Conditional-unit measured costs

The reference-bearing extension is measured against `16a6109c3` with the same
native Go 1.26.0 / Darwin ARM64 / Apple M3 Pro setup. Three 100 ms samples use
the existing 1/8/64-loop and action benchmarks. Final medians for loop pipelines
are 69,197 / 408,671 / 3,306,982 ns/op; parent medians are 96,234 / 471,043 /
3,614,199. Action medians are 70,218 / 317,993 / 2,429,210 versus parent
79,780 / 382,290 / 2,551,605. Timing varies substantially in these short runs;
these numbers are cost evidence, not a general speedup claim.

Ordinary allocation counts match the parent within one allocation of variation
in the largest cases: 1,152 / 4,774 / 26,191-26,192 for loops and 1,242 / 3,799 /
20,042-20,043 for actions. At 64 loops the final range is 3,681,824-3,681,977
bytes/op versus 3,681,994-3,682,156; at 64 actions it is 2,530,532-2,530,565
versus 2,530,523-2,530,539. The conditional map is allocated only when needed.
Supply guards use the supplied value's own identity with a compact marker;
ordinary plans do not store another SSA identity per supply.

An initial 8-action result had 3,802 allocations and 333,818-333,831 bytes/op
instead of 3,799 and 333,365-333,377. Exact allocation profiling attributed the
extra three allocations to rewriting unchanged edge-map entries during lowering.
Skipping those writes restored 3,799 allocations and 333,364-333,371 bytes/op
in three 200 ms samples. Conditional CFG splits still update their exit entries.
No proof or cleanup operation was removed to obtain that result.

`BenchmarkConditionalReplacement` measures verification, unit planning and
lowering of the repeated replacement CFG, excluding graph construction and
machine execution. It takes 91,167-129,977 ns/op, 108,851-108,856 bytes/op and
1,278 allocations. The initial implementation used 109,563-109,564 bytes and
1,283 allocations; compact supplies and reuse of the existing edge lookup
removed that avoidable bookkeeping.

Identical compiler builds grow from 28,900,850 to 28,917,970 file bytes (+17,120),
with +2,688 instruction bytes. Guarded execution contributes a 1,344-byte helper
plus the 432-byte conditional-drop dispatcher and its 112-byte callback. Lane
demand grows by 4,128 bytes to account for verified RC uses, while the main unit
verifier shrinks by 4,496 bytes as its typed invariant logic changes Go's code
generation. Remaining file changes include metadata and alignment. This is
compiler machinery for conditional ownership, not a boxed optional-state runtime
allocation. No size or performance baseline changes.

## Remaining connection to cleanup

Conditional payload units now cover projections, snapshots, replacement and
allocator pressure in the typed ARM64 pilot. Binding promotion must next preserve
optional state across partial initialization,
joins and lifetime resets. Registration must keep action identity and capture
availability correlated, and replay must test that same action before reading
late binding values. Source activation remains blocked until that full path is
verified. The definite-initialization fast path remains useful throughout.
