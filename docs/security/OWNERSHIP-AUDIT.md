# Ownership and view escape audit

Status: [reference] probe record for [SEC-004](https://github.com/JakeChampion/lang/issues/13506).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).

Each probe is a short program with one suspect pattern.
The first audit ran each probe through both checkers.
A probe that both checkers accept also ran natively under `-sanitize -g` with a heap array.
Since [#16331](https://github.com/JakeChampion/lang/issues/16331), only the self-host checker reports the ownership codes `E050` and `E051`.
The Go checker does not implement the ownership rules, and the conformance cases are the second check.
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

## Iteration over an owned parameter

A `for` loop over an `own` parameter borrows the parameter and does not consume it.
The function can use the parameter after the loop.
[MODE-LATTICE.md](../MODE-LATTICE.md) states the rule, and claim `ML-11` in [semantics.md](../../spec/semantics.md) pins it with the case `own_param_iterated_then_used`.

The first audit found that the two checkers did not agree on this pattern.
The Go oracle refused the later use with `E050`.
The self-host walk accepted it.
The rule follows the self-host walk for three reasons:

- Parameters borrow by default, and a loop reads its source.
- A `for` over a borrowed array also borrows the array (`RH-39`).
- The lowering keeps the value alive through the loop.

The case reads and appends to a scalar array after the loop.
It also appends to a struct array in an outer loop and a match.
It runs natively under `-sanitize -g` with balanced allocations.
A consuming call of the parameter inside the loop body stays refused with `E050` (`RH-32`).

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

## Probes of 2026-11-28

The self-host checker ran these probes after #16331.

| Pattern | Result |
|---|---|
| A local alias of an owned parameter, then the parameter consumed, then the alias read | `E050` at the consume |
| A view of a borrowed array captured by a returned closure | `E082` |
| An owned parameter consumed inside the body of a `for` loop over it | `E050` |
| An owned parameter read after a `for` loop over it | Accepted, sound under `-sanitize -g` |
| An owned parameter appended to after a `for` loop over it | Accepted, sound under `-sanitize -g` |
| An owned struct array appended to after an inner loop, inside an outer loop and a match | Accepted, sound under `-sanitize -g` |

## Limits

These probes are scoped evidence for the current Perceus and borrow implementation.
They do not prove a complete alias or lifetime model.
The runtime observations use one allocator and one target, x86-64 Linux.
