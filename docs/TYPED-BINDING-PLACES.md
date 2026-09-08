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

Verified promotion consumes only semantic operations, BindingIDs, types and CFG
edges. It records reads at their exact instruction positions and computes
reaching values with demand-driven phis in predecessor order. Loop recursion
publishes a phi before reading backedges. Read aliases are canonicalized once
so a chain of replacements does not require repeated full-chain walks.

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
