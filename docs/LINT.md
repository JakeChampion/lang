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

### ambient-capability

Default: `warn`.

A handler's second parameter is its capability bag
(`handle(req: HttpRequest, plat: Platform)`, docs/PLATFORM-RESEARCH.md
Rec §1), and `std/platform` puts the host effects on it as methods. The free
functions still resolve inside a handler body — nothing in the type system
stops a bare `eprint` — so this rule reports the handler that reaches around
the bag it was handed, which is what breaks a mock platform in a test and
what a per-target capability set cannot see.

| Ambient call | Through the bag |
|---|---|
| `eprint(msg)` | `plat.log(msg)` |
| `now_unix_ms()` | `plat.now_ms()` |
| `monotonic_ns()` | `plat.elapsed_ns()` |
| `env(name)` | `plat.env(name)` |
| `random_i32()` | `plat.random_i32()` |

Only exact equivalents are listed: a suggestion that changes what the call
DOES — a different clock, a different stream — is worse than no suggestion,
so `print` (the stdout stream, which the proxy world does not have at all)
and `args` are the target gate's business, not this rule's.

The scope is the body of a `handle` whose second parameter is a `Platform`.
Working off the parse tree there is no call graph, so an effect one call
deeper — inside a helper, which is the shape whose fix is to pass the bag
along — is out of reach.

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
numbers per tree — the highest complexity any function reaches, and the total
distance over the limit — each with a **5% tolerance**:

- growth past the tolerance **fails**;
- a shrink past it **logs**, asking for the improvement to be banked;
- anything inside it is silent.

### Why a tolerance, and why the asymmetry

The first draft was exact in both directions. Measuring it against real main
traffic killed that: in one two-hour window main landed rc commits that moved
the ceiling 468 → 477 (+1.9%) and the excess 19847 → 19869 → 19878 (+0.11%,
then +0.05%), while a full CI run on a PR takes about three and a half hours.
An exact gate is therefore stale before it can land, and once landed would
red-light main for whoever pushed the next rc commit. A gate nobody can keep
fails the same way a limit nobody can meet does.

Five per cent absorbs that churn and still bites — a ceiling past 500 fails,
and enough new complexity to move a 20,000-point excess by 5% is a thousand
forks. The shape (checked-in baseline, tolerance, growth fatal, both
directions reported) is the one `scripts/ci-check-perf` and
`scripts/ci-check-driver-sizes` already use in this repo for the same reason.

A shrink never fails, because making an unrelated PR red for simplifying
something is how a gate gets deleted. A stale-low baseline only makes the
gate stricter, so it rots in the safe direction. Measured 2026-08-27:

| Tree | Ceiling | Total excess |
|---|---|---|
| `examples/self_host` | 477 (`lower_call_named`) | 19878 across 1035 functions |
| `internal/stdlib/std` | 68 (`_wb_break`) | 780 across 93 functions |

### Why summed distance, and not a count

Counting the functions over the limit is the obvious metric and the wrong
one. Splitting a 472-fork monster into ten readable 40-fork helpers takes
that count from **1 to 10** — so a count-based gate reports the single most
valuable refactor available as a regression, and blocks it.

Summed distance calls the same split what it is: **462 down to 300**. It
keeps falling as the pieces get simpler, all the way to zero, so it is
monotone in the direction the campaign actually moves.
`TestSplittingImprovesExcessButNotCount` pins that, and fails if anyone
switches the gate back to counting.

The ceiling covers what summed distance is weak at — it stops one new
monster — so the pair between them resists both failure modes.

Per-function exceptions go on the function, as an `allow` comment with a
line saying why. They do not go in that table: an exception is reviewable
where it applies and invisible in a list.

The two numbers treat an `allow` differently, on purpose. It removes a
function from the EXCESS — that is what the annotation asks for — but not
from the CEILING, because "this one may exceed the limit" is not "this one
may be worse than anything here has ever been". So a new 500-fork function
fails the gate however it is annotated.

The excess comes from `lint.File` rather than from `lint.Score` directly, so
an `allow` comment in a Fern source exempts a function from the gate exactly
as it does from the command line — one suppression mechanism, not a second
one only the gate honours. Each finding carries its score in `Finding.Value`,
so the gate compares numbers instead of parsing them back out of prose.

## Not here yet

- **Literate documents.** `fern -lint` refuses a `.fern.md` rather than
  parsing markdown as Fern. Wiring it up means routing findings through the
  generated-line → document-line remap, which `docs/LITERATE.md` names as
  the most regression-prone surface in that engine — worth its own change.
- **The self-host mirror.** The linter is native-only tooling, like
  `internal/printer`; nothing in the bootstrap path needs it. See
  `docs/NATIVE-CONVERGENCE.md` for when that stops being free.
- **`--fix`.** `ambient-capability` is the first finding with a mechanical
  rewrite (`eprint(x)` → `plat.log(x)`, plus the `std/platform` import when
  it is missing), which is what a fixer would need building for.
