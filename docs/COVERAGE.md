# Coverage (`-cover` / `FERN_COVER=1`)

Status: [reference] `-cover` and `-cover-report` as shipped.

Fern's test culture is strong and its coverage story was nothing at all:
nothing said which lines a suite exercised, so "is this tested?" was answered
by reading. `-cover` answers it with a number.

```sh
fern -target x86-64-linux -cover -o prog prog.fern
./prog 2> cov.txt          # the report shares stderr with the program
fern -cover-report cov.txt # per-file line + branch totals, uncovered lists
fern -cover-report -lcov cov.txt > lcov.info
```

The flag is read at **compile** time — it changes the code the backend emits —
so it goes on the build, not on the run. `FERN_COVER=1` is the same switch for
a driver you don't invoke directly.

## What it measures

Two things, from one flag.

**Line (statement) coverage** — how many times each executable source line
ran. The unit is a **line**, not a statement: several statements on one line
share one counter. Where one line holds several basic blocks — `if (c) { a(); }
else { b(); }` all on one line — each block bumps the shared counter, so the
hit count sums the blocks the way gcov's does.

**Branch coverage** — for each conditional the program wrote (`if`, `while`,
`&&`, `||`), how often it was evaluated and how often it went the true way.
This is the thing line coverage structurally cannot state:

```fern
while (i > 99) { i = i + 1; }   // the line runs. the body never does.
if (n > 10) { return 1; }        // no `else` exists to report as uncovered.
if (a > 0 && b > 0) { … }        // both operands share one line.
```

In each case the line reports a hit and the report still says an edge was
never taken.

Only the conditionals the **author wrote** are counted. The lowering opens far
more — drop glue, bounds checks, rc tests — and counting those would report
branches nobody can cover and make the denominator move with unrelated codegen
changes. `match` arms are not branch sites either; each arm body is on its own
line, so line coverage already answers which ran.

## The report

An instrumented binary writes its whole counter table to stderr as it exits,
one line per instrumented source line, **hit or not**:

```
fern-cover: /path/to/prog.fern:12 3
fern-cover: /path/to/prog.fern:13 0
fern-branch: /path/to/prog.fern:12:5 E 4
fern-branch: /path/to/prog.fern:12:5 T 3
```

A branch is **two** counters, not one per arm: `E` counts evaluations of the
conditional and `T` counts the ones that went true, so the false edge is
`E − T`. Fern's structured control flow has no `else` arm to hang a third
counter on when the source wrote none, and synthesising one to hold a counter
would have meant the instrumentation changing the shape of the code it
measures. Subtraction costs nothing and cannot.

The column is part of a branch's identity — `12:5` and `12:19` are the `if` and
the `&&` on one line. Without it they would share counters and the report could
not say which arm was missed. An `&&` / `||` is placed at its operator; an
`if` / `while` at the start of its condition, since the compiler's `if` and
`while` nodes carry no column.

Both exit seams report — falling off `main` and the `exit()` builtin — so a
CLI that ends in an explicit `exit` measures the same as one that returns.

Reporting the zeros is the point: the rows present are the DENOMINATOR
`fern -cover-report` divides by. A row that went missing would shrink the
total rather than show up as uncovered, which is the one mistake a coverage
number must never make quietly. For the same reason `-cover-report` errors on
a prefixed row it cannot parse instead of skipping it.

Rows without the `fern-cover: ` prefix are the program's own stderr and are
ignored, so piping the whole stream is fine. Rows repeat-and-sum, so several
runs' output can be concatenated into one measurement.

## Scoring functions by change risk

```sh
fern -target x86-64-linux -cover -o prog src/main.fern
./prog 2> cov.txt
fern -lint -lint-cover cov.txt src/
```

The lint reads the same saved run and scores every function by
`complexity^2 * (1 - coverage)^3 + complexity`, the change-risk lint rule:
the complex functions the run never exercised. The run names each file as
the compile was given it, and the lint matches those names to the files it
walks, so compile and lint from the same directory. `docs/LINT.md` has the
rule; the stream here is its input unchanged.

## What a `-cover` build costs

