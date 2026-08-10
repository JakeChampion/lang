# Niche-language research: borrowable ideas for Fern

Survey of niche, research, and unusual programming languages for
concrete design mechanisms Fern could borrow, run 2026-07-12.
Companion to the mainstream-language survey in
`LANGUAGE-DIRECTION.md` ("Convergent signals from cross-language
research" mined MoonBit / Roc / Zig / Odin / Hare / Gleam / Rust);
this one deliberately goes further afield: array languages (BQN,
Uiua, APL/J/K), concatenative languages (Factor, Forth, Joy),
the Perceus research lineage (Koka, Lean 4, the FIP calculus,
Granule), capability/linearity systems (Pony, Austral, Vale,
E/Monte), and a long tail (Icon, Mercury, Nushell, Janet,
Lobster, Futhark, Clean, Eiffel, Erlang, Unison, Hylo, Rebol,
Smalltalk/Self/Io, Raku).

## Method and evidence tiers

The survey ran through a fan-out research harness: 5 search
angles → 21 sources fetched → 80 falsifiable claims extracted →
the top 25 adversarially verified by 3 independent refute-voters
each (23 confirmed, 2 refuted). Verification budget concentrated
on the memory-management, effects/capabilities, and
metaprogramming axes, so claims are labelled by tier:

- **[verified]** — survived 3-0 adversarial verification against
  primary sources (papers, official docs).
- **[sourced]** — extracted verbatim from a fetched primary or
  reputable source, but not run through the refute-vote pass.
- **[background]** — assistant background knowledge of the
  language, not independently verified this pass. Treat as
  directionally right; re-verify before building on a specific
  technical detail.

Two claims were **refuted** and must not be relied on: "Koka's
fip/fbip annotations are not yet available" (false — they shipped
in Koka v2.4.2 and persist in v3.x) and "Lean 4 is entirely
self-hosted" (overstated — ~90% Lean, with a C++ runtime/kernel
core remaining).

Ideas Fern already shipped or explicitly rejected are not
re-proposed (pipe `|>`, Gleam `use`, `let else`, match guards,
traits, Perceus RC itself, structured `concurrent{}` decision,
rejected lazy-iterator chains / function coloring / open effect
systems — see `LANGUAGE-DIRECTION.md`).

---

## Part I — The Perceus lineage (highest-value axis)

Fern's goal-2 work (porting Perceus to the self-hosted compiler)
sits in an active research lineage. The verified findings here
are directly actionable.

### Koka — reuse as *specified behavior*, and the FIP calculus

**Mechanism 1: guaranteed, programmer-visible reuse.** [verified]
Koka's book documents reuse analysis as a *language guarantee*,
not an opaque optimization: pattern-match destructuring is paired
with same-sized constructor allocations per branch, and "the
reuse optimization is guaranteed and a programmer can see when
the optimization applies" — mapping over an unshared list is
specified to be zero-extra-allocation. Verifiers flagged an
important refinement: the original PLDI 2021 reuse analysis was
found fragile and superseded by **drop-guided reuse** (Lorenzen &
Leijen, ICFP 2022, "Frame-Limited Reuse") — a Fern port should
target the drop-guided variant, not the original algorithm.
Sources: koka-lang.github.io/koka/doc/book.html; PLDI 2021
Perceus paper (dl.acm.org/doi/10.1145/3453483.3454032).

