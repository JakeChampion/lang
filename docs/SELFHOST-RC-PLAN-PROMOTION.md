# Promote the rc plan: retire the credit table

Status: proposal, 2026-08-23. Companion to #7253 (keying/de-multiplexing —
treats the table's encoding, this treats its existence), #4399 (escape-taint
retirement), `docs/TYPED-IR-REWRITE.md` (the same argument on the type axis).

## The diagnosis: goal 2's bug rate is structural, not incidental

The self-host decides every retain/release through a **shape-enumerated
allowlist**: a credit (`"<FAMILY>:" + site key`) is granted per syntactic
shape by a collector, denied by escape scans, stamped at the binding, and
cashed by the exit sweep. Measured on main today:

- ~80 credit family prefixes in `reclaimable_names`; 27 `collect_fresh_*`
  collectors; 9 `body_unsafe_for*` variants with 145 call sites plus 35
  bespoke `*_escapes` predicates; `reclaimable_names_of` is 980 lines;
  `emit_dec_sweep_except_list` is 481 lines over 25 `slot_is_reclaimable_*`
  predicates.
- Every credit must agree across **four scattered points** — collector,
  escape gate(s), site stamp (`bind_var_slot`; 180 `add_local` sites can
  bypass it), sweep arm — and the rc-log shows each point missed at least
  once.
