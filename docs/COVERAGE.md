# Coverage (`-cover` / `FERN_COVER=1`)

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
not say which arm was missed.

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

## What a `-cover` build costs

- **One increment per executable line reached, and two per conditional
  evaluated.** No call, no allocation: an `inc` against a fixed `.bss` slot on
  x86-64, an `ldr`/`add`/`str` triple on arm64.
- **One `.rodata` string and one 8-byte counter per instrumented line, and two
  of each per conditional.**
- **Every function the program declares stays in the binary's lowering.** A
  function nothing calls is the most useful thing a coverage report has to
  say, so the AST tree-shake is skipped under `-cover` and the sites are
  registered before the post-lowering dead-function cull drops the code
  again. The consequence: a `-cover` build compiles code an ordinary build
  never lowers.

It is a measurement build, not a shipping one. With the flag **off**, the
emitted asm is byte-identical to a build from a compiler without the feature —
no coverage op is lowered, no symbol is emitted, and the self-host's
byte-identical fixpoint never sees one.

## Where it works

x86-64 Linux and arm64 (Linux, Darwin, Android) — the natives, matching
`-sanitize`'s reach. Any other target **errors** rather than emitting an
uninstrumented binary: a coverage run that silently measures zero is worse
than one that refuses.

## How it is built

One IR pass, so a backend gets it by wiring the emit rather than by
reimplementing the analysis:

- `ir.CoverPoints()` makes the builder emit an `OpCoverPoint` at each
  statement that opens a new source line in its basic block, and publishes the
  counter table as `Program.CoverSites`. The op is `— → —`, modelled on
  `OpLine` — no stack effect, no pass has to understand it.
- **Branch counters reuse that same op and the same table.** A branch is two
  more entries with a different `CoverKind`, so adding branch coverage needed
  no backend change at all: the emit already knew how to bump counter `i` and
  print row `i`. `CoverSite.ReportLine` renders a row's fixed text and lives in
  `internal/ir`, so the two natives cannot drift on the wire format.
- The eval counter is emitted **before** the condition is lowered, where the
  operand stack is in a known state and no comparison is waiting for its
  branch; the true counter goes at the top of the arm, past the conditional
  jump. So a conditional whose condition diverges (a call that exits) counts as
  evaluated — the block-entry convention gcov uses.
- The counter is keyed by `(file, line)`. `ast.Position` carries only a line,
  so the file half comes from `FuncDecl.SourceFile`, which `modload` stamps and
  never clears — unlike `SourceModule`, which is blanked on flat-loaded stdlib
  decls to keep them universally visible.
- Each backend emits a `.bss` counter array, a `.rodata` (pointer, length)
  table of pre-baked `fern-cover: <file>:<line> ` literals, and
  `__fern_cov_report`, a loop over the two called from both exit seams.
- `ast.CoverLinePrefix` is the one spelling of the wire format, shared by both
  natives and the reader.

## Coverage-guided fuzzing of the self-host compiler

Every `go test -fuzz` target in the repo steers by Go's coverage
instrumentation, which cannot see inside a Fern binary. So the self-host
compiler — the implementation `NATIVE-CONVERGENCE.md` makes the definition
once the freeze lands — had no coverage-guided fuzzing at all.

`-cover` closes that, and the feedback needs nothing new: one input is one
compiler process, so the report the binary already writes at exit *is* that
input's coverage.

```sh
FERN_SELFHOST_COVER_FUZZ=1 FERN_SELFHOST_COVER_FUZZ_TIME=5m   go test -run '^TestSelfHostCoverageGuidedFuzz$' ./internal/e2e/
```

The loop is AFL-shaped: mutate a corpus entry, let fernsmith turn the bytes
into a program, run the instrumented compiler, and keep the entry when it
reached a counter nothing had reached before. `FERN_SELFHOST_COVER_FUZZ_CORPUS`
points at a directory the corpus is loaded from and saved back to, which is
what makes the nightly lane compound instead of restarting from six seed bytes
every night.

Counter **ordinals** are the identity, not the site text: the table is baked in
at compile time, so the Nth row of one run is the Nth of every run, and the hit
set is a bitset over row positions. At ~148k rows per iteration that is the
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
- **Literate sources** — a `.fern.md` reports against the tangled `.fern`
  line numbers, not the document's. The remap exists (`internal/literate`) but
  is not wired to the report.
