# Reuse contract — what Fern's RC/reuse promises

Plan item **B1** of `docs/NICHE-BORROWS-PLAN.md`, from the
niche-language research (`NICHE-LANGUAGE-RESEARCH.md`): Koka's
verified design stance is that reuse is a **specified,
programmer-visible behavior** — "the reuse optimization is
guaranteed and a programmer can see when the optimization
applies" — not an opaque best-effort compiler heuristic. This doc
is Fern's version of that stance: it names each reuse/move shape
the native compiler performs, what statically qualifies and
taints a site, the runtime guard semantics, and the test that
locks each shape. It is the reference for (a) users writing
allocation-sensitive hot loops, (b) the self-host port
(`SELFHOST-PERCEUS-REUSE.md` — the port must preserve exactly
these behaviors), and (c) the drop-guided evaluation (plan E3).

**Honest status.** Fern's reuse today is *statically selected,
dynamically guarded*: a site fires only when the static analysis
recognises the pattern AND the runtime uniqueness check passes.
The shapes below are stable, documented, and test-locked — users
may rely on them firing for the described patterns. What Fern
does NOT yet have is Koka's *visibility* half (a way to SEE that
a site qualified — `fip`/`fbip` in verify-and-enable form, plan
E2) or the drop-guided source selection (plan E3). Where a shape
has known non-firing edges, they are listed under "taints".

## The two-layer model

Every reuse shape has the same skeleton:

1. **Static selection** (`internal/ir/rc_analysis.go`): an
   analysis pass proves a *pairing* — a construction site C can
   take over the box of a dead, owned donor D of compatible
   layout. This is the PLDI 2021 Perceus "reuse token" model
   (the token threads from D's drop to C's alloc). See
   `computeReuseSources` and the per-shape hooks below.
2. **Runtime guard**: the paired site still checks
   `is_unique(D)` (refcount == 1) at runtime. Unique → C writes
   into D's box in place (no alloc, no free). Shared → C falls
   back to a fresh allocation and D is dec'd normally. The
   fallback is semantically invisible; only allocation traffic
   differs. This mirrors Koka/Lean exactly (and is why FIP-style
   annotations can layer on top without code duplication).

A shape additionally passes the **borrow-model UAF guard**
(`freeEligible`) — donors whose box may be aliased by a borrow
are never reused — and `rhsTainted`'s conservative default for
values built from literals/untracked sources.

## The shapes

### R1 — struct self-overwrite

`p = Point { … };` where `p` already holds a uniquely-owned
`Point`: the new literal writes into `p`'s existing box.
Hook: `tryStructReuseOverwrite` (`internal/ir/ir.go`), wired
into `assign`. Old field payloads are released to balance
StructLit's field incs (`emitReuseOldFieldDrops`).
Locked by: `internal/ir/struct_reuse_test.go`.

### R2 — enum self-overwrite

`c = Variant(...);` reusing `c`'s box when uniquely owned.
Cross-variant reuse is sound only when every payload-carrying
variant shares one box size — gated on `uniformEnumBoxSize`.
Unlike R1 there is **no old-payload release**: enum construction
doesn't inc its payloads, so the reuse is a pure alloc-elision
that is rc-neutral vs the baseline flat-dec. Scalar enums built
from literal args are conservatively tainted (`rhsTainted`) and
don't fire.
Locked by: `internal/ir/enum_reuse_test.go`.

### R3 — general pairing reuse (cross-local FBIP)

A construction C (`*ast.StructLit` / `*ast.TupleLit`) takes over
the box of a *different* dead, owned local D of the same layout —
the general Perceus reuse-token win beyond self-overwrite.
Selection: `computeReuseSources`; D is marked `reuseConsumed` so
the precise-drop pass doesn't double-free it.
Locked by: `internal/ir/general_reuse_test.go`.

### R4 — consuming-match reuse (C2, zero-alloc FBIP)