**Mechanism 2: the `fip` / `fbip` annotations (ICFP 2023 FIP
calculus).** [verified] A statically checked per-function keyword
proving the function executes with **zero heap (de)allocation and
constant stack space**. Implemented on top of Perceus in Koka
v2.4.2+: the compiler emits one `is-unique` check per destructive
match, so a single function body updates in place when
refcount==1 and gracefully falls back to fresh allocation when
shared — avoiding the code duplication Clean-style uniqueness
typing forces. The static calculus (λ-fip: reuse credits,
borrowing, atoms, unboxed tuples) is proven a strict subset of an
extended Perceus linear resource calculus, so FIP functions
coexist with ordinary code at no RC overhead beyond the
uniqueness check. Concrete discipline [sourced]: owned parameters
are consumed by destructive match (yielding a reuse credit sized
to the constructor); borrowed parameters (`^`) may be shared but
not destructively matched, stored, or returned; graded variants
`fip(n)` / `fbip(n)` permit at most n allocations (splay-tree
insert is `fip(1)`). Caveat carried by verifiers: in-place
execution at runtime still requires the argument to be
dynamically unique; shared inputs run correctly but allocate.
Sources: webspace.science.uu.nl/~swier004/publications/2023-icfp.pdf;
microsoft.com/en-us/research/wp-content/uploads/2023/07/fip.pdf.

**Mechanism 3: full effect rows.** [verified] Every Koka function
type has three parts — arguments, effect row, result — with
concrete effect constants (`total`, `exn`, `div`, `console`,
`ndet`; `pure` = `exn`+`div`; `st`; `io`) and row polymorphism
(`map`'s effect is exactly its argument function's effect);
effect handlers then make exceptions, generators, and async/await
typed user libraries. This is the maximal end of the spectrum
Fern already weighed in `LANGUAGE-DIRECTION.md` (Kyo entry) and
deliberately scaled down to a closed effect vocabulary. Nothing
here reverses that call; Koka is the reference if the effect-row
prototype ever runs.

**Fit:** the memory work is *directly adoptable* — Fern's
existing pipeline (reuse analysis, borrow inference, drop
specialisation) is exactly the substrate `fip` slots into. Effect
rows remain deliberately deferred.

### Lean 4 — production proof that RC+reuse scales to a self-hosted compiler

[verified] Lean 4's runtime uses reference counting with FBIP
execution — the destructive-reuse-on-unique-refcount technique
from "Counting Immutable Beans" (Ullrich & de Moura, IFL 2019),
the direct ancestor of Perceus — powering a (mostly) self-hosted
compiler and Mathlib at scale. RC-with-reuse is not a research
toy; it carries a theorem prover's own compiler in production.
The self-hosting claim is ~90% (the kernel/runtime core stays
C++), which is precisely Fern's own trajectory (Go native
compiler as oracle + runtime, Fern self-host on top). Lean is the
strongest existence proof available that Fern's goal-1 + goal-2
combination is sound.

Lean's metaprogramming architecture is covered in Part III.

### Granule / Clean — linearity vs uniqueness, precisely

[verified] The ESOP 2022 Granule work (Marshall, Vollmer &
Orchard) nails a distinction that matters for goal-2 design
conversations: **linear types restrict a value's future** (no
duplication/discard ahead); **uniqueness types guarantee its
past** (never aliased). Perceus-style reuse needs the
*past-facing uniqueness* guarantee — which Perceus checks
dynamically via refcount==1, and which Clean-style uniqueness
types make a compile-time guarantee (their classic payoff:
statically-safe in-place array update without a state monad,
empirically confirmed faster than copy-based update in Granule's
benchmark [sourced]). Both disciplines can coexist in one type
system (Granule adds uniqueness as a third modality).

**Fit:** the conceptual distinction is directly usable design
guidance (it names what a future `unique`/`iso` annotation must
mean); a full graded-modal system is research-grade / poor fit.

### Pony — reference capabilities; `iso` as the borrowable kernel

