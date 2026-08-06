# The Fern specification

Status: normative, and partial.

| File | What it defines |
| --- | --- |
| `grammar.ebnf` | The syntactic grammar. Gated by `internal/grammar`. |
| `diagnostics.md` | The index of every rejection the front-end can report, and which conformance case pins it. Gated by `TestDiagnosticsIndexIsAccurate`. |

See `docs/SPECIFICATION-RESEARCH.md` for the staged shape these are
layers of, and §"What is not specified" below for what a reader must
not mistake this directory for.

Before this file existed, the only description of Fern's syntax was
`internal/parser/parser.go` — 5.9k lines of hand-written recursive
descent. That is a description of *a* parser, not of the language, and
it cannot be read, cited in a bug report, or implemented against.

## The two grammars

Following Go and ECMA-262, the lexical and syntactic grammars are
separate. `grammar.ebnf` is the **syntactic** grammar and its terminals
are tokens, not characters. The **lexical** grammar is
`internal/lexer`, which produces exactly eight token kinds:

| Kind | Written in `grammar.ebnf` as |
| --- | --- |
| `Ident` | `IDENT`, or a quoted literal where a name is contextual |
| `Number` | `NUMBER` — decimal or `0x` hex, optional `i32`/`i64`/`u8`/`u32`/`u64` suffix |
| `Float` | `FLOAT` — optional `f32`/`f64` suffix |
| `String` | `STRING` |
| `FString` | `FSTRING` — the lexer splits the body into literal and interpolant parts |
| `Keyword` | a quoted literal, e.g. `'function'` |
| `Punct` | a quoted literal, e.g. `'|>'` |
| `EOF` | not written; consumed implicitly at the end of `Program` |

A quoted literal matches a token of any of the name-shaped kinds with
that exact text. That is what lets the grammar spell **contextual
keywords** — `own`, `borrow`, `fip`, `fbip`, `async`, `Self`, `str`,
`char`, `float` are ordinary identifiers to the lexer and only mean
something in the positions where the grammar names them. `own` is the
sharpest case: it is a parameter modifier in `f(own xs: string[])` and
an ordinary parameter name in `f(rl: i32, own: string[])`, and both
occur in the self-host compiler's own sources.

## It is a PEG, deliberately

Alternation is **ordered** — the first branch that matches wins — and
repetition is **greedy and possessive**. This is not a shortcut. The
artefact being described is a recursive-descent parser: `|` is a chain
of `if p.match(…)`, and `{ … }` is a `for` loop over `p.accept(…)`. A
context-free grammar would describe a parser nobody wrote, and its
ambiguities would have to be resolved by prose that no test could check.

Two consequences when editing:

- `{ X } X` can never match. The repetition takes every `X` and does not
  give one back.
- A choice that succeeds is not retried when the *enclosing* sequence
  later fails. `PostfixOp` spells out `'[' Expr ']'` and
  `'[' TypeArgList ']'` as separate alternatives, each closing its own
  bracket, precisely because sharing one `'[' … ']'` around an inner
  choice made `arr[i - 1]` commit to reading `i` as a type argument and
  then fail on the `-`.

## The grammar is a superset

`grammar.ebnf` derives some programs the parser rejects. This is
intentional: syntax and static semantics are different layers, and
folding the second into the first is what makes a specification
unreadable. The known cases:

| The grammar derives | The parser rejects it as |
| --- | --- |
| `1 = 2` — any expression as an assignment target | `P003`, left-hand side is not assignable |
| A match arm with no separating comma after a struct-pattern arm | `P001` — the arm body's `{` and the pattern's `{` are ambiguous, so the comma is required there |
| `'string' '.' IDENT` in any expression, including where no such module is imported | `E001`, unknown module |

The differential gate below therefore runs in **one direction only**:
everything the parser accepts, the grammar must derive.

## Normative prose that lives in `docs/`

Several rules were written down as policy notes before this directory
existed, and they are normative — a conforming implementation must
honour them. They are listed here rather than moved, because they are
maintained as living documents and a copy would drift:

