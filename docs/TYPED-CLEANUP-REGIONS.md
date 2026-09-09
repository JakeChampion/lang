# Typed cleanup regions

Design prerequisite for the [typed ownership migration](TYPED-OWNERSHIP-IR-MIGRATION.md).
This document specifies the full cleanup migration contract. The typed-SSA
pilot now implements bounded function and iteration actions described below, not
the complete registration/availability model. Native and self-host production
still use their existing cleanup paths. Conditional function/iteration actions
now execute through verified typed activation and late optional captures; see
[conditional execution](TYPED-CONDITIONAL-CLEANUP-2026-09-09.md) for the current
pass contract, validation and measured costs. Dated sections below retain the
measurements and restrictions of their original implementation slices.

## Executable action slice

Plain function and iteration actions with a registration that dominates each replay
now build once into a private typed SSA region. Capture parameters and yield
fields address enclosing BindingIDs rather than storing registration-time
values. Expansion reads the current bindings immediately before each action,
copies its typed CFG, and publishes its binding outputs for the next action.
The tuple yield is only a region interface: expansion removes it before
ordinary ownership planning, so it creates no runtime environment or tuple.
Saved return values remain ordinary live SSA values across cleanup.

Expansion is now a standalone typed-IR pass, not a source-builder operation.
The source producer resolves names and constructs each private action once,
then records invocations as empty CFG sites carrying their continuation. It
does not copy action operations while visiting exits. Once the complete source
CFG exists, verification checks registration/boundary state, private-region
phase, site shape and definite initialization of every captured binding.
Invalid input is rejected before expansion mutates the graph.

The expansion pass has no source syntax or checker input. It reads the current
BindingIDs at each site, installs the already-typed action CFG and publishes
simultaneous binding outputs before continuing. It reuses the site as the action
entry, so phase separation adds no continuation-only blocks. Multi-block actions
move the original continuation to the action exit, preserving successor phi
operand order. Boundary-end and exit-finish identities relocate in one pass,
rather than scanning every exit for every replay. Source positions, resolved
types and call identities remain attached to their copied operations.

An explicit pending-expansion phase cannot enter binding promotion or ownership
analysis. Expanded place graphs still pass verification before promotion;
promoted graphs still pass whole-program verification before ownership. This
separates the pass boundaries needed by conditional availability. The pre-expansion
verifier now admits safe conditional registration using the correlated proof below;
expansion implements it using typed activation, late capture guards and guarded
writeback, all independently reverified after promotion.
Registration admission uses
the complete typed CFG. The source builder no longer computes dominance at each
exit while control flow is still under construction.

The verifier checks action types, capture identities/contracts, final yields,
registration dominance and exact registration state across every CFG edge. Region
branches and local loops reuse the existing source producer; source locations,
call identities and ownership operands survive expansion. No AST action is
retained for replay or ownership analysis. Raw and optimized ARM64 execution
tests include late replacement, action-to-action writes, simultaneous binding
outputs, returned projections, own/borrow aliases and allocator balance.

Registration pushes a pending action; replay must consume the most recent one,
and returns require an empty pending stack. Joins and backedges must have equal
incoming registration states. An independent registration history prevents a
cycle from repeatedly registering and consuming a function action while leaving
the pending stack deceptively empty. A nonreturning path may still carry a
pending action without executing it. Canonical persistent stack nodes make
push/pop and state equality efficient without copying each action list at
every CFG edge. Each reachable block is processed once; incompatible paths are
rejected, not unioned into a state that loses registration correlation.

Malformed CFG tests demonstrate why return dominance was insufficient:
direct and indirect replay cycles, replay in a cycle without a return, and a
balanced registration/replay cycle previously passed verification. An additional
case rejects a join of pending and consumed states even without a return. The graphs
retain valid SSA, so the independent cleanup proof must reject them. The executable
ordinary path retains these exact-state restrictions. The conditional proof
below instead preserves small correlated state sets.

Function and iteration boundaries now carry explicit identities, lexical parent
links and CFG entry/header/exit points. Every action belongs to one boundary.
Typed exit records identify the exact boundary suffix crossed by normal tails,
local or labelled break/continue, and function return. Each boundary closes only
after its own LIFO actions finish. Its saved registration-state prefix is then
restored, allowing the next iteration to register again without leaking completed
actions into a later function return. Value blocks and match arms do not introduce
cleanup lifetimes. Loop conditions retain the enclosing loop's jump targets.