[verified] Every Pony reference's capability is part of its
static type (a compile-time qualifier, zero runtime cost,
strictly stronger than `const`). `iso` is read-and-write-unique —
exactly the statically-known-unique property that upgrades
Perceus reuse from opportunistic (dynamic refcount==1) to
*guaranteed*. But the full system is six capabilities (`iso`,
`trn`, `ref`, `val`, `box`, `tag`) with strict
denial-of-alias rules and a real learning burden. Pony's
deny-style specification ("what other aliases are forbidden from
doing") is also a good documentation pattern for any annotation
Fern adds [sourced].

**Fit:** a single `iso`-like uniqueness annotation is
*adaptable*; the six-capability system is *poor fit* for a
small-CLI language. Counterpoint to weigh [sourced]: Roc's FAQ
explicitly refuses linear/uniqueness types forever, arguing they
move behind-the-scenes optimizations into the type system and tax
all users. Fern's cheapest path is Koka's: keep types clean, get
the static guarantee via the opt-in `fip`/`fbip` function
annotation instead of a type-level capability.

### Vale — generational references, linear style, Higher RAII

**Generational references** [sourced, self-reported figures]:
non-owning references carry a generation number checked on
dereference (~11% overhead in the author's own single benchmark
vs ~25% for naive RC; not independently reproduced). As a *whole
memory model* it's a poor fit — Fern already committed to Perceus
— but the supporting result is relevant: Vale claims residual
checks can be driven to zero by "linear style" (single-ownership,
move-oriented code) — corroborating that uniqueness information,
which Fern's reuse analysis already computes, can eliminate
safety/RC overhead entirely on hot paths.

**Higher RAII / linear must-consume types** [sourced]: a
single-ownership value that *forbids implicit drop* can enforce
arbitrary future obligations at compile time — fulfil a promise
exactly once, commit-or-roll-back a transaction, close a TCP
connection — by handing the caller an undroppable zero-sized
"reminder" object. Rust/C++ cannot express this (their types are
mandatorily affine); it needs a per-type undroppable marker, not
a borrow checker. For Fern's edge-handler shape ("response sealed
at end of scope", sockets, subprocess handles) a `@must_consume`
struct attribute checked by the same checker machinery as E063
would statically enforce "respond exactly once" — and it layers
cleanly on RC (the checker forbids the last drop from being
implicit; Perceus still does the freeing).

**Fit:** generational refs poor fit; must-consume marker types
*adaptable* and unusually well matched to the HTTP-handler use
case.

### Lobster / Hylo / Austral / Inko — corroborating data points [background]

- **Lobster** does compile-time RC elision via flow analysis
  (claims ~95% of RC ops removed statically) — same family as
  Perceus, corroborates the approach from an independent
  implementation.
- **Hylo (Val)** bets on *mutable value semantics*: `let` /
  `inout` / `sink` / `set` parameter conventions instead of
  references. The borrowable kernel is the **parameter-passing
  vocabulary**: declaring at the signature whether a parameter is
  read, mutated in place, or consumed is exactly the
  owned/borrowed distinction Fern's borrow inference already
  computes internally — Hylo shows a clean surface syntax for it
  if Fern ever wants to let users state it (`fip`'s `^` borrow
  marker is the lighter-weight version).
- **Austral** is the "linear types, no exceptions, no macros,
  capability-based FFI" design point: every IO-capable operation
  requires threading an unforgeable capability value. Its
  radical-simplicity posture (a spec you can read in an
  afternoon) is closer to Fern's temperament than Pony's; the
  capability-value idea reappears (verified) in Roc's platform
  model below, which is the better-packaged version for Fern.
- **Inko** runs actor-style processes with per-process heaps over
  RC and no function coloring — corroborates Fern's colorless
  `concurrent{}` decision from an independent design.

---

## Part II — Stdlib & runtime architecture for CLI + edge HTTP

### Roc — the platform model (strongest architectural borrow)

[verified] Three mechanisms, all confirmed against primary
sources:

1. **I/O-free stdlib.** Roc's standard library contains only data
   structures; *every* I/O primitive comes from the "platform"
   the application targets. A platform therefore restricts at
   build time which effects are even *expressible* — a browser
   platform exposes no file I/O; a webserver platform no blocking
   stdin.
2. **No escape hatches.** Even FFI must go through
   platform-provided primitives, enabling capability-style
   security (e.g. a "safe scripting" platform that prompts before
   harmful I/O). (Design assertion by the project, not
   independently audited.)
3. **Host-supplied allocator.** The host provides malloc/free, so
   a platform tailors memory management — the `nea` web server
   allocates each request into an arena where free is a no-op and
   the arena resets after the response, making heap allocation
   roughly stack-cheap. (nea itself is WIP/possibly dormant.)

**Fit for Fern: adaptable, and the best architectural idea in the
survey.** Fern already *is* multi-platform (arm64/x86-64 Linux,
arm64 Darwin, wasm32 WASI, `-target wasm32-wasi32-wasi-http`), already routes
IO through per-target runtime helpers, and already had (then
generalised away) a per-request arena. The Roc framing turns that
accumulation into an architecture: define the target profile as
*the* capability boundary — `wasi-http` simply does not link
`subprocess`/`read_line`, and the checker can reject them at
compile time rather than trapping at runtime. This also converges
with the closed-effect-vocabulary sketch already in
`LANGUAGE-DIRECTION.md` (the runtime, not user code, discharges
effects) and gives it a cheaper enforcement point: per-target
symbol availability instead of effect rows in types. The
host-supplied-allocator half maps to letting embedders (edge
hosts) provide the bump-arena backing under Perceus.

### Erlang / Elixir — crash-only error philosophy [background]

"Let it crash": don't defensively handle corrupt states; isolate
failures at a supervision boundary and restart from known-good
state. Fern's edge-handler model has a natural supervision
boundary — one handler invocation — but today a trap
(bounds-check, arena exhaustion, assert) kills the whole process,
which for a long-running `tcp_serve` loop or `wasmtime serve`
pool is the wrong blast radius. The borrowable policy (not
mechanism): **per-request fault isolation** — a trapping handler
yields a 500 and the server keeps serving. On wasm this is nearly
free (instance-per-request or trap-then-reinstantiate); native
needs the runtime to catch the trap signal and unwind the request
arena. Worth an explicit design note when native `tcp_serve`
hardening comes up. Rating: adaptable (policy-level).

### Nushell — structured pipelines [sourced]

Nushell pipes **typed structured data** (not text) between
commands; every command declares a typed input/output signature
(`list<any> -> table`) inspectable via `help`; rendering happens
only at the end via a `display_output` hook — computation is
separated from presentation. It also has an implicit **topic
variable `$in`** (the current pipeline input) with defined
scoping, and documents that using `$in` on a stream forces
collection (materialisation cost made explicit).

**Fit:** Fern is not a shell, but two ideas transfer: (a) for
`|>` chains where the value isn't wanted as the *first* argument,
a topic placeholder (`x |> parse(_, opts)`) is the established
answer — Fern's pipe is data-first (matching Roc's verified
rationale: piping into the first argument composes with
trailing-lambda style), and a `_` placeholder generalises it
without breaking the common case; (b) the "commands return
values; the driver renders" split is the right shape for
`ferndoc`/CLI tooling output. Rating: (a) directly adoptable
small win; (b) a stdlib design posture.

