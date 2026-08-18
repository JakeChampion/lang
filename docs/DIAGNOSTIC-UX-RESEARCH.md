# Diagnostic UX research — making errors a feature

`internal/diag/diag.go` already does the right things: composable
error interfaces (`Positioned`, `Spanned`, `Hinted`, `Filed`),
a single rendering point (`Format`), Levenshtein-based did-you-
mean suggestions, source-line + caret rendering with span
underlines, multi-error aggregation via `Errors`. That's well
above the "stderr.println(stack.toString())" floor most
languages start at.

The question this doc asks: **what would it take to get from
"competent" to "best-in-class"** — Elm-grade, Rust-grade,
where the compiler's diagnostic output is *itself* a teaching
tool. The sources for that bar are Elm (the original), Rust
(the broadest deployment), ReasonML (the deliberate redesign),
and TypeScript (the most-improved-over-time).

Best-in-class isn't a single feature. It's:

- The error tells the user **what's wrong** in plain language.
- It points at **where in the source** the problem is, with
  ranges, not just positions.
- It distinguishes the **primary location** (where the error
  was detected) from **secondary locations** (declarations,
  origin sites, conflicting uses).
- It offers a **suggested fix** when one exists, machine-
  applicable for the IDE to apply on click.
- It uses **colour, indentation, and whitespace** to guide
  the eye, but degrades gracefully on `NO_COLOR` / narrow
  terminals.
- It avoids **cascading noise**: one root cause shouldn't
  produce 30 follow-on errors.

The codebase nails ~3 of these. This doc surveys the rest.

## Framing — why diagnostic UX matters now

Two reasons specific to this codebase:

1. **Single-user language → diagnostics are the primary
   feedback loop.** With no Stack Overflow, no Discord, no
   second user to ask, the compiler is the only thing that
   teaches the user about their own language. Every minute
   spent on diagnostic UX saves minutes of bafflement later.

2. **fernsmith fuzzer produces weird programs**
   (`IMPROVEMENTS.md ▸ fernsmith`). The error-rendering path
   sees every shape the type system can reject. Improving
   diagnostic clarity directly improves the differential-
   oracle workflow (compare *what the diagnostics say*, not
   just *whether the program errored*).

## What we already do well — call out so we don't drift

- **Errors collected, not first-fail.** Parser and checker
  both return `diag.Errors` aggregating every error in one
  pass. (See `LSP-INTEGRATION-PLAN.md ▸ Why this is tractable`.)
- **Composable error interfaces.** `Positioned + Spanned +
  Hinted + Filed`. Each error opts into whatever metadata it
  has. New interfaces compose without forcing existing errors
  to change shape. Rust's `Diagnostic` trait does roughly
  the same job; ours is cleaner because it's small.
- **Single rendering point.** `diag.Format(filename, src,
  err)` is *the* renderer. Adding features (colour, multi-
  label, suggested fixes) lands in one place.
- **Did-you-mean via Levenshtein.** `diag.Suggest(name,
  candidates)` does the standard distance-1 / distance-2
  suggestion. Good baseline.
- **Source-line context + caret rendering.** The output
  shape (`path:line:col: error: msg \n source-line \n ^~~~~`)
  matches what every modern compiler converges on.

## Single-source deep dives

### Elm — the language that put diagnostics on the map

Sources:
- https://elm-lang.org/news/compiler-errors-for-humans
- Evan Czaplicki's "The Hard Parts of Open Source" + "What is
  Success?" talks.
- The Elm 0.16 → 0.18 error redesign git log.

**Elm 0.16 was the watershed.** Before, Elm's errors were
type-theoretic ("could not match `Maybe Int` with `String`").
After, they were teacherly:

```
-- TYPE MISMATCH ---------------------------------------- src/Main.elm

The 2nd argument to `map` is not what I expect:

42|     map negate "hello"
                  ^^^^^^^
This argument is a string of type:

    String

But `map` needs the 2nd argument to be:

    List a

Hint: I always figure out the argument types from left to right. If an
earlier argument is invalid, it may be giving me bad information about
this later argument. So focusing on the first error might be the
trick!
```

**What Elm did differently:**

- **No jargon.** "I expect" instead of "expected"; "needs to
  be" instead of "must unify with."
