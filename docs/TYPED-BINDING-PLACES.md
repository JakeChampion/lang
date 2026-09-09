# Typed binding places

The opt-in typed pipeline now separates source name resolution from binding
value-flow construction. This is a prerequisite for the conditional availability
contract in [typed cleanup regions](TYPED-CLEANUP-REGIONS.md), not its activation
or a production native/self-host cutover.

## Pass boundary

Checked-source construction resolves declarations to function-local BindingIDs
and emits typed `binding_init`, `binding_read` and `binding_replace` operations
on the existing SSA CFG. Binding metadata retains the complete resolved type
and declaration origin; reads and writes retain their operation origins.
The binding is a semantic place, not an address, heap cell or counted unit.
Initialization and replacement have no result; a read produces an ordinary
typed value that can remain live after the binding changes.

An independent initialization verifier runs before promotion. Reads and
replacements require initialization on every incoming path. A finite must-state
bitset analysis intersects predecessor states and reaches a fixed point over
loop backedges. It uses reverse postorder, not source allocation order. A later
loop iteration cannot retroactively initialize the first entry. Each BindingID
has at most one initializer instruction, which may execute on repeated loop
iterations. Repeated declaration execution creates the next value of that
non-addressable binding; no old value is read as part of initialization.

Source bindings also identify their owning typed cleanup boundary. The cleanup
flow verifier requires initialization to occur in that active boundary, not a
foreign boundary or a falsely claimed outer scope. Function and iteration ends
reset initialized state for exactly their own bindings after the block's
operations. Existing verified cleanup-end records supply these events: no AST
rescan, synthetic end instruction or runtime environment is needed. Value blocks
and match arms retain the enclosing cleanup lifetime.

Ending a place does not read or destroy a missing payload. It also does not
invalidate an SSA value read earlier from that place. Saved returns and outer
binding replacements retain their value-flow and ownership obligations. Private
action bindings promote within the action region before expansion; captures
read and update the enclosing function's boundary-owned BindingIDs.

Verified promotion consumes only semantic operations, BindingIDs, types and CFG
edges. It records reads at their exact instruction positions and computes
reaching values with demand-driven phis in predecessor order. Loop recursion
publishes a phi before reading backedges. Read aliases are canonicalized once
so a chain of replacements does not require repeated full-chain walks.
Reaching-definition lookup stops at lifetime ends, rather than recovering a
stale value from an earlier iteration or an exited scope.

The pass removes all binding operations and their value/effect metadata, then
verifies the resulting semantic SSA, including explicit availability values when
requested by the internal snapshot operation. Ownership, return provenance,
containment-aware liveness and RC-unit planning operate only after this phase
transition. Ownership analysis explicitly rejects an unpromoted graph, even
when its individual operations would otherwise be valid.

This replaces the source builder's per-block binding maps, recursive binding
reads and block-sealing machinery. Expression-result phis, typed match decisions,
callee identities and cleanup boundaries retain their existing representation.
Module parameter identities are established with signatures before constructing
any body, so a private action region can verify a forward call without waiting
for the callee's source traversal. Whole-program verification still requires
every body to be built and to agree with that contract.

## Immutable availability snapshots

Promoted observations retain an independent typed semantic contract through
read/phi aliasing and DCE. See [the post-promotion proof and measured costs](TYPED-SNAPSHOT-CONTRACT-2026-09-09.md).

Internal `binding_snapshot` observes one BindingID at its instruction position
and produces `Absent | Present(T)`. Unlike an ordinary read, it can safely observe
an uninitialized place. It does not expose a T payload on that path. Parameters,
ordinary operations and returns still cannot accept an internal availability
value accidentally, and the operation is confined to the unpromoted phase.

Snapshot extraction requires the existing presence proof for that exact immutable
SSA identity. A test of a different snapshot is not sufficient. Ordinary binding
reads and replacements still require must-initialization, even in a branch that
tests a snapshot; no initialization diagnostic is weakened. This is a semantic
operation for later cleanup integration, not a new source feature.

Promotion collects observed BindingIDs during its existing instruction scan.
Only graphs containing snapshots run optional reaching-definition construction.
Before initialization, or after a verified lifetime end, the reaching state is
Absent. A demanded write becomes Present immediately after that write. A join
uses state phis in actual predecessor order, with the result identity cached
before traversing backedges. Ordinary source phis and instruction order remain
intact. A saved snapshot keeps its old value after replacement or scope exit;
a subsequent snapshot observes the new value or absence.