- **One increment per executable line reached, and two per conditional
  evaluated.** Each is a call to `__fern_cov_hit`, which allocates the
  counter table on the first hit.
- **One `.rodata` string and one 8-byte counter per instrumented line, and two
  of each per conditional.**
- **Every function the program declares has its rows.** A function nothing
  calls is the most useful thing a coverage report has to say. The compiler
  instruments before its tree shake, so the rows of a function the shake
  drops stay in the table and report 0.

It is a measurement build, not a shipping one. With the flag **off**, the
emitted asm is byte-identical to a build from a compiler without the feature:
the pass does not run.

## Where it works

x86-64 Linux and arm64 Linux. Any other
target **errors** rather than emitting an uninstrumented binary: a coverage run
that silently measures zero is worse than one that refuses. So does `-interp`,
which lowers nothing.

## How it is built

It is a source pass, `compiler/cover.fern`, which
runs after every gate and before the tree shake:

- Each statement that opens a new source line in its list is preceded by
  `__fern_cov_hit(k)`. `if (c)` and `while (c)` become `hit(E) && c` with
  `hit(T)` opening the body; `a && b` becomes `(hit(E) && a) && (hit(T) && b)`,
  and `a || b` becomes `(hit(E) && a && hit(T)) || b`.
- `exit(…)` statements are preceded by `__fern_cov_report()`, and `main` is
  renamed and wrapped by one that reports when it returns.
- The helpers are generated Fern: the counter table hangs off one `.bss` word
  reached through the `__raw_cover()` intrinsic, and the report's row texts
  are one string literal, a line each, so the compiler instrumenting itself
  (about 180k counters) still compiles one string rather than an array.

## Coverage-guided fuzzing of the self-host compiler

Every `go test -fuzz` target in the repo steers by Go's coverage
instrumentation, which cannot see inside a Fern binary. So the self-host
compiler, the only compiler, had no coverage-guided fuzzing at all.

`-cover` closes that, and the feedback needs nothing new: one input is one
compiler process, so the report the binary already writes at exit *is* that
input's coverage. The instrumented compiler is built by the self-host's own
`-cover`.

```sh
FERN_SELFHOST_COVER_FUZZ=1 FERN_SELFHOST_COVER_FUZZ_TIME=5m   go test -run '^TestSelfHostCoverageGuidedFuzz$' ./internal/testing/e2e/
```

The loop is AFL-shaped: mutate a corpus entry, let fernsmith turn the bytes
into a program, run the instrumented compiler, and keep the entry when it
reached a counter nothing had reached before. `FERN_SELFHOST_COVER_FUZZ_CORPUS`
points at a directory the corpus is loaded from and saved back to, which is
what makes the nightly lane compound instead of restarting from six seed bytes
every night.

Counter **ordinals** are the identity, not the site text: the table is baked in
at compile time, so the Nth row of one run is the Nth of every run, and the hit
set is a bitset over row positions. At ~180k rows per iteration that is the
difference between a fuzzer and a parser.

Cost is ~500 ms an iteration — a process spawn, a front-end run, and ~10 MB of
counter rows. Four orders of magnitude above the in-process Go targets, which
is why it is a nightly lane (`nightly-fuzz.yml`) and not a per-PR one.

`TestSelfHostCoverageFeedbackBeatsBlindMutation` is the property that keeps it
honest: two arms, same seeds, same iteration count, same RNG stream, differing
only in whether a discovery joins the corpus. Feedback must reach strictly more
counters. Without it the lane would keep reporting a plausible number long
after the steering stopped working.

## Known gaps

- **`match` arm coverage** — not branch coverage, and not built. Arm bodies sit
  on their own lines, so line coverage answers which arms ran; a guard on an
  arm is a conditional the report does not currently see.
- **wasm** — no instrumentation, so `-cover` errors on the wasm targets.
- **arm64 Darwin and Android** — the pass is target-neutral,
  but only the two Linux targets are tested, so the others refuse.
- **Literate sources** — a `.fern.md` reports against the tangled `.fern`
  line numbers, not the document's. The remap exists (`internal/tools/literate`) but
  is not wired to the report.