- **Error category as a heading** (`-- TYPE MISMATCH ---…`).
  Skimmable; scrolling through 30 errors, the user can find
  the relevant one.
- **Specific quantification** ("the 2nd argument to `map`").
  Generic compilers say "this argument"; Elm names it.
- **Inline source with caret.** Same as `diag.Format` today.
- **"Hint" as a separate, longer block.** Not "did you mean
  X" — actual teacher-shaped advice. The hint about left-to-
  right inference is a *teaching moment*, not an error.

**Elm's stance: errors are documentation.** Each common error
shape gets a hand-tuned message. The compiler is a domain-
specific text-emitter as much as a type checker.

**What translates:**

- **Hand-tuned error messages for the top-N most common
  errors.** Generic `type mismatch: expected X, got Y` is
  the floor. For the 10-20 errors users actually hit
  often, hand-tuned messages with category headings and
  "I expect" phrasing.
- **Category headings.** `-- TYPE MISMATCH ---…` style. A
  single banner per error, so scanning N errors is
  tractable. Probably overkill at our scale (small handler
  programs), but useful framing.
- **Hints separated from messages.** Today's `Hint()` is
  one-line. Allow multi-line hints. Use them for "why this
  error happened" prose, not for "try X instead" (which is
  a *suggested fix*, see Rec §3).

**Considered, left:**

- *Anthropomorphic phrasing ("I think you meant…").* Elm
  uses it; it's a style call. The codebase's current style
  is more clinical ("undefined identifier `x`"; "did you
  mean `y`?"). Either works; just be consistent.

### Rust — the broadest-deployed best-in-class diagnostics

Sources:
- https://doc.rust-lang.org/error-index.html
- https://github.com/rust-lang/annotate-snippets-rs
- https://blog.rust-lang.org/2016/08/10/Rust-1.11.html
  (the multi-line error introduction).
- "How to write good error messages" — Rust contributing docs.

**Rust's diagnostic system has *many* layers stacked atop
each other. The transferable bits, layer by layer:**

#### 1. Multi-label diagnostics (primary + N secondary)

```
error[E0308]: mismatched types
  --> src/main.rs:5:9
   |
 3 |     let x: i32 = 1.0;
   |            ---   ^^^ expected `i32`, found floating-point
   |            |
   |            expected due to this
```

The **primary label** (`^^^ expected i32, found floating-
point`) points at the offending expression. The **secondary
label** (`--- expected due to this`) points at the
annotation that *caused* the expectation. The user sees both
sites and the relationship between them.

**Why this is load-bearing.** Half the time, the bug isn't
*at* the position the compiler detected the error. It's at
the position that set up the expectation. The annotation is
on line 3; the bug is on line 5; without the secondary
label, the user doesn't know to look at line 3.

#### 2. Error codes + `--explain`

Each error has a stable code (E0308). Running `rustc
--explain E0308` prints a paragraph of explanation with
worked examples. The code is also a search anchor —
"E0308 mismatched types" matches Stack Overflow / blog
posts.

#### 3. Machine-applicable suggestions

```
help: change the type of the numeric literal from `1.0` to `1`
  |
 3 |     let x: i32 = 1;
   |                  ~
```

`rustc` knows what the *fix* is, not just what's wrong. The
suggestion is structured (a `(span, replacement-text)` pair),
which the IDE picks up via LSP `CodeAction` and applies on
click.

#### 4. Tower of related notes

```
error[E0277]: the trait `Display` is not implemented for `Foo`
   |
17 |     println!("{}", x);
   |                    ^ `Foo` cannot be formatted with the default formatter
   = help: the trait `Display` is not implemented for `Foo`
   = note: in format strings you may be able to use `{:?}` instead
note: required by a bound in `core::fmt::Display`
   |
```

When the immediate error has a *story* (this trait isn't
implemented because of that bound because of this other
constraint), Rust prints the whole chain. Truncated by
default, full chain on `--verbose`.

#### 5. Suppression of cascading errors

Once Rust commits to a type assumption to recover from an
error, it suppresses downstream errors that depend on the
recovery. The user sees the *root cause*, not 30 secondary
errors.

**What translates:**

- **Multi-label diagnostics.** Today's `Positioned + Spanned`
  carries one location per error. Extend to a *list* of
  `(span, message, kind)` where kind ∈ {Primary, Secondary,
  Help}. Renderer interleaves source lines + labels.
  Substantial change to the renderer; small change to error
  *construction* (an error opts into multi-label by adding
  a `Labels() []Label` method).

- **Error codes + `fern explain CODE`.** Each error has a
  stable identifier (e.g. `E001` for "undefined identifier",
  `E002` for "type mismatch"). `fern explain E002` prints a
  paragraph with worked examples. The catalogue lives in
  `internal/diag/codes/E002.md` (one Markdown file per
  code), auto-linked from rendered errors.

- **Machine-applicable suggestions, structured.** Today's
  `Hint() string` is freeform text. A structured `Suggestion
  { Span, Replacement, Description }` lets the LSP render
  it as a `CodeAction` the user clicks to apply. Today's
  did-you-mean (`"did you mean y?"`) becomes a structured
  suggestion (`replace span x..x+1 with y`).

- **Cascading-error suppression.** When a type error is
  detected, downstream uses of the same variable get a
  reduced-severity follow-up rather than full error noise.
  Implementation: the checker carries a per-scope "tainted
  symbols" set; errors that reference a tainted symbol use
  a quieter rendering.

**Considered, left:**

- *Full trait-bound chain rendering.* We don't have
  traits or trait bounds (yet). The pattern would apply
  to "this enum variant is wrong because the match was
  expected to be of type X due to Y" — a much smaller
  cone of cases.

- *`#[deny]` / `#[warn]` lint-severity attributes.* Rust's
  rich lint-severity story is a multi-year build-out;
  for our scale, errors-as-errors and warnings-as-warnings
  is enough.

### ReasonML — the deliberate redesign that prioritised UX

Sources:
- Sharon Tahvildary's blog posts on Reason's error redesign
  (~2018-2019).
- https://reasonml.github.io/docs/en/syntax-cheatsheet
- The bsb / bs-platform error-rendering source.

**Reason's situation rhymes with this codebase's.** They had
OCaml's compiler underneath (excellent semantics, terrible
default error messages), wanted to ship to a broader audience,
and treated error UX as the highest-leverage product feature.

