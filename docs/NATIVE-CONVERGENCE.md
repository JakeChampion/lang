# Native convergence: freeze native as the stage-0 bootstrap + oracle

**Status:** POLICY ADOPTED, freeze DEFERRED behind explicit preconditions
(decided 2026-07-03).
**Freeze-eligibility review:** when every precondition below is green
(tracked, not date-driven).
**Owner:** compiler / self-host.

## The question

A single language feature lands in a lot of places today:

- native `internal/ir` (+ the three native backends `codegen/{arm64,x86_64,wasmbin}`)
- native `internal/interp`
- self-host `examples/self_host/irlower.fern` (+ the three self-host backends
  `asm.fern` / `asm_arm64.fern` / `wasm.fern`)
- self-host interp

That double maintenance is the dominant tax on the project. Worse, it is
*open-ended*: every new native-only construct (the 2026-07-01/02 SSA backends
are the canonical example) widens the surface the self-host must eventually
mirror, so the two compilers can drift forever with no defined point at which
they converge. This doc forces the call — like `SSA-DECISION.md` did for the
SSA cutover — by writing down **when native stops being the product and
becomes the toolchain bootstrap**, and **what measurable gates prove parity**
before that flip.

## Decision: native becomes stage-0, but only when the parity gates close

We adopt the convergence *policy* now and defer the *freeze* until it is
earned. Rationale for splitting the two: the policy is the load-bearing part
(it reframes "native is the product" as "native is the bootstrap + oracle"),
but flipping the freeze switch while the differential gates are still sampling
rather than closed would trade one drift hazard for a worse one — a self-host
that is *declared* at parity without the tests to prove it.

### 1. The native feature-freeze point

After the Perceus port (roadmap **goal 2**) reaches parity, `internal/`
accepts only:

- **bugfixes** (correctness, never new surface),
- **oracle needs** (whatever the differential suites require to keep anchoring
  on native semantics), and
- **whatever the self-host sources require to bootstrap** — the "Go 1.4 rule":
  native must forever compile the self-host compiler's *language subset*, and
  that subset may be deliberately conservative to keep the bootstrap small.

New language features then land **self-host-first / self-host-only**, gated by
the whole-compiler fixpoint. Native is no longer where a feature is born; it is
the frozen floor the self-host stands on.

### 2. The parity contract = the differential suites, promoted from sampling to closure

The freeze is only safe once "native and self-host agree" is a *closed*
property, not a spot-check. Three suites carry that contract; each has a
concrete completion criterion:

- **Checker-codes differential** — `checker_codes_run.fern` +
  `internal/e2e/self_host_checker_codes_test.go`. Today it filters the Go
  checker's output through `selfHostImplementedCodes`
  (`self_host_checker_codes_test.go:27-85`) and asserts parity **only** on
  that set (`filterImplemented`, `:124-133`, applied at `:856`, `:979`,
  `:1045`). That filter is an open-ended contract with no completion criterion.
  **Freeze precondition: burn the filter down to empty.** As of this writing
  the Go checker emits every code the map already lists **plus six it does
  not** — the remaining gap is exactly:

  | code | meaning |
  |------|---------|
  | E023 | unknown enum (variant pattern names an undeclared enum) |
  | E032 | `use` clause inference error |
  | E044 | captured variable has an unsupported type |
  | E053 | `fip` function performs a heap allocation |
  | E060 | invalid `as?` downcast target |
  | E062 | ambiguous method on a multi-trait object |

  When `checker.fern` emits all six and `selfHostImplementedCodes` is deleted
  (the differentials comparing raw sets, unfiltered), this precondition is
  green. Each checker-port slice should shrink this table by one or more rows;
  see `docs/SELFHOST-CHECKER-PORT.md`.

- **Fuzz-diff execution oracle** — `FuzzGenerate_ExecutionAgrees`
  (`internal/e2e/diff_oracle_test.go`), run by `.github/workflows/fuzz-diff.yml`,
  alongside the `TestDifferential*` corpus. This becomes the **standing
  regression net**: post-freeze it is the mechanism that catches a self-host
  behavioural divergence from native semantics, so it must stay in CI on every
  push and its corpus must grow with the language, never shrink.

- **SH-057-class semantic gaps** — the enumerated deep semantic divergences
  (mutable scalar captures / #2850 and its siblings). These are freeze
  **blockers**, not filtered-away deltas. They live in one place (this section)
  so "are we at parity?" has a single checklist rather than tribal knowledge.
  Freeze precondition: this list is empty. Known open entries at adoption:
  - mutable scalar captures across closure boundaries (SH-057 / #2850).

  Add a row when a new class is found; strike it when a differential test
  pins the fix.

### 3. `internal/interp` is the long-term keeper — even post-freeze

Native `internal/interp` is **not** frozen out of existence. It is the
semantics reference every differential test anchors on, it is cheap to carry
(~3.9k lines), and it is the oracle that makes goal-2 correctness checkable at
all. Post-freeze it keeps receiving the same bugfix / oracle-need updates as
the rest of `internal/`. Retiring the native *backends* is on the table once
the self-host backends reach parity; retiring the native *interpreter* is not.

## Freeze preconditions (all must be green before native is frozen)

1. Roadmap **goal 2** complete — the Perceus port (inc/dec, borrow inference,
   drop specialisation, reuse analysis) at parity in the self-host compiler.
2. #3451 / #3457 complete (the bootstrap-budget / bundle prerequisites).
3. **Checker-codes filter empty** — `selfHostImplementedCodes` deleted, the
   six-code gap above closed, all three differentials comparing unfiltered
   sets. **GREEN as of 2026-07-12.**
4. **SH-057-class semantics closed** — the blocker list in §2 is empty, each
   former entry pinned by a differential test.

None are date-driven; the freeze fires when the last one goes green.

## What a freeze does NOT change

- The differential suites keep running forever — they are the regression net,
  not a one-time gate.
- `internal/interp` keeps getting bugfixes (§3).
- The self-host compiler's *language subset that native must bootstrap* stays
  conservative on purpose; a self-host-only feature is allowed to be
  un-bootstrappable by native as long as it is not on the bootstrap path.

## Maintenance contract while the freeze is DEFERRED

So the policy doesn't rot into ambient drift before it fires:

- Every new native language feature is a **debt entry**, not a free win: it
  widens the self-host mirroring surface and pushes precondition 1 further out.
  Prefer landing new surface self-host-first even now, where the fixpoint
  allows it.
- Keep the three differential suites green in CI (they already run).
- When a precondition goes green, update its row here; when the last one does,
  open `NATIVE-FREEZE.md` recording the freeze date and the final gate state,
  and start rejecting new non-bootstrap surface in `internal/`.
- New checker rules land self-host-side too and shrink the six-code table;
  don't grow `selfHostImplementedCodes`, shrink toward deleting it.
