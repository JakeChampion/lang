# PLT landscape 2026 — external developments, gap analysis, ranked actions

Status: survey + recommendations (2026-07). This is the external
counterpart to `NICHE-LANGUAGE-RESEARCH.md`: where that doc surveyed
individual languages for adoptable mechanisms, this one asks what has
*moved* in programming-language theory and practice recently, which of
those movements change the cost-benefit of decisions Fern has already
staged, and which missing features would be genuinely game-changing
versus merely fashionable. Claims about Fern's current state were
verified against the code, not just the docs — see §5 for two places
where the existing docs lag reality.

Companion briefs produced with this survey:

- `docs/DST-PLATFORM-BRIEF.md` — deterministic simulation testing
  (recommendation §2.4, the top new build item).
- `docs/PACKAGE-CAPABILITIES-BRIEF.md` — per-dependency capability
  grants (recommendation §2.5).

## 1. Where Fern already sits at the frontier

Calibration, so the rest of the doc argues from the right baseline.
The following are shipped and current-state-of-the-art, not catch-up
items: Perceus RC with drop-guided reuse and TRMC (Koka/Lean/Roc
lineage); colorless library-level async over a plain `Future[T]` enum
(`std/async` — no function coloring, no CPS transform); lambda-set
defunctionalisation for monomorphic closure flows; MVS package
resolution with content-addressed stores and vendoring; fully-total,
portable integer semantics pinned by a cross-backend differential
fuzzer; `@must_consume` linear-obligation types; error-converting `?`
through the `From` trait (#2697 — see §5.1); literate programming; and
a TAP-native pure-Fern test runner. Most languages ship none of these.

## 2. Developments that change a staged decision's cost-benefit

### 2.1 Effect handlers became a compiler *mechanism*, not a type-system feature

OCaml 5 shipped untyped effect handlers as a runtime mechanism.
Effekt showed capability-passing with *lexically scoped* handlers and
no row-polymorphism annotation tax. On the wasm side, the
stack-switching / typed-continuations proposal (WasmFX) and WASI 0.3's
native async mean the *host* is converging on delimited one-shot
continuations as the suspension primitive.

The Fern-relevant reading: three separately-deferred features are the
same feature at the IR level.

1. The `concurrent{}`/`await` postmortem concluded a suspending await
   belongs in the IR as a CFG state machine, not the parser
   (`LANGUAGE-DIRECTION.md`).
2. Lazy iterators are deferred until the IR can fuse or otherwise
   avoid the allocation trap (`ITERATOR-FUSION-CONTRACT.md`).
3. `ASYNC-REDESIGN.md` PR5 wants `Future[T]` as a first-class IR type
   for component-model async.

A single IR-level "suspend point + resumable frame" construct — a
delimited one-shot continuation — services all three: await lowers to
it, a generator's `yield` is a degenerate handler over it, and a lazy
iterator is a generator the fusion pass may inline away. The surface
stays colorless and effect-row-free (the standing posture holds; see
§4 non-actions). This does NOT change the sequencing — the wasm
component-model lane (#4315–#4320) stays where it is — but when that
lane lands `Future[T]`-in-IR, the construct should be shaped as the
general suspension primitive, not an async-only special case, so
generators and lazy iterators fall out of it later instead of needing
a second mechanism. Design-note issue: #5364.

### 2.2 Mode-based memory typing consolidated (OCaml modes, Mojo origins, Hylo MVS)

Jane Street's OCaml mode extensions (`local`/`unique`/`once`), Mojo's
origin system, and Hylo's `let`/`inout`/`sink`/`set` parameter
conventions all converged on the same shape: ownership properties are
*modes on bindings*, orthogonal to types, checkable intra-procedurally
without a Rust-scale borrow checker.

Fern has grown four surfaces that are points in one mode lattice:
`own` consuming params (E050/E051), `fip` functions (E053), the
`T[]`-owned vs `[T]`-view spelling (E063), and `@must_consume`
(consumption obligations — at-least-once as implemented, see
`MODE-LATTICE.md` §2.4). Each is individually justified; together
they are an ad-hoc modal system nobody designed as one. The unified
lattice (borrowed ≤ owned ≤ unique; droppable vs must-consume as an
orthogonal axis) is now written down in `MODE-LATTICE.md`, which also
corrected this section's original premise: the four checker walks are
ALREADY ported to the self-host checker — what remains for goal 2 is
consolidating them into one analysis when next touched, plus the
IR-side fip reuse verification (E068). The
OCaml-modes papers ("Oxidizing OCaml") are the reference design.
Research issue: #5365.

### 2.3 The cycle question has a proven middle answer (and a cheaper one)

Standing position: cycles leak; full immutability will eventually make
them unconstructible; acceptable per-request
(`CYCLE-COLLECTION-ANALYSIS.md`, `LANGUAGE-REVIEW-2026-07.md`). The
standing counterexample is the self-hosted compiler itself, and the
immutability migration that would close the loophole is the review's
own top-listed incomplete item.

Nim's ORC (Perceus-style ARC + trial-deletion cycle collection scoped
to types the compiler proves *can* be cyclic) is the existence proof
that deterministic RC and a cycle collector coexist without giving up
prompt destruction. But the cheaper move matches Fern's testing
culture better: a **debug-build cycle/leak detector** — a heap census
at exit behind a flag, reporting unreclaimed objects and the cycle
members among them — converts "cycles leak silently" into "cycles are
a reported, testable bug" with zero production-runtime cost and no
codegen changes. That is the right tool while the immutability
migration is the actual fix. Issue: #5362.

### 2.4 Deterministic simulation testing went mainstream — and Fern is accidentally built for it

TigerBeetle (whose TigerStyle this project already cribs),
FoundationDB, and Antithesis normalised DST: run the system under a
virtual clock and simulated I/O, drive it from a seeded RNG, and every
concurrency bug becomes a replayable seed. Retrofitting DST onto a
runtime with ambient syscalls is heroic; Fern doesn't have to,
because its entire async surface already funnels through one seam —
the `poll` / `monotonic_ns` / timer-pollable builtins inside
`std/async`'s combinator loops — and `Future[T]` is a plain enum a
library can construct.

A simulated driver (virtual clock, virtual readiness, scripted
endpoints, seeded interleaving) makes every `gather`/`race`/
`with_deadline` program deterministically testable on **every**
backend — including the interpreter, where fd-backed `Pending`
futures today never resolve at all, so this also closes a real
backend gap. For a language pitching reliable edge handlers,
"concurrency bugs reproduce from a seed" is a differentiator none of
the mainstream competition offers out of the box. Full design:
`docs/DST-PLATFORM-BRIEF.md`. Tracking issue: #5360. This is the top
new build item from this survey.

### 2.5 Capability security became a supply-chain feature

The npm/PyPI supply-chain incidents moved per-dependency permissions
from academic (E, Austral) to demanded. Deno grants capabilities per
*process*; WASI per *component*. Fern can grant them per **package**,
at compile time: it has a manifest (`fern.toml`), a closed import
graph with package-scoped visibility, and a small closed set of
I/O-capable builtins. "Your JSON library provably cannot open a
socket" is a checker feature, not a runtime one — and it is
complementary to (not blocked by) the WASI-level attenuation noted as
out-of-scope in #4907, which guards the process boundary rather than
the intra-binary package boundary. It also answers
`PLATFORM-RESEARCH.md`'s open question 3 (checker error vs link-time
absence) in favour of the checker. Full design:
`docs/PACKAGE-CAPABILITIES-BRIEF.md`. Tracking issue: #5361.