**What Reason changed:**

- **Translated OCaml type-theory language into beginner-
  friendly phrasing.** `This pattern matches values of
  type 'option Foo.t' but is here used to match values of
  type 'list (option Foo.t)'` became `Looks like this
  pattern only matches option, but the expression is a
  list. Did you mean to wrap the match in a List.map?`.
- **Stripped path-prefixes from type names where they were
  noise.** `Foo.Bar.Baz.Quux.t` became `Quux.t` unless
  multiple `t`s were in scope, in which case the minimal
  disambiguating prefix appeared.
- **Specific suggestions for common mistakes.** Missing
  semicolon, missing `;` at end of statement, JS-style
  `if (x)` instead of OCaml's `if x`. Each gets a
  hand-tuned message.

**The deeper lesson: error messages are *user-facing copy*.**
Treat them like product copy — review, iterate, A/B with
real users. A senior engineer should write the top-100
messages by hand.

**What translates:**

- **Strip noise from type names.** Today's checker reports
  types as their fully-qualified form. Render short names
  unless ambiguous in scope. `module::SubModule::Foo` →
  `Foo` if no other `Foo` is visible.

- **Targeted messages for common-typo errors.** Missing `;`
  (already a parser concern), unbalanced `{}`, `if (x)`
  vs `if x` (we use the former; this case doesn't apply,
  but the pattern does for other paren / brace mismatches).

- **Treat the top-N errors as product copy.** Catalogue
  them; iterate the messages by hand. The fernsmith
  fuzzer surfaces the *shapes* of errors users will hit;
  the top occurrences by frequency are where to invest.

### TypeScript — most-improved over a decade

Sources:
- https://github.com/microsoft/TypeScript/blob/main/src/compiler/diagnosticMessages.json
- Daniel Rosenwasser's posts on each major release's
  diagnostic improvements.
- The `error related information` LSP feature, introduced
  by TS.

**TypeScript's error messages were *legendarily bad* in
2015 and now are competitive with Rust.** Worth studying as
a progression — what they added, in what order:

