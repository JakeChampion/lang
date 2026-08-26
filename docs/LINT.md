# The Fern linter

`fern -lint` reports code that compiles and still costs the next reader more
than it should. It is not the checker: the checker rejects programs that
cannot run, a lint names a program that runs badly. That split decides
everything else about the design.

Engine: `internal/lint`. CLI: `cmd/fern/lint.go`.

## Why it runs on the parse tree

A lint sees `parser.Parse` output and nothing else — no type-check, no
import resolution, no constant folding.

That buys three things worth more than type information would be:

- **A broken file still lints.** Type errors and lint findings are usually
  present in the same draft, and a linter that goes quiet exactly when the
  code is at its messiest is a linter nobody runs.
- **A tree costs one parse per file.** `fern -lint examples/self_host`
  reads 94 modules in about a second. A `-check`-shaped linter would resolve
  the whole import graph per entry point.
- **The desugars have happened, the lowerings have not.** `if let`, `let
  else`, `assert`, `todo` and range-`for` are already the shapes the
  compiler works in, so a rule matches one node kind rather than two
  spellings of it. But the *type-directed* lowerings — `for … in` over an
  array, over a stream — have NOT run, so a rule still sees the loop the
  author wrote.

A rule that genuinely needs types does not belong here; it belongs in the
checker, as a warning.

## Rules

A rule implements `lint.Rule`: a stable kebab-case `Name()`, a one-line
`Doc()`, a `DefaultSeverity()`, and `Check(*Pass)`. `register()` in the
rule file's `init` adds a constructor to the registry. `Rules()` hands back
a FRESH instance per call, because `SetOption` mutates the rule value.

Add options by also implementing `lint.Configurable` — `SetOption(key,
value)` and `Options()`. An unknown key or an unusable value must error:
linting at a default the user did not ask for is worse than refusing.

`Pass.Report` handles suppression, so no rule knows suppression exists.

### cyclomatic-complexity

Default: `warn`, `max = 10` (McCabe's own recommendation).

`lint.Score` is one for the entry path plus one per fork. The scoring model
is the whole content of the metric, so `complexity.go` writes it down as one
table and `TestScoreModel` pins every entry — a traversal change that
quietly starts or stops counting one of them moves every number in the repo
gate below.

| Construct | Adds |
|---|---|
| `if`, if-expression | 1 per condition (an `else if` nests a second `If`, so a chain costs one per test) |
| `while`, C-style `for`, `for … in` | 1 |
| `loop` | 1 — an unconditional loop is still a back edge |
| `match` arm | 1 per arm that can fail to match; `_` is the fall-through, not a test |
| `when` guard | 1 — a second condition after the pattern |
| `&&`, `\|\|` | 1 each — short-circuiting is a branch |
| `?` | 1 — an early return on the error path |
| `assert(…)` | 0 — a precondition, not a second path, and `-O` deletes it |
| `todo` | 0 — its `loop { … }` is a stub marker |
| `break`, `continue`, `return` | 0 — the fork that created the path already counted |

A lambda's body counts INTO the function that spells it. An inline closure
is code the reader walks past, so hiding an `if` in one must not make the
enclosing function read as simpler than it is.

## Configuration

Severity is `allow` (the rule does not run), `warn` (prints, exit 0), or
`deny` (prints, exit 1). Innermost wins:

1. the rule's `DefaultSeverity()`
2. the `[lint]` table of the `fern.toml` governing the first target
3. `-lint-set RULE=SEVERITY` on the command line

```toml
[lint]
cyclomatic-complexity = "deny"

[lint.options]
cyclomatic-complexity.max = 25
```

`internal/manifest` collects those tables as raw `key = value` pairs and
validates the value's SPELLING only — which rules and options exist is
`internal/lint`'s to know, and a manifest parser that had to import the
linter would drag it into every build that reads a dependency's manifest.
`lint.Config.SetPair` does the validating, and rejects a name no rule
answers to: a typo would otherwise silently configure nothing.

## Suppression

```fern
// fern-lint: allow cyclomatic-complexity
// dispatch_op is a flat table; splitting it would only hide the shape.
function dispatch_op(op: Op): i32 { … }
```

A directive alone on its line covers the next line carrying code — skipping
blank lines and further comments, so an `allow` may sit above a doc comment
rather than wedged between the doc and what it documents. A directive
trailing code covers the line it sits on. `allow-file` covers the file.

A directive naming a rule that does not exist silences nothing, and silence
is what the author asked for, so nothing else would ever surface the typo:
it is reported under the reserved rule name `lint-directive`. That name is
not configurable — a warning about your own comment is not something to
switch off.

## The gate on this repository's own Fern sources

`internal/lint/repo_gate_test.go`, in the ordinary `go test ./...` lane.

The self-host compiler and the stdlib are held to `DefaultMaxComplexity`
via a RATCHET rather than a hard limit, because 1128 of their 7724 functions
are over it today and a limit nobody can meet is a limit nobody keeps. Two
numbers per tree — the highest complexity any function reaches, and how many
exceed the limit — and the test fails when either MOVES:

- **up**, because a change made the tree worse. Split the new function, or
  annotate it.
- **down**, because a change made it better and the recorded number now
  claims less than the tree has earned. Bank it in the same diff.

So the gate is real from the day it lands — nothing may regress — and
cannot quietly go slack. Measured 2026-08-26:

| Tree | Ceiling | Over the limit |
|---|---|---|
| `examples/self_host` | 472 (`lower_call_method`, a 1752-line function) | 1035 of 5601 |
| `internal/stdlib/std` | 68 (`_wb_break`) | 93 of 2123 |

Per-function exceptions go on the function, as an `allow` comment with a
line saying why. They do not go in that table: an exception is reviewable
where it applies and invisible in a list.

The two numbers treat an `allow` differently, on purpose. It removes a
function from the BUDGET — that is what the annotation asks for — but not
from the CEILING, because "this one may exceed the limit" is not "this one
may be worse than anything here has ever been". So a new 500-fork function
fails the gate however it is annotated.

The count comes from `lint.File` rather than from `lint.Score` directly, so
an `allow` comment in a Fern source exempts a function from the gate exactly
as it does from the command line — one suppression mechanism, not a second
one only the gate honours.

## Not here yet

- **Literate documents.** `fern -lint` refuses a `.fern.md` rather than
  parsing markdown as Fern. Wiring it up means routing findings through the
  generated-line → document-line remap, which `docs/LITERATE.md` names as
  the most regression-prone surface in that engine — worth its own change.
- **The self-host mirror.** The linter is native-only tooling, like
  `internal/printer`; nothing in the bootstrap path needs it. See
  `docs/NATIVE-CONVERGENCE.md` for when that stops being free.
- **`--fix`.** Nothing the one rule reports has a mechanical fix.
