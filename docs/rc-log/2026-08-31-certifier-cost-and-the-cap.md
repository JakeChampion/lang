# The certifier was 16 minutes, and capped (2026-08-31)

Sizing the compile-path certifier — the remaining soundness step #7784
names — turned up two things, and only one of them was the cost.

## The cost, staged

Over `examples/self_host/fern.fern`, 6480 functions:

| stage | time |
| --- | --- |
| front end + lower | 14.5 s |
| lift | 1 m 23 s |
| solve ownership | **3.6 s** |
| **certify** | **16 m 13 s** |

The interprocedural fixpoint — the part #7786's cost note warned about,
with Roc's Bell-number blowup — is 3.6 seconds. The per-path walk is
sixty-five times the entire front end.

## Two fixes, and one of them changed the answer

**Keep unreportable values out of the dataflow state.** `applyBlock` was
recording `ownGone` for every `UnitNone` value — the bulk of a function's
values, all scalars — and the per-round cost is a map copy proportional
to the state. Nothing can promote a `UnitNone` root (only a borrow is
retained into holding) and nothing reports one, so it is pure weight.

> 16 m 13 s → **4 m 48 s**, findings identical at 7091.

Identical findings is the part that matters: the trim is
behaviour-preserving by construction and measured to be.

**Sweep only the blocks that can have changed.** Cost was
(rounds × blocks × state) with a full re-scan each round.

> 4 m 48 s → **3 m 34 s**, and no single function dominates any more:
> the worst went from `parser__parse_stmt_at` at 1 m 41 s to
> `asm_ir__emit_function_via_ir_pre` at 46 s.

**A FIFO worklist is worse than a round-robin sweep, measured**: 8 m 51 s.
Blocks come out of the lift in reverse post-order, so sweeping in index
order and skipping unqueued blocks converges; a FIFO queue keeps
revisiting a loop body before its header has settled. Recorded because
"use a worklist" is the obvious advice and the obvious advice was
2.5× slower here.

## The cap was hiding 41 findings

The worklist versions reported **7132** where round-robin reported
**7091**, and two different orders agreeing on 7132 is what made that
worth chasing rather than dismissing.

The original walk had `round < 64` — described in its own comment as "a
backstop against a malformed CFG rather than an expected exit". It was
not. Measured:

> **3 functions need more than 64 sweeps, and one needs 206**
> (`asm_arm64_ir__emit_function_via_ir`).

So the cap silently truncated the answer on exactly the functions least
able to afford it, and the 41 missing findings were all in those three.
That is the same failure this whole line of work keeps meeting from a
new angle — an aggregate concealing a per-item truth — and it was mine.

The cap is gone. The lattice only ever moves toward `ownMaybe`, so the
walk terminates on its own; `CertifyReport.Passes` now reports what it
took, so "it ran to completion" is observable rather than assumed.

## What this says about the compile path

Not yet. 3 m 34 s against a 14.5 s front end is not a per-compile cost,
and the honest reading is that the certifier is a **gate over a corpus**,
not a compiler pass, until it is roughly two orders of magnitude faster.
The lift is 1 m 23 s of that and has had no attention at all.

The import cycle everyone expects to be the obstacle — `internal/ssa`
imports `internal/ir`, so `ir.Verify` cannot call `Certify` — is not the
binding constraint, and neither is a missing caller: `ir.Verify` has no
non-test caller either. Native's IR verifier family is test-time by
construction; only the self-host's runs on a compile path.

## And the number that matters more than the speedup

**7132 findings over the self-host compiler**, against 0 over the
conformance corpus.

The zero is real and it is corpus-bounded, and this is what the bound
costs. 323 small fixtures do not exercise what 6480 functions do. Some
of the 7132 will be live leaks — `docs/rc-log/` tracks several — and
some will be false-positive classes the corpus never reached. Sorting
them is the next slice, and the method is the one that has worked five
times now: one class at a time, each traced end to end before anything
is claimed about the total.
