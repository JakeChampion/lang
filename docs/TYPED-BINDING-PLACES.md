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
verifies the resulting ordinary semantic SSA. Ownership, return provenance,
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
places through joins, iteration resets and replay. Promotion must either prove
initialization dominates the read, preserve a typed optional state without an
absent payload, or split control so payload uses occur only on initialized paths.
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
