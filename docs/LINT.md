# The Fern linter

Status: [reference] shipped.

`fern -lint` reports code that compiles but is hard for the next reader. It
is not the checker. The checker rejects programs that cannot run, and a lint
names a program that runs but is badly made. That split decides
everything else about the design.

Engine: `compiler/lint.fern`. CLI: `run_lint` in `compiler/fern.fern`.

A literate `.fern.md` document is linted as the modules it tangles to, and
each finding is reported at its line in the document (`lint_module`). A
chunk library, a document with no root chunk and no `file=` blocks, has no
module to lint: a directory walk skips it, and naming it is an error.

The findings and the summary line go to stderr, as the diagnostics of the
checker do. `-q`, or `FERN_DIAG_FORMAT=terse`, prints one line for each
finding, without the source line and the help.

## Why it runs on the parse tree

A lint sees `parser.parse_module` output and nothing else — no type-check,
no import resolution, no constant folding.

That gives three things that are worth more than type information:

- **A broken file still lints.** Type errors and lint findings are usually
  present in the same draft, and a linter that goes quiet exactly when the
  code is at its messiest is a linter nobody runs.
- **A tree costs one parse per file.** `fern -lint compiler` reads the
  1,264 files of the tree in about three seconds. A linter in the shape of
  `-check` resolves the whole import graph for each entry point.
- **The desugars ran, and the lowerings did not.** `if let`, `let else`,
  `assert`, `todo` and range-`for` are already in the shapes that the
  compiler uses. Thus a rule matches one node kind, not two spellings of it.
  But the *type-directed* lowerings, such as `for … in` over an array or a
  stream, did NOT run. Thus a rule sees the loop that the author wrote.

A rule that needs types does not belong here. It belongs in the checker, as
a warning.

## Rules

A rule has a stable kebab-case name, a one-line description, a default
severity and, optionally, `<rule>.<key>` options. There are three rules.
`lint.Config` holds the severity and the threshold of each rule.

A new rule
adds its fields there, an arm to `set_pair` / `set_option`, its run to
`lint_file`, and its entry to `rules_listing` and `known_rules`. An unknown key
or an unusable value must give an error. A lint at a default that the user
did not ask for is worse than a refusal.

`lint_file` applies the `fern-lint:` suppressions, so no rule knows they
exist.

### cyclomatic-complexity