### 2.6 Typed errors won the argument — Fern should finish collecting the winnings

Swift 6 shipped typed throws after a decade of untyped `throws`;
Rust's ecosystem consolidated on `thiserror`-style concrete errors at
boundaries. Fern's `Result` + `?` model is the vindicated shape, and
the biggest ergonomic gap (`?` requiring exact error-type equality)
is **already fixed** — #2697 wired `From`-conversion into `?` (see
§5.1; the July review doc is stale on this). What remains is
adoption, not mechanism: `LANGUAGE-REVIEW-2026-07.md` Part II's
finding that the stdlib still mixes `Option` / `Result` / sentinel
returns (`-1`, `""`) stands, and sentinel APIs no longer have the
"error-type friction" excuse. That cleanup is tracked stdlib work,
not a new mechanism, so this survey files nothing new for it.

### 2.7 f64-by-default: decide it

Not a new development, but the survey's "most expensive to defer"
flag: `FLOAT-SEMANTICS.md` leaves default-float as an explicit open
owner decision, and every month of f32-default accretes more code
against the eventual answer. The JSON/edge workload, the scripting
expectation, and the shrink-the-surface argument (one default width +
suffix opt-in) all point to f64. Filed as a decision issue so it
stops being ambient: #5363.

### 2.8 Multicore: the one frontier with no Fern story at all

