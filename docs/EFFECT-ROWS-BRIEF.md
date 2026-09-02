# Effect rows — prototype and findings

Status: **research** (2026-09-02). Issue **#5320**, split out of #4416,
absorbing `PLATFORM-RESEARCH.md` Rec §10. The two design sketches this
descends from are `PLATFORM-RESEARCH.md` §10 (`uses [io.http, io.kv]`,
inference-primary) and `LANGUAGE-DIRECTION.md ▸ Algebraic effects`
(`<io, throws[E]>`, closed vocabulary, no user handlers). Prior art:
`EFFECT-SYSTEMS-PRIOR-ART.md`.

**Outcome: the analysis ships; the surface syntax does not.** A
`uses [...]` clause was built, measured, and then deliberately dropped —
§2 says why. What landed is `internal/effects` (a shared call-graph +
effect solver) and `fern -effects` (the per-function report). No new
surface syntax, no new diagnostic, no commitment to an effect system.

The deliverable #5320 asked for was a prototype plus findings feeding
the Platform-capability design. Both are here, and the findings are the
part that should change anyone's mind — §4 in particular, which reverses
the conclusion an earlier draft of this brief reached.

## 1. The thesis, and a correction to the name

**Fern does not want an effect row. It wants an effect *set*.**

A row — Koka's `<exn|μ>`, with scoped and duplicable labels — exists to type
*elimination* forms. Leijen's duplicate-label rule is justified specifically
by `catch`: if a handler may itself throw, `μ` unifies with `<exn|μ′>` and
the action's row becomes `<exn, exn|μ′>`, which is the right answer and is
untypable with `lacks` constraints. Take away the elimination form and the
entire justification for scoped labels evaporates.

Fern has no elimination form for these effects and is not getting one. The
effects it cares about — `net`, `fs`, `env`, `time`, `random`,
`subprocess` — are what Flix calls **primitive effects**: *"cannot be handled
and never go out of scope … a primitive effect represents a side-effect that
happens on the machine"*, and they are **viral**. There is no `catch fs`.

So the right point in the design space is the simple end of Flix's family:
a **closed set of labels with subsumption**, over a fixed vocabulary, with
union at joins. Set-subset over a six-element universe is a bitmask compare;
row unification is not. `EFFECT-SYSTEMS-PRIOR-ART.md` §15 reaches the same
conclusion from the literature; this brief reaches it from the code.

The second half of the thesis is about *posture*, and it is the reason the
prototype is small. Fern already has two shipped disciplines of exactly this
shape:

- **`fip` / `fbip` (E053)** — a declared property, verified against the body
  by a whole-program walk with a contagion rule (`fip` may call only `fip`).
  `MODE-LATTICE.md` calls this **verify-don't-enable**.
- **`caps.Analyze` (E070)** — call-graph reachability from a package's
  functions into a builtin→capability table.

An effect set is the third: `fip`'s declare-and-verify posture applied to
`caps`' reachability analysis, at **function** granularity instead of package
granularity. Nothing in it is new machinery. That is the argument for
prototyping it rather than reasoning about it.

## 2. The clause was built, then dropped

The prototype carried a contextual `uses [...]` clause between the return
type and the body, verified against the inferred row by a checker pass
with its own diagnostic. It worked. It is not in the shipped change, for
four reasons that only became clear once it existed:

**It was opt-in, so it caught nothing.** Zero functions in the tree
declared a row. A rule that fires only when someone opts in protects only
the person who already thought about the problem. Making it mandatory on
`pub` signatures — the middle ground the prior art recommends — costs 77
annotations on the self-host (§4.5) plus the churn every effect system in
`EFFECT-SYSTEMS-PRIOR-ART.md` §14.7 reports: Rust priced adding `const`
at ~75% of its stdlib, and OCaml shipped effects *untyped* rather than
change every `.mli`.

**The questions people actually ask are already answered, at the right
granularity.** E070 asks "can this dependency reach the network?" — a
supply-chain question, correctly answered at the *package* boundary,
because that is where the trust boundary is. E066 asks "does this target
provide what this program needs?". A per-*function* declared row is a
finer answer to a question nobody was asking; a reader who wants to know
what one function does can read it, and `fern -effects` prints it.

**It would have frozen a vocabulary prematurely.** Putting `uses [net]`
in the surface makes `internal/caps`' v1 names a 1.0 commitment. Rec §10
itself wanted finer names (`io.http`, `io.kv`); §4 shows three internal
vocabularies that already disagree. Shipping syntax before that is
settled is backwards.