The pass does not wrap every initializer and replacement. It materializes only
definitions demanded by snapshots and shares a wrapper for repeated observations
of the same definition. Construction detaches the original instruction slices
before generating values, appends temporary phis without repeated prefix scans,
then rebuilds each block once with phis first and wrappers at their defining
writes. Absent definitions dominate their uses from entry; no fake payload or
runtime place/environment is introduced. The resulting state values go through
the existing independent conditional-unit proof before guarded RC lowering.

## Guarded replacement

Internal `binding_replace_guarded` publishes an ordinary replacement payload
using a separate immutable snapshot witness. It addresses an explicit BindingID
and disappears during promotion. It introduces neither source syntax nor an
AST ownership heuristic. This is the writeback prerequisite for guarded cleanup
expansion; conditional source cleanup execution remains gated.

The verifier independently requires:

- A direct `binding_snapshot` of the same BindingID, with the same complete
  availability/payload types. An arbitrary Present value, state phi, or snapshot
  of a sibling binding cannot authorize the write.
- The existing exact-state presence proof: the true successor of a StateHas
  test dominates the write, and no alternate predecessor can bypass that guard.
- No end of the binding lifetime between the snapshot definition and the write.
  Snapshot presence alone is insufficient: an old value can remain alive after
  its place has ceased to exist.

SSA dominance supplies the definition/use ordering. A backward CFG walk checks
lifetime edges and stops at the exact snapshot definition. When a loop executes
that definition again, the new observation is valid for the new iteration.
The same witness can authorize a later replacement after another replacement:
it proves continued initialization, not equality with the old value. Ended
lifetimes cannot be resurrected this way.

Writes are grouped by snapshot with flat linked indices. A completed query's
closed predecessor set can serve later writes using that same immutable witness.
Every newly inspected edge checks its lifetime end before reusing a predecessor
entry. Groups and writes retain first-seen order for deterministic diagnostics;
the cache is cleared between witnesses and never survives this verification call.
For S witnesses, W writes and graph size G, this lifetime phase is O(S G + W)
with O(G + S + W) storage, rather than a separate full walk per write.

Ordinary BindingRead/BindingReplace must-initialization checks are unchanged.
Guarded replacement is not an initializer and does not establish a must fact on
absent incoming paths. Both ordinary and optional reaching-definition promotion
use its replacement operand, not its snapshot operand. Later snapshots see the
new value; saved old snapshots keep their own identities and ownership lifetimes.
Ordinary-read payload aliases are rewritten through the existing promotion path.

An independent forward interpreter agrees on 1,514 lifetime queries (922 accepted,
592 rejected), including multiple writes sharing a witness, diamonds, loops,
nested loops and irreducible flow. It uses no backward-walk or lifetime-mask
helper and has no execution-length cap. Full verifier tests separately exercise
scope identity, exact/false/mismatched guards, source diagnostics, phase/type
errors, expired places and fresh loop observations.

Native Linux ARM64 raw/optimized tests cover borrowed/counted inputs, absent
paths, sequential replacement, saved old arrays and allocator pressure. A second
matrix executes 0, 1, 2, 3 and 64 replacement iterations, returning either the
latest array or the original immutable snapshot. Allocation/free balance and
result reference counts are checked by the existing runtime harness.

The lifetime-query oracle alone does not establish initialization or payload
semantics. A separate epoch regression places the snapshot before or after the
current iteration's initializer and records actual guarded-write counts across
0, 1, 2, 3 and 64 iterations. A loop-latch lifetime end makes every pre-initializer
snapshot absent, even though the previous iteration initialized the same place;
post-initializer snapshots are present. Both modes pass promoted-state checks and
20 native ARM64 raw/optimized cases. Disabling lifetime ends in snapshot promotion
makes the absent-state regression fail, independently of the witness verifier's
backward walk. Re-executing a snapshot observes the new lifetime; it does not
restore an old lifetime or imply that the new observation is present.

### Guarded replacement costs, 2026-09-09

Five 100 ms samples on native Darwin ARM64, Apple M3 Pro, measure complete
verification of prebuilt graphs; only the number of guarded writes changes.
A one-write smoke run preceded scaling. Observed ranges are not confidence
intervals or a general speedup claim.