1. **Position + range + message** (2015 baseline).
2. **Related information** — the "X is declared here" /
   "expected to be Y because of this" auxiliary locations
   surfaced as LSP `DiagnosticRelatedInformation`. Roughly
   Rust's secondary labels.
3. **"Codefixes"** — machine-applicable suggestions. Rust's
   suggestions equivalent, exposed in LSP `CodeAction`.
4. **Quickinfo** — hover-time signature display, related to
   errors via shared type-display code.
5. **Targeted phrasing for common shapes** — array-vs-tuple
   mismatch, missing-property, excess-property. Each error
   shape gets its own pre-rendered template.
6. **Error stacks** — for cascading generics, the chain of
   "X is not assignable to Y because A is not assignable
   to B because…" with truncation + `--diagnostics`
   verbose mode.

**What translates:**

- **LSP `relatedInformation`** mapping from
  multi-label diagnostics. Once Rec §1 lands, the LSP can
  expose secondary labels as related info; clients
  render them as clickable cross-references.

- **`CodeAction` for machine-applicable suggestions.**
  Same as Rust's; goes hand-in-hand with structured
  suggestions (Rec §3 below).

- **Targeted phrasing per error shape.** Same lesson as
  Reason. The codebase's checker already emits typed
  errors (`*Error` struct with Pos + Msg + Note); the
  phrasing of each shape is hand-written. Make a list,
  iterate.

### Hare — minimal, position-anchored, single-message

Source:
- https://harelang.org/
- Hare's tree at `src/compiler/`.

**Hare is at the other end of the spectrum.** Errors are
strict, position-anchored, one-line:

```
foo.ha:5:9: expected identifier, found '1'
```

No source line. No caret. No related info. No suggestions.
The user gets a position and a message; they navigate to
the position in their editor.

**Why this works for Hare:** the language is small, the
target audience is hackers comfortable with terse tooling,
and the error rate per program is low because the type
system is conservative.

**Whether this works for this codebase:** mostly *no*. The
audience is one user iterating on language features; the
error rate is high during development. But there's a
useful lesson: **the "diagnostic UX is everything" framing
isn't universal**. Some audiences want the terse form.
Worth a `--terse` / `-q` flag for batch tooling that just
wants positions + messages.

**What translates:**

- *Terse mode for batch / scripting use.* `fern -q` or
  `FERN_DIAG_FORMAT=terse` emits the Hare-shape (one line
  per error, no source context, no colour). Useful for
  `grep`-driven workflows.

### codespan-reporting + ariadne (Rust libraries)

Sources:
- https://github.com/brendanzab/codespan
- https://github.com/zesterer/ariadne
- "Beautiful diagnostics with Ariadne" blog post.

**These are *libraries* implementing the multi-label-
diagnostic rendering. Worth studying because they encode
the visual design decisions in stable structured form.**

Key visual decisions (both libraries agree):

- **Source lines are numbered in a left gutter.** Right-
  aligned, padded to width of largest line number in view.
- **Vertical pipes (`|`) connect labels to source.** When
  a single span covers multiple lines, the pipe shows
  the extent.