Structured concurrency is settled (Trio → Kotlin → Swift → Java
Loom); Fern's single-threaded combinators are structured-concurrency-
shaped already. What Fern lacks entirely is a *parallelism* story,
and general-purpose ambition (`CLAUDE.md`'s stated direction)
eventually demands one. The design that doesn't break Perceus is
share-nothing workers with per-worker heaps (Erlang/Inko shape):
refcounts never cross threads, so no atomic-RC tax; messages are
deep-copied or ownership-transferred at the boundary. Nothing should
be built now — but the research doc should exist *before* stdlib and
platform decisions accrete against an implicit single-thread
assumption. Research issue: #5366.

## 3. Languages worth mining that the existing surveys haven't drained

`NICHE-LANGUAGE-RESEARCH.md` and `LANGUAGE-DIRECTION.md` already
mined Roc, Koka, Lean, MoonBit, Zig, Rust, Gleam, Odin, Hare, Vale,
Pony, Hylo, Austral, Granule deeply. The under-mined ones:

- **Effekt** — capability-passing effects with lexical handlers and
  *second-class* capability values. The formal bridge between the
  `Platform`-capability-bag design (`PLATFORM-RESEARCH.md` Rec §1)
  and any future effects prototype; closest model if that prototype
  branch ever opens, precisely because it avoids Koka's row
  annotations.
- **Flix** — `region r { … }` blocks: scoped *local* mutation inside
  observably-pure functions. The most promising shape for the tail of
  the immutability migration — a sanctioned place for the ~500
  load-bearing `p.field = v` sites that cannot leak a mutable alias,
  rather than functional-update-everywhere. Also: purity-aware stdlib
  API design.
- **OCaml + Jane Street modes** — the deployed reference for §2.2.
- **Swift 6** — typed throws (§2.6) and `~Copyable` noncopyable types
  (a shipped mainstream `@must_consume`); their API-evolution notes on
  where consuming/borrowing annotations annoy users are free field
  data.
- **Nim (ORC)** — the RC+cycle-collector coexistence proof (§2.3).
- **Unison** — beyond content-addressed caching (already noted in
  `NICHE-LANGUAGE-RESEARCH.md`): *abilities* are the cleanest
  minimal-annotation effect surface in a shipped language, and the
  content-addressed function-level build cache maps directly onto
  Fern's bit-identical self-compile infrastructure.
- **Inko** — `recover`-based share-nothing concurrency on single
  ownership without a borrow checker; the closest sibling for §2.8.

## 4. Deliberate non-actions (deferrals this survey endorses)

Re-examined and left standing, so nobody re-litigates them from this
doc: **comptime** stays deferred on `COMPTIME-BRIEF.md`'s trigger
conditions (nothing since changes them). **Surface effect rows** stay
deferred — Koka itself demonstrates the annotation burden; Effekt is
the model *if* the trigger (a real handler corpus) ever fires.
**Salsa-shape query compilation** stays deferred (Carbon's bet is
aging well; `RESEARCH-ROADMAP.md` Tier F). **SIMD**, **borrow
checker**, **wasm-GC targeting** (linear-memory RC is the right
posture for the component-model direction): all correctly out. The
shelved SSA framework (`SSA-DECISION.md`) stays shelved but warm —
§2.1's suspension construct and the fusion contract both eventually
want basic blocks, which is one more reason the suspension design
should be paper-first, not code-first.

## 5. Corrections — where the docs lag the code

Verified while preparing this survey (the standing "trackers lag
reality" caveat in `CLAUDE.md` cuts both ways):

1. **`?` DOES convert error types via `From`.** #2697 (closed
   2026-06-22) wired it: `?` on `Result[T, E1]` in a function
   returning `Result[_, E2]` type-checks when `impl From[E1] for E2`
   exists and converts the propagated error
   (`internal/checker/checker.go:11289` area). Verified end-to-end
   with a two-enum probe program through `-interp`.
   `LANGUAGE-REVIEW-2026-07.md`'s friction #1 ("no `From`-style
   conversion") described the pre-#2697 state; a correction note now
   sits on that section.
2. **Named arguments + parameter defaults exist** (self-host IR
   coverage: `internal/e2eselfhost/self_host_named_args_ir_test.go`),
   so they are not a gap despite being absent from the feature-audit
   tables this survey started from.

## 6. Ranked actions

| # | Action | Kind | Tracking |
|---|--------|------|----------|
| 1 | DST simulation driver for `std/async` (slice 1: virtual clock + deterministic reactor) | build now | #5360 |
| 2 | Package capability grants, report-mode first | build next | #5361 |
| 3 | Debug-build cycle/leak detector | build next | #5362 |
| 4 | f64-default decision | owner decision | #5363 |
| 5 | Suspension-primitive design note (await/generators/lazy iterators, one IR construct) | design, gated on #4315–#4320 lane | #5364 |
| 6 | Mode-lattice unification write-up, folded into the goal-2 Perceus port | design with goal 2 | #5365 |
| 7 | Multicore research doc (share-nothing workers) | research | #5366 |
| 8 | Immutability migration + stdlib error-convention cleanup | already tracked | existing |

Items 1–3 are additive (no breaking surface, no backend lowering
changes, no overlap with the active #4315–#4320 lane). Item 1 is in
progress on the branch that introduced this doc.