| Writes sharing a snapshot | ns/op | bytes/op | allocs/op |
| --- | --- | --- | --- |
| 1 | 3,002-3,376 | 1,200 | 42 |
| 64 | 97,359-103,050 | 57,320-57,321 | 149 |
| 1,024 | 17,459,521-19,110,056 | 950,974-951,338 | 269-270 |

Grouping removes repeated lifetime walks. A CPU profile of the deep 1,024-write
case attributes most sampled time to the existing depth-based dominance queries
in SSA verification and availability guard verification, not the grouped lifetime
walk. That remaining scaling cost is explicit; this slice does not change the
general dominance implementation or claim large-function verification is optimal.

Ordinary source-to-physical-lowering cleanup samples retain the parent's
1,242 / 3,799 / 20,042-20,043 allocations at 1 / 8 / 64 actions. Candidate times
are 72,261-103,192 / 304,401-332,068 / 2,504,805-2,669,488 ns/op, versus parent
77,562-102,036 / 302,444-322,140 / 2,451,022-2,589,683. These noisy ranges do not
establish a speedup. The guarded-write analysis only runs when the new operation
is present.

Identical `go build -trimpath -buildvcs=false` builds compare parent `a59990d31`
(28,937,042 bytes) with the candidate (28,970,658 bytes): +33,616 file bytes and
+4,624 Mach-O instruction bytes. The rest of the file-size difference includes
data/debug information and segment alignment. The added code verifies the new
guard/lifetime contract and promotes its payload: the lifetime verifier accounts
for 2,848 symbol bytes, the operation/type verifier adds 416 bytes and the two
promotion paths add 208 bytes. Guard dispatch, callers and opcode handling also
contribute. No baseline is changed and
the internal operation introduces no runtime environment or runtime instruction.

## Cleanup integration

Private actions contain their own typed binding operations. Demand-discovered
captures initialize from region parameters at entry, before any action read.
Each action promotes before expansion into its caller. Its protocol yield
therefore still carries ordinary SSA values, not mutable storage references.

At each replay the enclosing function explicitly reads the captured BindingIDs.
These reads and output replacements are inserted by the standalone typed action
expansion pass after source CFG construction, not by source-time action copying.
Pending sites independently require initialized captures, even before ordinary
read operations exist. Binding promotion rejects the pending-expansion phase.
The expanded action writes its outputs back through binding replacements before
the next LIFO action reads them. No registration-time environment copy is added.
Return reads stay distinct SSA values across these replacements; subsequent
ownership analysis retains their arrays or containment anchors as needed.

## Remaining availability work

This slice accepts only definitely initialized reads. It does not turn absence
into a zero pointer, an undefined array value or a fictitious reference-count
unit. Conditional cleanup registration remains explicitly unsupported.

The next phase must preserve correlation between active actions and initialized
places through joins, iteration resets and replay. The internal snapshot path
now preserves optional state without an absent payload; cleanup expansion must
connect that state to verified registration and replay before source activation.
A standalone boolean flag or independent union of available bindings is not
sufficient. Stack storage would require a separate verified lifetime contract;
the current pipeline removes all places before physical RC lowering.

## Validation

Malformed-place tests retain valid SSA while checking reads/replacements before
initialization, branch-local absence, first loop entry, duplicate initializer
identities, invalid BindingIDs and phase escape. Promotion tests reverse block
allocation order and predecessor creation order, preserve a saved read across
replacement and collapse a long read chain to its original value.

Existing typed source, match, effect and cleanup matrices remain the differential
contract, including raw/optimized ARM64 execution, allocator balance and actual
CLI return-snapshot pressure tests. No existing expectation or size baseline is
changed. Compile-time and binary-size measurements accompany publication; this
representation change is not by itself a runtime speedup claim.

Snapshot validation additionally compares promoted presence alternatives with an
independent exact `(block, initialized)` oracle over 168 diamond, loop,
nested-loop and irreducible-cycle cases. The oracle has no iteration bound and
does not use production reaching-definition or dominance helpers. Tests preserve
the initialized payload identity, check 128 independent state phis, and verify
that 64 writes with one final observation need only one Present wrapper.
Malformed snapshot tests reject foreign identity, wrong phase/type/operands,
unguarded or different-snapshot extraction, and ordinary reads/replacements that
remain uninitialized. Native raw/optimized tests cover borrowed/consumed arrays,
absence, loop-carried initialization and saved payloads across replacement and
verified lifetime ends, with allocator pressure at 1 and 64 rounds.

