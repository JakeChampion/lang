# Writing a specification for Fern — feasibility, prior art, and a staged shape

Status: survey + recommendation (2026-08). Not a commitment; this doc
exists to answer "would a spec pay for itself, and if so what kind?"
before any of it is built. Claims about Fern's current state were
checked against the code, not the docs.

Companion reading: `docs/NATIVE-CONVERGENCE.md` (the freeze that gives
this work a deadline), `docs/TEST-GATES.md` (what is and is not gated
today), `docs/LANGUAGE-REVIEW-2026-07.md` §Soundness ("this is a type
system whose soundness is maintained empirically … There is no formal
spec or soundness argument").

## 1. Short answer

**Plausible: yes. Helpful: yes, but only in one particular shape.**

A prose specification of the ECMA-262 kind — a single large normative
document describing the whole surface language — would be a bad
investment for Fern right now. It would take months, it would be
~100k words, nothing would mechanically check it, and
`CLAUDE.md` explicitly treats the current surface as historical rather
than as a constraint to preserve. It would rot faster than it could be
written.

What *would* pay is the opposite decomposition: a **small, mechanically
checked, executable spec of the core**, plus **promoting artefacts Fern
already has** into normative status. Fern is unusually well placed for
this because it already has most of the machinery and none of the
label. The single strongest argument is in §3.1: the roadmap contains a
scheduled event that destroys Fern's current de-facto specification, and
nothing is staged to replace it.

## 2. What Fern already has (audited 2026-08)

A specification is (a) a description of intended behaviour, (b) a way to
tell whether an implementation matches it, and (c) an authority for
settling disputes. Fern has substantial (a) and (b) already, scattered
across `docs/` and `internal/`, and no (c).

| Spec ingredient | Fern's existing version | Gap |
| --- | --- | --- |
| Conformance suite | `internal/e2e/testdata/cases/` — 364 `.fern` fixtures, declarative sidecars (`expected.stdout`, `expected.exit`, `stdin`, `match`, `backends`, `expected.error`), run across interp / x86-64 / arm64 / wasm | Lives inside `internal/`, i.e. inside one implementation. Incidental, not normative: nothing says which behaviours are *required* vs which merely happen to be what the interp does. |
| Conformance report | `selfhost-{wasm,x86_64,arm64}-known-divergences.txt` — per-target expectation files, where a new divergence fails *and* a listed fixture that starts passing fails | Already the right mechanism. Framed as a bug list rather than a conformance delta. |
| Multiple implementations to keep honest | Five: `internal/interp`, three native backends, and the self-host compiler (its own parser + checker + three emitters) | The self-host is measured *against native*, not against a spec. |
| Static-semantics catalogue | 71+ stable diagnostic codes with a `fern explain E0NN` catalogue (`internal/diag/explanations`) and `catalogue_completeness_test.go` forbidding an unexplained code | The codes are stable and explained; the *rules* they enforce are written down only as checker code. |
| Normative prose, per topic | `INTEGER-SEMANTICS.md` (portable, never-trapping, wrapping at width), `FLOAT-SEMANTICS.md` (IEEE core, explicitly under-specified edges), `ARRAY-BOUNDS.md`, `CLOSURE-CAPTURE.md`, `MODE-LATTICE.md`, `REUSE-CONTRACT.md`, `MUST-CONSUME.md`, `ITERATOR-FUSION-CONTRACT.md` | These are spec chapters in all but name — `INTEGER-SEMANTICS.md` even does the hard part, enumerating the *deliberate freedoms*. They are unindexed as such, not cross-referenced from tests, and their status tag is "policy doc". |
| Reference implementation | `internal/interp` (4.4k lines), the oracle for every differential suite | Being frozen (see below). |
| Grammar | None. `internal/parser/parser.go` is 5.9k lines of hand-written recursive descent; `site/…/reference/syntax.md` is 146 lines of examples | The largest single hole. There is no artefact anywhere that says what Fern's syntax *is*. |
| Dynamic semantics | None written. `internal/ir` + the backends | The rc/ownership half — when things are freed, when reuse fires, what `own` promises — is unspecified, and it is precisely where the live bugs are (#6127). |

Rough call: **Fern has perhaps 60% of a specification already, in pieces
that were built for other reasons.** That changes the economics a lot.
The work is mostly consolidation, labelling, and closing two holes
(grammar, dynamic semantics) — not writing a spec from nothing.

## 3. Why it pays here specifically

### 3.1 The native freeze removes Fern's current spec, on a schedule

Today the specification of Fern is "whatever `internal/` does". That is
a perfectly workable arrangement — Python ran on it for decades — and it
is why the divergence files can be phrased as bug lists: when the
self-host wasm leg disagrees with native, native is right *by
definition*.

`docs/NATIVE-CONVERGENCE.md` plans to end that. After goal 2 reaches
parity and the freeze preconditions (#4451) go green, `internal/`
accepts only bugfixes and bootstrap needs, and new language features
land self-host-first or self-host-only. At that moment:

- "native does X" stops being an answer to "is this a bug?", because for
  new surface native will not do X at all;
- the self-host compiler becomes the definition — but it is *one*
  implementation with three emitters that already disagree with each
  other (that is what the divergence files record);
- the 364-fixture corpus keeps running, but its expected values were
  generated by the implementation that is about to stop being
  authoritative.

This is the classic moment a language needs a written spec, and it is
foreseeable rather than hypothetical. Spec work has a natural deadline:
**before the freeze, not after.** Retrofitting is what Rust is doing a
decade after 1.0, at very large cost.

### 3.2 The interesting semantics are the unspecified ones

Fern's syntax is not where the risk is. The risk is in the memory
model, and there the gap between "documented" and "specified" is doing
real damage:

- `docs/TEST-GATES.md` states outright that **allocation volume and
  over-retains are gated by nothing**.
- #6127 found seven unbounded leaks the self-host has and native does
  not — found by *measurement* (`FERN_LEAKCHECK=1`), because there was
  no contract to check against. Four of that issue's own attributions
  were wrong.
- `docs/REUSE-CONTRACT.md` describes reuse as a design; it does not
  define an observable that an implementation can be tested against.
- `own`, the mode lattice, `@must_consume`, FIP, borrow inference and
  drop specialisation all have design docs and zero normative statement
  of what a conforming implementation must do.

A spec that says nothing about syntax and everything about the store
would be the useful one. That is the inverse of what most language specs
spend their pages on, and it follows directly from Fern's actual defect
distribution.

### 3.3 Under-specification has to be written down to exist

`FLOAT-SEMANTICS.md` already gets this right: it publishes a table of
what is portable and explicitly marks NaN bit-patterns as *not*. That is
the single most valuable thing a spec does — it grants implementations
freedom on purpose, so that a backend cannot accidentally become
normative.

Everywhere the table doesn't reach, the opposite is happening by
default. Every incidental behaviour of `internal/interp` — map iteration
order, drop order within a scope, the exact point a temporary dies,
stdout buffering across a trap — is a de-facto requirement the moment a
fixture pins it, whether or not anyone decided it should be. With five
implementations, that is a slow accumulation of unintended constraints,
and the divergence files are partly a record of it.

### 3.4 Nobody in the peer group has one

`CLAUDE.md` names Roc, MoonBit, Rust, Zig and Go as the languages to
crib from. Of those: Go has a deliberately short spec, Rust is
mid-retrofit, and **Roc, MoonBit and Zig have no specification at all** —
Zig says so explicitly and treats `test/behavior/` as the contract. So a
spec is not table stakes at Fern's stage; it is a differentiator, and
one that happens to be much cheaper for Fern than for its peers because
of §2. It is also the kind of artefact that makes a self-hosted,
multi-backend language legible to anyone evaluating it.

## 4. State of the art — prior art worth stealing from

Grouped by technique rather than by language, because the technique is
the transferable part.

### 4.1 Prose spec + separately-maintained conformance suite

The ECMA-262 model, and the most familiar one.

- **ECMA-262 + test262** — the reference example. Worth stealing
  specifically: numbered algorithm steps that implementations and bug
  reports can cite by name; the spec authored in `ecmarkup` (a
  structured HTML dialect) rather than prose, so cross-references and
  algorithm structure are machine-checked; test262's per-file YAML
  frontmatter (`features:`, `flags:`, `includes:`, `negative: {phase,
  type}`) which lets engines slice the suite by feature and lets a test
  declare it must *fail* and how; and engine-side expectation files
  plus the cross-engine conformance dashboards built on them. Fern's
  fixture sidecars are a less expressive version of the frontmatter, and
  the divergence files are the expectation files.
- **Ruby: ISO/IEC 30170 + `ruby/spec`** — the cautionary pair. The ISO
  document standardised a subset in 2012 and is effectively dormant; the
  thing that actually functions as Ruby's specification is `ruby/spec`
  (formerly RubySpec), an executable suite in an RSpec-shaped DSL run by
  CRuby, JRuby, TruffleRuby and others. **The test suite won and the
  prose document lost.** If only one artefact gets maintained, make sure
  it is the executable one.
- **Java: JLS + TCK/jtreg** — the JLS is the most thorough prose spec of
  a mainstream language, and the Java Memory Model (JSR-133) is the
  standing lesson that a *memory* spec is the hard part and can be
  subtly wrong for years. Relevant if `docs/MULTICORE-RESEARCH.md` ever
  lands threads.
- **Go** — the closest template in taste and scale: a short spec
  (EBNF plus prose, readable in an afternoon), a *separate* memory model
  document, and `test/` in the repo. Deliberately says less than it
  could. Fern should aim here rather than at ECMA-262.
- **Kotlin** — a modern retrofit that worked: `kotlinlang.org/spec` is a
  real, structured, semi-formal spec written years after the language
  shipped. Evidence retrofitting is possible; also evidence of what it
  costs.
- **C / C++** — ISO standard, no official test suite. What actually
  finds compiler bugs in practice is differential fuzzing (§4.4), not
  the standard. A pointed data point about where correctness effort
  goes.
- **Python** — the language reference plus "CPython is the spec".
  Exactly Fern's current arrangement with `internal/`, and it works
  right up until the reference implementation stops being the leading
  one.

### 4.2 Executable / mechanised specs — the state of the art

- **WebAssembly** — the gold standard, and directly relevant since Fern
  targets wasm. Four artefacts kept in sync: a prose spec, a *formal*
  small-step reduction semantics with typing rules, an OCaml **reference
  interpreter that is part of the spec repo**, and the `.wast` script
  format in `spec/test/core` that every engine runs as its conformance
  suite. The 3.0-era core spec is generated from **SpecTec**, a DSL in
  which the rules are written once and the prose/LaTeX, the reference
  interpreter, and test generation are all derived. Also **WasmCert**
  (Coq/Isabelle mechanisations). If Fern copies one project's structure,
  this is the one — and it is the ecosystem Fern already lives in.
- **K framework** — write an operational semantics, get an interpreter,
  a symbolic execution engine, a model checker and a verifier for free.
  Real deployments: **KJS** (JavaScript), **KEVM** (Ethereum bytecode),
  the Ellison–Roșu **C semantics**, P4K. The purest "executable spec"
  answer; heavyweight, and a large commitment to an external toolchain.
- **PLT Redex** — the light-weight version, in Racket: model a reduction
  semantics for a *core calculus*, then randomly generate terms and
  differentially test the model against the real implementation. The
  best cost/benefit in this category for a small team, because you model
  a core, not a surface.
- **Ott / Lem / Sail** (Cambridge REMS) — write inference rules once,
  emit LaTeX *and* Coq/Isabelle/HOL *and* executable OCaml. **Sail** is
  how ARM's and RISC-V's ISA specs became machine-readable and
  executable emulators; ARM ships ASL. Directly interesting given Fern
  hand-maintains two instruction-selection layers.
- **Cerberus** — executable semantics for C, including the parts ISO
  leaves ambiguous; the project's real output was discovering how much
  of C nobody agreed on. The analogous exercise for Fern's rc semantics
  would likely find the same.
- **Lean 4's external kernel checkers** (`lean4export` + independent
  re-checkers such as Trepplein / lean4lean) — a self-hosted system that
  keeps a *small, independently re-implementable* core artefact so a
  second program can verify the first's output. Cheap, high-leverage,
  and see §5.4.
- **CompCert (Coq), CakeML (HOL4)** — the fully verified end: a
  mechanised semantics plus a proof the compiler preserves it. Correct
  and out of scope; noted so the ambition is calibrated rather than
  ignored.
- **RustBelt / Iris; Stacked Borrows and Tree Borrows; Miri** — the
  ownership half. The important practical lesson is **Miri**: an
  executable operational semantics for the parts of Rust that are
  otherwise unspecified (aliasing, UB), shipped as a tool users actually
  run. It caught more real bugs than the prose ever did. This is the
  single closest analogue to what Fern's rc/ownership semantics needs.
- **The Definition of Standard ML** (+ **HaMLet**, a reference
  implementation written to follow the Definition rule-for-rule) — the
  classic proof that a real language can be fully specified in ~100
  pages of inference rules. The aesthetic to aim at for a *core*.

### 4.3 Interface / ABI specs Fern already consumes

**WIT + WASI** are machine-checked interface specs with generated
bindings and validators (`wasm-tools`, `wit-bindgen`), and Fern already
ships a `fern` world. Worth noting because it establishes the
in-repo precedent that a machine-checked contract is normal here, and
because the preview-2/3 async ABI churn is a live reminder that a
versioned written contract is what makes a breaking change diagnosable
(`invalid leading byte (0x43)`) rather than mysterious.

### 4.4 Differential and property-based testing — the pragmatic spec

For compilers this is where correctness actually comes from, and Fern is
already doing much of it.

- **Csmith**, **YARPGen**, **EMI / equivalence-modulo-inputs** — random
  program generation plus differential execution against other
  compilers. Fern's cross-backend differential fuzzer for integer
  semantics is the same idea at a smaller scale.
- **sqllogictest** (SQLite) — run the same query against many engines,
  hash the result, compare. Extremely cheap, catches a lot. SQLite's
  other suite (TH3) is proprietary; the free one is the influential one.
- **Alive2** — SMT-based translation validation for LLVM peephole
  optimisations: prove the rewrite is semantics-preserving rather than
  test it. The obvious Fern target is `internal/ir`'s passes (constfold,
  TRMC, tail-call optimisation, the rc passes), which are shared by every
  backend and therefore the highest-value place to be sure.
- **Property-based testing** generally — the round-trip properties Fern
  can state cheaply (parse∘print, IR-verify∘lower, rc-balance) are worth
  more per line than any prose paragraph.

## 5. What an executable spec for Fern should actually look like

Ordered cheapest-and-most-certain first. Each layer is independently
useful; nothing later is required to justify anything earlier.

### Layer 0 — make the existing corpus normative (days)

Move the fixture corpus out from under `internal/` into a top-level,
implementation-independent location with a documented manifest format,
and extend the sidecars so a case's own metadata says how much it
asserts. Keep the existing runner reading it; add a second, tiny runner
that the self-host compiler can drive without Go.

Payoff: 362 incidental tests become the suite the self-host is measured
against once native freezes, and the divergence files become a
conformance report rather than a bug list. This is the highest
value-per-hour item on the list by a wide margin, and most of it is
`git mv` plus a schema.

**Correction, from building it (#6337 + follow-up).** This section
originally proposed the metadata as a `features:` list, a `spec:`
reference, and `normative: required | implementation-defined |
unspecified`. Two thirds of that was wrong, and reading the corpus is
what showed it:

- `features:` and `spec:` had no consumer. Nothing yet slices the corpus
  by feature, and there is no spec document for `spec:` to point into
  until Layers 2–3 exist. Adding either now would have shipped a field
  that nobody fills, which is the same failure mode as an allowlist
  nobody prunes.
- `normative: implementation-defined` had **no instances**. The
  hypothesis was that some cases pin one legal answer among several. Of
  the ten cases asserting less than byte-exact-on-all-backends, not one
  was that. They were an unimplemented backend (1), a limitation of the
  runner rather than of any backend (1), a self-test of the runner (1),
  three assertions weakened for reasons the case's own comment
  contradicted, and four `backends` files that listed all four backends
  and so restricted nothing.

So the shipped design is a **waiver**: a case that asserts less than the
maximum must say which of `implementation-gap` (with a tracking issue) /
`harness-limit` / `unspecified` / `harness-self-test` applies, and a case
that does not weaken must carry no waiver at all. The second half is the
half that pays. `f64_sqrt` had been excluded from arm64 and x86-64 since
`sqrt` needed a libm call to link — which stopped being true, silently,
leaving two backends of coverage missing behind a case that still looked
green. A waiver that must be deleted when it stops applying turns that
from archaeology into a test failure.

The general lesson for the later layers: **the taxonomy has to come from
reading the artefact, not from the survey.** `unspecified` — the
category this doc built the whole design around — is currently the one
kind with no instances at all.

### Layer 1 — extract the grammar, and check it mechanically (1–2 weeks)

Write the EBNF. Then make it *false-if-wrong*: generate a parser (or a
recogniser) from the grammar and differentially test it against
`internal/parser` over every `.fern` in the repo — the 364 fixtures, the
279 examples, and the self-host compiler sources, which together are a
large and adversarial corpus. A grammar nobody checks is a lie within a
month; a grammar with a differential gate is a permanent artefact.

Payoff: closes the largest hole; makes the parser's actual precedence
and ambiguity decisions visible for the first time; is the prerequisite
for any second front-end (a formatter, a syntax-highlighter, a
tree-sitter grammar, another implementation).

**Correction, from building it.** The plan above said "generate a parser
(or a recogniser) from the grammar and differentially test it against
`internal/parser`". That is what shipped (`spec/grammar.ebnf` +
`internal/grammar`, 736/736), and the derivation gate was the easy part —
the first draft reached 731/731 in four iterations. What the plan MISSED
is that derivation alone does not keep a grammar honest.

A production nothing uses is invisible to a derivation gate, and the
first draft had four: `race`/`concurrent` blocks (a **retired** surface
the parser now rejects), `resource` declarations (assembled from a
keyword in the parser's dispatch table; no spelling parses), `use x = e`
(the real form is `use x <- e`), and a bare `x => e` lambda. All four
read exactly like the productions that were true. So the gate grew a
third check — **every rule must be exercised by a real program** — and
that is the one to carry into Layer 3: a semantics with an unexercised
rule has the same defect and is far harder to spot by eye.

Coverage also pays in the other direction. Three rules it flagged were
real but had **zero** conformance coverage — `pub use` re-exports,
struct patterns, and the `use` bind — so the corpus grew by three cases
rather than the grammar shrinking. A grammar-coverage gate doubles as a
corpus-coverage gate, which was not why it was built.

### Layer 2 — promote the diagnostics and the policy docs (days)

The `E0NN` catalogue plus `catalogue_completeness_test.go` is already a
static-semantics chapter with a completeness gate. Publish it as one,
and cross-reference each code to the rule it enforces. Same for the
policy docs — `INTEGER-SEMANTICS`, `FLOAT-SEMANTICS`, `ARRAY-BOUNDS`,
`CLOSURE-CAPTURE`, `MODE-LATTICE`, `MUST-CONSUME` — which need a status
promotion and an index, not a rewrite.

Payoff: forces the question "is the rule behind E035 written down
anywhere?" for 71 rules, and the answer is currently no for most of
them.

**Correction, from building it.** "Days, and a status promotion rather
than a rewrite" was right about the policy docs and wrong about the
diagnostics. Publishing the catalogue is not the work; asking what
*checks* it is. Two things fell out that the plan did not anticipate:

- **The Go tests do not survive the freeze.** 71 of the 75 codes were
  exercised by a test under `internal/checker` or `internal/parser`, so
  by the usual measure the catalogue was well covered. But a Go test
  measures `internal/`, and after the freeze the self-host compiler is
  the definition — a conformance case can be run against any
  implementation, a Go test cannot. By that measure coverage was
  **16 of 75**. It is now 54 of 74, mostly by deriving cases
  mechanically from the catalogue's own examples, which has the side
  effect of checking that those examples produce the errors they claim.
- **`E039` was dead.** It documented a bare `len(x)` builtin with its
  own arity and argument-type errors; no `errfCode` site emits it, and
  `len` is a method, so the example in its own explanation could not
  compile. `docs/SELFHOST-CHECKER-PORT.md` had already recorded the code
  as dead without anyone deleting the explanation, so `fern explain
  E039` went on describing a construct the language does not have. This
  is the diagnostics analogue of Layer 1's invented grammar rules, and
  it is the same lesson: catalogues acquire entries that nothing
  disproves.

The policy docs were indexed rather than rewritten, as planned — but
that half shipped **ungated**, and `spec/README.md` says so rather than
implying otherwise. Nothing checks that a claim in `INTEGER-SEMANTICS.md`
is pinned by a case, or that a case has not quietly contradicted one.
That is the next increment, and it is the diagnostics index's shape
again: derive the truth, make the document match it.

### Layer 3 — specify **Fern Core**, executably (the real work; 1–3 months)

Do not specify the surface language. Specify the core, and let the
surface be defined by desugaring into it.

Fern is unusually lucky here: **the core already exists.**
`internal/ir` is a target-agnostic IR that all five implementations
consume, and every entry in the divergence files is a lowering bug, i.e.
a disagreement *about the core*, not about syntax. So:

1. Define Fern Core: the IR's op set, its typing rules, and a
   small-step operational semantics over a store.
2. Put the memory model **in** the semantics. The store tracks rc
   counts; `inc`/`dec`/`drop`/`reuse` are reduction rules; the trace of
   allocation and free events is an *observable* alongside stdout and
   the exit code. This is the piece that does not exist anywhere today
   and is where the bugs are.
3. Write a reference interpreter for Fern Core. Writing it **in Fern**
   is the honest choice for a self-hosted language and makes it
   runnable on every backend.
4. Gate it: for each corpus program, lower to IR, run under the
   reference semantics, and compare final state, stdout, exit code
   **and the allocation trace** against each real backend
   (`__heap_bump_bytes()` and `FERN_LEAKCHECK=1` are the existing
   observables to reconcile with).

Point 4 is the prize. It converts the entire class of #6127 bugs —
"the self-host leaks where native does not" — from something found by
measuring after the fact into a conformance failure at the point of
divergence. `docs/TEST-GATES.md`'s "nothing gates allocation volume"
stops being true.

If this layer is ever mechanised further, SpecTec is the model to
follow: keep one source of rules and derive the prose, the reference
interpreter and the tests from it, rather than maintaining three
artefacts that drift.

### Layer 4 — an independent IR verifier (1 week; worth doing regardless)

A well-formedness and type checker for `internal/ir` that is *not* the
compiler — LLVM's verifier, or Lean's external kernel checkers. Run it
between lowering and emit on every backend.

Payoff is immediate and independent of the rest of this doc: self-host
lowering bugs currently surface as a SIGSEGV or a silent miscompile
several stages downstream (the closure-dispatch cluster
#5001/#5007/#5009/#5026 is the example), and a verifier turns most of
them into an error naming the malformed op. It is also the natural
enforcement point for Layer 3's typing rules, which is why it belongs
here rather than in a testing doc.

### Layer 5 — mechanisation and translation validation (not recommended yet)

Coq/Lean/K mechanisation of the core, or Alive2-style SMT validation of
the `internal/ir` passes. Both are real and both would be excellent; both
are large, and neither addresses a problem Fern is currently losing time
to. Revisit after Layer 3 exists, since Layer 3's rules are the input
either would need.

## 6. Recommendation

1. **Do Layers 0, 2 and 4 opportunistically.** They are days-to-a-week
   each, they are useful in isolation, and none of them commits the
   project to a specification effort.
2. **Do Layer 1 (the grammar, with a differential gate) as a discrete
   piece of work.** It is the biggest single hole, it is finite, and it
   cannot rot silently once gated.
3. **Design Layer 3 around Fern Core and the store semantics — not
   around the surface syntax — and treat the native freeze as its
   deadline.** The freeze is what makes it necessary; before the freeze
   is when it is cheap, because native is still available as an oracle
   to *validate the spec against*. Writing a semantics while you still
   have a trusted implementation to check it with is far easier than
   writing one afterwards.
4. **Do not write an ECMA-262-shaped prose document.** If exactly one
   artefact is going to be maintained, `ruby/spec` says it must be the
   executable one.

The honest counter-argument: all of this is orthogonal to goals 1 and 2
in `CLAUDE.md`, and Layer 3 is comparable in size to a roadmap goal. It
should not displace the Perceus port. But Layers 0/1/2/4 total maybe
three weeks and materially harden the freeze, and Layer 3 is the thing
that would let goal 2 declare *done* against a definition rather than
against a diff with native.

## 7. Open questions

- Does the spec cover the stdlib, or only the language? (test262 covers
  the JS library; Go's spec does not cover its stdlib.) Suggest: language
  only, with the `std/test` TAP contract specified separately.
- Is `internal/ir` stable enough to be the normative core, or does
  specifying it freeze it prematurely? It has churned (`OpExt`, the
  `WidthPtr` sentinel, the typed-IR rewrite) — but always structurally
  rather than semantically, which is the right kind of churn for this.
- What is the versioning story? A spec implies editions. Fern has no
  stability commitment yet, and issuing one implicitly via a spec would
  be a bigger decision than the spec itself.
- Where does the deliberate under-specification list live, and who
  maintains it? `FLOAT-SEMANTICS.md`'s table is the prototype; it needs
  siblings for map iteration order, drop order, temporary lifetimes,
  and the rc observables.
- Naming: the language is Fern; the Go module path and repo are still
  `lang`. A published spec would be the point at which that becomes
  awkward in public.