`match` on a consumed `own` enum parameter where an arm returns a
fresh variant constructor: the scrutinee's box shell is handed
straight to the arm's construction. The old payloads were MOVED
into the arm bindings, so the construction must NOT drop old
fields (`consumingMatchReuse` tells `emitEnumNew` to skip the
release). This is the true `map`-over-unique-list shape: on
unique data the loop allocates nothing (#4475).
Locked by: `internal/ir/c2_consuming_reuse_test.go`.

### R5 — consuming owned matches (drop-specialised release)

The non-`own` sibling (#4400): a match consuming an owned-by-
default enum param emits per-arm drop-specialised release — on
the unique branch the box is shallow-freed and the extracted
bindings inherit the payload counts (the dup/dec pairs cancel
statically); on the shared branch bindings are dup'd and the box
flat-dec'd. Guarded/wildcard arms fall back to the exit sweep.
Not an alloc-elision itself, but the enabler that makes R4 legal
and keeps match-heavy code from paying dup/dec per arm.

### R6 — array in-place mutation (push / set)

`arr.push(v)` and `arr.with(i, v)`-style writes go through the
COW runtime (`__fern_arr_cow_inplace`): rc==1 mutates in place,
shared copies first. The analysis adds a receiver inc
(`arraySetInc`) when the receiver is provably live after the
call, forcing the copy path — the #2832 aliasing guard.
Locked by: `internal/ir/append_inplace_test.go`,
`push_counted_store_test.go`.

### M — the move family (pair cancellation)

Not reuse, but the same contract class (allocation/RC traffic
users may rely on not existing): move-on-return, move-on-alias
(last top-level alias of a local skips its inc and the exit
sweep skips the dec — `movedLocals`/`moveSites`), move-on-
construction (struct/array/tuple/closure containers), and
move-on-destructure. Every genuine last-use move opportunity at
the nine `emitAliasInc` call sites is gated on `moveSites`
(audited — see `RC-PERCEUS-PLAN.md` "Remaining frontier").

## What taints a site (why a shape didn't fire)

- **Borrows**: a donor whose box may be referenced by a borrowed
  binding fails `freeEligible` — no reuse (UAF guard).
- **Literal/untracked sources**: `rhsTainted`'s conservative
  default (e.g. scalar enums from literal args in R2).
- **Layout mismatch**: R2 needs `uniformEnumBoxSize`; R3 needs
  same box layout.
- **Liveness**: any read of the donor after the construction
  site kills the pairing (and in R6, forces the copy path — that
  one is a *correctness* gate, not a missed optimisation).
- **Sharing at runtime**: the `is_unique` guard — the site still
  compiled to the reuse form; it just took the fresh-alloc branch
  this execution.

## Known gaps (deliberate, tracked)

- **5f** — cross-local reuse through aliases needs the alias
  analysis the borrow model postponed.
- **5g** — string reuse is hard-blocked on the SSO native flip.
- **Drop-guided selection** (ICFP 2022): **evaluated 2026-07-13**
  (plan item **E3**) — implemented behind `ast.RcReuseDropGuided`
  (default OFF; `FERN_RC_REUSE_DROP_GUIDED=1`) in
  `internal/ir/rc_dropguided.go`. Finding: on this codebase's
  shapes the token flow selects a **superset** of the PLDI
  pairing — equal on every shape above (R1–R6/M unchanged), plus
  one genuinely new shape: a donor whose LAST USE sits inside a
  dominated non-loop if/match arm, claimed by a later
  construction in the same arm (`drop_guided_reuse_test.go`,
  `TestDropGuidedFiresArmDropShape`). Verdict — **keep pairing
  as the default, revisit at the self-host port** — with the
  measured numbers lives in `RC-PERCEUS-PLAN.md` ("E3 drop-guided
  reuse evaluation — verdict"). The shapes' observable behavior
  is identical under both strategies by construction (shared
  gates + is_unique guard + degrade path; 224-seed differential
  in `drop_guided_differential_test.go`).
- **Visibility** (`fip`/`fbip` verify-and-enable): plan item
  **E2**. Until then, the way to *see* reuse is the rc dump
  (`internal/ir/rc_dump.go`) and the allocation counters in the
  reuse tests.

## Contract for the self-host port

`SELFHOST-PERCEUS-REUSE.md` ports the selection analyses; this
doc defines the behavior bar: for each shape R1–R6/M above, the
self-host compiler must (a) fire on the same test patterns
(the Go test files named per-shape are the executable spec) and
(b) never fire where native doesn't (the taints are part of the
contract — they encode soundness guards, not just missed wins).
When E3's verdict lands, this doc updates to name the chosen
algorithm; the shapes' observable behavior must not change.