## Snapshot-promotion costs

Review follow-up: rejecting snapshots *inside* unreachable blocks alone does not
make predecessor recursion safe. A reachable read can have an incoming edge from
an unreachable single-predecessor cycle. Both snapshot and ordinary promotion now
prune dead CFG predecessors before following reaching definitions. The existing
initialization verifier reports dead blocks from its RPO outputs when promotion
requests them, without another reachability walk or allocation on all-reachable
graphs. SSA's existing pruning preserves live phi slots; semantic metadata for
removed operations is deleted, and unused optional iteration exits become nil.
Tests cover 1-, 2- and 8-block dead cycles feeding live snapshots/reads, verified
physical lowering, live phi-slot preservation and unused iteration exits. A
snapshot located inside dead code still rejects before mutation. This fixes a
verified-input recursion hazard; it does not relax initialization requirements.

On the same Darwin ARM64 build configuration, this correction adds 144 compiler
file bytes and 640 Mach-O instruction bytes relative to `517aa70fe`; the pruning
adapter accounts for 432 symbol bytes. Five 100 ms ordinary cleanup samples retain
1,242 / 3,799 / 20,042-20,043 allocations for 1 / 8 / 64 actions. Their time ranges
are 70,571-75,969 / 301,810-321,194 / 2,467,101-3,156,869 ns/op, versus the control's
70,764-72,843 / 303,995-422,966 / 2,442,929-2,618,486. These observations do not
establish a speedup. No baseline is changed; dead-edge normalization adds no
runtime code or allocation on all-reachable input graphs.

Measured against `9a360bd34` using Go 1.26.0 on native Darwin ARM64 / Apple M3 Pro.
Three 100 ms samples follow a one-write smoke run. The snapshot benchmark includes
synthetic typed graph construction, promotion, verification, ownership planning
and ARM64 SSA lowering; it excludes machine optimization and execution.

| Writes, one final snapshot | ns/op range | Bytes/op range | Allocations/op |
| --- | ---: | ---: | ---: |
| 1 | 8,310-9,207 | 13,832 | 171 |
| 64 | 18,674-18,910 | 34,845-34,846 | 316 |
| 1,024 | 174,484-239,070 | 346,006-346,010 | 2,255 |

Existing ordinary loop and cleanup-action pipelines retain their allocation
counts and bytes within sampling variation. At 64 loops, parent and snapshot
versions both use 26,191-26,192 allocations; bytes are 3,681,896-3,682,006 versus
3,681,903-3,682,013. At 64 actions they use 20,042-20,043 versus 20,042 allocations,
with 2,530,518-2,530,585 versus 2,530,538-2,530,553 bytes. The ordinary path does
not allocate snapshot maps or perform a second graph scan.

Parent versus snapshot median times for 1/8/64 loops are 70,417 / 458,370 /
3,299,242 versus 69,927 / 432,997 / 3,122,824 ns/op. Action medians are
70,719 / 303,577 / 2,579,715 versus 70,098 / 327,434 / 2,482,760 ns/op. These short
samples vary in both directions and do not establish a general throughput win.

Identical `go build -trimpath -buildvcs=false ./cmd/fern` builds grow from
28,917,970 to 28,935,826 bytes (+17,856), including +7,152 instruction bytes.
The new optional-promotion function and its closures account for 5,776 instruction
bytes; the existing promotion entry grows by 160 and operation verification by
368. Remaining growth includes opcode handling, type metadata and alignment.
This cost implements optional reaching definitions and validation rather than a
boxed runtime environment. No binary-size or performance baseline is changed.

## Initial cost measurements, 2026-09-08

Five 100 ms samples on native Darwin ARM64, Apple M3 Pro, measure checked-source
construction through verified physical lowering, excluding frontend checking and
machine optimization/emission. Only loop/action count changes within each family.
These are observed ranges, not confidence intervals or a general speedup claim.

| Workload | ns/op | allocs/op |
| --- | --- | --- |
| 1 iteration action | 64,345-66,897 | 1,106 |
| 8 iteration actions | 394,373-482,269 | 4,682 |
| 64 iteration actions | 3,750,647-3,929,324 | 27,673-27,674 |
| 1 function action | 68,016-70,222 | 1,200 |
| 8 function actions | 298,989-379,376 | 3,651 |
| 64 function actions | 2,307,929-2,459,646 | 19,353-19,354 |