| Doc | Rule |
| --- | --- |
| `docs/INTEGER-SEMANTICS.md` | Integer operations are portable and never trap. `+ - * <<` wrap at the operand's width on every backend. |
| `docs/FLOAT-SEMANTICS.md` | IEEE 754 for ordinary arithmetic, with the edges **deliberately** under-specified — the NaN bit-pattern in particular. The model for how to write down a freedom. |
| `docs/ARRAY-BOUNDS.md` | Bounds-checking semantics. |
| `docs/CLOSURE-CAPTURE.md` | Capture-by-value, enforced as `E049`. |
| `docs/MODE-LATTICE.md` | The `own` / borrow / view mode lattice. |
| `docs/MUST-CONSUME.md` | `@must_consume` linear obligations, enforced as `E067`. |

Unlike `grammar.ebnf` and `diagnostics.md`, **none of these has a gate
tying its prose to a test**. Each states rules that the conformance
corpus happens to exercise, but nothing checks that a claim in the prose
is pinned by a case, or that a case has not quietly contradicted one.
Closing that is the natural next increment, and it is the same shape as
the diagnostics index: derive the truth, make the document match it.

## What is not specified

Everything else — and the gap is larger than what is here. There is no
statement anywhere of what a Fern program *means*: no evaluation order,
no reference-counting or ownership semantics beyond the mode-lattice
sketch, no typing rules, no memory model. `conformance/cases` pins
behaviour by example, which is a description of the current
implementations' behaviour rather than a definition of the language's.

A reader should not mistake this directory for a language definition. It
defines what a Fern program looks like and which programs are rejected,
and almost nothing about what an accepted one does.

## How this is kept true

A grammar nobody checks is fiction within a month, so this one is not
prose. `internal/grammar` reads `grammar.ebnf` and gates it three ways;
the whole suite runs in ~5 seconds.

1. **Well-formedness** — no rule is left-recursive (such a rule silently
   never matches), and every rule is reachable from `Program` (an
   unreachable rule reads as normative and describes nothing).

2. **Derivation** — every `.fern` source in the repository that
   `internal/parser` accepts, the grammar must derive: the conformance
   corpus, the examples, the stdlib, and the self-host compiler's own
   sources, which at 7000+ lines a file are the most adversarial input
   available. Currently **736 of 736**. There is deliberately no
   known-gaps list: if the grammar stops deriving something, the answer
   is to fix the grammar.

3. **Rule coverage** — every rule must be exercised by some real
   program. This is the check that matters most for a *normative*
   document, and it is the one that is easy to leave out. The first
   draft of this grammar passed 731/731 on derivation while containing
   four productions that were invented rather than extracted:

   - `race { … }` and `concurrent { … }` — a **retired** surface.
     `internal/stdlib/std/async.fern` records that the keyword form was
     replaced by plain library functions over `Future[T]`. The parser
     rejects both.
   - `resource Foo { … }` — assembled from a keyword in the parser's
     top-level dispatch. No spelling of it parses.
   - `use x = e;` — a guess. The real form is `use x <- e;`.
   - a bare `x => e` lambda — the parameter list is always
     parenthesized. This one was not merely inert: it made
     `when x == y => x + y` read `y => x + y` as a lambda, so the match
     arm lost its arrow.

   Derivation cannot see any of these, because nothing uses them. In a
   normative document that is the worst failure mode available: an
   invented rule reads exactly like a true one. Coverage is what
   distinguishes a description of Fern from a guess about Fern.

Where coverage found a rule that was real but untested, the corpus grew
rather than the grammar shrinking — `pub use` re-exports, struct
patterns, and the `use` bind each had **zero** conformance coverage
before this, and now have a case each.

## Changing the grammar

Edit `grammar.ebnf` and run `go test ./internal/grammar/`. If a
construct fails to derive, the failure names the file and prints the
tokens around the point the grammar could not get past, which is
normally where the missing production goes.

Add a case to `TestGrammarDerivesConstruct` for anything subtle — every
entry there is a construct the first draft got wrong, reduced to one
line. Its cases assert that `internal/parser` accepts the snippet first,
so a snippet cannot pin the grammar to something outside the language.