The verifier independently checks boundary structure against active CFG state.
An internally consistent but false parent chain is rejected. Missing actions,
missing exits, wrong targets and incomplete or reversed boundary sequences also
reject while preserving valid SSA. State equality at joins and backedges remains
exact; only a verified boundary close resets registration history.

Registration in loop conditions, error-only actions, and nonlocal control/nested
registration inside actions remain explicit unsupported contracts. The place
model below is still required for the broader migration. This slice does not
activate the seven conditional/iteration conformance cases as typed-pilot tests
or establish self-host parity or production cutover.

## Action-slice validation, 2026-09-08

Seventeen source cases pass typed ownership planning and raw/optimized ARM64
execution with balanced allocation/free counts. Fifteen malformed-region cases
exercise independent verification. The actual CLI regression keeps a returned
array alive across cleanup replacement and subsequent allocator churn. Full
semantic IR, source-lint and CLI packages, targeted race tests and `make lint-all`
pass. This is not a full integration or self-host parity result.

On native Darwin ARM64, Apple M3 Pro, five 100 ms samples of
`BenchmarkTypedCleanupActions` measured 54,799 to 58,737 ns/op for one action,
232,653 to 247,718 ns/op for eight, and 2,461,662 to 3,222,720 ns/op for 64.
Allocation counts were 950, 3,048 and 16,804 to 16,805 per compilation. The
benchmark covers checked-source production through verified physical lowering,
excluding frontend checking and machine optimization/emission. Only action count
changes. These are initial scaling measurements, not a speedup claim.

With `go build -trimpath -buildvcs=false` on the same host, the Go compiler
grows from 28,780,642 to 28,798,530 bytes compared with the contract-only parent.
The five newly linked action construction, expansion and verification functions
account for 13,024 symbol bytes of the 17,888-byte total growth; existing caller
changes and metadata also contribute. The new code implements the typed cleanup
contract. No compiler-size baseline is changed. Region interfaces introduce no
runtime environment allocation, independently checked in the executable graph.

Review follow-up coverage pins cross-action binding reads through cloned joins:
the checking action captures a scalar or array projection that the preceding
branching action does not capture. A third case crosses successive branching
actions. Both branch choices execute, and structural assertions ensure these
cases cannot silently turn into direct capture-output forwarding tests. The
shared matrix checks interpreter results, typed unit planning, physical lowering,
raw/optimized ARM64 execution and balanced allocation counts. This adds coverage,
not a compiler behaviour change or a performance claim.

## Registration-state proof measurements, 2026-09-08

On the same Darwin ARM64 host and benchmark configuration used above, five
100 ms samples compare the parent dominance verifier with the exact-state
verifier. Only the action count changes between workloads. Times are observed
ranges, not a general speedup claim or statistical confidence interval.

| Actions | Parent ns/op | Exact-state ns/op | Parent allocs/op | Exact-state allocs/op |
| --- | --- | --- | --- | --- |
| 1 | 54,781-59,303 | 57,156-66,107 | 950 | 971 |
| 8 | 248,649-267,967 | 234,005-243,689 | 3,048 | 3,132 |
| 64 | 2,443,075-2,555,932 | 1,920,700-2,491,410 | 16,804-16,805 | 17,057-17,058 |

The indexed arena avoids the initial proof implementation's per-node heap
allocation: at 64 actions it measured 17,057-17,058 allocations instead of
17,539-17,540. The remaining additional state storage buys the stronger edge
proof. These benchmarks include all repeated verification in the typed pipeline,
not just one isolated verifier call; non-cleanup functions retain the fast path.

With identical `go build -trimpath -buildvcs=false` commands, the compiler grows
from 28,798,530 to 28,815,730 bytes, a 17,200-byte increase. Mach-O `__text`
grows by 2,752 bytes: the new flow verifier is 4,032 symbol bytes and the old
verifier shrinks by 1,280. Type/constant data, line/debug information and segment
alignment also change. No baseline is raised. Program cleanup generation and
runtime code are unchanged; this change strengthens compilation-time validation.

## Iteration-slice validation and costs, 2026-09-08