**The `Platform` bag is the better answer, and it is already landing.**
`std/platform` (#4414) gives the handler's bag real capabilities, and the
`ambient-capability` lint pushes handlers to reach effects *through* it.
That is Effekt's capability-passing design — the model
`PLT-LANDSCAPE-2026.md` §4 already named as preferable to Koka's rows. If
the bag becomes the only route to host effects, **the bag's type is the
effect row**, enforced by construction rather than by a declaration the
checker cross-checks. A `uses` clause alongside it is a second mechanism
for one job.

For the record, the clause's design is worth keeping even though the code
is not, because the same questions recur for any future attempt:

| Spelling | Source | Verdict |
| --- | --- | --- |
| `: T <io, fs>` | `LANGUAGE-DIRECTION.md` | `<` / `>` are comparison operators; genuinely ambiguous on a function TYPE (`(i32) => i32 <io>`). |
| `\ {FsRead, Clock}` | Flix | No precedent in Fern; `\` reads as an escape. |
| `uses [...]` | `PLATFORM-RESEARCH.md` §10 | Unambiguous in both positions, `[]` already the bracket for type parameters. What the prototype used. |

And the **direction of the check**, where the two source sketches
contradict each other outright:

- `PLATFORM-RESEARCH.md` §10: *"the programmer can declare a subset (for
  documentation) and the checker verifies"* — declared ⊆ reached.
- `LANGUAGE-DIRECTION.md`: a row that *omits* an effect the body needs is
  rejected — reached ⊆ declared.

Only the second is sound as a promise to a caller. The first is a real
check too — it catches a stale signature — but making it an *error* is
the single most expensive mistake available here, for the ecosystem-churn
reason above. Any future attempt should take the second and leave
over-declaration legal.

## 3. Vocabulary — reuse, do not invent

Rec §10 sketches `io.http` / `io.kv` / `io.log`. That vocabulary exists
nowhere in the tree, and adopting it would make **four** capability
vocabularies:

| Where | Names | Question it answers |
| --- | --- | --- |
| `internal/caps` | `env fs net random subprocess time` | what a *dependency* may reach (E070) |
| `internal/platforms` | `log stdout stdin now pollfd env args random fs fsmode cabi tcp proc subprocess arena fetch` | what a *target* provides (E066) |
| `Platform` bag (planned) | `fetch kv secrets log now` | what the host hands the handler |
| Rec §10 sketch | `io.http io.kv io.log` | — |

The clause uses `internal/caps`' v1 set. It is the *authority* vocabulary,
which is the question "which effects does this function perform" actually
is; it is already mirrored into `examples/self_host/caps.fern` and pinned
entry-for-entry by a parity test; and it is what `fern -capabilities`
already prints.

`internal/effects` is nevertheless **vocabulary-agnostic**: `Build` records
which builtins each function reaches, `Solve` projects that through a
caller-supplied table. So `fern -effects` prints the same call graph under
both shipped vocabularies, and they disagree in ways worth seeing — `net` vs
`tcp`, `time` vs `now`, `subprocess` vs `proc`, and stdio, which `caps`
deliberately does not gate and `platforms` does. A function "pure" under one
is not pure under the other.

That is also the first concrete payoff: the two enforcement passes are now
two projections of one call-graph walk, not two walks.

## 4. What the prototype measured

This is the section the deferral rests on. `PLT-LANDSCAPE-2026.md` §4:
*"Surface effect rows stay deferred — Koka itself demonstrates the annotation
burden."* The prior art says the same, repeatedly and with receipts. **None of
it was measured on Fern.**

Two corpora, both under both vocabularies. `fern -effects` prints all of it.

### 4.1 The self-hosted compiler — 6,075 functions

| | authority (`caps`) | host (`platforms`) |
| --- | --- | --- |
| reach **no** effect | 5,848 (96.3%) | 5,811 (95.7%) |
| reach ≥1 effect | 227 | 264 |
| …**call a tagged builtin themselves** | 43 | 71 |
| …only **inherit** from a callee | 184 | 193 |
| `pub`, reaching ≥1 effect | 77 of 1,807 | 81 of 1,807 |
| reach an effect **only via a function value** | **0** | **0** |

### 4.2 The handler corpus — `examples/wasm/native_http_handler.fern`, 1,184 functions

| | authority (`caps`) | host (`platforms`) |
| --- | --- | --- |
| reach **no** effect | 1,169 | 860 |
| reach ≥1 effect | 15 | **324** |
| …**call a tagged builtin themselves** | 12 | 13 |
| …only **inherit** from a callee | 3 | 311 |
| reach an effect **only via a function value** | **0** | **315** |

That last cell is the finding.

### 4.3 The over-approximation is cheap right up until it isn't

The prototype has no effect row on function *types*. A call it cannot resolve
to a name is therefore charged the union over every escaping callable — the
crudest sound approximation available. The question is what that costs.

On the self-hosted compiler, and on the handler corpus under the authority
vocabulary: **nothing**. Not one function is charged an effect it would not
reach anyway.

On the handler corpus under the host vocabulary: **315 of 1,184 functions**,
which is 97% of everything that reads as effectful there. `cmp__min` is
charged `log`. So is `bigint__parse`. The witness chain says exactly why:

```
cmp__min  log  (via a function value: log)
  (example: cmp__min → (function value) → handle → __method_Platform_log → eprint)
```

The mechanism is the handler shape itself. `handle` is passed to `tcp_serve`
as a value, so it escapes; `std/platform` (#4414) then gave the `Platform` bag
real capabilities, so `handle` calling `plat.log(...)` makes an *escaping*
function effectful; and from that moment every unresolvable indirect call
anywhere in the program inherits `handle`'s entire row.

**This reverses the conclusion an earlier draft of this brief drew.** Measured
before `std/platform` landed, the indirect cost was zero on every corpus, and
the reading was that row-polymorphic function types would buy precision Fern
does not need. That was true only because no escaping callable was effectful
yet. The platform-capability design makes escaping callables effectful *by
construction* — that is what the `Platform` bag is for — so the zero was a
property of an unfinished feature, not of Fern's shape.

The honest statement is narrower and more useful:

- Whole-program inference with no row on function types is **precise enough
  for a compiler-shaped program** — a large, mostly-pure call graph whose
  escaping callables are comparators and predicates.
- It **collapses for a handler-shaped program** the moment the handler carries
  capabilities, which is the workload #4414 is deliberately building toward.
- The collapse is not gradual. One effectful escaping function takes the
  precision from exact to useless, because the indirect row is a single
  program-wide union.

That is the concrete, measured case for effect rows on function types, and it
is the first one in this repository that is not an argument from the
literature. It also says *where* the row is needed — on the parameter of
`tcp_serve`, i.e. on the one function type a handler is passed through — not
on every arrow in the language.

**4.4 Effects have very few origins.** In the self-host, 43 of 6,075 functions
call a tagged builtin directly; the other 184 effectful ones inherit. An
annotation on an inheritor restates its callee's signature; an annotation on
an origin says something a reader cannot get anywhere else. If a mandatory
rule is ever wanted, "origins must declare" is a 43-function obligation and is
where the information is.

**4.5 The annotation burden is not the one in the literature.** Under a
Koka-style "annotate everything" rule the cost is 6,075 clauses. Under the
prior art's recommended middle ground — required on `pub` signatures — it is
**77 non-empty rows**. Under the prototype's opt-in rule it is **0**. The
distribution is skewed enough that the choice between the last two is a
question about documentation policy, not about ergonomics.

**4.6 The two vocabularies genuinely disagree, and now there is a one-line
proof.** In the handler example, `handle` reaches **nothing** under the
authority vocabulary and **`log`** under the host one — the same function,
same body. `caps` does not gate stdio on purpose (a channel the invoker
already handed the process is not authority a dependency escalates through);
`platforms` gates it because a freestanding target genuinely has nowhere to
put a line. Both are right about their own question. A single effect
vocabulary would have to pick one and make the other enforcement pass less
precise, which is why the analysis stays vocabulary-agnostic.

**4.7 The analysis cost is nil.** The pass bails before building anything when no
function in the program declares a clause — the same early-out
`checkFipFunctions` takes. `PERFORMANCE-RESEARCH.md` objects to effect rows on
the grounds that they force multi-pass typing; this shape does not, because it
is not part of type checking at all. It is one post-body walk over an AST the
checker has already produced.

## 5. What this is not

- **Not a row on `ast.FuncType`** — scoped out of the prototype, not argued
  against. Two mechanical grounds made it the wrong thing to attempt first:
  `checker.assignable` has no `*FuncType` case at all — function types are
  compared by `ast.Equal`, i.e. invariantly — so sub-effecting on closure
  assignment is net-new work with no precedent to copy; and `monomorph`
  re-enters `checker.Check` on a rewritten program, so anything stashed must
  be reconstructible from the AST alone. The whole-program analysis sidesteps
  both, which is what made a prototype cheap enough to measure with. §4.3 is
  the measurement, and it says the omission costs nothing on a
  compiler-shaped program and 315 false positives on a handler-shaped one.
- **Not handlers.** Tracking is erased and free; handlers are a
  delimited-continuation discipline, and under Perceus RC with no GC, no
  segmented stacks, and a WASI target with no stack switching, the monadic
  route is the only one available. Koka's own escape hatch is that every
  effect Fern wants is `linear` in its sense — a virtual call through an
  evidence vector, no capture. If handlers ever come, that is the shape, and
  they layer over this vocabulary rather than replacing it.
- **Not `throws[E]`.** `LANGUAGE-DIRECTION.md` already deprioritised error
  effects: `use` plus postfix `?` cover the ergonomics.
- **Not `suspend`.** The concurrency decision is explicit — *"No function
  coloring — suspension is a property of the block, not the function
  signature."* An effect row must not re-introduce colouring through the
  back door.
- **Not on trait methods.** A row on a trait method constrains every impl,
  and every system that met this had to extend its interface mechanism: Flix
  invented **associated effects**, Rust needs effect-generic trait
  declarations (still unshipped), Scala uses classifiers. Fern has associated
  types, so Flix's answer is available — but it is its own piece of work, and
  a prototype that guessed at it would be guessing.
- **Not a replacement for the `ambient-capability` lint** (#4414, landed
  alongside this). That rule says a handler should reach its effects
  *through its bag* rather than around it; the analysis says what a function
  reaches, by whatever route. They compose: the lint moves an effect onto
  `plat.*`, and the walk then attributes it through `__method_Platform_*` —
  exactly how the 315 false positives in §4.3 became visible.
- **Not mirrored into the self-host.** The clause parses and checks in the
  native compiler only. Mirroring is cheap for the table and not cheap for
  the walk; it should follow a decision to ship, not precede one.

## 6. Open questions

1. **A row on the handler's function type — the one §4.3 asks for.**
   The 315 false positives all come from one edge: `handle` passed to
   `tcp_serve` as a value. A row on *that parameter's type* would let the
   analysis charge an indirect call the callee's row instead of the
   program-wide union, and it is a far smaller change than effect rows on
   every arrow. It costs the `assignable` / `unifyType` / `monomorph` work in
   §5. Whether the narrow version is coherent on its own, or drags in the
   general case, is open — and it is the most decision-relevant question here.
2. **Would a points-to pass fix it more cheaply than types?** The 315 is
   produced by the crudest sound approximation: every unresolvable indirect
   call is charged the union over *all* escaping callables. Tracking which
   function values actually flow to which call sites would likely collapse
   most of it with no type-level rows at all. This was not attempted, so
   "the approximation is too crude for the handler shape" is established and
   "rows on function types are required" is not.
3. **Should the vocabulary include `alloc` and `panic`?** Rust's
   `core`/`alloc`/`std` split and `FREESTANDING-CORE.md` both say the
   interesting boundary includes allocation; no effect-system paper models
   it. The most Fern-specific question in the area, and it connects to
   `BARE-METAL-PLAN.md`: "callable from an interrupt handler" is structurally
   an effect (no allocation, no blocking).
4. **`main`'s row as a WIT world.** The prior art's one genuinely novel
   suggestion: derive a WASI `world` from `main`'s inferred row and reject a
   program whose row exceeds the target's provision — turning today's
   post-tree-shake E066 into a definition-site diagnostic *and* a build
   artefact. Buildable on the analysis exactly as it stands, and the strongest
   argument for having landed the engine.
5. **The `init` vs `handle` split.** `PLATFORM-RESEARCH.md` §3 wants `init`
   and `handle` held to different rows — `init` may read env and connect,
   `handle` may not re-do startup work. Now that main's E075 checks the two
   agree *structurally*, the rows are the next question, and the analysis can
   answer it without any surface syntax.
6. **Package granularity vs function granularity.** `caps.Analyze` attributes
   a closure's capabilities to its *defining* package — the object-capability
   reading, deliberately. `internal/effects` attributes them to whoever
   invokes the value, because that function does perform them. Both are right
   for their own question; whether either report should say so out loud is
   open.

## 7. Where it lives

| Piece | Path |
| --- | --- |
| Call graph, solver, witness chains, report | `internal/effects/` |
| Builtin-name predicate the graph needs | `internal/checker/builtinnames.go` |
| Report command | `cmd/fern/effects.go` (`fern -effects`) |
| The shipped enforcement of the same reachability, per package | `internal/caps` (E070) |
| …and per target | `internal/platforms` (E066) |

`internal/caps` now builds its per-package report on `internal/effects`'
graph rather than walking the call graph a second time; that is the one
change this makes to shipped behaviour, and it is output-identical
(verified byte-for-byte across the 35 example programs).

The `uses [...]` clause, its checker pass, its diagnostic, its conformance
case and its grammar production existed on this branch and were removed
before merge — §2. Nothing in the language surface changed.
