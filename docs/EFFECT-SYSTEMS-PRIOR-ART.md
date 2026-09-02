# Effect systems and effect rows — prior-art survey

Status: **research** (2026-09-01). Input to #5320; the findings and the
prototype it fed are `EFFECT-ROWS-BRIEF.md`. Nothing here is a build item.

Target question: **should Fern put a type-level effect row on function
signatures, so that platform/OS capabilities (`io`, `net`, `clock`, `fs`,
`rand`, …) are checkable rather than ambient — and if so, in which of the
four known shapes?**

Fern's constraints are unusual for this literature and they matter throughout:
statically typed, AOT-compiled to ARM64/x86-64/WASI, **Perceus-style reference
counting with no tracing GC**, traits, generics, closures, and an existing
*coarse, whole-program* capability check (`internal/platforms` E066
post-tree-shake + `internal/caps` package capabilities). Almost every system
below assumes a GC; the two that do not (Koka, Effekt) are the ones whose
implementation notes matter most.

Sourcing: primary sources were read by shallow-cloning the languages' own
doc repositories (koka-lang/koka, flix/book, effekt-lang, scala/scala3,
roc-lang/roc, rust-lang/keyword-generics-initiative, unisonweb/unison,
haskell-effectful/effectful, tomjaguarpaw/bluefin, WebAssembly/component-model,
granule-project/granule) and by extracting text from the two Microsoft-hosted
Koka papers. Every quotation in the syntax gallery (§17) is verbatim from
those.

---

## 0. Executive summary

**Four families**, not one. The literature is usually presented as "effect systems",
but there are four genuinely different designs with different costs:

| Family | Representative | What is on the arrow | Polymorphism mechanism |
|---|---|---|---|
| **A. Effect rows** (extensible, unifiable) | Koka, Links, Unison, Frank | a *row* of labels, possibly open `<l\|μ>` | row variables + row unification |
| **B. Effect sets with subtyping / Boolean algebra** | Flix, Verse, Roc (degenerate), Rust `const` | a *set* or a lattice point | set variables + sub-effecting, or a fixed lattice |
| **C. Capabilities tracked in types (capture sets)** | Effekt, Scala 3 CC, Wyvern | which *values* (capabilities) a term retains | contextual / capture-set polymorphism; often *no* effect variables at all |
| **D. Capabilities as plain values, untracked** | Roc platforms, Bluefin, `ReaderT`/handle pattern, Java scoped values, WASI worlds, OCaml 5 | nothing | none needed |

**The headline finding for Fern**: the systems that report the *worst* ergonomics
are the row systems (A), and the pain is concentrated in exactly one place —
**higher-order functions and trait/interface signatures**, where every arrow needs an
effect variable and every combinator needs to union them. The systems that report the
*best* ergonomics for the specific goal "make platform capabilities checkable"
are (C) — Effekt's contextual effect polymorphism and Scala's capture checking both
advertise "effect polymorphism for free, no effect variables in source" — and the
degenerate end of (B) — Flix's `\ {FsRead, Clock}` with a default handler per effect,
and Roc's binary `->` / `=>`.

**Second headline**: effect *tracking* and effect *handlers* are separable, and
Fern should separate them. Tracking costs a type-system feature. Handlers cost a
**runtime discipline for capturing and resuming delimited continuations**, and under
reference counting with no GC and no segmented-stack runtime, that is the expensive
half (§8). Koka's own answer is that most operations are *tail-resumptive* and can be
compiled as ordinary (virtual) calls, and that effects declared `linear` "removes the
need for the monadic transformation" entirely — i.e. Koka itself carves out precisely
the subset Fern would want.

---

## 1. Koka — row-polymorphic effect types with scoped, duplicable labels