Eleven additional source cases execute in the raw and optimized ARM64 matrix
with balanced allocation/free counts. They cover normal tails, local/labelled
exits, early exits before registration, late binding replacement, value blocks,
return projections and an outer jump from an inner loop condition. The CLI
regression preserves a returned array across nested cleanup and allocator churn.
Twenty-two malformed-boundary tests retain valid SSA. Separate tests prove that
false active parents and balanced but unclosed iteration cycles reach and fail
the independent flow proof rather than only a structural metadata check.

Five 100 ms samples on native Darwin ARM64, Apple M3 Pro, use the same checked
source-to-verified-physical-lowering pipeline as the action benchmark. The new
benchmark changes only the number of sequential single-action loops. Observed
ranges are not statistical confidence intervals or a general speedup claim.

| Loops | ns/op | allocs/op |
| --- | --- | --- |
| 1 | 56,796-67,824 | 1,040 |
| 8 | 344,980-479,303 | 4,402 |
| 64 | 3,392,352-3,896,861 | 26,139-26,141 |

Consolidating structural and flow events into one indexed arena reduced the
initial implementation's allocations from 1,110 to 1,040 for one loop, 4,605
to 4,402 for eight and 26,679-26,680 to 26,139-26,141 for 64. The existing
function-action workload now measures 1,122 / 3,279 / 17,143-17,144 allocations
at 1 / 8 / 64 actions, against the parent 971 / 3,132 / 17,056-17,057. Unlike
the preceding slice, source functions without actions also carry and verify
their boundary contracts. Remaining storage tracks entry, parent, exit and
saved registration history, including through action-free nested loops.

Identical `go build -trimpath -buildvcs=false` builds against parent `fe77ee32a`
grow from 28,815,730 to 28,866,178 bytes: 50,448 bytes overall and 8,752 bytes
in Mach-O `__text`. The new boundary verifier accounts for 5,344 symbol bytes;
the flow verifier grows by 1,776 bytes and exit emission by 496 bytes while
the action verifier shrinks by 48 bytes. Caller changes and data/debug/segment
layout account for the rest. This implements and independently verifies new
iteration semantics; it does not add a runtime cleanup environment or change
a compiler-size baseline. Production native/self-host cutover remains pending.

## Standalone expansion validation and costs, 2026-09-09

Five malformed pending-action cases reject before graph mutation. Additional
tests reject promotion/ownership phase escape and repeated expansion, preserve
continuation phi ordering with reversed predecessor order, pin the exact added
action-block count and leave action-free graphs unchanged. Two conditional cases
prove source construction completes before the typed admission diagnostic.
Existing cleanup-cycle
tests pass unchanged. Full semantic IR, source-lint and CLI packages, targeted
race tests and lint pass. Strict Linux ARM64 raw/optimized typed runtime and
actual CLI tests pass with the existing balanced allocator census expectations.
Full integration and self-host CI remain separate publication gates.

Five 100 ms samples on native Darwin ARM64, Apple M3 Pro, compare the same
checked-source-to-physical-lowering workloads against parent `0b92aba21`. The
one-loop smoke run preceded scaling. Only loop/action count changes. These are
observed ranges, not confidence intervals or a speedup claim.

| Workload | Parent ns/op | Separate expansion ns/op | Parent allocs/op | Separate expansion allocs/op |
| --- | --- | --- | --- | --- |
| 1 iteration action | 66,083-68,159 | 68,876-92,404 | 1,114 | 1,152 |
| 8 iteration actions | 395,663-506,972 | 402,733-435,927 | 4,714 | 4,774 |
| 64 iteration actions | 3,740,241-4,059,020 | 3,091,099-3,301,443 | 27,705-27,707 | 26,191-26,193 |
| 1 function action | 68,130-73,216 | 71,880-96,611 | 1,208 | 1,242 |
| 8 function actions | 293,676-354,932 | 300,105-311,351 | 3,659 | 3,799 |
| 64 function actions | 2,342,470-2,460,950 | 2,440,658-2,591,185 | 19,361-19,362 | 20,042-20,043 |