- The enumeration is a cross-product: ~80 type families × the binding-origin
  axis (local, parameter, receiver, field read, container element, match
  payload, as-pattern, for-in element, reuse recipient, tuple-destructure —
  #7253's running list) × scope forms. Default is deny → leak; widening
  without full agreement is a UAF. **Correctness equals enumeration
  precision**, so every unenumerated cell is a future rc-log entry.

Native answers the same questions with **one analysis battery**
(`internal/ir/rc_analysis.go`): one taint fixpoint (`computeFreeEligible`),
one interprocedural borrow inference (`inferParamEscapes`), one type-driven
inc trigger (`needsRcIncOnAlias`), one last-use order (`identOrder`). Origins
are not cases there; a for-in binder and an alias of a parameter are both
just a store the fixpoint sees.

The cost, measured:

- 331 logged one-off rc fixes (291 in `RC-PERCEUS-SELF-HOST-PORT.md` §9 + 40
  in `docs/rc-log/`), 36 of them in the last four days; ~⅓ of the recent
  commit stream; 172 of ~1151 files in `internal/e2eselfhost` are
  one-fix-one-test.
- `irlower.fern` is 62k lines (+51% in a month); the credit machinery is
  roughly a third of it.
- The convergence freeze's only open precondition is goal 2 — the one
  `make freeze` prints UNVERIFIABLE.
- Of the leak matrix's 18 open rows, most are **structural denials**: origins
  no collector can credit (for-in binder, `alias_param`, rebind). Under the
  credit model each needs a hand-written family; under an analysis they are
  not cases at all.
- The in-repo control group: arrays are immune to the entire defect class
  because their sweep is driven by the `is_arr` slot **flag**, not a deniable
  credit (#7253/#7282 threads). That is the model generalised below.

The site-keying migration (#7272→#7358) fixed the over-release direction —
`reclaim_slot_name` is deleted, aliasing bindings are refused rather than
handed a sibling's credit. What remains is the leak direction, and that is
exactly the direction the allowlist regenerates forever.

## The change: make the ported analyses the implementation

**The principled half already exists in the self-host and is parity-tested.**
`consumed_params_of` (irlower.fern:52908) and `free_eligible_of` (:53479) are
transliterations of native `computeConsumedParams` / `computeFreeEligible`,
pinned against native per-function by `TestSelfHostRcPlanDiff` via
`rc_plan_dump` (:54662). Their sole consumer is the dump; its sole caller is
the `irlower_run` diff driver. **The analysis drives zero emitted bytes; the
78-family table drives all of them.**

Promote the plan:

1. The **release** side becomes uniform: an rc-typed slot whose name is
   free-eligible is released at exit/rebind/last-use — type facts from the
   checker (the #5986 carriers / `LocalInfo` type columns), verdicts from the
   fixpoint. This is the `is_arr` model extended to every rc kind.
2. The **retain** side becomes one trigger: port `needsRcIncOnAlias` and emit
   it at the store, whatever statement shape performed the bind. The
   #7253-thread rule stands: gate the retain on the same verdict that grants
   the release, never on a type predicate — the two stay one set by
   construction.
3. Collectors, escape scans, families, and sweep arms **delete** as their
   last consumer disappears (the Erasure rule; #7046 already showed the
   string traffic alone was worth four PRs).

Why this is the highest-leverage change available:

- It collapses the origin axis (the current defect generator) and ends the
  one-cell-at-a-time fill of the type×origin×scope cube.
- It has an oracle the credit table never had: **analysis-vs-analysis parity**
  (`rc_plan` diff, per-function strings, seconds per run) catches a divergence
  before any emitted byte changes. The existing pinned-divergence list in the
  diff gate is the burn-down list, and burning it down is progress on goal 2
  by definition — the goal is "match native's memory management", and the
  plan diff is the literal statement of that.
- It is the same move the project already committed to on the type axis
  (#5531/#5986: stop re-deriving what is already known) and on the keying
  axis (#7253); this applies it to the axis that produces the bugs.

## Migration: family-by-family, plan-behind-a-switch

Same discipline as the typed-IR carriers and the site-key migration:

- **Step 0** — widen `rc_plan_dump` to also render the retain plan
  (alias-inc sites) and last-use table; widen the diff gate to pin them
  against native. Burn down or pin-with-reason the existing divergences.
- **Step 1** — single choke points: one `retain(slot, site)` and one
  `release(slot, site)` emitter, site key as a parameter so the type checker
  enumerates producers (the #7349 technique).
- **Step 2..N** — per release family: route its decision through the plan,
  behind an env switch (`FERN_SELFHOST_RC_PLAN=0` reverts, mirroring
  `FERN_SELFHOST_NO_REUSE`); delete the family's collector, escape gates, and
  sweep arm when nothing consults them. Gates per step: `internal/e2eselfhost`
  primary (leak matrix — exits must match native, underflow guard on every
  cell — plus rcCorpus), fixpoint secondary (`docs/TEST-GATES.md`).
- The **reuse layer stays** (its 8 site pre-passes + `is_unique` guard are
  substantially complete and independently gated); a later step can read
  donor deadness from the plan's last-use table instead of its own scans.
- Sequencing with #4399: promotion first — it needs no native change and the
  parity oracle exists today. Once both sides run the same analysis, each
  taint arm #4399 deletes natively deletes from the self-host by parity
  rather than by a second hand-migration.

## Companion: make the silent two-thirds of the failure space loud

The rc-log's meta-cause is instrument blindness — of the three defect shapes
(fault / latent / denial), only fault moves an exit code; the census cannot
see an over-release into the freelist, a retain on a non-box, or a premature
free (it reads *better*). Two cheap instruments, worth landing alongside
step 0:

- **Validate rc-op targets under `-sanitize`**: an inc/dec whose operand is
  not a live allocation traps. This converts the #7368 class (retain on a
  scalar — clean census, SIGSEGV in CI) into an immediate local failure.
- **Run leak-matrix cells with quarantine + `FERN_RC_UNDERFLOW_TRAP=1`** so
  the latent class (stray dec into an unclaimed box) goes red before its leak
  is fixed, not after. The census now reaches every backend — the x86-64 hev
  block, `asm_arm64_ir.fern`'s emitter (with its own matrix leg), and
  `wasm_ir.fern`'s mode-0 emitter — with the sanitizer still x86-only.

## What this is not

- Not a rewrite of lowering, and not blocked on Phase B/SSA — native runs the
  same analyses over the AST; the port sits where the credit table sits today.
- Not a reason to pause leak fixes — but a leak fix that adds a new credit
  family to the allowlist deepens the thing being retired; prefer landing new
  coverage as plan verdicts from step 2 onward.

## Acceptance

- The 18 open leak-matrix rows go clean (matching native) without any new
  per-row credit family.
- `reclaimable_names_of`, the `body_unsafe_for*` set, the `collect_fresh_*`
  set, and the per-family sweep arms are deleted, not bypassed.
- The rc-log entry rate falls because the shape-cell backlog stops being the
  work.
