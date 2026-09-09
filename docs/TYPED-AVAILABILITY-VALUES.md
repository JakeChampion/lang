# Typed availability values

This scalar executable slice supplies an internal representation prerequisite
for [conditional cleanup availability](TYPED-CLEANUP-REGIONS.md). It does not
enable source conditional registration or reference-bearing optional state,
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

Booleans and settled integer widths are supported. Zero and false are ordinary
present payloads, never absence sentinels. Reference-bearing payloads are rejected
explicitly until their conditional-unit protocol is implemented and independently
verified. The current counted-unit maps must not silently reinterpret absence as
an unconditional payload owner.

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
machine instructions. This scalar lane analysis is not a reference ownership
proof and must be revisited alongside conditional-unit support.

Multi-lane phis are indexed by their actual physical identities and retain
predecessor order. Forward aliases are canonicalized before rewriting machine
operands. Ordinary single-lane functions retain their original direct phi/fixup
path. Full source locations and payload widths survive lowering.

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

## Measured costs

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

## Remaining connection to cleanup

Reference-bearing state needs conditional payload-unit transfer, borrowing and
reclamation, with no payload obligation on an absent edge. Its independent proof
must cover projections, snapshots, replacement and allocator pressure. Then
binding promotion can preserve optional state across partial initialization,
joins and lifetime resets. Registration must keep action identity and capture
availability correlated, and replay must test that same action before reading
late binding values. Source activation remains blocked until that full path is
verified. The definite-initialization fast path remains useful throughout.