The independent pre-expansion verification and site/capture indexing add
compiler allocations at small scales and in the function-action family. Moving
registration admission to the completed CFG removes repeated per-exit dominance
construction: the 64-loop workload now takes less time and allocation space than
the parent in these samples, despite the added verification. The function-action
family does not show the same improvement; this is not a general speedup claim.
Expansion reuses invocation blocks, adds no runtime protocol
allocation, and relocates all boundary endpoints with a single linear scan.
An intermediate continuation-block scaffold was removed before measurement;
the retained regression prohibits reintroducing those unnecessary blocks.

Identical `go build -trimpath -buildvcs=false` builds measure 28,883,218 parent
bytes and 28,883,426 candidate bytes: 208 bytes net growth. Mach-O `__text`
grows by 2,976 bytes; segment padding makes total file growth much smaller than
code growth. The old builder expansion method (3,648 symbol bytes) is removed;
the standalone site expansion is 3,792 bytes and its phase driver is 816 bytes.
The remaining code changes enforce site shape, capture availability and consumer
phase gates. No baseline is changed. Production native/self-host AST ownership
retirement and conditional cleanup activation remain outstanding.

## Correlated pre-expansion admission

Conditional pending graphs now have an independent typed proof. Structural
verification still checks the private action interface, binding identity, empty
invocation sites and explicit lifetime ends. An exact active-boundary analysis
rejects false scope parents, mismatched joins, stale initializers and unreachable
events. Only registration state is allowed to differ across paths.

The proof checks finite projections of CFG traces:

- One action has never-registered, pending and replayed states. Re-registration
  and repeated replay reject; history resets only at its own boundary end.
- A pair of actions additionally records which registered last while both are
  pending. Replaying the older one while the newer remains pending rejects.
- An action and each captured BindingID track registration and initialization
  together. An active replay with an absent capture rejects. An inactive replay
  neither reads nor initializes its capture. Lifetime ends clear only the
  corresponding registration or binding component.

The CFG worklist unions complete product states, not independent may-flags.
It terminates because each pair has at most 18 encoded states per block, and
each action/capture product has six. There is no path-length or iteration cap.
Every LIFO violation has a witness consisting of the replayed action and an
outstanding newer action; no triple or whole-stack subset is needed for that
property. Single-action checks cover missing/repeated replay independently.
All CFG paths are considered, including infeasible combinations of ordinary
branch predicates: this is not symbolic reasoning about source boolean values.

For A actions, C total capture occurrences, and graph size G including operations
and edges, the bound is O((A squared + C) G) time with O(G) reusable projection
storage, in addition to existing IR/event metadata. The existing canonical-stack
path is retained for functions with only dominating registrations; it does not
incur these pairwise walks. Guarded executable graphs use the correlated proof
too, with initializer events retained after binding promotion.

Ordinary BindingRead and BindingReplace still require initialization on every
incoming path. Conditional action sites use the correlated capture proof.
The admission result is ephemeral and cannot survive graph mutation as a cached
certificate. Expansion receives it from the same verification call, avoiding a
second dominance traversal. Pending actions cannot enter binding promotion or
ownership lowering; they must first become verified executable dispatches.

Guarded expansion now materializes exact action activation, takes late immutable
binding snapshots at each replay, guards extraction and publishes sequential
outputs without inventing absent payloads or relaxing ordinary write preconditions.
The original admission slice below did not enable source execution; the subsequent
[executable integration](TYPED-CONDITIONAL-CLEANUP-2026-09-09.md) does. Neither
slice retires production AST analyses.

Validation includes branch-local and mutually exclusive actions, late replacements,
nested iteration resets, labelled exits, returns and nonreturning paths. Negative
tests cover wrong LIFO, missing/duplicate replay, initialization on the opposite
branch, expired inner captures and unchanged ordinary must-initialization errors.
An independent full-stack interpreter agrees on 11,520 finite-state comparisons
(181 accepted and 11,339 rejected), using all permutations of three registration
and three replay events across eight graph families, with and without a captured
place. These include bypasses, joins, cycles and lifetime resets. The oracle uses
complete stacks/history and no production transfer or fixed-point helper. That
permutation corpus has one scope, so it does not establish nested-scope coverage.