### Janet / Rebol / Red / Raku — grammars in the stdlib [background]

Three unrelated ecosystems converged on shipping a **PEG /
grammar engine in the standard library** as the primary
text-processing tool (Janet's `peg` module, Rebol/Red's `parse`
dialect, Raku's first-class grammars) — and all three communities
treat it as strictly better than regex for real formats:
composable, readable, no catastrophic backtracking. Fern's stated
use cases are *exactly* the workloads this serves (CLI tools
parsing config/logs/CSV; edge handlers parsing routes, headers,
content types), and Fern has no regex engine — a deliberate gap a
`std/peg` would fill without one. A pure-Fern PEG module needs
only closures + recursion (both IR-mature) and would be a strong
stdlib differentiator. Rating: **directly adoptable** (stdlib
work, no language surface).

### Icon — goal-directed evaluation [background]

Every Icon expression either *succeeds with a value* or *fails*,
and failure drives control flow: `if x > 5` is success/failure,
generators re-suspend to produce alternatives, and `every`
iterates all results of a goal-directed expression. It's the most
elegant ancestor of `Option` + `?`. Fern already has the
practical 80% (Option/Result, `?`, `if let`, iterators);
full goal-directed evaluation (implicit backtracking across
expression boundaries) is a whole-language semantics, poor fit.
The remaining borrowable sliver: Icon's *bounded* backtracking
suggests generator-style `for x in gen()` resumable functions —
which Fern's CPS/state-machine concurrency substrate could later
expose cheaply as sync generators. Rating: research-grade.

### Prolog / Mercury — determinism annotations [background]

Mercury's borrowable idea is per-predicate **determinism
declarations** (`det`, `semidet`, `multi`, `nondet`) checked by
the compiler. The Fern-shaped echo: function-level checked
annotations of a semantic property, same shape as `fip`. No
direct borrow beyond that pattern — logic-variable semantics are
poor fit. Rating: poor fit (pattern already arriving via `fip`).

---

## Part III — Metaprogramming & compile-time evaluation

Fern's current posture (`LANGUAGE-DIRECTION.md`): comptime
deferred "until we feel the pain of separate generic + const
systems." This survey sharpens what to build *if/when* that pain
arrives.

### Zig — comptime's discipline is the borrowable part [sourced]

The load-bearing properties of Zig comptime are its
*restrictions*:

- **Target-faithful:** comptime code observes the *target's*
  usize width/endianness, never the host's — comptime behavior
  matches runtime behavior under cross-compilation. (Fern is
  always cross-compiling — wasm32 vs arm64 vs x86-64 with
  different `WidthPtr` — so this rule is existential: a Fern
  comptime that observed the host would miscompile every
  multi-target program.)
- **Hermetic:** no I/O whatsoever at comptime — evaluation is
  reproducible and cacheable; build-time codegen needing external
  data belongs to the build system.
- **One mechanism, no macros:** no token-tree macros, no string
  mixins; partial evaluation of ordinary code (`comptime`
  parameters + `inline for` + `@typeInfo`) covers reflective
  printing, generic containers, and compile-checked format
  strings (format DSLs passed as comptime strings).
- Known cost: no declaration-site type checking of comptime code,
  and comptime-generated types can't grow methods.

### Lean 4 — the architecture reference [verified]

Lean exposes essentially every compiler stage — parser,
elaborator, tactics, pretty printer, code generator — to user
extension, from ordinary user code, with the meta layer itself
written predominantly in Lean. The copyable core is the **staged
two-tree pipeline**: parsing produces a concrete syntax tree
(`Syntax`); macros are Syntax→Syntax rewrites applied per-node
before/during elaboration; elaborators map Syntax to the typed
core `Expr`. Clean separation, hygiene handled in the macro
monad.

**Fit assessment for Fern:** full Lean-style extensibility is
research-grade overkill. But Fern *already* does macro-like work
as ad-hoc parse-time desugars (f-strings, `use`, `for-in`, tuple
matches in the self-host parser) — each hand-fused into the
parser. The Lean lesson: when the next few desugars land, factor
them as explicit CST→CST rewrite passes over a stable syntax-tree
API rather than parser inlining; that is 80% of a macro system's
internal architecture with none of its user surface, and it keeps
the door open. Combined rating: Zig's *rules* + Lean's *pipeline
shape* = the design brief for an eventual Fern comptime;
directly adoptable as internal refactoring guidance today.

### Strymonas (POPL 2017) — the fusion contract for iterators [sourced]

The staging-based strymonas library achieves **complete fusion
with a compositional guarantee**: if each operator individually
runs without calls/allocation, the whole composed pipeline
(including zip, flat_map, take, filter over finite and infinite
streams — the operators that defeat simpler fusion schemes)
compiles to a single fused loop with zero intermediate
structures. Its motivation section matches Fern's standing
concern verbatim: library-level stream APIs pay order-of-magnitude
penalties vs hand-written loops.

**Fit:** Fern already rejected Rust-style lazy chains *until the
IR can fuse*. Strymonas defines what "can fuse" should mean: a
**guaranteed, compositional contract** (like Koka's guaranteed
reuse — specified behavior, not best-effort optimization). The
staging mechanism itself would arrive with comptime; near-term,
the contract can be an `internal/ir` fusion pass over the
existing cursor-iterator protocol with a documented operator
algebra. Rating: adaptable (design principle now, mechanism
later).

---

## Part IV — Syntax & paradigm outliers (BQN, Factor, and friends)

The user-facing paradigms here mostly rate poor-fit for a
statically-typed CLI/edge language — but each family contributes
one transferable lesson.

### BQN (and APL / J / K / Uiua) [sourced + background]

Verified-adjacent facts [sourced from mlochbaum.github.io/BQN]:
BQN has a **context-free grammar** where syntactic *roles* (not
runtime types) determine how tokens combine — the redesign that
makes first-class functions possible in an APL descendant; its
"based array model" eliminates APL's floating-array/boxing
surprises; and it **self-hosts** (compiler written in BQN,
bytecode-compiled by CBQN, a low-dependency fast-startup C
runtime) — an existence proof that even an array language can
self-host on a small native core, mirroring Fern's own
bootstrap.

Borrowable lessons, honestly assessed:

1. **Context-free grammar as a tooling asset.** BQN's single
   biggest engineering win. Fern is already close; the lesson is
   *stay* close — every future syntax decision that requires type
   feedback or unbounded lookahead (the deferred explicit
   type-args `f[i32](x)` ambiguity is exactly such a case) taxes
   the LSP, the formatter, and the self-hosted parser forever.
   Treat grammar simplicity as a budget, spend consciously.
2. **The leading-axis model / rank polymorphism.** Poor fit —
   requires an array-shaped runtime and dynamic rank dispatch
   Fern doesn't want.
3. **Tacit trains and combinators.** Poor fit as syntax (write-
   only for the target audience), but the *underlying itch* —
   composing transformations without naming intermediates — is
   already served by `|>`; a topic placeholder (Part II) finishes
   the job.
4. **Uiua** (stack + array hybrid) [background]: notable for
   making rank polymorphism work without variable names at all;
   same poor-fit verdict, plus its glyph-based source conflicts
   with Fern's plain-text ergonomics.

### Factor (and Forth / Joy / Kitten) [background]

Factor's distinctive mechanisms: **quotations** (anonymous code
blocks as first-class stack values) + a rich **combinator
vocabulary** (`bi`, `tri`, `cleave`, `map-reduce`) instead of
variable binding; **stack-effect declarations** (`( a b -- c )`)
statically checked — an unusually early "types for a dynamic
language" success; and an interactive image-based workflow.
Kitten shows the statically-typed variant is viable.