- **Labels use unicode box-drawing for visual hierarchy.**
  `┌`, `│`, `└`, `═`. Degrades to ASCII (`/`, `|`, `\`,
  `=`) on terminals that don't render unicode.
- **Colour is by *severity*, not by element type.** Errors
  red, warnings yellow, notes blue, help green. Same
  scheme across all rendering.
- **Suggestions get a contrasting block.** Often inverted
  colour or a distinct background.

**Ariadne specifically** introduces multi-line label
rendering that's still readable when spans cross 5+
lines:

```
   ┌─ src/main.fern:5:9
   │
 3 │     var x: i32 = 1.0;
   │            ───   ─── expected `i32`, found floating-point
   │            │
   │            expected due to this annotation
 4 │
 5 │     foo(x);
   │     ──────── error use site (see line 3)
   │
```

**What translates:**

- **Adopt the multi-label visual vocabulary.** Don't
  rewrite from scratch; copy ariadne's or codespan's
  spec for box-drawing characters, gutter, colour
  defaults. We're not going to do better than they
  already did.

- **Unicode default with ASCII fallback.** Detect
  terminal capability (`LANG=C.UTF-8` etc.) or env var
  override; render box-drawing when supported, ASCII
  otherwise.

- **Colour is opt-out, not opt-in.** Default on for
  TTY output; off for piped output. `NO_COLOR=1` env
  forces off; `--color=always` forces on. Standard.

### Idris — typed-presentation of errors

Source:
- Edwin Brady's "Idris 2: Quantitative Type Theory in
  Practice" + the Idris 2 compiler source.

**Idris ships errors as structured trees, not strings.**
Each error has a typed representation; the renderer is
a tree-walker. Editor integration can manipulate the
tree directly — collapse a sub-error, expand it, jump to
its source.

**Why this matters: errors-as-data.** Today's `diag.Error`
implements `error` (string-shaped). An LSP that wants to
show a collapsible structured view of "this error has 3
sub-errors that you can expand" needs the underlying
structure, not a pre-rendered string.

**What translates:**

- **Structured error type.** Today: each concrete error
  is a struct (e.g. `checker.Error{Pos, Msg, Note}`).
  Generalise to:

  ```
  type Diagnostic struct {
      Code    string
      Severity Severity
      Primary Label
      Secondary []Label
      Helps   []Help
      Cause   *Diagnostic   // sub-error chain
  }
  type Label struct { Span Span; Message string }
  type Help struct {
      Message string
      Suggestion *Suggestion  // optional fix
  }
  ```

  The renderer (`diag.Format`) walks this; the LSP layer
  also walks this. No string-parsing on either side.

## Cross-cutting themes

1. **Multi-label is universal in best-in-class.** Elm,
   Rust, TypeScript, ariadne, codespan, Idris. The single-
   primary-position shape is the floor; secondary labels
   are the next step up.

2. **Suggestions should be machine-applicable, not freeform
   prose.** Rust's `Suggestion`, TypeScript's `CodeAction`.
   Structured `(span, replacement-text)` lets the IDE
   apply the fix on click. Freeform "did you mean to
   write X?" requires the user to manually edit.

3. **Error catalogue + `--explain` for the top errors.**
   Rust, Elm, Roc. Stable codes, long-form explanation,
   worked examples. Search anchors as a side benefit.

4. **Targeted phrasing for the top-N error shapes.** Reason,
   TypeScript, Elm. Generic "type mismatch" is the floor;
   "this list was expected to have items of type X but
   got Y" is the bar.

5. **Suppress cascading errors.** Rust, Roc, OCaml. When a
   single bug produces 30 follow-on errors, the user
   sees 30 messages instead of 1; root-cause identification
   becomes harder, not easier.

6. **Colour by severity, opt-out by env var.** Universal.

7. **Box-drawing characters with ASCII fallback.** Universal
   in modern renderers.

8. **Errors-as-data, rendered late.** The compiler builds
   structured diagnostics; the renderer turns them into
   text. The LSP renders them via its own pathway. Two
   consumers, one source of truth.

## Concrete recommendations

Ranked by leverage × cost.

### 1. Multi-label diagnostics (primary + N secondary)

**Cost: 1 week.** **Impact: very high; gates Rec §4.**

Extend the `Diagnostic` shape to carry multiple labels:

```
type Label struct {
    Span     Span
    Message  string
    Kind     LabelKind  // Primary, Secondary, Help
}

type Diagnostic interface {
    Labels() []Label
    // existing Positioned / Spanned / Filed satisfied as well
}
```

Migrate `checker.Error` to satisfy `Labels()`. The renderer
handles {1, 2, 3+}-label cases; existing single-label
errors are unaffected (return `[{primary}]`).

Concrete example use: "type mismatch" gets two labels —
primary at the use site, secondary at the declaration
that set the expectation.

### 2. Structured `Diagnostic` type as the source of truth

**Cost: 1 week.** **Impact: gates Rec §4, §5, §8.**

Promote `Diagnostic` from a freeform `error`-interface bag
to a concrete struct:

```
type Diagnostic struct {
    Code      string
    Severity  Severity   // Error, Warning, Note
    Labels    []Label
    Helps     []Help
    File      string
}
type Help struct {
    Message    string
    Suggestion *Suggestion  // optional structured fix
}
type Suggestion struct {
    Span        Span
    Replacement string
    Title       string  // shown in IDE code-action menu
}
```

Existing concrete error types (`parser.Error`,
`checker.Error`, etc.) are still allowed to satisfy
`error`; an adapter converts them to `Diagnostic`. The
renderer and the LSP both consume `Diagnostic`. Resist
the temptation to have the renderer parse text.

### 3. Machine-applicable suggestions

**Cost: 1 week after Rec §2.** **Impact: high — turns
diagnostics into auto-fixes in the IDE.**

Today's `Hint() string` becomes `Suggestion`. Structured
shape `{Span, Replacement, Title}`. LSP surfaces them as
`CodeAction`; the user clicks "apply suggestion" and
the edit is applied.

Concretely: today's "did you mean `y`?" becomes a
suggestion of `{span: <x>, replacement: "y", title:
"Replace `x` with `y`"}`. The IDE shows a yellow
lightbulb; click → fixed.

Other obvious cases:

- Missing `import "std/foo"` when a stdlib function is
  used → suggestion to add the import line at the top
  of the file.
- `if (x)` vs `if x` style preference (if we ever go
  that way).
- Misspelled variant → suggested correct variant.

Each one is a few-line check at the error site; pays off
the first time the LSP applies a fix.

### 4. Error catalogue + `fern explain <code>`

**Cost: 2 weeks (initial catalogue; ongoing thereafter).**
**Impact: medium-high. Search anchors + teaching
resource.**

Assign stable codes (e.g. `E001` for "undefined
identifier", `E002` for "type mismatch", …). Each
diagnostic carries its code.

`fern explain E002` reads `internal/diag/codes/E002.md`
and prints it. Markdown files contain:

- A paragraph explaining when the error occurs.
- A short broken example.
- The fixed version.
- A "see also" pointing at related codes.

Initial catalogue: pick the 30 most common errors (per
checker source + fernsmith fuzzer corpus). Iterate the
copy as users (well, *user*) hits each error.

### 5. Targeted phrasing for the top-30 error shapes

**Cost: ongoing, ~1 hour per phrase.** **Impact: high,
compounds.**

Audit `internal/checker/checker.go`'s error-construction
sites. For each, ask:

- Does the message tell the user *what's wrong* in plain
  English?
- Does it tell them *why* (the expectation site)?
- Does it suggest *what to do* (when applicable)?

Iterate the phrasing of the top 30 by hit-frequency. The
fernsmith corpus gives an objective frequency count.

This is *editorial work*, not engineering. A single pass
yields disproportionate UX wins.

### 6. Cascading-error suppression

**Cost: 2 weeks.** **Impact: medium-high; quality-of-life
during fast iteration.**

When the checker detects an error involving symbol `s`,
record `s` in a per-scope `tainted` set. Subsequent
errors that reference a tainted symbol are downgraded:
either suppressed entirely, or rendered as a brief
"(see error above)" line rather than a full error.

The danger is false-negatives: don't hide errors that
have a different root cause but happen to involve a
tainted symbol. Conservative implementation: only
suppress when the immediate error's primary span is
on the same line as the previous error's, *and* the
symbol is the same.

### 7. Color + box-drawing rendering

**Cost: 1 week.** **Impact: medium — visible-from-day-one
quality signal.**

Adopt ariadne / codespan's visual vocabulary:

- Gutter with right-aligned line numbers.
- Box-drawing characters for label connectors
  (`┌`, `│`, `└`).
- Colour by severity (red error, yellow warning,
  blue note, green help).
- Suggestions get an inverted-colour block.

Detect TTY (`isatty(stderr)`) for colour; `NO_COLOR=1` /
`--color=never` overrides. Detect UTF-8 (`LANG=…UTF-8`)
for box-drawing; `--ascii` overrides.

### 8. LSP `relatedInformation` + `CodeAction` wiring

**Cost: 1 week after Rec §2, §3.** **Impact: ties the
above into the LSP world.**

Once diagnostics are structured (Rec §2), the LSP layer
maps:

- `Diagnostic.Labels[1..]` → LSP `relatedInformation`.
  IDEs render them as clickable cross-references.
- `Diagnostic.Helps[*].Suggestion` → LSP `CodeAction`.
  IDEs render them as lightbulb / quick-fix entries.
- `Diagnostic.Code` → LSP `code` + `codeDescription.href`
  pointing at the explain output (or a doc URL).

Three small mappings, big visible-in-editor payoff.

### 9. Terse mode for batch / scripting

**Cost: 1 day.** **Impact: low; nice-to-have.**

`fern -q` / `FERN_DIAG_FORMAT=terse` outputs one line per
error, no source context, no colour:

```
src/main.fern:5:9: E002 type mismatch: expected i32, found string
```

For `grep`-driven workflows, CI summary jobs. Cheap to
add.

### 10. Diagnostic regression tests + golden files

**Cost: 1 week.** **Impact: medium; protects diagnostic
quality long-term.**

Right now the checker tests assert that an error
*occurs*. They don't assert *how it's rendered*. Once
diagnostics are user-facing copy (Rec §5), regressions
in phrasing become user-facing regressions.

Add a `internal/diag/golden_test.go` that:

- Runs each fixture program through the compiler.
- Captures the rendered diagnostic (text form).
- Compares against a checked-in `*.golden` file.
- On `-update`, refreshes the golden file.

The fernsmith corpus is a natural input source — top-N
errors from a fuzz run, frozen as golden fixtures.

## Anti-patterns — explicit "do not adopt"

- **Throwing the entire AST / type-graph at the user
  in an error.** Some compilers (early TypeScript was
  this) dump "expected `(x: T) => U & V | W` got `…`"
  with full type expansion. Hide internal-machinery
  unless `--verbose`.

- **Localising error messages prematurely.** Single-user
  language; English-only is fine. Localising before the
  message catalogue stabilises is a maintenance trap.

- **Anthropomorphic phrasing inconsistently used.**
  Elm uses "I" throughout; the codebase uses clinical
  voice ("undefined identifier"). Pick one. The
  clinical voice is more *informationally dense*; the
  anthropomorphic is more *learner-friendly*. Either
  is fine — *mixing* is what to avoid.

- **Suggesting fixes that aren't actually applicable
  to the source.** A suggestion's `replacement` should
  produce a syntactically valid program when applied;
  if not, the suggestion is *misinformation* worse
  than no suggestion. Test golden suggestions for
  apply-and-re-parse soundness. This one has bitten
  twice and is now gated three ways (#6990, #7018):
  `internal/checker/derive_hint_test.go` COMPILES the
  spelling each hint suggests (and, for a type from
  another module, pins that the routes it withholds
  are ones the checker really refuses),
  `internal/sourcelint/diag_suggestion_spelling_test.go`
  makes a bare trait name unrepresentable in either
  compiler's sources, and
  `internal/e2eselfhost/self_host_checker_hint_text_test.go`
  compares the advice the two compilers give.

- **Verbose-by-default error chains.** Rust's full
  trait-bound chains can run 30 lines. Default-collapsed,
  expand on `--verbose` or in the IDE on click. Don't
  drown the user.

- **Box-drawing without UTF-8 detection.** Renders as
  garbage on incapable terminals. Detect; fall back to
  ASCII. (Codespan and ariadne do this; just copy.)

- **Colour by element type.** Some compilers colour
  type names blue, function names green, identifiers
  purple. Inconsistent across messages; muddles the
  severity signal. Colour by *severity* (red error,
  yellow warning, blue note) — universal.

## When to revisit

- **When fernsmith catches its first diagnostic
  regression** (different error text for the same shape
  across a refactor). That's the signal Rec §10 (golden
  fixtures) is overdue.

- **When a user hits the same confusing error twice in
  a row** during development. Hand-tune that specific
  error (Rec §5). Each one is ~1 hour of work and pays
  back many times.

- **When the LSP rendering quality starts visibly
  trailing the CLI rendering quality** — that's the
  signal Rec §8 (LSP relatedInformation/CodeAction
  wiring) is overdue.

The single highest-leverage *cheap* recommendation is
**Rec §5 (targeted phrasing for the top-30 errors)**.
Editorial work, no engineering, immediate UX win.
Rec §1 (multi-label) + §2 (structured Diagnostic) +
§3 (suggestions) are the *architectural foundation* that
unlocks the rest — once they land, every subsequent
diagnostic upgrade lands in one place.
