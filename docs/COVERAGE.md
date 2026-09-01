# Line coverage (`-cover` / `FERN_COVER=1`)

Fern's test culture is strong and its coverage story was nothing at all:
nothing said which lines a suite exercised, so "is this tested?" was answered
by reading. `-cover` answers it with a number.

```sh
fern -target x86-64-linux -cover -o prog prog.fern
./prog 2> cov.txt          # the report shares stderr with the program
fern -cover-report cov.txt # per-file totals + the uncovered lines
fern -cover-report -lcov cov.txt > lcov.info
```

The flag is read at **compile** time — it changes the code the backend emits —
so it goes on the build, not on the run. `FERN_COVER=1` is the same switch for
a driver you don't invoke directly.

## What it measures

Line (statement) coverage: how many times each executable source line ran.
Not branch coverage — a `if (a && b)` line reports as covered when the line
ran, whatever `b` did. That is the second slice of #5548 and is not built.

The unit is a **line**, not a statement: several statements on one line share
one counter. Where one line holds several basic blocks — `if (c) { a(); } else
{ b(); }` all on one line — each block bumps the shared counter, so the hit
count sums the blocks the way gcov's does.

## The report

An instrumented binary writes its whole counter table to stderr as it exits,
one line per instrumented source line, **hit or not**:

```
fern-cover: /path/to/prog.fern:12 3
fern-cover: /path/to/prog.fern:13 0
```

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

- **One increment per executable line reached.** No call, no allocation: an
  `inc` against a fixed `.bss` slot on x86-64, an `ldr`/`add`/`str` triple on
  arm64.
- **One `.rodata` string and one 8-byte counter per instrumented line.**
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
  `(file, line)` table as `Program.CoverSites`. The op is `— → —`, modelled on
  `OpLine` — no stack effect, no pass has to understand it.
- The counter is keyed by `(file, line)`. `ast.Position` carries only a line,
  so the file half comes from `FuncDecl.SourceFile`, which `modload` stamps and
  never clears — unlike `SourceModule`, which is blanked on flat-loaded stdlib
  decls to keep them universally visible.
- Each backend emits a `.bss` counter array, a `.rodata` (pointer, length)
  table of pre-baked `fern-cover: <file>:<line> ` literals, and
  `__fern_cov_report`, a loop over the two called from both exit seams.
- `ast.CoverLinePrefix` is the one spelling of the wire format, shared by both
  natives and the reader.

## Known gaps

- **Branch coverage** — slice 3 of #5548. Both edges of each conditional need
  their own counter; the line counter cannot distinguish them.
- **Coverage-guided fuzzing** — slice 4. `internal/fernsmith` generates random
  programs blind; feeding it the live edge-hit set turns it into a
  coverage-maximising fuzzer.
- **wasm** — no instrumentation, so `-cover` errors on the wasm targets.
- **Literate sources** — a `.fern.md` reports against the tangled `.fern`
  line numbers, not the document's. The remap exists (`internal/literate`) but
  is not wired to the report.