Fit: the concatenative paradigm itself is poor fit (point-free
data flow trades named locals for stack choreography — wrong
trade for Fern's audience). Two transferable lessons: (a)
Factor's `with-` resource combinators and quotation-passing style
validate the `use`-keyword/trailing-callback direction Fern
already shipped; (b) stack-effect declarations are another
instance of the "small checked semantic annotation on a function"
pattern (`fip`, Mercury determinism) — the survey's recurring
shape. Nothing further to take directly.

### Smalltalk / Self / Io, Rebol / Red, Unison, Futhark, Eiffel, E/Monte [background]

- **Smalltalk/Self/Io:** image-based development and prototype
  objects — poor fit for an AOT fast-startup language. One echo:
  Smalltalk's keyword messages read as self-documenting calls;
  Fern's answer (already available) is good parameter names +
  struct-literal option arguments. No action.
- **Rebol/Red:** dialecting (embedded DSLs over a uniform data
  syntax) — the `parse` dialect lesson is absorbed into the
  `std/peg` recommendation (Part II). Full dialecting is poor
  fit.
- **Unison:** content-addressed definitions (code identified by
  AST hash; renames are free; incremental everything). As a
  language mechanism, poor fit (demands the whole codebase-as-
  database model). As *infrastructure inspiration* it is real:
  Fern's self-host fixpoint builds and `/tmp/selfhost-bincache-*`
  are already function-hash-shaped problems; content-addressed
  caching of per-function IR/asm in the bundle cache is the
  practical borrow (build-tooling work, no language surface).
- **Futhark:** size-dependent types and fusion for GPU array
  programs — wrong target domain; its fusion-guarantee culture
  points the same direction as strymonas. Poor fit.
- **Eiffel:** design-by-contract (`require`/`ensure`/`invariant`
  clauses, inherited and tool-visible). Fern's TigerStyle
  adoption already commits to `assert(cond, "msg")`; the
  incremental Eiffel borrow is *placement* — allowing asserts to
  be written in a declaration-adjacent position (a `requires`
  line under the signature) so they document as they check.
  Adaptable-lite; revisit when `assert` lands.
- **E / Monte:** object capabilities — no ambient authority; you
  can only affect what you were handed. The principle arrives in
  Fern via the Roc-platform framing (Part II) at zero type-system
  cost: the target profile decides what authority exists at all.
  Vats/promise-pipelining: poor fit.

### Gleam / Odin — already-mined languages, residual small wins [background]

Both were covered in the mainstream survey (`use`, allocator
context), but two small items weren't taken then:

- **Gleam's `todo` keyword:** typechecks as any type, compiles,
  and panics with location + message at runtime; the compiler
  warns on every `todo` left in the tree. Cheap to add (parser +
  checker + a trap), directly useful for Fern's own
  compiler-porting workflow (stub the next `asm_ir.fern` case,
  keep the fixpoint building). Directly adoptable.
- **Odin's `or_else` / `or_return`:** subsumed by Fern's `?` +
  `get_or`; no action.

---

## Part V — Ranked shortlist: ten most promising borrows

Ranked by (value to Fern's stated goals) × (evidence strength) ×
(implementation cost). Items 1–3 are goal-2-adjacent and
verified; the rest descend from architectural to ergonomic.

1. **Target drop-guided reuse (ICFP 2022) in the Perceus port,
   and specify reuse as guaranteed behavior** — [verified]
   (Koka). Goal 2's reuse-analysis slice is the remaining large
   piece; port the drop-guided variant (the original algorithm is
   documented-fragile), and write the reuse contract into Fern's
   docs the way Koka's book does, so users (and the self-host
   compiler's own hot loops) can rely on it.
2. **`fip` / `fbip` function annotations** — [verified] (Koka,
   ICFP 2023). A checked per-function guarantee of
   zero-allocation / constant-stack execution that slots directly
   into the existing inc/dec + borrow + reuse pipeline, with
   graceful runtime fallback on shared data. The single
   highest-leverage *language-surface* borrow available; also the
   answer to "should uniqueness enter the type system?" — no
   (Roc's objection stands), it enters as an opt-in function
   annotation.
3. **Roc's platform split: capability-scoped stdlib per target +
   host-supplied allocator** — [verified]. Formalise what Fern's
   targets already half-do: the target profile *is* the
   capability boundary (compile-time rejection of primitives the
   target doesn't wire, e.g. `subprocess` under `wasi-http`), and
   edge hosts supply the arena backing under Perceus. Cheaper
   than effect rows, converges with the existing closed-effect-
   vocabulary sketch.
4. **`std/peg` — a PEG/grammar module in the stdlib** —
   [background] (Janet / Rebol / Raku convergence). Three
   ecosystems independently made grammars the stdlib's
   text-processing core. Pure-Fern, no language surface, serves
   both stated use cases directly, and doubles as a self-host IR
   widening workload.
5. **Must-consume (linear) marker types** — [sourced] (Vale's
   Higher RAII / Austral). A `@must_consume` attribute enforcing
   "this value must be explicitly consumed before scope exit" —
   statically guarantees respond-exactly-once handlers, closed
   sockets, committed transactions. Layers on RC; enforcement is
   checker-side, same family as E063.
6. **Fusion as a guaranteed contract for iterators** — [sourced]
   (strymonas POPL 2017). When lazy iterators return to the
   agenda, adopt the compositional guarantee ("if each operator
   is non-allocating, the composed pipeline is") as an
   `internal/ir` pass over the cursor protocol with a documented
   operator algebra — the same specified-not-best-effort stance
   as item 1.
7. **Per-request fault isolation (crash-only handlers)** —
   [background] (Erlang). A trapping handler returns 500; the
   server survives. Policy-level change to `tcp_serve` / the
   wasi-http wrapper; pairs with the arena/RC teardown that
   already exists per request.
8. **Comptime design brief: Zig's rules + Lean's pipeline** —
   [sourced]/[verified]. If/when comptime lands: hermetic (no
   I/O), target-faithful (observes target layout, critical for
   Fern's three backends), one partial-evaluation mechanism, no
   token macros; internally, restructure parse-time desugars as
   explicit CST→CST passes (Lean's Syntax/Expr split) starting
   with the next desugar that lands.
9. **Pipe topic placeholder `_`** — [sourced] (Nushell's `$in`;
   Roc's data-first pipe rationale). `x |> f(a, _)` for the
   minority of pipelines where the value isn't the first
   argument; parse-time desugar, formatter round-trip, no codegen
   impact. Small, immediate ergonomic completion of `|>`.
10. **Gleam's `todo` keyword** — [background]. Typed hole that
    compiles and traps with location; compiler warns on
    leftovers. Trivial cost, immediately useful for the
    self-host porting workflow itself.

Honourable mentions: content-addressed function-level build
caching (Unison-inspired, tooling-only, would attack the 16–18 GB
self-host build peak); `requires`-position asserts (Eiffel) once
TigerStyle `assert` ships; BQN's grammar-simplicity budget as a
standing review criterion for new syntax.

## Open questions raised by the survey

1. `fip` fallback semantics: Koka's `fip` still allocates when an
   argument is dynamically shared. Should Fern's variant follow
   (annotation = *shape* guarantee), or offer a strict mode that
   requires provably-unique arguments at call sites (approaching
   Pony `iso` / Granule uniqueness)? Koka's choice is the cheaper
   first step; measure before strengthening.
2. Cycle policy under Perceus for long-lived processes: the
   verified garbage-freedom guarantee is cycle-free-only. Fern's
   current answer (no process-lifetime state; leak-on-cycle
   acceptable within a request) holds for the stated use cases,
   but the self-hosted compiler is the standing counterexample —
   `docs/CYCLE-COLLECTION-ANALYSIS.md` remains the tracking doc.
3. Platform-capability enforcement point: checker error (needs
   per-target decl availability in the checker) vs link-time
   symbol absence (works today, worse diagnostics). The Roc model
   argues for the checker; cost is threading target profiles into
   `modload`/checker.
4. A second research pass on the axes verification didn't reach
   (array-language execution models, concatenative type systems,
   goal-directed evaluation) is unlikely to change the shortlist
   — the poor-fit verdicts above rest on paradigm mismatch, not
   missing facts — but items 4/9/10 came from the unverified
   tier and deserve the usual per-feature design scrutiny when
   picked up.

## Primary sources

Verified-tier claims trace to: the Perceus paper (PLDI 2021,
dl.acm.org/doi/10.1145/3453483.3454032), the FIP paper (ICFP
2023, webspace.science.uu.nl/~swier004/publications/2023-icfp.pdf),
the Koka book (koka-lang.github.io/koka/doc/book.html), the
Granule uniqueness paper (ESOP 2022,
starsandspira.ls/docs/esop22-draft.pdf), Roc's platforms page
(roc-lang.org/platforms) and FAQ, the Pony tutorial
(tutorial.ponylang.io/reference-capabilities/), the Lean 4 paper
(lean-lang.org/papers/lean4.pdf) and metaprogramming book, and
"Counting Immutable Beans" (arXiv:1908.05647). Sourced-tier:
BQN docs (mlochbaum.github.io/BQN), Nushell book
(nushell.sh/book/pipelines.html), strymonas
(yanniss.github.io/streams-popl17.pdf), matklad's "Things Zig
comptime Won't Do", verdagon.dev (generational references,
Higher RAII), antelang.org/blog/why_effects.