A separate source-derived nested corpus adds 960 comparisons (265 accepted and
695 rejected), deleting every combination of action registration, replay and
captured-place initialization events. It covers outer actions pending across an
inner close, nested iteration with outer and inner captures, three-level labelled
breaks, continues and returns. The interpreter maintains initialization separately
for every binding and resets only actions and places owned by an ending boundary.
Thus an inner close preserves an outer pending action and its captured value,
while an inner binding cannot remain initialized across an iteration reset.
An event index built directly from the typed contract avoids sharing the
production indexing, lifetime-mask or transfer helpers. Intact source fixtures
also pass full semantic verification; generated mutations compare event traces,
not structural SSA or scope admission. Restoring the original whole-stack-empty
end check makes the outer-pending/inner-close regression fail as expected.

## Correlated-admission costs, 2026-09-09

Five 100 ms samples on native Darwin ARM64, Apple M3 Pro, measure complete
`Verify` calls on prebuilt conditional graphs. A one-action smoke run preceded
scaling; only action count changes. These are observed ranges, not confidence
intervals or a runtime speedup claim.

| Conditional actions | ns/op | bytes/op | allocs/op |
| --- | --- | --- | --- |
| 1 | 4,458-4,817 | 3,193-3,194 | 63 |
| 8 | 67,057-81,021 | 43,465-43,467 | 344 |
| 64 | 13,498,521-14,028,984 | 359,593-359,609 | 1,882 |

The pairwise proof's scaling cost is explicit: it is not yet a claim of cheap
admission for large action sets. Whole-stack enumeration is avoided, and every
worklist has a finite bound independent of execution length. One flat successor
arena replaces per-block slices, reducing initial allocation counts from
67 / 383 / 2,201 to 63 / 344 / 1,882. Pair walks already check both individual
histories, so separate single-action walks are omitted unless there is only one
action. Sharing that call site also removes a duplicated compiled transfer body.

The existing source-to-physical-lowering cleanup benchmark retains
1,242 / 3,799 / 20,042-20,043 allocations for 1 / 8 / 64 ordinary actions, matching
parent `8ce8a3eae`. Integrated samples before the conditional-only walk refinement
measured 70,890-75,409 / 306,386-384,964 / 2,454,962-2,547,326 ns/op. The parent
measured 70,571-75,969 / 301,810-321,194 / 2,467,101-3,156,869. These noisy ranges
do not establish a speedup; the existing executable route remains unchanged.

Identical `go build -trimpath -buildvcs=false` builds measure 28,935,970 parent
bytes and 28,937,042 candidate bytes, a 1,072-byte net increase. Mach-O instruction
size increases by 8,208 bytes. The new correlated scope/projection functions and
transfer bodies account for 7,712 symbol bytes; caller/indexing changes account
for the remaining instruction difference. Consolidating the pair call site
removed 832 instruction bytes and avoided crossing a segment-alignment boundary
in this build. Data, debug information and alignment explain why net file growth
differs from instruction growth. No compiler-size baseline changes, runtime
environment allocation or source conditional activation are included.

## Observable contract

The existing language contract is in the `defer` section of
[LANGUAGE-DIRECTION.md](LANGUAGE-DIRECTION.md). The interpreter's `runDefers`
and `runIterDefers`, native IR cleanup emitters, and self-host parser cleanup
lowering provide the current implementations. The shared conformance corpus
must constrain the replacement, rather than taking a new backend as an oracle.

- Registration happens only if execution reaches the statement. An unentered
  conditional or an earlier exit must neither run an action nor read its locals.
- The cleanup boundary is the enclosing function or enclosing loop iteration.
  Leaving a conditional, match arm or value block is not itself that boundary.
- Actions resolve original lexical binding identities and read their values
  when they run. Sibling bindings with the same spelling remain independent.
- Replay is sequential: an action's binding writes are visible to subsequent
  actions that read those same bindings. Capturing all operands at registration,
  or even all operands at the start of cleanup, changes this behaviour.
- Return expressions are evaluated before cleanup. Their resulting values remain
  live across cleanup even when an action replaces the source binding.
- Normal iteration tail, break and continue consume that iteration's pending
  registrations. Labelled exits clean exited iterations from the inside out.
  Completed iterations must not replay again on a subsequent function exit.
- A return or propagating `?` during an iteration exits the function with the
  registrations still pending for that exit. Plain actions replay LIFO, followed
  by error-only actions LIFO on an error exit. Normal iteration completion
  discards error-only registrations without running them.