Parent allocation counts were 1,040 / 4,402 / 26,140 for the iteration family
and 1,122 / 3,279 / 17,143-17,144 for function actions. Additional storage holds
explicit typed operations and independent initialization/promotion data. Early
timing samples overlapped other validation and are not used for a speed comparison.

An intermediate version reverified promoted functions before the already-required
whole-program phase-exit verification. Removing that duplicate pass reduces
one-loop allocations from 1,167 to 1,106 and 64-function-action allocations from
20,739-20,740 to 19,353-19,354. Pre-promotion validation and whole-program
verification of promoted bodies/actions remain mandatory before ownership.

With identical `go build -trimpath -buildvcs=false` commands, the compiler grows
from 28,849,682 to 28,866,498 bytes against parent `7fb8fd11c`: 16,816 bytes overall.
Mach-O `__text` grows by 7,088 bytes. The new initialization verifier is 3,488
symbol bytes and promotion plus its helpers is 3,840; the old builder binding
read/fill/seal functions, totaling 1,904 bytes, are removed. Caller and metadata
layout changes contribute to the net totals. No size baseline is raised. This
replaces source-time machinery with verified IR passes; native/self-host
production ownership retirement remains future work.

## Independent initialization oracle and scaling check

An exact test oracle explores `(block, initialized)` states without using the
production bitset analysis or bounding loop iterations. All 168 initializer/read
placements across diamonds, loops, nested loops and irreducible cycles agree
with the verifier. Every accepted graph also promotes and passes ordinary SSA
verification. Both same-block instruction orders are tested.

Single-initializer dominance was tested against `2897b8a78` as an alternative
proof, before binding lifetime ends were introduced. It was equivalent for that
earlier slice, which had no absence/reset operation. The prototype agrees with
the same oracle, but the existing dominance query walks an ancestor chain on
each read. Three 100 ms native samples of `BenchmarkVerifyBindingInitialization`
gave the following results; this benchmark includes full semantic verification.

| Bindings and blocks | Existing bitset ns/op | Dominance prototype ns/op |
| --- | --- | --- |
| 1 | 779.3-925.2 | 723.6-735.8 |
| 64 | 35,886-38,151 | 65,412-68,517 |
| 1,024 | 740,469-752,859 | 9,247,705-11,969,135 |

The larger case allocates 900,205-900,317 bytes per verification with bitsets and
844,948-845,844 bytes with the prototype. The small memory reduction does not
justify the deep-CFG time regression. The prototype is not retained; the exact
oracle and benchmark are. Future alternatives must preserve the proof and be
measured, including the tradeoff between CFG depth and initialization-set size.

Lifetime ends now provide an additional reason dominance alone is insufficient:
an initializer can dominate a read but its place can have ended in between.
The exact oracle now interprets these reset events directly. Regressions prove
that stale reads and replacements would pass initialization-only verification,
but fail once lifetime ends are respected; saved SSA values and outer binding
updates remain valid.

## Lifetime-end cost measurements, 2026-09-08

Five 100 ms samples on the same native Darwin ARM64 host measure the same full
typed compilation pipeline. Only action/loop count changes within each family.
These are observed ranges, not a general speedup claim.

| Workload | ns/op | allocs/op |
| --- | --- | --- |
| 1 iteration action | 65,570-70,039 | 1,114 |
| 8 iteration actions | 391,221-445,304 | 4,714 |
| 64 iteration actions | 3,755,977-3,977,891 | 27,705-27,707 |
| 1 function action | 67,963-75,685 | 1,208 |
| 8 function actions | 279,842-304,969 | 3,659 |
| 64 function actions | 2,282,737-2,380,404 | 19,361-19,362 |

Against the prior place implementation, the function-action family adds eight
allocations per compilation for lifetime masks and indexing. The iteration
family adds eight at one loop and more at larger CFGs. Masks remain compiler
metadata; there is no generated runtime allocation. Initializer-scope checks
run only before promotion, when initializer operations still exist.

Identical `go build -trimpath -buildvcs=false` builds grow from 28,866,498 to
28,883,218 bytes against parent `2897b8a78`: 16,720 bytes overall and 2,512 bytes
in Mach-O `__text`. The lifetime-mask constructor contributes 944 symbol bytes;
identity validation, initialization transfer, active-scope checking and promotion
barriers account for the remaining code changes. Data/debug/segment layout also
changes. No size baseline is raised and no native/self-host cutover is claimed.