Default: `warn`, `max = 10` (McCabe's own recommendation).

`lint.score` is one for the entry path plus one for each fork. The scoring
model is the whole content of the metric, so `TestSelfHostLintScoreModel` pins
each entry below. A change to the traversal that starts or stops the count of
one of them moves each number in the repository gate.

The parser desugars some forms before the linter sees them. The score is of
the source, not of the desugar:

- A tuple, struct, literal or nested pattern match scores the arms as the
  author wrote them (the `sugar` that the printer prints again).
- A C-style `for` scores the loop that `printer.as_c_for` recovers.
- A `todo` scores nothing.

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
| `todo` | 0 — a stub marker |
| `break`, `continue`, `return` | 0 — the fork that created the path already counted |

The body of a lambda counts INTO the function that contains it. The reader
reads an inline closure as a part of that function. Thus an `if` in a closure
must not make the function look simpler than it is.

### change-risk

Default: `warn`, `max = 30`. Runs only when `-lint-cover FILE` names a saved
`-cover` run (`docs/COVERAGE.md`): the stderr of an instrumented binary,
several runs' output concatenated if the suite is several programs.

The score is CRAP1 (Savoia and Evans, 2007, "Change Risk Anti-Patterns"):

```
complexity^2 * (1 - coverage)^3 + complexity
```

with `complexity` the cyclomatic score above and `coverage` the share of the
function's instrumented lines the run hit. The two inputs pull in opposite
directions on purpose. A complex function under test scores about its
complexity. A simple function with no test also scores about its complexity.
Only complex code that no test runs climbs.

Thirty is the threshold of Savoia. The
formula is a fit to the judgements of developers, not a derivation. Thus the
number is a convention, not a measurement.

The lines of a function go from its first line to `span_end`. That is the
last line on which a statement or an expression of its body starts. The
closing brace can be lower, but no instrumented line can be lower. Thus the
span needs no end marker from the parser. The rows of a lambda count into the
function that contains it, as its forks do. Coverage is by LINE, not branch edge: every statement has a
row, so the denominator is dense and stable, where edges exist only for
functions with conditionals.

The run names a file as the compile got it, and the lint names it as the walk
found it. Thus a file of the run matches a linted file when the names are the
same. It also matches when one name is the other with leading directories
added or removed.
The longest such name wins. `risk_score` calculates the score in integers,
rounded half up, so it is the same on each backend.

Five things are deliberately NOT a quiet pass. The rule skips a function that
has no row in the run, because the run did not instrument it. An example is
a function in a file outside the covered program. But each of these is a usage error
(exit 2):

- a `-lint-cover` file with no rows
- a `-lint-cover` file with a row that does not parse
- a `-lint-cover` file that cannot be read
- a `-lint-cover` file that names none of the linted files
- a configuration of the rule, by `[lint]`, `-lint-set` or `-lint-opt`, with
  no run

A rule set to `allow` scores nothing, so none of the five applies to it. The
ratchet test guards against a gate that can pass when its measurement is
missing, and this rule holds itself to the same bar.
The repository ratchet does not include this rule: it needs a run of every
suite, which the gate's one-parse-per-file cost cannot afford.

### modern-form

Default: `allow`. No options.

The rule keeps the modern form that #14246 and #14280 gave to coreutils, the
examples and the stdlib (#14288). It finds three things:

- A `let` annotation that the initializer already gives. The parse tree has
  no types, so the rule sees only an initializer whose syntax gives its type:
  a string, a boolean, a number (`i32`, `f64` or the type of its suffix), a
  struct literal, or a cast. `let w: u64 = 0;` stays, because the annotation
  changes the type of the literal.
- `x = x op e`, for `x op= e`, when the target is a name.
- `0 - e`, for `-e`. `0 - 1` stays: in an unsigned type, `-1` does not fit.

`scripts/check-ratchets` holds the three trees: a change must not add a
finding that the tree of its base commit does not have. The Lint lane and the
pre-push hook run it. No count is pinned, so a change that removes findings
edits nothing.

### Why the gate compares with the base commit

The first gate compared each tree with two pinned numbers. Main moved those
numbers many times a day. Two pull requests that each added complexity and
each pinned the same new number passed alone, and git merged the equal edits
with no conflict. Main then failed for the next person.

`TestSelfHostRepoComplexityDelta` has no pinned number. `scripts/check-ratchets`
gives it the tree of the base commit, and it measures that tree and the
working tree with one build of the linter. One change can raise the total
excess of a tree by 1% of the excess of the base, and not less than 10. The
worst function of a tree can be 5% above the worst function of the base. A
decrease passes and needs no edit.

### Why summed distance, and not a count

The count of the functions over the limit is the obvious metric, and it is
the wrong one. A split of one 472-fork function into ten readable 40-fork
helpers takes that count from **1 to 10**. Thus a gate on the count reports
the most valuable refactor as a regression, and blocks it.

Summed distance calls the same split what it is: **462 down to 300**. It
keeps falling as the pieces get simpler, all the way to zero, so it is
monotone in the direction the campaign actually moves.
`TestSelfHostLintSplittingImprovesExcess` pins that, and fails if anyone
switches the gate back to counting.

The ceiling covers what summed distance is weak at — it stops one new
monster — so the pair between them resists both failure modes.

Per-function exceptions go on the function, as an `allow` comment with a
line saying why. An exception is reviewable where it applies and invisible in
a list.

The two numbers treat an `allow` differently, on purpose. It removes a
function from the EXCESS, because that is what the annotation asks for. It
does not remove the function from the CEILING. "This function can go above
the limit" is not "this function can be worse than any function before it".
Thus a new 500-fork function fails the gate, whatever its annotation.

The gate is `TestSelfHostRepoComplexityDelta`, which runs
`compiler/drivers/lint_ratchet_run.fern` over each tree of the base commit and
of the change. Its excess comes from `lint.lint_file`, not directly from
`lint.score`. Thus an `allow` comment in a Fern source exempts a function from
the gate in the same way as from the command line. There is one suppression
mechanism, not a second one that only the gate obeys. Each finding carries its score in `Finding.value`, so the gate
compares numbers instead of parsing them back out of prose.
