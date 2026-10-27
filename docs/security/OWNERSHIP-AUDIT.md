# Ownership and view escape audit

Status: [reference] probe record for [SEC-004](https://github.com/JakeChampion/lang/issues/13506).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).

Each probe is a short program with one suspect pattern.
Both checkers ran each probe.
A probe that both checkers accept also ran natively under `-sanitize -g` with a heap array.
The probes live in the normative corpus where a rule was missing, and in this record otherwise.

## Refused by both checkers

| Pattern | Code |
|---|---|
| An owned parameter used after a consuming call in one branch of a loop | `E050` |
| Two consuming calls of one owned parameter in one expression | `E050` |
| An owned parameter stored in a struct literal, then consumed | `E050` |
| One owned parameter passed to two owned parameters of one call | `E050` |
| An owned parameter consumed inside a `for` body | `E050` |
| An owned receiver method called twice in a loop | `E050` |
| An owned parameter consumed, then returned on a later path | `E050` |
| An owned parameter moved into a struct argument, then used | `E050` |
| An owned parameter consumed in a match arm that does not diverge, then used | `E050` |
| A local array passed to an owned parameter | `E051` |
| A struct field moved out through a consuming call | `E051` |
| An array element moved out through a consuming call | `E051` |
| A slice of a local array returned | `E063` |
| A view of an array that is then updated | `E043` |

## Repaired in the self-host checker

A closure body that consumes a captured owned parameter moves it for the enclosing function.
The Go oracle refused a later use with `E050`.
The self-host checker discarded the closure's moves and accepted the use.
The ownership walk now propagates the closure body's moves and restores only the closure's own parameters.
The normative case is `diag_e050_closure_consumes_captured_owned`.

## Divergence left for a language decision

A `for` loop over an owned parameter, followed by a later use of that parameter, splits the checkers.
The Go oracle counts the iteration as a consuming move and refuses the later use with `E050`.
The self-host walk borrows the iteration source and accepts the later use.
[MODE-LATTICE.md](../MODE-LATTICE.md) does not state which rule holds.

Three probes show the split.
One appends to a scalar array after the loop.
One reads a scalar array after the loop.
One appends to a struct array after the loop, inside a nested loop and a match.
The lowering keeps the value alive through the loop, so the permissive side is sound at run time.
The strict side rejects a natural pattern that `compiler/semlower.fern` on #13646 writes.

The owner must pick one rule.
Then one checker changes and a normative case pins the rule.

## Accepted by both checkers, sound at run time

| Pattern | Observation |
|---|---|
| A `str` view of a borrowed string parameter returned inside a struct, with the argument a temporary | The view keeps its storage alive. The bytes survive eight same-size allocations. |
| A `str` view taken, then the owner reassigned | Same. The old storage stays alive for the view. |
| A closure returns a `str` view of its own local string | Same. The view keeps the local's storage alive. |
| A deferred action reads an owned parameter that a consuming call took before the return | The lowering keeps the value alive until its real last use. The sanitizer reports balanced allocations and no quarantined touch. |
| A closure captures an owned parameter, the function consumes it, then calls the closure | Same. The capture holds a reference, so the consuming call does not release the storage. |

The first three follow the view rule in [STR-VIEW-CONTRACT.md](../STR-VIEW-CONTRACT.md): a view keeps its storage alive.
The last two are permissive at the checker and sound in the lowering.
A stricter checker rule for them is a language decision, not a defect.

## Limits

These probes are scoped evidence for the current Perceus and borrow implementation.
They do not prove a complete alias or lifetime model.
The runtime observations use one allocator and one target, x86-64 Linux.
