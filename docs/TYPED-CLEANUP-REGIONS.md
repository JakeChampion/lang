# Typed cleanup regions

Design prerequisite for the [typed ownership migration](TYPED-OWNERSHIP-IR-MIGRATION.md).
This document specifies obligations for its next implementation slice, not an
implemented cleanup pass. The typed-SSA pilot still rejects `defer` explicitly.
Native and self-host production still use their existing cleanup paths.

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