The `defer_binding_*` conformance cases isolate the binding and availability
requirements with reference-bearing arrays. They cover conditional locals,
sibling identities, cleanup-to-cleanup replacement, returned snapshots, labelled
continue, value-block timing and exits before registration. Expected results
are derived from the rules above; no existing expectations are changed.
The same files are discovered by native and self-host fixture runners. They
have no target exclusions or known-divergence allowances.

## Representation boundary

An action must become a typed region once, before ownership analysis. It needs
an action identity, source position, cleanup-boundary identity and explicit
registration and replay edges. Its operations retain complete checked types,
callee identities and own/borrow contracts, just like ordinary semantic IR.
Region-local bindings are distinct from captured bindings. Free-name resolution
belongs to checked-source production; ownership must not rescan the AST.

Captures identify bindings, not registration-time SSA values. For example:

```fern
var items: i32[] = [2];
defer seen = items[0];
defer items = [9];
```

The two regions share one binding identity for `items`. The second region's
write precedes the first region's read. A separate captured copy per action is
incorrect, even if every copy has the right element type and reference count.

Registration is not an ownership transfer to every action. Keeping a binding
available may require retaining its current value, but multiple readers must
not manufacture independent permanent captures. Replacing the binding ends the
old binding-held lifetime only when other ordinary values or projections no
longer need it. A saved return value has its own obligation across this write.

## Conditional availability

A boolean active flag does not prove that a branch-local value dominates a
later replay block. In particular, forming a phi with a fabricated zero or
undefined `i32[]` on the unentered path is invalid semantic IR. Machine-level
storage being zero-initialized is not a proof that a Fern value exists there.

The planned representation is a typed, function-local binding place with
explicit uninitialized versus initialized state, shared by the actions and
ordinary operations that address that binding. This is an internal semantic
place, not a new user-visible mutable heap reference. Registration records
availability of the places an action needs. Places for a conditional local
exist structurally, but do not contain a value before its initializer executes.
Read, replace and destruction operations require proof of initialized state.
The active registration and its available environment must stay correlated
through joins, iteration resets and replay; independent unions are insufficient.

This representation must not force heap allocation or keep the original array
alive after every replacement. Promotion can turn places into ordinary SSA
values when initialization dominates all reads. When paths differ, promotion
must preserve a typed optional state or split control so a payload is used only
on its initialized path. The absent state contains no fake value of the payload
type and carries no counted unit. Stack storage remains a possible physical
implementation, but requires its own verified initialization and lifetime
contract before admission to RC lowering.

The current value-only unit planner cannot simply be told to accept place
operations. It must first understand their value-flow and lifetime effects,
or consume a verified promotion that removes those operations. Verification
must independently reject reads or drops of absent values, foreign binding
identities, stale registrations, duplicate cleanup execution, missing writes
between actions and unbalanced exit paths.

## Implementation and retirement gates

1. Pin behaviour across existing implementations before activating registration.
   New fixtures must retain exact outputs and the allocation census must be
   measured, not assigned an assumed baseline.
2. Add typed action/boundary/place identities and their verifier, including
   malformed-region and path-correlation tests. Resolve capture identities
   during ordinary source binding resolution, not an ownership syntax scan.
3. Construct explicit registration and cleanup control flow, then promote or
   plan place lifetimes before physical RC. Replay consumes registration and
   threads writes through the next action. Reuse the existing effect-only CFG
   builders for action contents and retain source locations through expansion.
4. Run the new source cases through the typed pipeline before and after machine
   optimization, with allocation/free balance, projection lifetime and return
   snapshot checks. Add error-exit and nested-boundary coverage beyond the
   binding-focused cases here.
5. Apply the same region contract in the self-host pipeline. Remove parser
   cleanup expansion only after its production replacement has semantic,
   diagnostic and target parity. Delete obsolete AST ownership consumers only
   after production cutover, as required by the migration's retirement inventory.

Control transfer or nested registration *inside* a deferred action still needs
an explicit cross-engine audit. The interpreter currently ignores evaluation
errors from actions; that is not sufficient evidence to define the semantics of
`return`, `break`, `continue`, `?`, or nested `defer` within an action. Do not
silently translate those forms into an ordinary action return, suppress effects,
or claim full cleanup support before resolving their contract. The typed pilot
must continue to reject any unimplemented form explicitly.