Primary sources: [Koka tour (`doc/spec/tour.kk.md`)](https://github.com/koka-lang/koka/blob/master/doc/spec/tour.kk.md),
[Koka spec](https://github.com/koka-lang/koka/blob/master/doc/spec/spec.kk.md),
[Leijen, *Koka: Programming with Row-polymorphic Effect Types*, MSFP 2014](https://www.microsoft.com/en-us/research/wp-content/uploads/2016/02/koka-effects-2013.pdf),
[Leijen, *Extensible Records with Scoped Labels*, TFP 2005](https://www.microsoft.com/en-us/research/publication/extensible-records-with-scoped-labels/),
[Xie & Leijen, *Generalized Evidence Passing for Effect Handlers*, ICFP 2021 / MSR-TR-2021-5](https://www.microsoft.com/en-us/research/wp-content/uploads/2021/03/multip-tr-v2.pdf).

### 1.1 Surface syntax (verbatim)

```koka
fun square1( x : int ) : total int   { x*x }
fun square2( x : int ) : console int { println( "a not so secret side-effect" ); x*x }
fun square3( x : int ) : div int     { x * square3( x ) }
fun square4( x : int ) : exn int     { throw( "oops" ); x*x }
```

Total is the default and is elided:

```koka
fun square5( x : int ) : int
  x*x
```

A wildcard effect can be requested:

```koka
fun square6( x : int ) : _e int
  println("I did not want to write down the \"console\" effect")
  x*x
```

Rows and aliases:

```koka
alias pure = <div,exn>
```

Row extension and effect polymorphism:

```koka
map : (xs : list<a>, f : (a) -> e b) -> e list<b>
while : ( pred : () -> <div|e> bool, action : () -> <div|e> () ) -> <div|e> ()
```

Heap effects, from the tour verbatim: *"The effects on heaps are allocation as
`:heap<h>`, reading from a heap as `:read<h>` and writing to a heap as `:write<h>`.
The combination of these effects is called stateful and denoted with the alias
`:st<h>`."*

`mask` has type `: (action: () -> e a) -> <l|e> a` and *"usually leads to duplicate
effect labels, for example, the effect of `mask<emit>{ emit("there") }` is
`: <emit,emit>`"*.

### 1.2 The lattice

`total` (= `<>`) ⊂ `exn`, `div`, `ndet` … ; `pure = <div,exn>` "corresponds directly
to Haskell's notion of purity"; `io` is "the 'worst' effect" — "can raise exceptions,
not terminate, be non-deterministic, read and write to the heap, and do any
input/output operations". Note this is *not* a subtyping lattice in the type system:
it is a set of aliases over rows, and the widening happens through **unification**,
not subsumption.

### 1.3 Type theory: scoped labels, duplicate labels

Koka's rows are Leijen's *scoped labels* from extensible records, reused for effects.
The load-bearing property, quoted from the 2014 paper:

> effect labels can be duplicated, i.e. ⟨exn, exn⟩ ≢ ⟨exn⟩

Why this matters (paper, verbatim-ish): *"during unification we may end up with
constraints of the form ⟨exn | μ⟩ ∼ ⟨exn⟩. With regular row-polymorphism, such
constraint can have multiple solutions, namely μ = ⟨⟩ or μ = ⟨exn⟩ … With rows
allowing duplicate labels, we avoid additional machinery since in our case μ = ⟨⟩ is
the only solution."*

So duplicate labels buy **principal unification without `lacks` constraints or
presence flags**. The second payoff is precise types for *elimination* forms:

```
catch : ∀α μ. ((() -> ⟨exn|μ⟩ α), exception -> μ α) -> μ α
```

If the handler itself throws, `μ` unifies with `⟨exn|μ′⟩` and the action gets
`⟨exn, exn|μ′⟩` — "which gives us exactly the right behavior". With `lacks`
constraints this example is untypable; with presence flags the type is "arguably more
complex".

**Contrast**: Links uses Rémy-style rows with *presence types* (`•` present / `◦`
absent) and presence polymorphism instead ([Hillerström & Lindley, *Liberating
Effects with Rows and Handlers*](https://homepages.inf.ed.ac.uk/slindley/papers/links-effect.pdf)).
That is the other principled point in this design space, and it is more verbose.

### 1.4 Inference

Hindley–Milner with row unification. Effects are inferred everywhere; annotations are
optional. Two practical notes from the tour:

* Effects **unify rather than subsume**: for `while`, "when effects are inferred at
  the call-site, both the effects of predicate and action are extended automatically
  until they match" — Koka fakes subtyping by extending both rows to a common
  supertype during unification.
* Generalisation is **restricted to total expressions** ("we restrict generalization
  to expressions that are total"), the value-restriction analogue.
* Divergence is inferred, and the checker is not a termination prover: the tour notes
  `fib` gets `: div int` because "the type inference engine is currently not powerful
  enough to prove that this recursive function always terminates". *This is a real
  ergonomic cost of a rich lattice: an effect nobody asked about shows up in every
  recursive signature.*
* Isolation: `st<h>` is discarded when the function is fully polymorphic in `h`
  (a `runST` analogue), so `fib3` using `ref` cells still types as `:total`.

### 1.5 Evidence passing and Perceus interaction

See §8. The short version, from the tour verbatim:

> The Koka compiler uses (generalized) *evidence passing* to pass down handler
> information to each call-site. At the call to `ask`, it selects the handler from the
> evidence vector and when the operation is a tail-resumptive `fun`, it calls it
> directly as a regular function (except with an adjusted evidence vector for its
> context). … This gives `fun` (and `val`) operations a performance cost very similar
> to *virtual method calls*.

and:

> For even better performance, one can mark the effect as *linear*. Such effects are
> statically guaranteed to never use a general control operation and never need to
> capture a resumption. During compilation, this removes the need for the monadic
> transformation and improves performance of any effect polymorphic function that uses
> such effects as well (like `map` or `foldr`). Examples of linear effects are state
> (`:st`) and builtin effects (like `:io` or `:console`).

**This is the single most important sentence in the survey for Fern.** Koka's own
answer to "what if my effects are just `io`, `fs`, `net`, `clock`?" is: mark them
linear, and the whole continuation-capture machinery disappears.

---

## 2. Unison — abilities

Primary source: [`docs/ability-typechecking.markdown`](https://github.com/unisonweb/unison/blob/trunk/docs/ability-typechecking.markdown).

### 2.1 Syntax and semantics (verbatim)

```
a ->{IO} b
a ->{IO, Abort, State Nat} b
```

> The `{}` should be thought of as being attached to the `->`.

> Within an abilities list, type variables like `{e1, e2}` can be instantiated to sets
> of abilities, so we should think of the `{}` as just taking the union of all the sets
> contained therein. `IO` within `{IO}` is really the singleton set.

So Unison ability sets are **unordered sets with union**, not scoped rows — the
opposite choice to Koka. (There is no duplicate-label story; `{IO, IO}` is `{IO}`.)

### 2.2 The checking rule: ambient abilities

> Unison's typechecker prevents calling a function whose required abilities aren't
> available in the current expression. We say that at each subexpression of the
> program, there's an *ambient* set of abilities available … The ambient abilities at a
> subterm is defined to be equal to the required abilities on the type of the *nearest
> enclosing lambda* … plus the abilities eliminated by enclosing handlers.

This is Frank's "ambient ability" design (§12.1), not Koka's "accumulate outwards".

The soundness trap, quoted:

```haskell
foo2 : Text ->{IO} Text ->{} ()
foo2 name1 name = IO.printLine ("Hello, " ++ name)
```

> This also triggers an ability check failure. The inner lambda still requires only
> `{}` … This would be unsound (you could partially apply the function, then obtain a
> function with a smaller abilities requirement than what it actually used).

**Currying interacts with effect rows.** Any language where partial application
produces a value must decide which arrow carries the effect. Fern's closures make this
live.

### 2.3 The inference decision (and the documented ergonomic problem)

> The type `a -> b` means `a ->{e} b` for some existential `e` to be inferred by
> Unison. It doesn't mean `forall e . a ->{e} b` or `a ->{} b`.

And the note on why the "obvious" alternative is wrong:

> I realized it's not sound to do Frank-style effect generalization after typechecking
> … suppose we have `map : (a -> b) -> [a] -> [b]` … what if that function `a ->{e} b`
> were actually being passed (within the body of `map`) to some other function that was
> expecting an `a ->{} b`? We can't just generalize this willy nilly.

Two mitigations are documented, both of which are *display* hacks:

> When displaying a type signature, we can elide any ability type variables that are
> mentioned just once by the type (as in `forall e . Nat ->{e} Nat`).

> [E]liminate any empty `{}` that aren't to the left of an `->`. So `Nat ->{} Nat ->{}
> Text` would display as just `Nat -> Nat -> Text`, but `(a ->{} b) -> blah` would
> still display as `(a ->{} b) -> blah`.

The acknowledged downside, verbatim: *"A downside is that the user will see more
ability type variables. But maybe that's a feature, not a bug."*

### 2.4 Reported ergonomic failures

* **Accidental concretisation.** [unison#1173, "Unison should protect against
  accidental inference of concrete abilities"](https://github.com/unisonweb/unison/issues/1173):
  a definition written with a "naked arrow" `->`, intended as pure, silently infers a
  concrete ability because something it calls uses one. The proposed fix was to make
  inferring a concrete (non-`{}`) ability on an unadorned arrow a type error.
* **Higher-order abilities crash the checker.** [unison#822, "Ability system crashes
  with higher-order abilities"](https://github.com/unisonweb/unison/issues/822).
* **Delayed computations everywhere.** `'{IO} Nat` is sugar for `() ->{IO} Nat`;
  practitioners note that "any higher-order function which manipulates effectful
  functions must do so lazily, because if it requested already computed values, the
  effects would have already happened"
  ([SoftwareMill, *Trying out Unison, part 3*](https://softwaremill.com/trying-out-unison-part-3-effects-through-abilities/)).
  A strict language pays this as a *thunking* tax on every effect-generic API.
* **Top-level values must be pure** ([Unison abilities FAQ](https://www.unison-lang.org/docs/fundamentals/abilities/faqs/)).

---

## 3. Effekt — effects as *capabilities*, lexical handlers, second-class blocks

**This is the closest fit to Fern's actual goal.** Primary sources:
[Brachthäuser, Schuster, Ostermann, *Effects as Capabilities: Effect Handlers and
Lightweight Effect Polymorphism*, OOPSLA 2020](https://dl.acm.org/doi/10.1145/3428194);
Effekt tour and docs ([`examples/tour/`](https://github.com/effekt-lang/effekt/tree/master/examples/tour),
[`docs/concepts/`](https://github.com/effekt-lang/effekt-website/tree/master/docs/concepts));
[Brachthäuser, Schuster, Lee, Boruch-Gruszecki, *Effects, Capabilities, and Boxes*,
OOPSLA 2022](https://doi.org/10.1145/3527320);
[Müller, Schuster, Starup, Ostermann, Brachthäuser, *From Capabilities to Regions*,
OOPSLA 2023](https://dl.acm.org/doi/10.1145/3622831);
[Muhcu, Schuster, Steuwer, Brachthäuser, *Multiple Resumptions and Local Mutable
State, Directly*, ICFP 2025](https://dl.acm.org/doi/10.1145/3747529).

### 3.1 The re-reading of "effect"

Verbatim from the Effekt docs:

> As opposed to other effect systems, where effects communicate the *side effects* a
> program has besides computing a result, the notion of effects in the Effekt language
> is that of a **requirement**.

```effekt
effect exc(msg: String): Nothing

def div(n: Double, m: Double): Double / { exc } =
  if (m == 0.0) do exc("Division by zero") else n / m
```

> "The function `div` computes a value of type `Double` requiring a capability for
> `exc` in its calling context."

And the sharp consequence:

> Here, `pureFun` simply imposes no requirements on its calling context. It would thus
> be unsound to call `div`, since it requires `Exc`.

### 3.2 Contextual effect polymorphism — the boilerplate elimination

```effekt
def myMap[A, B](xs: List[A]) { f: A => B }: List[B] = …
```

Expanded:

```effekt
myMap[A, B](xs: List[A]) { f: A => B / {} }: List[B] / {}
```

The docs are explicit that the naïve reading is wrong:

> The `/ {}` on `f` might look like it says "f must be pure", but that is **not** the
> correct reading. Instead, it means that `f` imposes *no requirements on `myMap`
> itself*. Any effects `f` actually uses simply propagate to the *call-site of
> `myMap`*, where they are handled in the lexical scope where `f` was written.

> This is *contextual effect polymorphism*: `myMap` stays effect-agnostic regardless of
> what `f` does.

And the contract framing:

```effekt
//                                  "provided" effects
//                                          vv
def foreach[A](l: List[A]) { f: A => Unit / {} }: Unit / {}
//                                                       ^^
//                                               "required" effects
```

> effects in return position are "required" (the calling context needs to provide
> them), and effects in argument positions are "provided".

**There are no effect variables in Effekt source.** That is the entire claim, and the
mechanism is that block parameters are **second-class**.

### 3.3 Second-class computations, boxing, capture sets

> Following Paul Levy's Call-By-Push-Value, Effekt distinguishes between **values** and
> **computations** … Functions (and all other computation types) are *second-class* in
> Effekt. To make this difference explicit, we pass values in parentheses (e.g.
> `f(42)`) and computations in braces (e.g. `f { x => println(x) }`).

When you need first-class-ness, you `box`, and *then* the captures become visible:

```effekt
def divide(n: Int, m: Int) {exc: Exception}: Int =
  if (m == 0) { exc.throw("uhoh") }
  else { n / m }
```

> In the case of the previous example, the return type of the handler is
> `Exception at {exc}`. This is called a boxed computation.

Restricting capture:

```effekt
def parallel (f: () => Unit at {}, g: () => Unit at {}): Unit = <>
```

> Here, by using `println` both arguments have the capture set `{io}` while the
> expected capture set is empty, denoted as `{}`. Hence, this call does not type-check.

And the crisp distinction Fern needs to internalise:

> - **Effects** express a *requirement* on the context — certain capabilities still
>   need to be provided by the caller.
> - **Captures** express a *restriction* on where a computation can be used — the
>   handler is already fixed in its lexical scope.
>
> In other words: an effect is an open demand; a capture is a fixed capability.

### 3.4 Built-in resources = platform capabilities

This is exactly Fern's `internal/platforms`, done at the type level:

> Capture sets can contain (at least) three different kinds of elements: capabilities
> introduced by handlers; memory regions; builtin resources.
>
> An example of a builtin resource is `io`, which is handled by the runtime. Other
> builtin resources include `global` (for global mutable state) and `async`.

```effekt
def helloWorld() at {io} = println("hello, world")
```

> Captures are always inferred but can also be manually annotated.

### 3.5 What Effekt gives up

Second-class blocks mean **you cannot return a function that closes over a
capability** without boxing it, and a boxed computation may only be unboxed where all
its captures are in scope. For a language with first-class closures stored in data
structures (Fern), that is a real restriction; Scala's capture checking (§4) is the
same idea generalised to first-class values, and it is much heavier as a result.

---

## 4. Scala 3 — capture checking / "capturing types" (Caprese)

Primary sources: [Scala 3 capture-checking reference](https://github.com/scala/scala3/tree/main/docs/_docs/reference/experimental/capture-checking)
(rendered: <https://docs.scala-lang.org/scala3/reference/experimental/capture-checking/overview.html>);
[Odersky et al., *Capturing Types*, TOPLAS 2023](https://dl.acm.org/doi/10.1145/3618003);
[Boruch-Gruszecki et al., *Capturing Types*](https://arxiv.org/abs/2105.11896);
[Xu, Odersky et al., *What's in the Box: Ergonomic and Expressive Capture Tracking*,
OOPSLA 2025](https://dl.acm.org/doi/10.1145/3763112).

### 4.1 Syntax (verbatim)

```scala
import language.experimental.captureChecking
import language.experimental.separationChecking
```

```scala
class File(path: String) extends ExclusiveCapability

val out: File^ = new File("~/some/bits")
val lg: Logger^{out} = new Logger(out)
```

> Generally, the type `A^{c₁, ..., cₙ}` stands for instances that retain capabilities
> `c₁, ..., cₙ`. If class `A` does not extend `Capability`, then the type `A` alone
> stands for instances that retain no capabilities, i.e. `A` is equivalent to `A^{}`.

Function types:

```scala
   A ->{c₁, ..., cₙ} B   =  (A -> B)^{c₁, ..., cₙ}
                A => B   =  A ->{any} B
```

Subtyping:

```
  A  <:  A^{lg}  <:  A^{out}  <:  A^{out, f}  <:  A^
```

### 4.2 Why it is *not* an effect row — and the "for free" claim

The reference says it outright:

> Due to the conventions established in previous sections, `f: A => B` translates to
> `f: A ->{any} B` under capture checking which means that the function argument `f`
> can capture any capability, i.e., `map` will have `f`'s effects, if we think of
> capabilities as the only means to induce side effects, then *capability polymorphism
> equals effect polymorphism*. By careful choice of notation and the capture tunneling
> mechanism for generic types, we get effect polymorphism *for free*, and no signature
> changes are necessary on an eager collection type such as `List`.

Lazy structures do pay, and the payment is *path-dependent* rather than variable-based:

```scala
extension [A](xs: LzyList[A]^)
  def map[B](f: A => B): LzyList[B]^{xs, f}
```

> This relationship … would be rather cumbersome to express in more traditional effect
> type systems with explicit generic effect parameters.

Explicit polymorphism exists when needed:

```scala
class Source[X^]:
  private var listeners: Set[Listener^{X}] = Set.empty
  def register(x: Listener^{X}): Unit = listeners += x
```

> Under the hood, a capture-set variable is implemented as a normal type parameter with
> special bounds: `class Source[X >: CapSet <: CapSet^]`.

### 4.3 Classifiers — directly applicable to Fern's platform taxonomy

Scala added a **classifier lattice over capabilities** with projection operators:

```scala
trait Classifier
sealed trait Capability
trait SharedCapability extends Capability, Classifier
trait Control extends SharedCapability, Classifier
trait ExclusiveCapability extends Capability
trait Unscoped extends ExclusiveCapability, Classifier
```

```scala
object Try:
  def apply[T](body: => T): Try[T]^{body.only[Control]} = ???
```

```scala
def runOnNewThread[T](body: () ->{any.except[Control]} T): T = ???
```

> `c.except[A]` stands for the parts of `c` that are *not* classified as `A` … A
> closure over a `FileSystem` is accepted, since `IO` is unrelated to `Control`.

Subcapturing rules given verbatim:

```
{c.except[A]} <: {c}
{c.only[A].except[B]} <: {c.only[A]}
{c.except[A]} <: {c.except[B]}
```

This is the most sophisticated *classification* scheme in the survey and is the
closest analogue to "core vs host" / "which platform provides what".

### 4.4 Global capabilities — the anti-refactoring argument

> In traditional object capability systems, global capabilities are ruled out. … But
> with tracked capabilities, we have another means to control access via tracked types.
> Consequently global capabilities can be allowed.

```scala
object SimpleLogger uses Console:
  def log(str: String): Unit = Console.out.println(str)
```

> Allowing global capabilities like `Console.out` is quite useful since it means that we
> don't need to fundamentally change a system's architecture to make it capability-safe.
> In traditional capability systems all capabilities provided by the host system have to
> be passed as parameters into the main entry point and from there to all functions that
> need access. This usually requires a global refactoring of the code base.

**This is the decisive argument against pure (D)-style capability passing for a
language with an existing stdlib.**

### 4.5 Checked exceptions as a capability (the `throws` pattern)

```scala
def f(x: Double): Double throws LimitExceeded =
  if x < limit then x * x else throw LimitExceeded()
```

desugars to `def f(x: Double)(using CanThrow[LimitExceeded]): Double`. Note the error
message design, which is excellent prior art for Fern's diagnostics:

```
|The capability to throw exception LimitExceeded is missing.
|The capability can be provided by one of the following:
| - Adding a using clause `(using CanThrow[LimitExceeded])` to the definition of the enclosing method
| - Adding `throws LimitExceeded` clause after the result type of the enclosing method
| - Wrapping this piece of code with a `try` block that catches LimitExceeded
```

### 4.6 Reported ergonomic failures

* **Boxes did not scale to the collections library.** From *What's in the Box*
  (OOPSLA 2025): capturing types' *"expressiveness has been insufficient for tracking
  capabilities embedded in generic data structures, preventing them from scaling to the
  standard collections library — an essential prerequisite for broader adoption …
  stemming from the inability to name capabilities within the system's notion of box
  types."* The fix required a new calculus (System Capless) and **reach capabilities**,
  and a full reimplementation plus migration of the collections library.
* **Confusion in practice.** Community threads and talks describe capture checking as
  "somewhat confusing"; see [users.scala-lang.org, "Capture checking being confusing
  again"](https://users.scala-lang.org/t/capture-checking-being-confusing-again/12083)
  and [Nicolas Rinaudo, *Hands on Capture Checking*](https://nrinaudo.github.io/articles/capture_checking.html).
* Still `experimental` after ~4 years, and separation checking is "still a bit more
  fluid at present".

**Lesson for Fern**: generic containers are where capture/effect tracking dies. Any
design must answer "what is the effect of `Array<fn() -> ()>`?" *before* shipping.

---

## 5. OCaml 5 — deliberately untyped effects

Primary sources: [Sivaramakrishnan, Dolan, White, Kelly, Jaffer, Madhavapeddy,
*Retrofitting Effect Handlers onto OCaml*, PLDI 2021](https://dl.acm.org/doi/10.1145/3453483.3454039);
[`ocaml-multicore/ocaml-effects-tutorial`](https://github.com/ocaml-multicore/ocaml-effects-tutorial).

Syntax — effects are an *extensible variant*, i.e. a data type, not a type-system
feature:

```ocaml
type _ Effect.t += Xchg : int -> int Effect.t
```

```ocaml
# let f () = perform E;;
val f : unit -> unit = <fun>
# f ();;
Exception: Stdlib.Effect.Unhandled(E)
```

Note the type of `f`: `unit -> unit`. **The effect is invisible in the type and the
failure is a runtime exception.**

### Why they shipped it untyped

The stated rationale is backwards compatibility: programs without matching handlers
are still well-typed, which yields *"simpler static semantics than languages that
ensure effect safety, which is important for backwards compatibility as their goal is
to retrofit effect handlers to a language with large legacy codebases"*. Effect safety
is explicitly future work.

The obstacle they name is **effect polymorphism**: every higher-order function
(`map`, `fold`, `iter`) in a 25-year-old standard library would need to become
effect-polymorphic, and every existing signature in every `.mli` would change.
Jane Street's *Effective Programming: Adding an Effect System to OCaml* talk and the
long-running [discuss.ocaml.org "What's the status of typed effects?"](https://discuss.ocaml.org/t/whats-the-status-of-typed-effects/18439)
thread track the state of play. Research directions include gradual effect typing
([*Gradual Typing for Effect Handlers*](https://arxiv.org/abs/2304.02145), GrEff,
explicitly aimed at OCaml 5) and, most promisingly, **modal effect types** (§12.6),
co-authored by OCaml maintainers Leo White and Stephen Dolan.

**Lesson for Fern**: Fern is *not* in OCaml's position — its stdlib is small and it
controls the ecosystem. The window to add effect tracking closes as the ecosystem
grows, and the cost is roughly proportional to the number of published
higher-order signatures.

---

## 6. Roc — deliberately *no* effect system, but a purity distinction

Primary sources: [`docs/langref/platforms.md`](https://github.com/roc-lang/roc/blob/main/docs/langref/platforms.md),
[`docs/langref/functions.md`](https://github.com/roc-lang/roc/blob/main/docs/langref/functions.md).

Roc's answer is **(D) + a binary bit**. Verbatim:

```roc
pure_fn : Str, Str -> Str

run_fx! : Str, Str => Str
```

> Note that `pure_fn` uses a `->` to indicate that it's a pure function, whereas
> `run_fx!` uses a `=>` instead to indicate that it's an effectful function.

> By design, Roc has no syntax for "either pure or effectful." That is, there's no
> concept of *effect polymorphism* like you might find in some languages that support
> algebraic effects.

The consequence is accepted explicitly — **duplicated higher-order API**:

> It makes it easy to distinguish between higher-order functions like `Try.map_ok` and
> `Try.map_ok!` which differ only in the effectfulness of the functions they accept.

Purity is **inferred**, and a wrong annotation is intended to be a *warning*, not an
error:

> Roc infers which functions are pure and which are effectful. You can choose to
> annotate functions as pure or effectful, and the compiler will warn you if the
> annotation is incorrect. … This is a useful feature, as it means you can do things
> like take a function that has been historically pure and add some debugging that
> involves doing I/O in the middle of the function. You'll get a warning (and
> potentially miss out on some optimizations) but you won't have to do the chore of
> going around changing a bunch of annotations just to be able to run the program.

**Where the actual capability control lives**: not in the type system at all, but in
the **platform/app split**. Verbatim:

> In most languages, I/O primitives come with the standard library. In Roc, the
> standard library contains only functions and data structures; an application gets all
> of its I/O primitives from its platform.

> Because Roc's I/O primitives come from platforms, these mismatches can be prevented at
> build time. A browser-based platform would not expose file I/O primitives, a web
> server would not expose a way to block on reading from standard input, and so on.

> These security guarantees can be relied on because platforms have *exclusive* control
> over all I/O primitives … There are no escape hatches that a malicious program could
> use to get around them.

Purity is used for **compile-time evaluation** ("all top-level values are evaluated at
compile time") and optimisation, not for capability control. Note the direct parallel
to Rust's `const`.

**Lesson for Fern**: Roc's design says platform capability control is a *linking*
problem, not a typing problem — and Fern already does exactly this
(`internal/platforms`, E066 post-tree-shake). The question is whether Fern wants the
check to be **modular** (per-function, per-module, diagnosable at the definition site)
rather than **whole-program** (diagnosable only after tree-shaking). That is the real
delta a type-level row buys.

---

## 7. Flix — set-based effects, sub-effecting, exclusion, associated effects, default handlers

Primary sources: the [Flix book](https://github.com/flix/book/tree/master/src) —
`effect-system.md`, `effect-polymorphism.md`, `primitive-effects.md`,
`associated-effects.md`, `effects-and-handlers.md`, `default-handlers.md`,
`purity-reflection.md`, `effect-oriented-programming.md`;
[Madsen & van de Pol, *Polymorphic Types and Effects with Boolean Unification*,
OOPSLA 2020](https://dl.acm.org/doi/10.1145/3428222);
[*With or Without You: Programming with Effect Exclusion*, ICFP 2023](https://dl.acm.org/doi/10.1145/3607846);
[*Qualified Types with Boolean Algebras*, OOPSLA 2025](https://dl.acm.org/doi/full/10.1145/3763096).

**Flix is the closest existing language to what Fern is contemplating**: a
statically typed, non-academic-toy language whose *primitive* effects are exactly
platform capabilities.

### 7.1 Syntax (verbatim)

```flix
def inc(x: Int32): Int32 \ { } = x + 1

def incAndPrint(x: Int32): Int32 \ {IO} = …

def copyFile(src: File, dst: File): Unit \ {FsRead, FsWrite, IO} = ...

def nth(i: Int32, a: Array[t, r]): Option[a] \ {r} = ....

def strange(a: Array[t, r]): Unit \ {r, Clock, Http, IO}
```

Effect polymorphism:

```flix
def map(f: a -> b \ ef, l: List[a]): List[b] \ ef = ...

def >>(f: a -> b \ ef1, g: b -> c \ ef2): a -> c \ (ef1 + ef2) = x -> g(f(x))
```

> In Flix, the language of effects is based on set formulas:
> - The *complement* of `ef` is written `~ef`.
> - The *union* of `ef1` and `ef2` is written `ef1 + ef2`.
> - The *intersection* of `ef1` and `ef2` is written `ef1 & ef2`.
> - The *difference* of `ef1` and `ef2` is written `ef1 - ef2`.

Effect **exclusion** (novel):

```flix
def onClick(listener: KeyEvent -> Unit \ (ef - Block), ...): ...

def recoverWith(f: Unit -> a \ Throw, h: ErrMsg -> a \ (ef - Throw)): a = ...
```

### 7.2 Sub-effecting, and where it is *not* allowed

> Flix supports *sub-effecting* which allows an expression or a function to *widen* its
> effect set.

Allowed at **lambda abstraction sites** and **instance definitions**; *not* for
top-level functions:

```flix
def foo(): Bool \ IO = true
```
```
❌ -- Type Error ------------------------------
>> Expected type: 'IO' but found type: 'Pure'.
```

> In summary, Flix allows effect widening in two cases: for (a) lambda expressions and
> (b) instance definitions. We say that Flix supports *abstraction site sub-effecting*
> and *instance definition sub-effecting*.

This is a carefully chosen compromise: full subsumption everywhere makes inference
harder and makes signatures lie; no subsumption anywhere makes every `if/else` over
two lambdas fail to typecheck.

### 7.3 Primitive effects are viral, unhandleable, and per-capability

> Unlike algebraic and heap effects, primitive effects cannot be handled and never go
> out of scope. A primitive effect represents a side-effect that happens on the machine.
> It cannot be undone or reinterpreted.

> The `IO` effect, and all other primitive effects, are *viral*. If a function has a
> primitive effect, all its callers will also have that primitive effect. That is to
> say, once you have tainted yourself with impurity, you remain tainted.

### 7.4 Traits × effects: **associated effects** — the answer Fern needs

Flix hit exactly the problem Fern will hit (traits whose implementations differ in
effect):

```flix
trait Dividable[t] {
    type Aef: Eff
    pub def div(x: t, y: t): t \ Dividable.Aef[t]
}

instance Dividable[Float32] {
    type Aef = { Pure } // No exception, div-by-zero yields NaN.
    pub def div(x: Float32, y: Float32): Float32 = x / y
}

instance Dividable[Int32] {
    type Aef = { DivByZero }
    pub def div(x: Int32, y: Int32): Int32 \ DivByZero = …
}
```

And with regions:

```flix
trait ForEach[t] {
    type Elm
    type Aef: Eff
    pub def forEach(f: ForEach.Elm[t] -> Unit \ ef, x: t): Unit \ ef + ForEach.Aef[t]
}
```

The error that motivates it is worth quoting because Fern will emit its analogue:

```
❌ -- Type Error --------------------------------------------------
>> Unable to unify the effect formulas: 'ef' and 'ef + r'.
```

**Flix needed a whole new type-system feature (associated effects, on top of
associated types) purely to make traits survive contact with effects.** Fern has
traits and associated types. Budget for this.

### 7.5 Default handlers — the "ergonomics escape hatch" for platform effects

```flix
def main(): Unit \ {Clock, Env, Logger} =
    let ts = Clock.currentTime(TimeUnit.Milliseconds);
    let os = Env.getOsName();
    Logger.info("UNIX Timestamp:   ${ts}");
    Logger.info("Operating System: ${os}")
```

is compiled to

```flix
def main(): Unit \ IO =
    run { … } with Clock.runWithIO
      with Env.runWithIO
      with Logger.runWithIO
```

```flix
@DefaultHandler
pub def runWithIO(f: Unit -> a \ ef): a \ (ef - Clock) + IO = ...
```

> Each effect may have at most one default handler, and it must reside in the companion
> module of that effect.

**This is a very good pattern for Fern**: fine-grained effects for checking, with a
platform-supplied default handler so `main` need not be ceremonial.

### 7.6 Kind errors — a real reported ergonomic problem

From `common-problems.md`, verbatim:

```flix
enum A[a, b, ef] {
    case A(a -> b \ ef)
}
```
```
❌ -- Kind Error -----------------------------------------------
>> Expected kind 'Bool or Effect' here, but kind 'Type' is used.
```

> This is because Flix assumes every un-annotated type variable to have kind `Type`.

Introducing effects introduces a **second kind** into the language, and every generic
data structure that stores a function now needs kind annotations. Fern's generics
would inherit this.

### 7.7 Purity reflection

```flix
@ParallelWhenPure
pub def count(f: a -> Bool \ ef, s: Set[a]): Int32 \ ef =
    match purityOf(f) { case Purity.Pure(g) => …parallel… ; case Purity.Impure(g) => …serial… }
```

An underrated *positive*: effect information enables automatic parallelisation, dead
code elimination and inlining. For Fern, the analogue is: an effect row tells the
optimiser when a call can be CSE'd, hoisted out of a loop, or dropped — which today
Fern cannot know for any call that might touch the OS.

---

## 8. Haskell — the library ecosystem, and its verdict

### 8.1 The landscape

* **`mtl`** — effects as type classes over monad transformers. Excellent inference for
  `get`/`put` via fundeps, but fundeps also mean *one `State` per stack*; slow; and
  most transformers other than `ReaderT` have subtle semantics under exceptions.
* **`fused-effects`** — fast (comparable to `mtl`), but "new effects take more code to
  define than polysemy" ([effects-benchmarks](https://github.com/patrickt/effects-benchmarks)).
* **`polysemy`** — "an order of magnitude less boilerplate" than `fused-effects`, but
  historically one to three orders of magnitude slower; performance depends on fragile
  inlining.
* **`eff`** (Alexis King) — delimited-continuation based, *stalled*: the `effectful`
  README cites "a [few](https://github.com/hasura/eff/issues/13)
  [subtle](https://github.com/hasura/eff/issues/7) [issues](https://github.com/hasura/eff/issues/12)
  related to its use of delimited continuations underneath."
* **`effectful`** — the current pragmatic winner.
* **`bluefin`** — the current *design* frontier.

### 8.2 `effectful`'s argument, verbatim ([README](https://github.com/haskell-effectful/effectful/blob/master/README.md))

On `mtl`:

> `mtl` style effects are slow. The majority of popular monad transformers (except
> `ReaderT`) used for effect implementations are rife with subtle issues. These are
> problematic enough that the ReaderT design pattern was invented. Its fundamentals are
> solid, but it's not an effect system.

> A solution? Use the `ReaderT` pattern as a base and build around it to make an
> extensible effects library! … The `Eff` monad it uses is essentially a `ReaderT` over
> `IO` on steroids.

And the price it deliberately pays — **it gives up delimited continuations**:

> The `Eff` monad doesn't support effect handlers that require the ability to suspend or
> capture the rest of the computation and resume it later (potentially multiple times).
> This prevents `effectful` from providing (in particular): a `NonDet` effect handler …
> [and] a `Coroutine` effect.
>
> It needs to be noted however that such `NonDet` effect handler in existing libraries
> is broken … so arguably it's not a big loss.

**The most-used effect system in Haskell has concluded that multi-shot continuations
are not worth their cost.**

### 8.3 `bluefin`'s framing: *synthetic* vs *analytic* effect systems

From [`bluefin/src/Bluefin.hs`](https://github.com/tomjaguarpaw/bluefin/blob/master/bluefin/src/Bluefin.hs) (verbatim):

> The approach of building effects from smaller pieces by combining algebraic data types,
> and then interpreting those pieces to "handle" some of the effects can be called the
> "synthetic" approach to effects. … the synthetic approach has two notable benefits:
> "fine-grained effects" and "encapsulation".

> Unfortunately, synthetic effects have two notable downsides: firstly they have
> unpredictable performance, and secondly they make it hard to achieve resource safety.
> The first point – that good performance of synthetic effects relies critically on
> fragile inlining optimizations – is described in detail by Alexis King in the talk
> "Effects for Less".

> We can have the best of both worlds using "analytic" effect systems. Analytic effect
> systems are those whose effects take place in a monad that is a lightweight wrapper
> around `IO`, with a type parameter to track effects.

> Analytic effect systems do not support multishot continuations because `IO` doesn't
> either, at least safely.

Bluefin's distinguishing move is **value-level capabilities**:

> It is distinct from prior effect systems because effects are accessed explicitly
> through value-level capabilities which occur as arguments to effectful operations.

with `ST`-style rank-2 scoping to prevent escape:

```haskell
incrementReadLine ::
  (e1 <: es, e2 <: es, e3 <: es) =>
  Modify Int e1 -> Throw String e2 -> IOE e3 -> Eff es ()
```

Handler signatures all have the shape

```haskell
(forall e. <Handle> e -> Eff (e :& es) a) -> Eff es r
```

> A benefit of value-level effect capabilities is that it's simple to have multiple
> effects of the same type in scope at the same time. It is simple to disambiguate them,
> because they are distinct values! By contrast, existing effect systems require the
> disambiguation to occur at the type level, which imposes challenges.

(Compare Koka: duplicate labels are Koka's *type-level* answer to the same problem.)

### 8.4 The "just pass a record of capabilities" argument

The steel-man is the **ReaderT design pattern** ([Snoyman](https://www.fpcomplete.com/blog/2017/06/readert-design-pattern/))
and the **handle pattern** ([van de Jeugt](https://jaspervdj.be/posts/2018-03-08-handle-pattern.html)),
plus `capability`-style ad-hoc interpreters ([Tweag](https://www.tweag.io/blog/2021-04-08-capabilities-ad-hoc-interpreters/)).
The counter-argument, again from Bluefin, is precise and is the strongest single
sentence for *why type-level tracking earns its keep*:

> once you are in `IO` you are now trapped inside `IO`. The function `exampleIO` above
> does not have any externally-observable effects. It always returns the same value each
> time it is run, but its type does not reflect that. There is no *encapsulation*.

### 8.5 The skeptic's case

[*On the purported benefits of effect systems*](https://typesanitizer.com/blog/effects-convo.html)
(Varun Gandhi) is the best-argued critique. Its claims, as reported: the usual benefits
(testability, visibility, user-defined control flow) "ultimately come down to API design
rather than whether the language supports algebraic effects"; the costs are that
"function signatures need to be annotated with all the effects they use", that effect
systems "add type system complexity, especially when polymorphism gets involved", and
that they "make it more difficult for a language designer to optimize core constructs".
Discussion: [lobste.rs](https://lobste.rs/s/qxmiqs/on_purported_benefits_effect_systems).

---

## 9. Java — Loom, structured concurrency, scoped values (the mainstream (D) answer)

[JEP 506: Scoped Values](https://openjdk.org/jeps/506) (final in JDK 25);
[JEP 533: Structured Concurrency](https://openjdk.org/jeps/533).

Not an effect system at all. A `ScopedValue` is *"a container object that allows a data
value to be safely and efficiently shared by a method with its direct and indirect
callees within the same thread, and with child threads, without resorting to method
parameters."* It is written once, is immutable, has a bounded lifetime tied to a
lexical scope, and is inherited by subtasks forked in a structured-concurrency scope.

The relevance to Fern is narrow but real: **this is the industry's chosen answer to
"capability passing is too much plumbing"** — dynamic scoping with a bounded extent,
rather than either explicit parameters or a type-level row. It gives you the
*ergonomics* of ambient authority with the *lifetime discipline* of a capability, and
zero static checking. Scala's `using` clauses (§4.4) play the same role with static
types. Koka's `val` operations are described in the tour as "essentially dynamically
bound variables (but statically typed!)" — the same idea again.

---

## 10. Rust — `const fn` as a shipped effect discipline, and why effect generics stalled

Primary source: [`rust-lang/keyword-generics-initiative`](https://github.com/rust-lang/keyword-generics-initiative)
(renamed the **Effects Initiative**), especially
[`CHARTER.md`](https://github.com/rust-lang/keyword-generics-initiative/blob/master/CHARTER.md),
[`updates/2024-02-09-extending-rusts-effect-system.md`](https://github.com/rust-lang/keyword-generics-initiative/blob/master/updates/2024-02-09-extending-rusts-effect-system.md),
[`explainer/effect-generic-bounds-and-functions.md`](https://github.com/rust-lang/keyword-generics-initiative/blob/master/explainer/effect-generic-bounds-and-functions.md).

### 10.1 The thesis

> we've unknowingly shipped an effect system as part of the language in since Rust 1.0.

Rust's effects: `const`, `async`, `try`/`?`, generators, (and formerly `unsafe`, since
retracted — *"semantically `unsafe` in Rust is not an effect"*).

### 10.2 The subset/superset framing — directly reusable by Fern

> - `const fn` creates a *subset* of "base Rust" … `const` functions can be executed in
>   "base" contexts, while the other way around isn't possible.
> - `async fn` creates a *superset* of "base Rust" … `async` types cannot be executed in
>   "base" contexts, but "base" in `async` contexts *is* possible.

with the diagram:

```
                      +---------------------------+                               
                      | +-----------------------+ |     Compute values:
                      | | +-------------------+ | |     - types
                      | | |                   | | |     - numbers
                      | | |    const Rust     |-------{ - functions               
                      | | |                   | | |     - control flow            
 Access to the host:  | | +-------------------+ | |     - traits (planned)                 
 - networking         | |                       | |     - containers (planned)
 - filesystem  }--------|      "base" Rust      | |                               
 - threads            | |                       | |                               
 - system time        | +-----------------------+ |     Control over execution:      
                      |                           |     - ad-hoc concurrency      
                      |         async Rust        |---{ - ad-hoc cancellation     
                      |                           |     - ad-hoc pausing/resumption
                      +---------------------------+
```

Note that "access to the host: networking, filesystem, threads, system time" is
already drawn as the effect boundary. **Fern's `io`/`net`/`fs`/`clock` row is exactly
this line, made explicit.**

### 10.3 The combinatorial argument (the best available motivation for polymorphism)

> Say we took the existing family of `Fn` traits and introduced effectful versions of
> them. That is: versions which work with `unsafe`, `async`, `try`, `const`, and
> generators. With one effect we're up to six unique traits. With two effects we're up
> to twelve. With all five we're suddenly looking at 96 different traits.

> From analyzing the Rust 1.70 stdlib, by my estimate about 75% of the stdlib would
> interact with the const effect. Around 65% would interact with the async effect. And
> around 30% would interact with the try effect. … close to 100% of the stdlib would
> interact with one or more effect. And about 50% would interact with two or more.

Proposed syntax (attributes as placeholder):

```rust
#[maybe(async)]
pub fn copy<R, W>(reader: &mut R, writer: &mut W) -> io::Result<()>
where
    R: #[maybe(async)] Read,
    W: #[maybe(async)] Write;
```

```rust
copy(reader, writer)?;                // infer sync
copy(reader, writer).await?;          // infer async
copy::<async>(reader, writer).await?; // force async
```

and the explicit acknowledgement that this *is* effect rows:

> The effect of the function and the effect of the bounds it takes all become the same.
> In literature this is also known as "row-polymorphism".

Desugaring: *"it's probably okay to think of effect generics as mostly syntactic sugar
for const bools + associated types"* — which is, note, exactly Flix's **associated
effects** (§7.4) rediscovered.

### 10.4 Where it stands

* `const fn` shipped in 2018 and is the only *complete* effect in Rust — and even then,
  `const` contexts long had "no access to iteration, `Drop` handlers, closures" because
  const traits were missing.
* The `effects` feature gate in rustc was folded into `const_trait_impl`
  ([rust-lang/rust#132479, "Yeet the `effects` feature"](https://github.com/rust-lang/rust/pull/132479)),
  with `#[const_trait]` / `~const` / `HostEffect` const-conditions the surviving
  mechanism ([tracking issue #67792](https://github.com/rust-lang/rust/issues/67792),
  [const traits project goal](https://rust-lang.github.io/rust-project-goals/2024h2/const-traits.html)).
* Effect-generic *functions* (the row-polymorphic part) remain unimplemented; the
  initiative's explainers are still draft RFCs.

**Why it struggled**: Rust tried to retrofit effect polymorphism onto (a) a stable
trait system with coherence, (b) a monomorphising compiler where each effect variant is
a different ABI and a different return type, and (c) a huge published API surface. Two
of those three apply to Fern. **The lesson is to decide before the stdlib is large, and
to prefer a design where the effect is not part of the calling convention.**

### 10.5 `no_std` as a coarse capability system

Rust's real shipped capability system is the `core` / `alloc` / `std` split: `core` is
OS-and-heap-free, `alloc` needs a global allocator, `std` needs an OS. It is enforced
by **linking and cfg**, not by types, and it produces exactly the ecosystem damage a
type-level system is meant to avoid: `no_std` support is a per-crate feature flag, is
not uniform, and forces duplicated constructors (`_in` variants for allocator-generic
types). Yosh Wuyts argues explicitly that capabilities are the fix:
*"capabilities could be the canonical way to solve the current core/alloc/std split in
Rust, and if we could opt-out of relying on ambient capabilities in std, the reason to
have separate core, alloc, and std libraries should go away"*
([*Nesting Allocators*](https://blog.yoshuawuyts.com/nesting-allocators)).

**Fern already has this exact split** (`docs/FREESTANDING-CORE.md`, core-vs-host).
This is the strongest single argument that Fern's effect row should include
*allocation* and *panic/abort*, not only I/O.

---

## 11. WebAssembly Component Model / WASI 0.2 — capabilities at the deployment boundary

Primary sources: [WIT format spec](https://github.com/WebAssembly/component-model/blob/main/design/mvp/WIT.md),
[`wasi:cli` world](https://github.com/WebAssembly/wasi-cli/blob/main/wit/imports.wit),
[WASI](https://github.com/WebAssembly/WASI).

A `world` is a *complete, checked declaration of a component's capability set*:

```wit
package wasi:cli@0.2.8;

world imports {
  include wasi:clocks/imports@0.2.8;
  include wasi:filesystem/imports@0.2.8;
  include wasi:sockets/imports@0.2.8;
  include wasi:random/imports@0.2.8;
  include wasi:io/imports@0.2.8;

  import environment;
  import exit;
  import stdin;
  import stdout;
  import stderr;
  …
}

world command {
  include imports;
  export run;
}
```

> A world is a complete description of both imports and exports of a component. A world
> can be thought of as an equivalent of a `component` type in the component model.

Worlds compose by **union** (`include`), which is precisely row/set union at the module
boundary, and de-duplicate shared interfaces. WASI has no ambient authority: a component
can only reach `wasi:filesystem` if the host supplies it, and the host can supply a
pre-opened subtree only.

**This is the deployment-level analogue of Fern's effect row, and it is already a
target Fern compiles to.** A Fern effect row that maps 1:1 onto WIT worlds would let
`fern build -target wasi` *derive* the world from the program's inferred effects, and
conversely *reject at compile time* a program whose row exceeds the declared world.
That is a concrete, shippable payoff that none of the research languages have.

Caveat for §12: WASI 0.2 has **no stack switching**. Effect handlers on WASI need
either the monadic/CPS translation (Koka's route) or the
[WasmFX / stack-switching proposal](https://github.com/WebAssembly/stack-switching)
(*Continuing WebAssembly with Effect Handlers*, OOPSLA 2023; phase 2 as of Aug 2024,
with reference-interpreter, Wasmtime, Wizard, Binaryen implementations in flight).

---

## 12. Other genuinely SOTA work

### 12.1 Frank — effect polymorphism *without effect variables in source*
[Lindley, McBride, McLaughlin, *Do Be Do Be Do*, POPL 2017](https://dl.acm.org/doi/10.1145/3009837.3009897)
([arXiv](https://arxiv.org/abs/1611.09259), [implementation](https://github.com/frank-lang/frank)).
Frank propagates an **ambient ability inwards** and expresses operators as an
**adjustment** (a delta) on it, "rather than accumulating unions of potential effects
outwards". Result: "lightweight types for operators that can be used in different
contexts (a form of effect polymorphism) without explicit quantification over effect
variables". Unison's ambient-ability rule is a direct descendant; Unison's docs record
that naive *post-hoc* Frank-style generalisation is **unsound** in their setting (§2.3).

### 12.2 Eff — the original
[Bauer & Pretnar, *Programming with Algebraic Effects and Handlers*](https://arxiv.org/abs/1203.1539);
[*An Effect System for Algebraic Effects and Handlers*, LMCS 2014](https://arxiv.org/abs/1306.6316);
[implementation](https://github.com/matijapretnar/eff). ML-style, HM inference plus
**effect subtyping** with subtyping constraints. Practical experience with
constraint-based effect subtyping is one of the sources of the standard warning that
"the constraints often became quite complex and the combination of polymorphism with
subtyping can make type inference undecidable". See also
[*Explicit Effect Subtyping*, JFP 2020](https://www.cambridge.org/core/services/aop-cambridge-core/content/view/4851B1C994BEAAB7F04A58B60F11D0AF/S0956796820000131a.pdf/explicit_effect_subtyping.pdf)
for the coercion-based reformulation.

### 12.3 Links — Rémy rows with presence types
[Hillerström & Lindley, *Liberating Effects with Rows and Handlers*](https://homepages.inf.ed.ac.uk/slindley/papers/links-effect.pdf);
[Lindley & Cheney, *Row-based Effect Types for Database Integration*](https://homepages.inf.ed.ac.uk/slindley/papers/corelinks.pdf).
Rows carry **presence/absence** annotations (`•` / `◦`) and presence polymorphism,
which is what lets handlers compose seamlessly. This is the principled alternative to
Koka's duplicate labels, at the cost of an extra kind and more verbose types.

### 12.4 Helium — effect *instances* via lexically scoped handlers
[Biernacki, Piróg, Polesiuk, Sieczkowski, *Binders by Day, Labels by Night*, POPL 2020](https://dl.acm.org/doi/10.1145/3371116).
Handlers as **binders** for effect operations, with an "open semantics" (handlers as
binders) and a "generative semantics" (each handler generates a fresh runtime label).
This is the theory behind first-class/named handler *instances* — the thing you need if
two `State` effects must coexist and be distinguished (compare Bluefin's value-level
answer and Koka's [*First-Class Names for Effect Handlers*, OOPSLA 2022](https://dl.acm.org/doi/10.1145/3563289)).

### 12.5 Granule — graded modal types (quantitative effects)
[Orchard, Liepelt, Eades, *Quantitative Program Reasoning with Graded Modal Types*,
ICFP 2019](https://www.cs.kent.ac.uk/people/staff/dao7/publ/granule-icfp19.pdf);
[implementation](https://github.com/granule-project/granule). Effects are a **graded
monad** on the *result type*, not on the arrow. Verbatim from `StdLib/File.gr`:

```granule
readFile : String -> String <{Open, Read, Close, IOExcept}>

auxiliaryWriteFile : Handle W -> String -> (Handle W) <{Write, IOExcept}>
```

and coeffects grade *inputs*:

```granule
dup : forall {a : Type} . a [2] -> (a, a)
map : forall {a : Type, b : Type, n : Nat} . (a -> b) [n] -> Vec n a -> Vec n b
```

Grades generalise sets: you can grade by a semiring (counts, security levels, effect
sets). Relevant to Fern mostly as a reminder that the *same* machinery could carry
Fern's linearity/uniqueness information if it ever wants it — but it is a much larger
commitment than an effect row.

### 12.6 Modal effect types — the current frontier, and the row-vs-capability unifier
[Tang, White, Dolan, Hillerström, Lindley, Lorenzen, *Modal Effect Types*, OOPSLA 2025](https://dl.acm.org/doi/10.1145/3720476)
([arXiv](https://arxiv.org/abs/2407.11816));
[Tang & Lindley, *Rows and Capabilities as Modal Effects*, POPL 2026](https://dl.acm.org/doi/10.1145/3776674)
([arXiv](https://arxiv.org/abs/2507.10301)).

The framing is exactly the problem statement Fern has:

> Conventional effect systems attach effects to function types, which can lead to
> verbose effect-polymorphic types, especially for higher-order functions.

Modal effect types **decouple effects from function types** and track them via
*relative* and *absolute modalities* that transform the ambient effect context —
achieving "modular effectful programming via modalities without relying on effect
variables", while noting that "occasionally effect variables are still useful,
particularly for the implementation of higher-order effects which take closures as
arguments". The 2026 follow-up gives **type- and semantics-preserving macro
translations from existing row-based *and* capability-based effect systems into one
framework**, i.e. it formally settles that rows and capabilities are two strategies
within one design space. If Fern wants one paper to read before choosing, it is this
pair. Note the author list includes OCaml's Leo White and Stephen Dolan — this is the
likely shape of typed effects in OCaml.

### 12.7 Idris 2 — QTT, `Control.App`, and the retirement of `Eff`
[Brady, *Idris 2: Quantitative Type Theory in Practice*, ECOOP 2021](https://drops.dagstuhl.de/entities/document/10.4230/LIPIcs.ECOOP.2021.9).
Idris 1's `Effects` library (a dependent, algebraic-effects DSL) was **retired** in
Idris 2 in favour of [`Control.App`](https://idris2.readthedocs.io/en/latest/app/index.html),
a much simpler `App` monad parameterised by a `Path` (linear or may-throw) and a
`List Error`. Linearity (multiplicity `1`) carries resource protocols instead. The
data point for Fern: **a language with a full dependent effect system replaced it with
a fixed, coarse, two-parameter one** on ergonomic grounds.

### 12.8 Lean 4
No effect system; `IO` is a monad (`EStateM` over a world token), `partial` marks
non-terminating definitions, and `unsafe`/`implementedBy` are the escape hatches. The
notable design point is that Lean, like Roc, encodes "can this run at compile time" as
a *separate* concept (`#eval`/`Meta`) rather than as a general effect.

### 12.9 Verse — a shipped effect *lattice* with subtyping
[Verse glossary](https://dev.epicgames.com/documentation/fortnite/verse-glossary),
[Book of Verse ch. 13](https://verselang.github.io/book/13_effects/).
Effect specifiers go in angle brackets after the parameter list. The hierarchy is
`converges ⊂ computes ⊂ varies ⊂ transacts`, i.e. **a total order, not a set** —
`computes` means same output for same input, `varies` means it reads non-constant
state, `transacts` means it mutates and can be rolled back, `no_rollback` is the
default for effects that cannot be undone, `decides` marks failable functions,
`suspends` marks async. Specifiers are split into **exclusive** (at most one) and
**additive** (combinable). `decides` and `suspends` cannot combine.

This matters because Verse is a *shipped, mass-market* language (UEFN/Fortnite) whose
effect system is a small fixed lattice with subsumption and no polymorphism — evidence
that the simple end of family (B) survives contact with non-expert users.

### 12.10 Wyvern — capabilities and effects for authority control
[Melicher, Shi, Potanin, Aldrich, *A Capability-Based Module System for Authority
Control*, ECOOP 2017](https://drops.dagstuhl.de/entities/document/10.4230/LIPIcs.ECOOP.2017.20);
[*Controlling Module Authority Using Programming Language Design*](https://www.cs.drexel.edu/~csg63/).
Modules are **first-class, statically typed capabilities**; authority is defined
*non-transitively* so that wrappers can attenuate a powerful capability. Later work
adds an effect system on top, because "effects are a good proxy for operations
performed on a resource". This is the most direct academic treatment of "which module
may reach the filesystem", i.e. Fern's `internal/caps`.

### 12.11 Gordon — capabilities *are* effects
[Gordon, *Designing with Static Capabilities and Effects: Use, Mention, and Invariants*,
ECOOP 2020](https://arxiv.org/abs/2005.11444). Thesis, verbatim from the abstract:

> capabilities (whether object or reference capabilities) are fundamentally tools to
> restrict effects. Thus static capabilities and effect systems take different technical
> machinery to the same core problem of statically restricting or reasoning about
> effects in programs.

Companion result: [Craig, Potanin, Groves, Aldrich, *Capabilities: Effects for Free*,
ICFEM 2018](https://link.springer.com/chapter/10.1007/978-3-030-02450-5_14) — in an
object-capability language you can *derive* an effect system from the capability
discipline. Historical background: Miller, Yee, Shapiro,
[*Capability Myths Demolished*](http://srl.cs.jhu.edu/pubs/SRL2003-02.pdf) (2003),
which dismantles the usual confusions between ACLs and capabilities.

### 12.12 Pony — reference capabilities (a *different* meaning, still instructive)
[Pony tutorial: reference capabilities](https://tutorial.ponylang.io/reference-capabilities/).
Six capabilities — `iso`, `trn`, `ref`, `val`, `box`, `tag` — annotate *aliasability
and mutability of a reference*, not side effects. They form a subtyping lattice,
support `consume` and `recover`, and are checked at every alias. The lesson is a
warning: Pony's ref caps are widely reported as its steepest learning cliff, and they
are a *smaller* system than a general effect row. **Anything Fern adds to every
function signature will be perceived the way Pony's ref caps are perceived.**

### 12.13 Affine/linear effect systems
[*Affect: An Affine Type and Effect System*, POPL 2025](https://dl.acm.org/doi/10.1145/3704841)
— relevant to Fern because affinity is exactly what makes one-shot resumptions
statically checkable, which in turn is what makes them cheap under reference counting
(§8).

---

## 13. Comparison table

| System | What is tracked | Representation | Polymorphism story | Inference burden (compiler) | Annotation burden (user, in practice) | Needs continuations? |
|---|---|---|---|---|---|---|
| **Koka** | side effects incl. `div`, `exn`, `ndet`, `st<h>`, `io` | **scoped row**, duplicate labels allowed | explicit row vars `<l\|e>`; unification widens both sides | HM + row unification; generalisation restricted to total exprs | low at definition sites (all inferred), **high when reading/writing signatures**; `div` pollutes recursive fns | yes for `ctl`; **no** for `fun`/`val` ops or `linear effect`s |
| **Unison** | abilities | **unordered set**, union | existential ability var per unannotated arrow; ambient-ability checking | HM-ish; ambient set from nearest enclosing lambda + handlers | moderate; `->` silently means `->{e}`; accidental concretisation is a known bug class | yes (deep handlers, multi-resume) |
| **Effekt** | *requirements* on the context | **set of capabilities** in return position + **capture sets** on boxed values | **contextual** — no effect variables in source; second-class blocks | capture inference; no row unification | **very low** — the headline claim; boxing is the only visible ceremony | yes (lexical handlers), but compiled without a GC (§8) |
| **Scala 3 CC** | capabilities *retained* by a value | **capture set** `^{c₁…cₙ}` with subcapturing | implicit (via `=>` = `->{any}`) + explicit `[X^]` capture-set vars + classifiers `.only[A]`/`.except[A]` | subcapturing inference; capture tunneling; separation checking | low for eager code, **high for generic containers** (needed System Capless to fix) | no — orthogonal to handlers |
| **Flix** | primitive (`IO`,`FsRead`,`Clock`,…), algebraic, heap/region | **set formula** over a Boolean algebra (`+ & - ~`) | effect vars `ef`; sub-effecting at abstraction sites and instances; **associated effects** for traits | Boolean unification (decidable, but exponential worst case) | moderate; explicit `\ {…}` on most signatures; kind annotations needed on generic data types | yes for algebraic effects; **no** for primitive effects (unhandleable) |
| **OCaml 5** | nothing | — | — | none | zero | yes (segmented stacks in the runtime) |
| **Roc** | pure vs effectful only | **one bit**: `->` vs `=>` | **none, by design** | purity inference | near-zero; cost paid as duplicated APIs (`map_ok` / `map_ok!`) | no |
| **Verse** | `converges`/`computes`/`varies`/`transacts` + `decides`/`suspends` | **fixed lattice**, exclusive + additive specifiers | subsumption only | trivial | low (specifiers in `<…>`) | yes for `suspends` |
| **Rust (`const`)** | compile-time evaluability | **one bit** + `~const` bounds | `~const`/`#[const_trait]`; effect-generic *functions* unimplemented | const-qualification analysis | moderate and rising; const traits took 7+ years | no |
| **Rust (proposed effect generics)** | `const`,`async`,`try`,`gen` | const-bool + associated types (≈ row) | `#[maybe(async)]` on traits, bounds, fns, types; turbofish to force | inference at call sites from `.await`/context | unknown; unshipped | `async` yes (state machines), others no |
| **Haskell `effectful`** | effect list `es` | **type-level list** + `:>` membership | constraint-based (`State Int :> es`) | GHC constraint solving | moderate; `HasCallStack`-style constraint noise | **explicitly not** — gave up multi-shot |
| **Haskell Bluefin** | value-level capability handles + tags | type-level tags `e1 <: es` | rank-2 scoping, `ST`-style | GHC infers well per author | moderate; one extra argument per effect | no |
| **WASI worlds** | imported interfaces | **set of imports**, `include` = union | none (monomorphic per component) | n/a (declared) | declared once per component | n/a |
| **Granule** | graded modality on the *result* | semiring grade `<{…}>` | grade polymorphism | SMT (Z3) | high | no |
| **Modal effect types** | effects via **modalities on the context** | modes, not arrows | modalities; **no effect variables** in the common case | novel; see paper | claimed low | yes (handlers) |

---

## 14. The known ergonomic failure modes, with sources

### 14.1 Effect-polymorphism boilerplate on higher-order functions

The universal complaint. Sources:

* Modal Effect Types abstract: *"Conventional effect systems attach effects to function
  types, which can lead to verbose effect-polymorphic types, especially for higher-order
  functions."* ([arXiv:2407.11816](https://arxiv.org/abs/2407.11816))
* Effekt's own motivation, verbatim: *"often, as soon as you start writing higher-order
  functions the type- and effect system starts to get into your way."*
* Koka's `while` signature — the effect variable must be threaded through *both*
  callbacks and the result: `while : ( pred : () -> <div|e> bool, action : () -> <div|e> () ) -> <div|e> ()`.
* Flix's `>>`: `def >>(f: a -> b \ ef1, g: b -> c \ ef2): a -> c \ (ef1 + ef2)` — two
  effect variables for a two-argument combinator.
* Frank's entire contribution is avoiding this ("without explicit quantification over
  effect variables").
* Reported summary of Koka experience: *"the frequent use of functions with many
  function parameters can render explicit polymorphism impractical"* and
  *"the constraints often became quite complex and the combination of polymorphism with
  subtyping can make type inference undecidable"* (as surveyed in the Effekt line of
  work, e.g. [*Effects as Capabilities*](https://dl.acm.org/doi/10.1145/3428194)).

### 14.2 Function colouring and API duplication

* Rust: *"Sometimes we'll write code which doesn't have the right effects, leading to
  effect mismatches. This is also known as the function coloring problem"*; with the
  quantified blow-up (96 `Fn` traits for 5 effects; ~100% of stdlib touched).
* Roc accepts colouring deliberately: `Try.map_ok` **and** `Try.map_ok!`.
* Rust `no_std`: the shipped instance of the problem — per-crate feature flags,
  non-uniform support, `_in` constructor variants.
* Haskell: `mtl` vs `effectful` vs `polysemy` vs `fused-effects` is itself an
  ecosystem-fragmentation story; libraries must pick one or expose all.

### 14.3 Inference producing *wrong-but-plausible* types

* Unison [#1173]: a "pure-looking" `a -> b` silently gets a concrete ability because a
  callee has one. Proposed fix: make it an error.
* Koka: `div` is inferred on any recursive function the termination checker can't
  discharge — `fun fib(n : int) : div int`. Signatures acquire an effect the programmer
  never thought about.
* Unison's soundness note on currying (`Text ->{IO} Text ->{} ()` fails) — a class of
  errors that only exists because effects live on arrows and arrows curry.

### 14.4 Error message quality

* Best-in-class example, Scala's `CanThrow` diagnostic, which lists **all three** ways
  to discharge the obligation (add a `using` clause / add a `throws` clause / wrap in
  `try`). Fern should copy this shape.
* Flix's effect-unification error is the archetype of the *bad* case:
  `>> Unable to unify the effect formulas: 'ef' and 'ef + r'.` — correct, and nearly
  useless without the associated-effects explanation.
* Flix's kind error is the *second* archetype:
  `>> Expected kind 'Bool or Effect' here, but kind 'Type' is used.` — a diagnostic in
  terms of a kind system the user never asked for, triggered by writing
  `enum A[a, b, ef] { case A(a -> b \ ef) }`.
* Scala CC has a whole community thread titled ["Capture checking being confusing
  again"](https://users.scala-lang.org/t/capture-checking-being-confusing-again/12083).

### 14.5 Generic data structures / containers

* Scala: *"expressiveness has been insufficient for tracking capabilities embedded in
  generic data structures, preventing them from scaling to the standard collections
  library"* — required a new calculus and a full collections migration
  ([*What's in the Box*](https://dl.acm.org/doi/10.1145/3763112)).
* Flix: every user-defined type that stores a function needs a kind-annotated effect
  parameter.
* Effekt: sidesteps it by making computations second-class and requiring `box`, which
  is a *restriction*, not a solution.

### 14.6 Traits/interfaces

* Flix needed **associated effects** (a new feature on top of associated types).
* Rust needs **effect-generic trait declarations** — the first and still-unshipped
  stage of its plan.
* Scala uses classifiers + capture-set parameters.
* **Every system with an interface mechanism had to extend it.** Fern has traits. This
  is not optional work.

### 14.7 Ecosystem churn when an effect is added

* OCaml's stated reason for shipping untyped effects: every existing `.mli` signature
  would change.
* Rust's estimate: adding `const` touches ~75% of the stdlib; `async` ~65%.
* Flix mitigates with `@DefaultHandler`, so adding a `Clock` effect doesn't force every
  `main` to add a handler.
* Koka mitigates with `mask`, whose own docs concede: *"The cases where `mask` is needed
  are much less common in our experience"* — i.e. an escape hatch that is deliberately
  rare, because using it hides the effect from the signature.

### 14.8 Runtime failure modes when tracking is absent
OCaml's `Exception: Stdlib.Effect.Unhandled(E)` is the concrete cost of skipping the
type system: an unhandled effect is a runtime crash in a language whose whole selling
point is that runtime crashes are rare.

---

## 15. The design decision tree for Fern

### Step 0 — Decide what the row is *for*

Three different goals get three different answers:

| Goal | Minimum viable design |
|---|---|
| **Make platform capability violations a modular, definition-site error** (today: whole-program, post-tree-shake E066) | a **set** of platform effect labels + sub-effecting; no handlers |
| **Enable optimisation** (hoisting, CSE, dead-call elimination, auto-parallelism) | a **purity bit** suffices (Roc, Flix purity reflection) |
| **User-defined control flow** (generators, async, backtracking, DI) | full **handlers** + continuations (expensive — §16) |

Fern's stated goal is the first. **The first goal does not require handlers, does not
require row polymorphism with duplicate labels, and does not require continuations.**

### Step 1 — Row vs set vs capability

**What row polymorphism (Koka/Links style) buys you, and what it costs.**

*Buys*: (a) principal types with an open tail, so a function can be used at any larger
effect context without subsumption machinery; (b) precise elimination forms — `catch`
*removes* `exn` from the row, `mask` skips a handler — which is the only reason you
need duplicate labels; (c) HM-style decidable inference with no constraint solving.

*Costs*: (a) an effect variable on every arrow of every higher-order signature (14.1);
(b) a second kind in the language, which leaks into every generic data type (14.5,
Flix's kind error); (c) row unification errors that mention machinery the user never
wrote; (d) it forces effects into the *type* of closures, which for a monomorphising
AOT compiler means the effect participates in specialisation and potentially in
layout/ABI.

**Fern does not need (b).** Fern's platform effects — `io`, `net`, `fs`, `clock`,
`rand` — are, in Flix's terminology, **primitive effects: viral and unhandleable**.
There is no `catch fs`. Without elimination forms, the entire justification for scoped,
duplicable labels evaporates. Koka's own duplicate-label argument is *specifically*
about typing `catch`.

**What a simple set with sub-effecting buys.** `{fs, clock}` ⊆ `{fs, clock, net}` with
plain subsumption. Union at joins. One effect variable when you genuinely need
polymorphism (`map`), and — critically — Flix's finding that sub-effecting is only
needed at **abstraction sites and trait-instance definitions**, not everywhere. This is
enormously simpler than row unification, and set-union/subset over a *fixed, closed*
label set (platform capabilities are enumerable and defined by `internal/platforms`) is
decidable in linear time, not by Boolean unification.

**What capability passing (Effekt/Scala style) buys.** No effect variables at all
(contextual polymorphism), and it composes with generics for free *as long as
computations are second-class*. But: Fern has first-class closures stored in arrays,
returned from functions, and captured in structs. Effekt's answer is `box` + capture
sets; Scala's is capture sets + boxes + reach capabilities + separation checking, and
that took a decade and still isn't stable. **Do not attempt full capture tracking.**

**The strong argument against pure value-passing (family D)**, from Scala's docs:

> In traditional capability systems all capabilities provided by the host system have to
> be passed as parameters into the main entry point and from there to all functions that
> need access. This usually requires a global refactoring of the code base and can lead
> to more complex code.

Fern's `std/fs`, `std/net` etc. would all need to take a capability parameter. That is
a whole-stdlib break for a mechanism that gives *less* checking than a row.

### Step 2 — The recommended shape for Fern

A **closed set of primitive effect labels, with subsumption, one effect variable form,
and no handlers**:

1. **Labels are declared, not open.** The label set is exactly the platform capability
   taxonomy already in `internal/platforms` / `examples/self_host/platforms.fern`.
   A new builtin already requires four classifications (per `CLAUDE.md`); this makes it
   five, and the completeness tests already exist to catch omissions.
2. **Default is total/pure and elided.** `fn f(x: int) -> int` means no effects. Follow
   Koka/Flix here, not Unison (whose bare `->` meaning "some inferred `e`" is a
   documented source of confusion).
3. **Effects are inferred within a module and *required* on public signatures.** This
   is the single most important ergonomic decision. Inference everywhere (Koka, Roc)
   makes definition sites free; requiring the annotation on `pub` items keeps the row
   from being an invisible part of the ABI that changes under you, and makes the
   diagnostic point at a signature the user wrote. It also bounds the blast radius of
   14.7: adding an effect to a private function is free; adding one to a `pub` function
   is a visible API change, which is correct.
4. **Sub-effecting at abstraction sites and trait impls only** (copy Flix exactly —
   they arrived at this after shipping the alternative).
5. **One polymorphism form, and make it cheap to write.** Fern needs `map`,
   `for_each`, `sort_by` to be effect-generic. Options, in increasing cost:
   * **(a) Effekt-style contextual reading**: a callback's declared effects are
     *provided by the caller's context*, and the combinator itself stays effect-free.
     Zero syntax. Requires the callback to be non-escaping, which Fern can check —
     it already does closure-capture analysis (`docs/CLOSURE-CAPTURE.md`).
     **This is the best fit and should be the default for non-escaping `fn` parameters.**
   * **(b) A single named effect variable** `fn map<T,U,E>(xs: [T], f: fn(T) -> U ! E) -> [U] ! E`
     for the escaping cases. Keep it to one variable and one union; do not add
     complement/difference (Flix's `~`/`&`/`-`) until a concrete need appears —
     they are the source of the Boolean-unification cost and of the worst error
     messages.
   * **(c) Do not do `mask`/`inject`.** Without handlers there is nothing to mask.
6. **Traits get associated effects** (Flix's design, which is also what Rust's
   "const bools + associated types" desugaring amounts to). Fern already has associated
   types (`docs/ASSOCIATED-TYPES.md`), so this is an extension of existing machinery
   rather than a new one.
7. **`main` declares its row; the platform declares what it provides.** This is Flix's
   `@DefaultHandler` and WASI's `world`, unified. `fern build -target wasi` should be
   able to **emit the WIT world** from the inferred row of `main`, and reject a program
   whose row exceeds the target's provision — turning the current post-tree-shake E066
   into a definition-site diagnostic *and* a build artefact.
8. **Diagnostics modelled on Scala's `CanThrow` message**: name the missing capability,
   and enumerate every way to discharge it (add it to this signature / add it to the
   caller / the target does not provide it at all).

### Step 3 — What to explicitly *not* do (and record why)

* **No duplicate/scoped labels.** Justified only by elimination forms Fern won't have.
* **No `div` / termination effect.** Koka's own docs show it polluting `fib`. A
  divergence effect on a systems language would appear on nearly every loop.
* **No effect *handlers* in v1.** §16. If they come later, they arrive as a *separate*
  feature over the same label vocabulary — which is exactly how Flix layers algebraic
  effects on top of primitive effects.
* **No complement/difference operators** until a real use case (exception handlers,
  thread spawning) demands them.
* **No second kind if avoidable.** If effect labels are a *closed enum* rather than
  arbitrary types, a generic `struct Cell<T>` holding `fn() -> () ! {io}` can carry the
  effect as part of the function type without introducing a `Bool or Effect` kind.
  This is the single biggest divergence from Flix and it avoids Flix's most-reported
  papercut.

### Step 4 — Sequencing

1. Purity bit (`!` / `!{}`) + inference + `pub`-signature requirement. Immediately
   enables optimisation work and catches the coarse errors.
2. Split the bit into the platform label set; wire to `internal/platforms`.
3. Contextual polymorphism for non-escaping callbacks; then one effect variable.
4. Associated effects on traits.
5. WIT-world emission for WASI; E066 becomes definition-site.
6. *Only then*, if user-defined control flow is wanted: handlers, restricted to
   tail-resumptive/linear operations (§16).

---

## 16. Interaction with reference counting, no GC, and AOT

This section is about *handlers*, not tracking. **Effect tracking has no runtime cost
at all** — it is erased. Handlers are where the money goes.

### 16.1 The core problem

An effect operation may capture the delimited continuation between the `perform` and
its handler, and resume it zero, one, or many times. Xie & Leijen state the difficulty
directly:

> it is not straightforward to compile effect handlers into efficient code: effect
> operations are generally able to capture- and resume a delimited continuation, which
> usually requires special runtime support to do efficiently.

Three known implementation strategies, with their runtime requirements:

| Strategy | Runtime requirement | Capture cost | Multi-shot | Used by |
|---|---|---|---|---|
| **Segmented stacks** | dedicated runtime, custom stack allocator, GC that can trace stack segments | O(1) for one-shot | needs a linear stack copy | OCaml 5, WasmFX |
| **Stack copying (`longjmp` + memcpy)** | none beyond C | O(stack depth) | O(stack depth) per resume | `libhandler` |
| **Monadic / CPS translation with evidence passing** | **none** — compiles to plain C/WASM/LLVM | O(continuation points) | shared, cheap | **Koka** |

Xie & Leijen's summary of the trade, verbatim:

> segmented stacks need a dedicated runtime system but can capture and resume an
> operation in constant time (for *one-shot* resumptions), while a multi-prompt monad is
> linear in the continuation points.

and, on why the monadic route was chosen:

> With all evaluation transitions localized, we can now define a direct *monadic
> translation* of effect handlers into a plain typed lambda calculus using a
> multi-prompt monad. Such program can be directly compiled to any target platform
> (including C/LLVM, WASM, JavaScript, Java VM, .NET, etc) without requiring special
> runtime mechanisms.

**For Fern, which targets ARM64/x86-64 native *and* WASI, the monadic route is the only
one available today**: WASI 0.2 has no stack switching, and adding a segmented-stack
runtime would contradict Fern's fast-startup, static-binary, freestanding-capable
posture (and would be flatly impossible in the bare-metal/kernel targets of
`docs/BARE-METAL-PLAN.md`, where an interrupt handler is already a second context).

### 16.2 Why this is worse under reference counting

Three specific costs, none of which show up in a GC'd language:

1. **A captured continuation is a heap object graph.** Under a tracing GC, capturing a
   continuation and duplicating it for multi-shot resumption costs a pointer copy; the
   GC sorts out sharing. Under Perceus-style RC, *duplicating* a resumption means
   incrementing the refcount of everything it holds, and *dropping* one (an operation
   that never resumes — i.e. an exception) means running the full drop chain for the
   captured frames. Koka gets away with this because the monadic translation makes
   continuations ordinary closures that RC already handles, and because — as the
   implementation notes report — **the resumption function is shared over multiple
   resumes in Koka (and Haskell), whereas OCaml must clone**. Sharing is the RC-friendly
   representation; stack copying is not.
2. **Ownership crosses the capture boundary.** Fern's Perceus insertion (`inc`/`dec`,
   borrow inference, drop specialisation, reuse analysis) is a *dataflow* analysis over
   a control-flow graph. A `perform` that may or may not return, and may return more
   than once, invalidates the linearity assumptions that make `dup`/`drop` placement
   correct. This is precisely the interaction the roadmap's goal 2 is already grinding
   through for ordinary control flow; multi-shot resumption would multiply it.
   Effekt's ICFP 2025 result is the state of the art here: *"By building on garbage-free
   reference counting and associating stacks with stable prompts, our approach enables
   constant-time continuation capture and resumption when resumed exactly once, as well
   as constant-time state access. We also support multiple resumptions by copying stacks
   when necessary."* Note the shape of the answer: **one-shot is constant time; multi-shot
   pays a copy.**
3. **Local mutable state must be saved and restored per resumption strand.** Koka's tour
   is explicit: *"`var` state is correctly saved and restored on resumptions (as part of
   the stack) and this is essential to the correct composition of effect handlers. If
   `var` declarations were instead heap allocated or captured by reference, they would no
   longer be local to their scope and side effects could 'leak' across different
   resumptions."* Fern's local variables are stack slots today; making them
   resumption-safe means either copying them with the continuation or forbidding
   multi-shot.

### 16.3 The escape hatch that Koka itself uses

Two levels, both directly available to Fern:

**Tail-resumptive operations.** Verbatim from Koka's tour:

> almost all operations in practice turn out to be *tail-resumptive*: that is, they
> resume exactly *once* with their final result value.

```koka
with fun op(<args>){ <body> }
```
desugars to
```koka
with ctl op(<args>){ val f = fn(){ <body> }; resume( f() ) }
```

> At the call to `ask` in `add-twice`, it selects the handler from the evidence vector
> and when the operation is a tail-resumptive `fun`, it calls it directly as a regular
> function (except with an adjusted evidence vector for its context). Unlike a general
> `ctl` operation, there is no need to yield upward to the handler, capture the stack,
> and eventually resume again. This gives `fun` (and `val`) operations a performance
> cost very similar to *virtual method calls*.

An effect can *declare* that all its operations must be tail-resumptive:

```koka
effect ask<a>
  fun ask() : a
```

**Linear effects.** Verbatim:

> Use `linear effect` to declare effects whose operations are always tail-resumptive and
> use only linear effects themselves (and thus resume exactly once). This removes monadic
> translation for such effects and can make code that uses only linear effects more
> compact and efficient.

and:

> Such effects are statically guaranteed to never use a general control operation and
> never need to capture a resumption. … this removes the need for the monadic
> transformation and improves performance of any effect polymorphic function that uses
> such effects as well (like `map` or `foldr`). Examples of linear effects are state
> (`:st`) and builtin effects (like `:io` or `:console`).

**Every effect Fern actually wants — `io`, `net`, `fs`, `clock`, `rand` — is linear in
Koka's sense.** They are dynamic dispatch to a platform-provided implementation, not
control flow. If Fern ever adds handlers, the right first (and possibly only) form is
Koka's `fun` operation over a `linear effect`: a **virtual call through an evidence
vector**, with no monadic translation, no stack capture, no interaction with Perceus.

### 16.4 The measured costs (Xie & Leijen, ICFP 2021, §5)

Benchmarks on AMD 5950X, Koka v2.0.16 vs multicore OCaml 4.10 vs `libhandler` vs two
Haskell libraries:

* `counter` (200M tail-resumptive get/set): Koka ≈ OCaml. `libhandler` is 1.5× faster
  than Koka *"because it does no allocation at all"*. Turning off the tail-resumptive
  optimisation makes Koka **10× slower**.
* `counter1` / `counter10` (1 and 10 intervening unused handlers): Koka is 1.5× faster
  than OCaml at `counter1` and essentially flat at `counter10`, because *"the handler is
  found at a constant offset in the canonical evidence vectors"*, whereas everything
  without evidence passing degrades linearly with handler-stack depth. The paper argues
  this pattern is common: *"a type checker may have a current substitution, the type
  environment, a unique identifier generator, etc."*
* `mstate` (non-tail-resumptive state, resumption captured under a lambda): Koka is
  **~5× slower than OCaml** — *"a worst-case for Koka as it needs to allocate a fresh
  resumption for each operation call."*
* `nqueens` (multi-shot backtracking): OCaml is ~5× *slower* than Koka, because Koka
  shares continuations across resumes while *"OCaml and libhandler need to linearly copy
  the stack on each resumption."*

Read as a design brief: **evidence passing + tail-resumption is competitive with a
custom runtime and needs none; general `ctl` operations cost an allocation per call.**

The paper's own caveat about cross-system comparison is worth repeating since it names
Fern's exact configuration: *"Koka uses compiler guided reference [counting] while
multi-core OCaml and Haskell use a generational tracing collector."*

### 16.5 Summary recommendation

| Feature | Runtime cost for Fern | Verdict |
|---|---|---|
| Effect **tracking** (rows/sets on signatures) | **zero** — fully erased | do it |
| **Tail-resumptive / linear** operations (dynamic dispatch to a handler) | one indirect call, ≈ virtual method | safe; the natural v2 |
| General `ctl` handlers via monadic translation | allocation per operation; pervasive monadic bind in *all* effect-polymorphic code; interacts with Perceus insertion | expensive; defer |
| Multi-shot resumption | continuation duplication under RC; local `var` save/restore; drop-chain on abandonment | avoid, or make it an opt-in `linear`-negative marker |
| Segmented stacks | new runtime, incompatible with WASI 0.2 and bare-metal | do not |

---

## 17. Concrete syntax gallery (verbatim, for comparison)

```koka
// Koka
fun square2( x : int ) : console int { println("..."); x*x }
alias pure = <div,exn>
map : (xs : list<a>, f : (a) -> e b) -> e list<b>
while : ( pred : () -> <div|e> bool, action : () -> <div|e> () ) -> <div|e> ()
effect ask<a>
  fun ask() : a
```

```haskell
-- Unison
a ->{IO, Abort, State Nat} b
foo2 : Text ->{IO} Text ->{} ()
map : (a ->{e} b) -> [a] ->{e} [b]
```

```effekt
// Effekt
effect exc(msg: String): Nothing
def div(n: Double, m: Double): Double / { exc } = …
def myMap[A, B](xs: List[A]) { f: A => B }: List[B] = …
def parallel (f: () => Unit at {}, g: () => Unit at {}): Unit = <>
def helloWorld() at {io} = println("hello, world")
```

```scala
// Scala 3 capture checking
val out: File^ = new File("~/some/bits")
val lg: Logger^{out} = new Logger(out)
A ->{c₁, ..., cₙ} B   =  (A -> B)^{c₁, ..., cₙ}
class Source[X^]:
  def register(x: Listener^{X}): Unit = …
def apply[T](body: => T): Try[T]^{body.only[Control]} = ???
def runOnNewThread[T](body: () ->{any.except[Control]} T): T = ???
def f(x: Double): Double throws LimitExceeded = …
```

```flix
// Flix
def copyFile(src: File, dst: File): Unit \ {FsRead, FsWrite, IO} = ...
def map(f: a -> b \ ef, l: List[a]): List[b] \ ef = ...
def >>(f: a -> b \ ef1, g: b -> c \ ef2): a -> c \ (ef1 + ef2) = x -> g(f(x))
def recoverWith(f: Unit -> a \ Throw, h: ErrMsg -> a \ (ef - Throw)): a = ...
trait Dividable[t] { type Aef: Eff ; pub def div(x: t, y: t): t \ Dividable.Aef[t] }
@DefaultHandler
pub def runWithIO(f: Unit -> a \ ef): a \ (ef - Clock) + IO = ...
```

```roc
# Roc
pure_fn : Str, Str -> Str
run_fx! : Str, Str => Str
```

```ocaml
(* OCaml 5 — no tracking at all *)
type _ Effect.t += Xchg : int -> int Effect.t
(* val f : unit -> unit  — the effect is invisible *)
```

```rust
// Rust, proposed
#[maybe(async)]
pub fn copy<R, W>(reader: &mut R, writer: &mut W) -> io::Result<()>
where R: #[maybe(async)] Read, W: #[maybe(async)] Write;
copy::<async>(reader, writer).await?;
// Rust, shipped
const fn meow() {}
```

```granule
-- Granule (graded modality on the result type)
readFile : String -> String <{Open, Read, Close, IOExcept}>
map : forall {a : Type, b : Type, n : Nat} . (a -> b) [n] -> Vec n a -> Vec n b
```

```wit
// WASI / component model
world command {
  include imports;   // clocks, filesystem, sockets, random, io, env, exit, std*
  export run;
}
```

```haskell
-- Bluefin (value-level capabilities)
incrementReadLine ::
  (e1 <: es, e2 <: es, e3 <: es) =>
  Modify Int e1 -> Throw String e2 -> IOE e3 -> Eff es ()
-- handler shape
(forall e. Handle e -> Eff (e :& es) a) -> Eff es r
```

Verse (from documentation, not fetched verbatim): effect specifiers appear in angle
brackets after the parameter list, e.g. `F(x:int)<decides><transacts>:int`, with
`converges ⊂ computes ⊂ varies ⊂ transacts`, at most one *exclusive* specifier plus any
number of *additive* ones.

---

## 18. Annotated bibliography

### Koka / row-based effects
* Leijen, **Extensible Records with Scoped Labels**, TFP 2005 —
  <https://www.microsoft.com/en-us/research/publication/extensible-records-with-scoped-labels/>
  The origin of duplicate/scoped labels; the record system Koka's effect rows reuse.
* Leijen, **Koka: Programming with Row-polymorphic Effect Types**, MSFP 2014 —
  <https://www.microsoft.com/en-us/research/wp-content/uploads/2016/02/koka-effects-2013.pdf>
  ([arXiv:1406.2061](https://arxiv.org/abs/1406.2061)). The core paper: row syntax,
  duplicate labels, the `catch` typing argument, generalisation restricted to total
  expressions.
* Leijen, **Algebraic Effects for Functional Programming**, MSR-TR-2016-29 —
  <https://www.microsoft.com/en-us/research/wp-content/uploads/2016/08/algeff-tr-2016-v2.pdf>
  Adds handlers to Koka; handler typing and the type-directed compilation.
* Xie, Brachthäuser, Hillerström, Schuster, Leijen, **Effect Handlers, Evidently**,
  ICFP 2020 — <https://dl.acm.org/doi/abs/10.1145/3408981>. Evidence *translation*.
* Xie & Leijen, **Generalized Evidence Passing for Effect Handlers**, ICFP 2021 —
  <https://dl.acm.org/doi/10.1145/3473576>, TR:
  <https://www.microsoft.com/en-us/research/wp-content/uploads/2021/03/multip-tr-v2.pdf>
  **The most important implementation paper for Fern.** Evidence vectors, yield
  bubbling, monadic translation with no runtime support, tail-resumptive optimisation,
  bind-inlining, and the benchmark table quoted in §16.4.
* Xie & Leijen, **First-Class Names for Effect Handlers**, OOPSLA 2022 —
  <https://dl.acm.org/doi/10.1145/3563289>. Named/scoped handlers.
* Reinking, Xie, de Moura, Leijen, **Perceus: Garbage Free Reference Counting with
  Reuse**, PLDI 2021 —
  <https://www.microsoft.com/en-us/research/publication/perceus-garbage-free-reference-counting-with-reuse/>
  Fern's memory model; read alongside the evidence-passing paper for the interaction.
* Koka book / tour / spec — <https://github.com/koka-lang/koka/tree/master/doc/spec>,
  rendered at <https://koka-lang.github.io/koka/doc/book.html>.

### Capability-based effects
* Brachthäuser, Schuster, Ostermann, **Effects as Capabilities: Effect Handlers and
  Lightweight Effect Polymorphism**, OOPSLA 2020 —
  <https://dl.acm.org/doi/10.1145/3428194>. Effects-as-requirements, System Ξ,
  capability-passing style, contextual effect polymorphism, second-class blocks.
* Brachthäuser, Schuster, Lee, Boruch-Gruszecki, **Effects, Capabilities, and Boxes**,
  OOPSLA 2022 — <https://doi.org/10.1145/3527320>. Boxing, capture sets, how
  second-class computation becomes first-class safely.
* Müller, Schuster, Starup, Ostermann, Brachthäuser, **From Capabilities to Regions**,
  OOPSLA 2023 — <https://dl.acm.org/doi/10.1145/3622831>. Lift inference; how
  second-classness gives you the region structure a CPS backend needs.
* Schuster, Brachthäuser et al., **Lexical Effect Handlers, Directly**, OOPSLA 2024 —
  <https://dl.acm.org/doi/10.1145/3689770>.
* Muhcu, Schuster, Steuwer, Brachthäuser, **Multiple Resumptions and Local Mutable
  State, Directly**, ICFP 2025 — <https://dl.acm.org/doi/10.1145/3747529>.
  **Read this if Fern ever wants handlers**: garbage-free reference counting + stable
  prompts, O(1) one-shot capture, stack copying only for multi-shot, LLVM backend.
* Effekt docs & tour — <https://effekt-lang.org/docs>,
  <https://github.com/effekt-lang/effekt/tree/master/examples/tour>,
  <https://github.com/effekt-lang/effekt-website/tree/master/docs/concepts>.

### Scala capture checking
* Boruch-Gruszecki, Odersky, Lee, Lhoták, Brachthäuser, **Capturing Types**, TOPLAS
  2023 — <https://dl.acm.org/doi/10.1145/3618003>, <https://arxiv.org/abs/2105.11896>.
* Xu, Odersky et al., **What's in the Box: Ergonomic and Expressive Capture Tracking**,
  OOPSLA 2025 — <https://dl.acm.org/doi/10.1145/3763112>,
  <https://arxiv.org/abs/2509.07609>. System Capless, reach capabilities; the honest
  account of why boxes did not scale to the collections library.
* Odersky, Boruch-Gruszecki, Lee, Brachthäuser, Lhoták, **Safer Exceptions for Scala**,
  Scala Symposium 2021 — <https://infoscience.epfl.ch/record/290885>.
* Reference docs —
  <https://docs.scala-lang.org/scala3/reference/experimental/capture-checking/overview.html>
  (source: <https://github.com/scala/scala3/tree/main/docs/_docs/reference/experimental/capture-checking>).
* Practitioner: Rinaudo, *Hands on Capture Checking* —
  <https://nrinaudo.github.io/articles/capture_checking.html>;
  users.scala-lang.org, *Capture checking being confusing again* —
  <https://users.scala-lang.org/t/capture-checking-being-confusing-again/12083>.

### Flix
* Madsen & van de Pol, **Polymorphic Types and Effects with Boolean Unification**,
  OOPSLA 2020 — <https://dl.acm.org/doi/10.1145/3428222>.
* **With or Without You: Programming with Effect Exclusion**, ICFP 2023 —
  <https://dl.acm.org/doi/10.1145/3607846>.
* **Fast and Efficient Boolean Unification for Hindley-Milner-Style Type and Effect
  Systems**, OOPSLA 2023 — <https://dl.acm.org/doi/pdf/10.1145/3622816>. Read this if
  set-formula effects with complement are ever seriously considered — it exists because
  the naive algorithm is too slow.
* **Qualified Types with Boolean Algebras**, OOPSLA 2025 —
  <https://dl.acm.org/doi/full/10.1145/3763096>. System F_<:B / F_<:BE.
* **Peaceful Coexistence of Effects, Laziness, and Parallelism**, ECOOP 2023 —
  <https://drops.dagstuhl.de/storage/00lipics/lipics-vol263-ecoop2023/LIPIcs.ECOOP.2023.18/LIPIcs.ECOOP.2023.18.pdf>
  Purity reflection.
* Flix book — <https://doc.flix.dev/effect-system.html> (source:
  <https://github.com/flix/book/tree/master/src>). The `associated-effects.md`,
  `default-handlers.md` and `effect-polymorphism.md` chapters are the most directly
  reusable prose in this entire bibliography.

### Unison
* `docs/ability-typechecking.markdown` —
  <https://github.com/unisonweb/unison/blob/trunk/docs/ability-typechecking.markdown>
* Language reference, abilities and handlers —
  <https://www.unison-lang.org/docs/language-reference/abilities-and-ability-handlers/>
* unison#1173, *accidental inference of concrete abilities* —
  <https://github.com/unisonweb/unison/issues/1173>
* unison#822, *ability system crashes with higher-order abilities* —
  <https://github.com/unisonweb/unison/issues/822>
* SoftwareMill, *Trying out Unison, part 3: effects through abilities* —
  <https://softwaremill.com/trying-out-unison-part-3-effects-through-abilities/>
* atacratic, *Unison abilities — unofficial alternative tutorial* —
  <https://gist.github.com/atacratic/7a91901d5535391910a2d34a2636a93c>

### OCaml
* Sivaramakrishnan, Dolan, White, Kelly, Jaffer, Madhavapeddy, **Retrofitting Effect
  Handlers onto OCaml**, PLDI 2021 — <https://dl.acm.org/doi/10.1145/3453483.3454039>,
  <https://arxiv.org/abs/2104.00250>. §"we leave effect safety for future work" is the
  citation for the untyped decision.
* `ocaml-multicore/ocaml-effects-tutorial` —
  <https://github.com/ocaml-multicore/ocaml-effects-tutorial>
* discuss.ocaml.org, *What's the status of typed effects?* —
  <https://discuss.ocaml.org/t/whats-the-status-of-typed-effects/18439>
* **Gradual Typing for Effect Handlers** (GrEff) — <https://arxiv.org/abs/2304.02145>
* Jane Street, *Effective Programming: Adding an Effect System to OCaml* —
  <https://www.janestreet.com/tech-talks/effective-programming/>

### Roc
* `docs/langref/platforms.md` —
  <https://github.com/roc-lang/roc/blob/main/docs/langref/platforms.md>
* `docs/langref/functions.md` (purity inference, `->` vs `=>`, "no effect polymorphism
  by design") — <https://github.com/roc-lang/roc/blob/main/docs/langref/functions.md>

### Rust
* keyword-generics / effects initiative —
  <https://github.com/rust-lang/keyword-generics-initiative>,
  rendered at <https://rust-lang.github.io/keyword-generics-initiative/>
* Wuyts, *Extending Rust's Effect System* (RustConf 2023 transcript) —
  <https://blog.yoshuawuyts.com/extending-rusts-effect-system/>
* rust-lang/rust#132479, *Yeet the `effects` feature* —
  <https://github.com/rust-lang/rust/pull/132479>
* Const traits tracking issue #67792 — <https://github.com/rust-lang/rust/issues/67792>;
  project goal — <https://rust-lang.github.io/rust-project-goals/2024h2/const-traits.html>
* Wuyts, *Nesting Allocators* (capabilities vs the core/alloc/std split) —
  <https://blog.yoshuawuyts.com/nesting-allocators>
* RFC 1184 `no_std` — <https://rust-lang.github.io/rfcs/1184-stabilize-no_std.html>;
  RFC 2480 `liballoc` — <https://rust-lang.github.io/rfcs/2480-liballoc.html>

### WebAssembly
* WIT format — <https://github.com/WebAssembly/component-model/blob/main/design/mvp/WIT.md>
* `wasi:cli` world — <https://github.com/WebAssembly/wasi-cli/blob/main/wit/imports.wit>
* Phipps-Costin, Rossberg, Guha, Leijen, Hillerström, Sivaramakrishnan, Pretnar,
  Lindley, **Continuing WebAssembly with Effect Handlers**, OOPSLA 2023 —
  <https://dl.acm.org/doi/10.1145/3622814>. WasmFX; the reason WASI cannot host
  segmented-stack handlers today.
* stack-switching proposal — <https://github.com/WebAssembly/stack-switching>

### Haskell
* `effectful` README (the `mtl` critique, the `ReaderT`-pattern basis, the explicit
  decision to drop multi-shot continuations) —
  <https://github.com/haskell-effectful/effectful/blob/master/README.md>
* Bluefin (synthetic vs analytic effect systems; value-level capabilities) —
  <https://github.com/tomjaguarpaw/bluefin>,
  <https://hackage.haskell.org/package/bluefin/docs/Bluefin.html>
* King, *Effects for Less* (Zurihac 2020) — <https://www.youtube.com/watch?v=0jI-AlWEwYI>;
  *Unresolved challenges of scoped effects* — <https://www.twitch.tv/videos/1163853841>
* Snoyman, *The ReaderT Design Pattern* —
  <https://www.fpcomplete.com/blog/2017/06/readert-design-pattern/>;
  *A Tale of Two Brackets* — <https://academy.fpblock.com/blog/2017/06/tale-of-two-brackets/>
* van de Jeugt, *The Handle Pattern* —
  <https://jaspervdj.be/posts/2018-03-08-handle-pattern.html>
* Tweag, *Ad-hoc interpreters with `capability`* —
  <https://www.tweag.io/blog/2021-04-08-capabilities-ad-hoc-interpreters/>
* effects-benchmarks — <https://github.com/patrickt/effects-benchmarks>
* Gandhi, *On the purported benefits of effect systems* —
  <https://typesanitizer.com/blog/effects-convo.html>
  (discussion: <https://lobste.rs/s/qxmiqs/on_purported_benefits_effect_systems>)

### Foundations, other systems, and the frontier
* Lindley, McBride, McLaughlin, **Do Be Do Be Do** (Frank), POPL 2017 —
  <https://dl.acm.org/doi/10.1145/3009837.3009897>, <https://arxiv.org/abs/1611.09259>
* Bauer & Pretnar, **Programming with Algebraic Effects and Handlers** —
  <https://arxiv.org/abs/1203.1539>; **An Effect System for Algebraic Effects and
  Handlers**, LMCS 2014 — <https://arxiv.org/abs/1306.6316>
* Hillerström & Lindley, **Liberating Effects with Rows and Handlers** (Links) —
  <https://homepages.inf.ed.ac.uk/slindley/papers/links-effect.pdf>
* Biernacki, Piróg, Polesiuk, Sieczkowski, **Binders by Day, Labels by Night** (Helium),
  POPL 2020 — <https://dl.acm.org/doi/10.1145/3371116>
* Tang, White, Dolan, Hillerström, Lindley, Lorenzen, **Modal Effect Types**,
  OOPSLA 2025 — <https://dl.acm.org/doi/10.1145/3720476>, <https://arxiv.org/abs/2407.11816>
* Tang & Lindley, **Rows and Capabilities as Modal Effects**, POPL 2026 —
  <https://dl.acm.org/doi/10.1145/3776674>, <https://arxiv.org/abs/2507.10301>.
  **The single best "which family should I pick" reference**: it encodes both row-based
  and capability-based systems into one framework with semantics-preserving translations.
* Orchard, Liepelt, Eades, **Quantitative Program Reasoning with Graded Modal Types**,
  ICFP 2019 — <https://www.cs.kent.ac.uk/people/staff/dao7/publ/granule-icfp19.pdf>;
  Granule — <https://github.com/granule-project/granule>
* Brady, **Idris 2: Quantitative Type Theory in Practice**, ECOOP 2021 —
  <https://drops.dagstuhl.de/entities/document/10.4230/LIPIcs.ECOOP.2021.9>;
  `Control.App` — <https://idris2.readthedocs.io/en/latest/app/index.html>
* Melicher, Shi, Potanin, Aldrich, **A Capability-Based Module System for Authority
  Control** (Wyvern), ECOOP 2017 —
  <https://drops.dagstuhl.de/entities/document/10.4230/LIPIcs.ECOOP.2017.20>
* Craig, Potanin, Groves, Aldrich, **Capabilities: Effects for Free**, ICFEM 2018 —
  <https://link.springer.com/chapter/10.1007/978-3-030-02450-5_14>
* Gordon, **Designing with Static Capabilities and Effects: Use, Mention, and
  Invariants**, ECOOP 2020 — <https://arxiv.org/abs/2005.11444>
* Miller, Yee, Shapiro, **Capability Myths Demolished**, 2003 —
  <http://srl.cs.jhu.edu/pubs/SRL2003-02.pdf>
* Pony reference capabilities — <https://tutorial.ponylang.io/reference-capabilities/>
* **Affect: An Affine Type and Effect System**, POPL 2025 —
  <https://dl.acm.org/doi/10.1145/3704841>
* Verse: glossary — <https://dev.epicgames.com/documentation/fortnite/verse-glossary>;
  Book of Verse ch. 13 (Effects) — <https://verselang.github.io/book/13_effects/>
* Java: JEP 506 Scoped Values — <https://openjdk.org/jeps/506>; JEP 533 Structured
  Concurrency — <https://openjdk.org/jeps/533>
* Yallop, **effects bibliography** —
  <https://github.com/yallop/effects-bibliography> (the standing index for this field)

---

## 19. Open questions this survey could not settle

1. **Closure types and monomorphisation.** No source addresses what an effect row does
   to a monomorphising AOT compiler's specialisation strategy. Flix and Koka both
   compile to a VM/C where a closure is uniformly boxed. Fern needs to decide whether
   `fn() -> () ! {io}` and `fn() -> ()` are the same runtime representation (they should
   be — the row is erased) and whether they are the same *type* for the purposes of
   generic instantiation (they must not be, or the check is vacuous).
2. **Whether the row should include `alloc` and `panic`.** Rust's core/alloc/std split
   and Fern's freestanding-core doc both say yes; no effect-system paper models
   allocation as a tracked effect (Koka's `alloc<h>` is region allocation, not heap
   exhaustion). This is a genuinely open design question and the most Fern-specific one.
3. **Interaction with `comptime`.** Rust's `const` is an effect; Roc's purity is what
   licenses compile-time evaluation. Fern has `docs/COMPTIME-BRIEF.md`. Whether
   `comptime`-evaluability is the *same* lattice point as "total" or a separate
   (subset-direction) effect is unresolved by prior art — Rust says subset, Roc says
   the same bit.
4. **The bare-metal case.** `docs/BARE-METAL-PLAN.md` notes that an interrupt handler is
   a second context racing non-atomic refcounts. An effect row could in principle
   express "callable from an interrupt context" (no allocation, no blocking) — this is
   what Verse's `converges` and Rust's `const` are, structurally. No prior art covers
   effect rows for interrupt safety.
