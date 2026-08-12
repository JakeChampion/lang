# The mode lattice — own / fip / views / @must_consume as one system

Status: design note (research issue **#5365**). **No surface change** —
every existing spelling stays exactly as it is; no new syntax is
proposed here. Thesis: Fern's four shipped ownership-adjacent
surfaces — `own` consuming params (E050/E051), `fip` functions
(E053/E068), the `T[]`-owned vs `[T]`/`str` view spelling
(E063/E065), and `@must_consume` (E067) — are four points in one
two-axis mode lattice (borrowed ≤ owned ≤ unique on the access
axis; droppable vs must-consume on the orthogonal disposal axis),
and their checkers are four spellings of one per-binding mode
state machine. Writing the lattice down now means the goal-2
Perceus/checker consolidation maintains ONE analysis with four
reporting surfaces instead of four parallel analyses that drift.

Reference design: Jane Street's OCaml mode extensions
("Oxidizing OCaml" — modes on bindings, orthogonal to types;
see §6 for what was actually verified).

**Dated correction (2026-07-19).** The framing in
`PLT-LANDSCAPE-2026.md` §2.2 ("the goal-2 Perceus port must
re-derive all four rule sets in the self-host checker anyway")
is already stale in one happy direction: all four *checker*
surfaces are ALREADY ported to `examples/self_host/checker.fern`
(§2 records each one's anchors). What remains un-ported is the
IR-side half of `fip` (`fbip`, graded `fip(n)`, the E068 reuse
verification). The unification opportunity has therefore
shifted: not "port once instead of four times" but "the four
ported walks in `checker.fern` are separate traversals of the
same per-binding facts — consolidate them when next touched, and
port the remaining IR half against the lattice rather than
against four rule sets." One smaller discrepancy is reported
in place: §2.4 (E067 is at-*least*-once, not the landscape doc's
original "exactly-once obligations" — corrected there).

## 1. The two axes

**Axis 1 — access strength, per binding:**

    borrowed  ≤  owned  ≤  unique

- **borrowed** — some other binding owns the value. Reads and
  projections only; never reclaimed through this binding; must
  not outlive its owner. Fern's *default* for parameters
  (docs/RC-PERCEUS-PLAN.md "Phase 2d-borrow": no caller inc, no
  callee exit dec) and for closure captures. *Views* (`[T]`,
  `str`) are borrowed specialised to a projection that aliases
  the owner's backing store — same mode, plus a frame fact
  ("my owner may be this function's own frame") that the escape
  rules consume.
- **owned** — the binding holds a counted reference it may
  transfer (consume) or drop. Fresh constructions are born owned;
  `own` marks a parameter owned at the call boundary. An owned
  binding's consume is *affine* (at most once) — after it, the
  binding is dead.
- **unique** — owned with rc==1: in-place update is sound.
  Deliberately a *dynamic* fact in Fern (the
  `__fern_rc_is_unique` gate; Koka's "shape guarantee" stance —
  docs/NICHE-LANGUAGE-RESEARCH.md, fip mechanism 2), not a static
  mode. The only static approximations of unique are E051's
  owned-argument admission (a fresh construction reaching an
  `own` param is unique at entry unless aliased en route) and
  `fip`'s write rule (in-place writes only through an `own`
  root).

The ordering is a submoding order read exactly as OxCaml's: a
binding at a stronger mode can be *used* where a weaker one is
expected (owned values flow into borrowed positions freely —
`argAssignable`), never the reverse (borrowed → owned is E051).

**Axis 2 — disposal obligation, per type, applied to owned
values:**

    droppable   (default: scope exit ⇒ implicit RC drop)
    must-consume (@must_consume: implicit drop forbidden;
                  ≥1 consuming use on every path — E067)

Axis 2 is orthogonal: it never changes *how* a value may be
accessed, only whether the owned state is allowed to exit scope
without an explicit consume. It is per-TYPE (the attribute sits
on struct/enum decls) where axis 1 is per-BINDING — that
asymmetry is fine; the obligation attaches to each binding of a
marked type at its binding site (`mcCheckBinding`,
`internal/checker/mustconsume.go:119`).

**Why the lattice is this small** (vs OCaml's eight axes — §6):
Fern's global laws collapse the rest. E048/E056 immutability
freezes the heap after construction — no contention axis
because there is no mutation to contend over, no cycle
collector because cycles are unconstructible
(checker.go:11045-11052). Views are the only uncounted
references, so the only locality-like rule needed is "a view
must not outlive its backing frame" (E063/E065) — one rule, not
an axis; everything else is RC-counted and lifetime-free.

## 2. The four surfaces as implemented today

### 2.1 `own` consuming params — E050 / E051

Surface: `own xs: T` on parameters and receivers (`own self`),
contextual keyword (`internal/parser/parser.go:887,1422,1483`;
`ast.Param.Own`, `internal/ast/ast.go:2497`).

Native analysis: `checkOwnedParams`
(`internal/checker/checker.go:7428`), a flow-sensitive
per-function walk threading a `movedSet` (name → consume
position). `recordExprUses` (checker.go:7538) classifies every
owned-ident occurrence: projection targets, call callees, binary
operands, casts, slices, borrowed-position call args, and
borrowed-self receivers are *borrows*; any other whole-value use
is a *consume*. Using a consumed binding is E050
(checker.go:7670); a consume inside a loop body of a param live
at loop entry is E050 (checker.go:7718); branch joins merge only
non-diverging arms (checker.go:7683); matching an owned
scrutinee moves its pointer payloads into arm bindings, owned
for the arm's scope (checker.go:7778-7814); consuming
(`own self`) and `dyn`-trait methods move their receiver
(checker.go:7315/7350). The call-site guard `guardCallArgs`
(checker.go:7512) enforces E051: an argument in an `own`
position must be provably owned — a fresh construction, another
`own` param, an all-pointer-params-own callee result, or the
self-reassign move shape (`SelfReassignOwnMoveArg`
checker.go:7388, shared verbatim with the IR so checker and rc
agree on what moved) — else E051 (checker.go:7523).

Guarantee: affine (at-most-once) consumption of owned params,
and that nothing borrowed is laundered into an owning sink. This
is the static foundation the IR's move-on-call, reuse, and
consuming-match slices rely on
(docs/OWNERSHIP-INFERENCE-PLAN.md).

Self-host status: **ported.** `own_diags`
(`examples/self_host/checker.fern:6392`) with the `ow_*` family
(checker.fern:6005-6390): E051 at checker.fern:6130, E050 at
6164/6173, self-reassign admission `ow_self_move_admits`
(used at checker.fern:6296).

### 2.2 `fip` functions — E053 (+ E068 at the IR)

Surface: `fip function` / `fbip function` / graded `fip(n)` /
`fbip(n)`, contextual modifiers (`ast.FuncDecl.Fip` ast.go:2647,
`Fbip` ast.go:2658, `FipAllowance` ast.go:2664).

Native analysis: `checkFipFunctions`
(`internal/checker/checker.go:6730`), a program-level default-
deny AST walk over each `fip`/`fbip` body. Rejected with E053:
array literals (6766), tuple/struct literals (6769/6773 — unless
`ctorOK`), f-strings and string concat (6776/6779),
payload-carrying variant construction (6784), methods off the
whitelist (`fipNonAllocMethods` = `{len}`, 6703; plus
`.with(i,v)` on an `own` array root, 6797 — the COW
unique-in-place branch), calls to non-`fip` functions (fip may
call only fip; fbip may call fip|fbip — 6811/6814), indirect
calls (6818), and writes whose root is not an `own` param
(`fipWriteAllocates` 6833, report 6821). `ctorOK`
(fbip or allowance > 0, 6756) relaxes ONLY the constructor
shapes; the IR then verifies each constructor site is
reuse-paired or within the allowance — `verifyFipAllocs`
(`internal/ir/fip_verify.go:57`), E068 at fip_verify.go:107.

Guarantee: verify-don't-enable (checker.go:6707) — the in-place
lowering already happens; `fip` asserts zero heap allocation,
`fbip`/`fip(n)` assert "every allocation is reuse-paired
(± n fresh)". In lattice terms: the function mints no new owned
heap values (allocation = minting owned), and every in-place
write goes through a unique root.

Self-host status: **ported, with the reuse layer behind.** All
three bits are stamped by the parser (`fip`, `fbip`, and the
graded allowance), the E053 walk applies native's constructor
relaxation and its asymmetric call rule, and the IR-side budget
check is `examples/self_host/irfipverify.fern` (#6639 slice 3),
driven by `irlower_run -verifyfip`. It counts the constructor ops
a fresh site lowers to rather than native's single `OpAlloc`, and
names a site by op index — the self-host `ir.Op` carries no source
position.

What is still behind is the *pairing*, not the verification: the
self-host reuse layer pairs the R3 general case but not the R1
struct self-overwrite or the R4 consuming-match rebuild, so a bare
`fbip` native accepts needs a grade here. The counts are pinned by
`TestSelfHostFipCensusOnNativesShapes`, which fails when the port
closes either gap.

### 2.3 Owned `T[]` vs view `[T]` / `str` — E063 / E065

Surface: the type spelling itself. `T[]` (`ast.ArrayType`) is an
owned counted array; `[T]` (`ast.SliceType`) is a non-owning
`{data,len}` view holding no RC reference; `str`
(`ast.StrType`) is the borrowed view of `string`
(docs/OWNERSHIP-TYPES-PLAN.md Phase A).

Native analysis: `checkSliceEscape`
(`internal/checker/checker.go:7084`) — reject `return` of a
slice provably viewing function-local storage: a cycle-guarded
binding chase (`sliceBorrowsLocal` 7112,
`sourceIsLocalStorage` 7139) bottoming out at an array literal
or locally-declared owned array; params are caller-owned and
safe; anything unprovable (call result, global, field read) is
assumed safe. Report: E063 (checker.go:7102). `checkStrEscape`
(checker.go:7183) is the `str` sibling: `strViewsLocal`
(7217) chases ident/slice chains to a local owned `string`;
literals are immortal; E065 at checker.go:7204. Both are
intra-procedural with a documented laundering hole (a view
returned through a callee is not chased — checker.go:7180).

Guarantee: a borrowed view never outlives the frame that owns
its backing store — the one place Fern needs a locality-style
rule (§1).

Self-host status: **ported.** `slice_escape_diags` /
`slc_walk` (checker.fern:2791/2747, E063 at 2764);
`e065_diags` / `e065_stmts` (checker.fern:7347/7246, E065 at
7319).

### 2.4 `@must_consume` — E067

Surface: `@must_consume` on struct/enum decls
(`ast.StructDecl.MustConsume` ast.go:2775, enum 2818;
docs/MUST-CONSUME.md).

Native analysis: `checkMustConsume`
(`internal/checker/mustconsume.go:42`), an at-least-once
obligation walk per binding of a marked type over the rest of
its scope (`mcSeq` mustconsume.go:153): consuming uses are call
arguments, `return`, match/if-let/let-else destructure,
rebinding (obligation transfers), and stores into *marked*
containers; stores into unmarked containers (structs 338, enums
355, arrays 367, tuples 379) and closure captures (390) launder
the obligation — violations at the store site; loop bodies are
opaque (mustconsume.go:27-32); overwriting an unconsumed
binding violates (139). `own` params are exempt
(mustconsume.go:53-55): an own-marked param declares the
function the value's SINK — E067's caller-side at-least-once
plus E050's sink-side at-most-once compose to exactly-once
across the boundary (mustconsume.go:46-52).

Guarantee: the forgotten-obligation bug class (unsent response,
unclosed socket) is a compile error; RC still does all freeing —
zero runtime cost.

**Discrepancy (reported):** `PLT-LANDSCAPE-2026.md` §2.2 calls
this "exactly-once obligations". As implemented E067 is
**at-least-once** (mustconsume.go:21-22, MUST-CONSUME.md
"Deliberately deferred"); exactly-once exists only as the
composition with `own` at a sink boundary.

Self-host status: **ported.** `must_consume_diags` + the `mc_*`
family (checker.fern:2798-3277; reports at 2972, 3009, 3037,
3051, 3063, 3192, 3206), 21 `mc-*` fixtures in the unfiltered
differential gate (MUST-CONSUME.md "Self-host parity").

## 3. Placing everything on the lattice

Derived placements (all existing spellings, no new ones):

- **default param** = borrowed. Borrow inference
  (`inferParamEscapes`, `internal/ir/rc_analysis.go:344`;
  consumed as `paramBorrowable`, ir.go:5187) silently promotes
  non-escaping params back to borrowed under owned-by-default —
  the mode is inferred, never surfaced (the Roc stance,
  OWNERSHIP-INFERENCE-PLAN.md §2).
- **`own` param** = owned at the boundary, droppable, affine
  consume. E050 is the owned→dead transition enforced; E051 is
  the illegal borrowed→owned coercion.
- **fresh construction** = owned (unique at birth); aliasing
  demotes to shared-owned; `is_unique` re-detects uniqueness
  dynamically.
- **`[T]` / `str` view** = borrowed + frame fact; E063/E065 are
  the borrowed-escape rule.
- **closure capture (pointer-shaped)** = borrowed, read-only;
  E049 (checker.go:11082-11087; self-host `e049_*`
  checker.fern:6412-6531) rejects writes through it — a write
  needs ≥ owned, a capture is ≤ borrowed. E049 IS a lattice
  rule.
- **`@must_consume` type** = axis 2 applied per-type; E067.
- **`fip`** = a function-level constraint over the same walk:
  no transition may mint a new owned heap value, all writes
  through unique roots, callees at least as constrained.
  `fbip`/`fip(n)` = the same with "mint" redefined as "mint
  without consuming a reuse token" (E068, IR-side).

Boundary cases, called explicitly:

- **Cell / E057** (`isCellElemType` checker.go:4936, annotation
  check 5021-5033, `cell_new` site 9982-9994; self-host
  checker.fern:4403-4414): **not a mode.** It is a type-shape
  well-formedness rule (cycle-free element types only) that
  protects the lattice's precondition — cycle-freedom is what
  makes RC sound and borrowed references safe without a
  collector. It runs at type-resolution, not in the binding
  walk, and stays separate (§4).
- **Field immutability / E048, E056** (checker.go:11054-11069;
  self-host checker.fern:4375-4401): **not a per-binding mode.**
  It is the *global law* that every heap value is frozen after
  construction — equivalently, OCaml's contention axis collapsed
  to a single point. It is what makes borrowed sound with no
  lifetimes and keeps axis 1 one-dimensional. Syntactic,
  single-node checks; no state machine needed.
- **Closure-capture write-back / E049**: IS a mode rule (capture
  = borrowed; write requires owned), see above — but its
  *motivation* in the code is cycle prevention
  (checker.go:11070-11081), i.e. it also serves the E048 global
  law. Both readings agree on the rule.

## 4. One analysis, four spellings

The single subsuming analysis is a **per-binding mode state
machine** run once per function over the same divergence-aware
walk all four checkers already use:

    state(binding) ∈ { Borrowed, View(root),
                       Owned{live, obligation?},
                       Dead(consumed-at) }

Transitions and their violations:

- bind from fresh construction → Owned{live, obligation if the
  type is marked}; bind from projection/default param →
  Borrowed; bind from slice-of-X → View(root of X).
- consume (arg in `own` position, return, destructure, store
  into marked container, rebind) : Owned{live} → Dead, and
  discharges the obligation. Consume of Dead → **E050**.
  Consume demanded of Borrowed/View → **E051**.
- read/projection: legal in every state except Dead (E050).
- write through binding: requires Owned with unique root
  (today: `own` array param in `fip`; the COW branch elsewhere).
  Write through a Borrowed capture → **E049**.
- `return` of View whose root is frame-local → **E063/E065**.
- scope exit with Owned{live, obligation} → **E067** (also
  overwrite and laundering, which are stores that fail to
  transfer the obligation).
- function-level predicate (`fip`): no transition that mints
  Owned-fresh (allocation), no non-unique write, callees at
  least as constrained → **E053**; the reuse-token refinement of
  "mint" → **E068** (IR-side, after pairing).

E-code → lattice-rule table:

| E-code | Lattice rule violated |
|--------|----------------------|
| E050 | use of a binding in state Dead (affine owned consume: at most once) |
| E051 | coercion borrowed→owned demanded at a call boundary (submoding runs the other way) |
| E053 | function-level: body mints a fresh Owned heap value, or writes through a non-unique root, or calls a less-constrained function |
| E068 | E053's refinement: a mint that consumed no reuse token, beyond allowance n (IR-verified) |
| E063 | View of frame-local storage escapes via return (borrow outliving its owner) — array/slice spelling |
| E065 | same rule, `str` view of a local `string` |
| E067 | Owned value of a must-consume type reaches implicit drop (scope exit / overwrite / laundering store / capture) |
| E049 | write through a binding at mode ≤ borrowed (pointer-shaped closure capture) |
| E048 / E056 | not per-binding: the global no-mutation law that keeps the lattice one-dimensional (fields / subscripts frozen) |
| E057 | not a mode: type-shape precondition (cycle-free Cell elements) protecting RC soundness |

**What stays separate, and why.** E057 (type resolution — no
dataflow); E048/E056 (single-node syntactic law — no state);
E068 (needs the IR's reuse-pairing results, so it runs after
lowering, not in the checker); and the dynamic `is_unique` gate
(uniqueness stays a runtime fact by design — promoting it to a
static mode is the Clean/Granule road Fern deliberately declined,
NICHE-LANGUAGE-RESEARCH.md "Granule / Clean"). Everything else —
E050, E051, E053's walk, E063, E065, E067, E049 — is one
traversal reading and updating `state(binding)`.

Today, natively, that "one traversal" is four-and-a-half:
`checkOwnedParams`, `checkSliceEscape`, `checkStrEscape`,
`checkMustConsume` run back-to-back per function
(checker.go:7055-7058) and `checkFipFunctions` runs
program-level; the self-host mirrors the split (`own_diags`,
`slice_escape_diags`, `e065_diags`, `must_consume_diags`,
`e053_diags`, `e049_*`). They do not disagree — they partition
the rules — but they quadruple the walk boilerplate (divergence
joins, loop opacity, binding chase) and each new rule must pick
a walk to bolt onto. The consolidation is mechanical because
the state they thread is one record.

## 5. Consequences

**(a) For the goal-2 consolidation.** Since the four checker
surfaces are already ported (§2), the actionable order is:

1. **Do not port the remaining IR half four ways.** `fbip` /
   `fip(n)` / E068 port together with the reuse-analysis slice
   (NICHE-BORROWS-PLAN.md E4) as the "mint consumes a token"
   refinement of the already-ported E053 — one new predicate,
   not a new walk.
2. **When any of the four self-host walks is next touched,
   merge it into a shared per-binding walk** (`checker.fern`
   already threads `Scope`; the merged walk threads
   `state(binding)` per §4). Candidate first pair: E063 + E065
   (checker.fern:2747 / 7246 are near-duplicates of the same
   binding chase). Then E067 (same divergence walk), then
   E050/E051 (the richest state). Each merge must keep the
   differential codes gate byte-stable per fixture — the merge
   is an internal refactor with zero diagnostic change.
3. **Keep the IR ownership analyses keyed to the same
   vocabulary**: native `inferParamEscapes`
   (rc_analysis.go:344) / self-host
   `borrowable_params_interproc` and
   `consume_safe_params_interproc`
   (`examples/self_host/irlower.fern:32676` / `:32769`) are the
   inference that assigns axis-1 modes to unannotated params;
   they should be documented (and eventually named) as mode
   inference, not as unrelated escape analyses.

**(b) For #5366 (share-nothing workers): sendable is a derived
judgment, not new machinery.** With per-worker heaps and
transfer-or-copy messaging (PLT-LANDSCAPE-2026.md §2.8), a value
is transferable to another worker exactly when it is **owned and
disjoint**: axis-1 Owned (the sender can consume it — precisely
E051's admission, `isOwnedExpr` checker.go:7462 /
`ow_is_owned_expr` checker.fern:6032) and transitively free of
borrowed/view references, which the type spelling already
exposes (no `[T]`, no `str`, no captured-env closures).
Must-consume obligations transfer with the value (the receiving
worker inherits the E067 obligation, exactly as a callee does
today). Anything not statically owned falls back to deep copy.
The send-site check is E051 with a different message.
This is exactly `docs/MULTICORE-RESEARCH.md`'s constraints C5
(spawn-closure captures checked send-safe; message closures not
sendable in v1) and C6 (sendability derived from this lattice,
not bolted on as a fifth analysis) — the two docs were written
in the same pass and agree; C6's "sendable = owned-and-disjoint"
is the judgment this section derives
(`docs/CONCURRENCY-RESEARCH.md` predates both and does not
discuss sendability).

**(c) For future surface.** If any mode ever earns syntax (a
`unique` assertion, an explicit `borrow` marker à la Koka's
`^`), it slots into axis 1 as a point or a bound — the checker
rule is already defined by §4's state machine. **Explicitly: no
such syntax is proposed here**; the existing spellings (`own`,
`fip`/`fbip`, `T[]`/`[T]`/`str`, `@must_consume`) remain the
complete user-facing mode vocabulary.

## 6. Divergences from OCaml modes (and what was verified)

What Fern's simpler world removes:

- **No mutation ⇒ no contention axis.** OxCaml needs
  uncontended/shared/contended (and portability) because mutable
  state crosses threads. Fern's E048/E056 freeze the heap;
  under #5366's share-nothing shape, refcounts never cross
  threads, so these axes never materialise.
- **No regions/stack allocation ⇒ no locality axis.** OxCaml's
  global ≤ local exists to make stack allocation safe. Fern
  allocates on the RC heap; the only frame-bound references are
  views, so locality shrinks to the single E063/E065 escape rule.
- **Uniqueness is dynamic, not modal.** OxCaml tracks
  unique ≤ aliased statically. Fern checks rc==1 at runtime
  (`is_unique`) and treats `fip` as a shape guarantee that
  degrades gracefully on shared data — Koka's stance — so no
  static unique mode, no Clean-style API duplication.
- **Linearity is split into two cheap halves.** OxCaml's
  once ≤ many is future-facing linearity on arbitrary values.
  Fern pays for it only where annotated: affine `own`
  (at-most-once, E050) plus at-least-once `@must_consume`
  (E067), composing to exactly-once at a sink boundary
  (mustconsume.go:46-52). Granule's ESOP 2022 distinction —
  uniqueness constrains a value's *past*, linearity its
  *future* — is the clean statement of why these are different
  axes (NICHE-LANGUAGE-RESEARCH.md "Granule / Clean").
- **No mode polymorphism.** OxCaml needs mode-polymorphic
  arrows so one `map` works at every mode — the known
  complexity explosion of modal systems. Fern avoids needing it
  three ways: (1) unannotated modes are *inferred whole-program*
  (`inferParamEscapes` fixpoint) and never surfaced in types, so
  there are no mode variables to quantify; (2) the user-facing
  modes sit on nominal boundaries (params, type decls) where
  monomorphic rules suffice; (3) higher-order code is tamed by
  forcing captures to borrowed-read-only (E049), so closures
  never carry interesting modes. The cost is expressiveness Fern
  does not want anyway (no user-visible borrow checker —
  OWNERSHIP-TYPES-PLAN.md "Non-goals").

Verification log (2026-07-19; the sandbox proxy 403-blocks
blog.janestreet.com, oxcaml.org, arxiv.org, and dl.acm.org, so
primary pages were verified through search-result snippets and a
mirrored secondary source rather than full-text fetches):

- **oxcaml.org modes documentation** (`/documentation/modes/intro`,
  `/modes/syntax`, `/uniqueness/intro`) — via search snippets:
  OxCaml documents **eight** modal axes (locality, uniqueness,
  linearity, portability, contention, yield, statefulness,
  visibility); locality is `global`/`local`, uniqueness
  `unique`/`aliased`, linearity `many`/`once`; submoding lets
  `many` pass where `once` is expected and `unique` where
  `aliased` is expected; mode expressions are `@`-prefixed, one
  mode per axis. Full pages unreachable (proxy 403) — details
  beyond these snippets are **unverified**.
- **Jane Street "Oxidizing OCaml" blog series** — existence and
  titles verified via search (*Locality*, *Rust-Style
  Ownership*, *Data Race Freedom*). Bodies unreachable — the
  summary "modes on bindings, fully inferred, backwards
  compatible" comes from snippets, otherwise **unverified**.
- **"Oxidizing OCaml with Modal Memory Management"** — title and
  DOI (10.1145/3674642, PACMPL/ICFP 2024) verified via search
  listing; full text unreachable, contents **unverified** here.
- **Secondary source read in full:** a course handout mirroring
  the OxCaml mode tables (github.com/fplaunchpad/cs6868_s26,
  lecture 11): five axes, `global ⊑ local`, `uncontended ⊑
  shared ⊑ contended`, `portable ⊑ nonportable`, `unique ⊑
  aliased`, `many ⊑ once`, defaults global / uncontended /
  nonportable / aliased / many; modes attach to bindings and
  parameters (`let r @ local`, `fun (x @ unique)`); capturing a
  `unique` value makes the closure `once`. Consistent with the
  oxcaml.org snippets (which add the three newer axes); treated
  as corroboration, not authority.

## 7. Cross-references

- `docs/MUST-CONSUME.md` — E067 design + self-host parity.
- `docs/NICHE-BORROWS-PLAN.md` — E1/E2/E2' execution record
  (fip/fbip/must-consume slices; E2's table row "no self-host
  support" for fip is superseded by its own E2(a) entry).
- `docs/OWNERSHIP-INFERENCE-PLAN.md` — the infer-vs-annotate
  axis, borrow inference (Slice 0/2d), `own`'s two jobs.
- `docs/OWNERSHIP-TYPES-PLAN.md` — the four-value Ownership
  axis (Owned/Borrowed/View/Static), `str` (A1/A2), the
  self-host CS slices. Note: it cites `inferParamEscapes` at
  `ir.go:3173`; the function now lives at
  `internal/ir/rc_analysis.go:344` (minor drift, reported here).
- `docs/RC-PERCEUS-PLAN.md` — Phase 2d-borrow (the borrowed-
  parameter model) and the Perceus borrowed-parameter rule.
- `docs/NICHE-LANGUAGE-RESEARCH.md` — Koka fip/fbip (ICFP 2023),
  Granule linearity-vs-uniqueness (ESOP 2022), Vale Higher
  RAII / Austral (must-consume), Hylo's parameter conventions.
- `docs/PLT-LANDSCAPE-2026.md` §2.2 (this note's origin), §2.8
  (multicore, #5366).
- `docs/CONCURRENCY-RESEARCH.md` — the structured-concurrency
  survey (no sendability sections yet; see §5b).
- `docs/SELFHOST-CHECKER-PORT.md` — port deviations for the
  `mc_*` and other families.
- `docs/REUSE-CONTRACT.md`, `docs/SELFHOST-PERCEUS-REUSE.md` —
  what E068's "reuse token" means and its self-host status.
- Issues: #5365 (this note), #5366 (multicore research doc),
  #4451 (native-convergence freeze preconditions), #4297
  (ownership types), #3457 (legacy AST emitter retirement).
