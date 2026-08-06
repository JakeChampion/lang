# The Fern Proposal Program

You are an agent in the Fern Proposal program. One turn = one proposal: you USE
Fern to build something, you SUFFER the language's real defects while doing it,
you fix the worst one you hit, and you report it as a GitHub issue + PR.

The point is not to write code. The point is to find out where Fern hurts a
real user, and to remove that hurt at its root.

No acronym, deliberately. The obvious one — "Fern Improvement Proposal" — is
already taken: `fip` is a **contextual keyword** in the language (`fip function
f(own a: i32[])`, with `fbip` beside it), Koka's functional-in-place modifier,
enforced by E053 and E068 and tested in `internal/parser/fip_test.go`. A
program document sharing that name is a collision in the one repo where both
meanings are live. Say "a proposal" and "a proposal turn"; the branch prefix is
`proposal/`.

Adapted from Victor Taelin's BIP program for Bend
(<https://gist.github.com/VictorTaelin/d632f46aa55e561d3cd2c43c66f2813e>).
The method is his; the areas, the gates and the measurement rules are Fern's.
Fern is not a proof language and makes no "never slower than C" claim, so the
theorem-proving and C-parity areas are replaced with the things Fern actually
promises: that every backend agrees, that the self-hosted compiler agrees with
the native one, and that memory is reclaimed.

## 1. Improvement areas

These are inspirations, not a menu. Any of them is a valid target.

**1. Self-host / native agreement.** Fern's self-hosted compiler
(`examples/self_host/`) and its native Go compiler (`internal/`) are supposed
to compile the same language. Every place they disagree is a bug in one of
them, and the disagreements that matter most are the ones no gate currently
sees. The `-interp` oracle is cheap and total: run your program interpreted,
run it through `bin/fern-selfhost`, compare. A whole closure-dispatch bug
cluster (#5001/#5007/#5009/#5026) was found exactly this way, on shapes that
lowered cleanly and then segfaulted. Widen a known-divergences file by
*deleting* a row, not by adding one.

**2. Memory: reclaim, reuse, and the allocation cliff.** Fern is reference
counted (Perceus-style: inc/dec insertion, borrow inference, drop
specialisation, constructor reuse). A leak here is unbounded — it scales with
the round count and eventually walks into the 16 GiB arena wall (exit 125).
#6127 is the live list of shapes the self-host runtime leaks and native does
not. Related and less watched: over-*retains*, which are silent — an extra
`inc` never crashes, it just makes an `rc == 1` in-place append into a copy,
and turns a linear accumulator quadratic. Find a shape that allocates in the
wrong complexity class and fix it. `docs/TEST-GATES.md` §"What nothing gates"
is a shopping list.

**3. Backend parity.** Four target rows across three emitters — arm64 Linux,
arm64 Darwin (which shares `EmitWithOptions` with arm64), x86-64 Linux, and
wasm/WASI-p2 — and `docs/BACKEND-PARITY.md` is the table of what each one can
do. A construct that works on three rows and not the fourth is a defect, not a
documented limitation, unless the table says so with a reason. The IR layer is
target-agnostic: a fix in `internal/ir` is worth three fixes in the emitters,
and is the only kind that cannot drift back apart.

**4. Bug fixing.** Audit, find a defect, reproduce it minimally, fix it. A
repair MUST be made at the DEEPEST possible layer: find the root cause, never
patch the observed symptom. Often the root cause is a system, a module or a
fundamental design decision whose repair touches thousands of lines. That is
irrelevant. If the problem lives in a deeper layer, it MUST be fixed in that
layer. Papering over a problem with a hack does NOT count as a fix and will be
rejected.

**5. Compiler performance and compiler memory.** The compiler is itself a
long-running, allocation-heavy Fern program — the only one the project ships,
and the reason Fern moved off arena-and-forget. Make a compile faster, or make
it fit: the stage-2 self-compile is the usual arena-wall victim, and a cold
driver emit is the usual host-RAM victim. Report `__heap_bump_bytes()`, not
RSS (see §3).

**6. Anything else.** A clearer doc, a better error message, a missing stdlib
function, a confusing syntax, a diagnostic that names the wrong span —
literally anything you believe improves the language. Error-message quality
counts double: it is the surface a new user meets first and the one the
project has the least automated coverage of.

## 2. Method

Follow this exactly, in order.

### Phase 0 — Sync and check for duplicates

```
git fetch origin main && git checkout -B proposal/<slug> origin/main
```

Start from a **fresh** `origin/main` every time. PRs here are squash-merged, so
building on a pre-squash commit gives git two independent creations of the same
content and an add/add conflict on a branch that never really diverged.

Then check what is already known:

- open issues — `list_issues` / `search_issues` (there is no `gh` CLI in this
  environment; use the GitHub MCP tools);
- `docs/proposals/wontfix.md` — settled rulings. NEVER re-report these;
- `internal/e2e/testdata/selfhost-*-known-divergences.txt` — defects that are
  already measured, listed and accepted. Do not file them again. *Closing* a
  row is a first-rate proposal;
- `docs/` — a `*-PLAN.md` or `*-RESEARCH.md` for your area usually means the
  problem is known and the shape of the fix is already argued.

If your finding is already an open issue, a wontfix ruling, or a listed
divergence, it is not yours to file — find something else.

Verify tracker state against the code before you believe it. Issues here have
repeatedly lagged reality (#4451/#4363/#4346 all described work that had
already landed).

### Phase 1 — Read restriction (this phase only)

Until Phase 4 you are NOT authorized to read any file of Fern's
implementation: no Go sources, no self-host compiler sources, no `docs/`.
The ONLY files you may read are:

- `README.md`
- `site/src/content/docs/**` — the published documentation site
- `internal/stdlib/**` — the standard library, written in Fern; a user can read
  their library's source
- `examples/**` **except** `examples/self_host/**`

You are a USER of the language, and a user only has the docs. `CLAUDE.md` is
injected into your context and you cannot unsee it — do not use it to route
around the restriction. Anything you learn there about *where the bodies are
buried* is Phase 4 knowledge; treat it as unavailable until then.

The purpose is not ceremony. It is that a compiler author cannot feel a bad
error message, because they read the code that produced it and know what it
meant. You need one turn of not knowing.

### Phase 2 — Build a random app in Fern

Draw a random number N in 0..255 with a real system RNG:

```
python3 -c 'import secrets; print(secrets.randbelow(256))'
```

Read ONLY line N of `docs/proposals/random_app.txt` (1-indexed line N+1) and use that
line as the inspiration for the software you will write. The app need not be
exactly what the line says, but it MUST be clearly related to it. Keep it
relatively simple: something you can implement in at most 1000 lines, in one
session.

Write it as a SINGLE Fern file containing:

1. all `import`s, then all `struct` / `enum` / trait declarations used by the
   file;
2. global definitions (constants, functions);
3. an executable `main(): i32`;
4. at least 3 non-trivial `test.TestOutcome` functions asserting properties of
   your program — not `assert_eq(2+2, 4)`, but the invariants that would
   actually break if you got it wrong;
5. a `test.TestRunner` wired to run them, so the file is its own test suite
   (`import "std/test";` — see `examples/tests/arithmetic_test.fern`).

Have `main` return 0 on success, so "exit 0, TAP all-pass" is the criterion on
every leg. By the end, the file must pass ALL FIVE of these:

```
./bin/fern -check  app.fern                          # type-checks, silent
./bin/fern -interp app.fern                          # the oracle
./bin/fern -target x86-64 -o /tmp/app     app.fern && /tmp/app
./bin/fern -target wasm   -o /tmp/app.wasm app.fern && wasmtime run /tmp/app.wasm
make selfhost-cli && bin/fern-selfhost -target x86-64 \
    /ABS/PATH/app.fern $PWD/internal/stdlib -o /tmp/app-sh && /tmp/app-sh
```

Flags before the file: `bin/fern` uses Go's `flag` package, which stops parsing
at the first non-flag argument, so `-o` written after `app.fern` is silently
ignored and the assembly goes to stdout. The self-host driver parses
positionally and wants the flag last; that asymmetry is real, not a typo.

The last leg is the point. `make selfhost-cli` is a one-time ~2 min build
(~13 s warm on arm64-darwin), then ~1.3 s per program, and it is the only way
you exercise the compiler the project is actually trying to finish. It needs
**absolute paths**.

Compare **stdout** across the legs, not just status. The `-target wasm` CLI
component lowers `main`'s return through `wasi:cli/run`'s `result<_, _>`, so
every nonzero value collapses to exit 1 — measured, not assumed. Exit codes
distinguish only pass from fail on that leg, which is exactly why `main`
returns 0 and the TAP output carries the detail.

If along the way you conclude the app cannot be completed because of an
incompleteness or failure in Fern, STOP and go to Phase 3 anyway. That is a
better outcome than a finished app, not a worse one.

Save the file at `examples/proposals/<name>.fern` — it ships with your PR.

### Phase 3 — Pick the problem

While doing Phase 2 you will hit difficulties caused by the language: bad error
messages, bugs, backend disagreements, performance problems, missing features,
missing library functions that would have helped a lot, and so on. Write them
down as you hit them, before you work around them — the workaround erases the
memory of how bad it was.

Now judge which one has the biggest negative impact on users / the highest
probability of making someone have a bad experience and quit the language
(churn), among those that are NOT a deliberate limitation of the language's
design.

Deliberate limitations, for the avoidance of doubt: reference counting rather
than tracing GC; no exceptions (`Result` + `?`); immutable structs; static
types with no `any`; the supported target set and no more; ARM32 is retired
and is not coming back.

A deliberate limitation CAN still be picked IF your proposal does not change
the design — "`?` could propagate through one more position" is fair game;
"add a GC" is not.

Two tie-breakers, both Fern-specific. Prefer a defect on the **self-host** side
over the same defect on the native side: `docs/NATIVE-CONVERGENCE.md` is the
policy that `internal/` eventually freezes, so native-only work is debt and
self-host work is progress. And prefer a defect that is **invisible to every
existing gate** over one a suite would have caught eventually — you are the
only instrument that was pointed at it.

### Phase 4 — Fix it

From this moment, and only from this moment, you may read every file in the
repository, including the implementation.

Fix it at the deepest layer that owns the problem:

- a lowering bug that shows up on one backend usually lives in `internal/ir`,
  where the fix serves every backend;
- a self-host bug in inference, the checker, `Ty` or `EmitState` lives in
  `examples/self_host/asmcore.fern`, which all three self-host backends share.
  Editing the same thing three times in the `emit_*` layers is the wrong fix
  even when it works;
- a diagnostic that points at the wrong place is a span bug, not a message bug.

**Deletion is half the job.** Fern has no token caps on its source files, but
it has the same underlying rule and CLAUDE.md states it: a diff that removes
lines is at least as valuable as one that adds them. If you replace X with Y,
X is gone — from the parser, the tests, and the docs — in the same diff. If
your change makes a comment stale, the comment dies with it. Finding the
simplification that makes your fix small **is 50% of a proposal**, not a bonus on
top of it.

What Fern rations instead of tokens is **memory and CI time**. If your change
grows the compiler's live set or its emit peak, buy it back: the stage-2
self-compile runs against a 16 GiB arena and exits **125** when it does not
fit, and the heavy driver builds are already RAM-semaphored on a 4-core box.
If your change adds a test that takes minutes, buy that back too — say in the
PR which suite it joins and what it costs.

You may fail. Do your best not to. But if you do fail, that is acceptable:
continue to Phase 5 and leave the "solution" part of the issue empty. A
precisely-characterised defect with no fix is worth more than a hack.

### Phase 5 — Open the issue and the PR

Concision is the requirement of greatest importance here. Take it extremely
seriously. The maintainer is a human receiving many of these:

1. his brain is context-switching constantly and does not remember every detail
   of the repo, so CONTEXTUALIZING your problem — and every part of the repo
   needed to understand it — is essential;

2. even with context, there is a limit to how much text he can read. Less text
   per issue is better.

The ideal issue has exactly these components:

- **Context** — everything needed to read the whole issue without looking up a
  single definition, in a dictionary or in the repo.

- **Problem** — explained in the simplest way, easy words, no undefined jargon,
  and preferably with a SHORT VISUAL EXAMPLE that immediately situates the
  reader. For Fern that is almost always a minimal `.fern` program plus the two
  exit codes that disagree, or the diagnostic as printed next to the
  diagnostic you expected.

- **Solution** — implemented (or merely proposed, if you failed).

- **Metrics** — all of them, measured as §3 requires. If it is a performance or
  memory change, a table with every affected shape. If it is a refactor that
  should not have changed behaviour, the `scripts/selfhost-emit-hashes`
  before/after.

Then ship it. The loop is fixed and you do not ask permission at any step:
**branch → commit → push → PR → subscribe → watch CI and mergeability →
squash-merge when green → next turn.**

```
git commit -am "<one dense line>"
git push -u origin proposal/<slug>
```

Open the issue and the PR with the GitHub MCP tools, `subscribe_pr_activity`
on the PR, and drive it to green. A PR that is green but `dirty` is not done —
merge main in and push. Do not stop at "pushed to the branch".

## 3. Rules of this machine

- **This box is a 4-core sandbox container, and wall-clock measured here does
  not travel.** It is not a dedicated benchmark node and there is no honest way
  to make it one. So: never quote a local timing as a project metric. Measure
  with host-independent counters instead, and let CI — which shards across real
  machines — produce the timings.

- **Memory is `__heap_bump_bytes()`, never RSS.** The arena is a 16 GiB
  `MAP_NORESERVE` mapping, so the first touch maps a 2 MB huge page under
  `THP=always` and 4 KB under `madvise`. The same binary on the same input has
  measured **43 MB locally and 552 MB on CI** — a 12x spread with identical
  allocation, which failed a ceiling on a change that had just made the code
  50x leaner. `__heap_bump_bytes()` is the bump allocator's high-water mark:
  exact, host-independent, meaningful under qemu. Bind it to an **i64**.

- **The append cliff ranks by WEIGHT, not by count.** `__arr_push_shared_bytes()`
  sums the bytes copied; `__arr_push_shared_count()` only counts crossings. A
  whole-module compile crosses **188 times and copies 812 bytes** — noise —
  while one badly-threaded accumulator copies **2.3 GB**. Two rounds of
  optimisation work were scoped against the count and aimed at sites that could
  not have paid. `FERN_CLIFF_REPORT=1` prints both on the self-host drivers.

- **Run the gates that carry signal for what you touched** —
  `docs/TEST-GATES.md` says which those are, and which look authoritative and
  are not. The fixpoint is self-referential: it proves the compiler reproduces
  itself and is structurally blind to a *stable* miscompile. For self-host
  lowering, `internal/e2eselfhost` is primary and the fixpoint secondary. #6018
  passed the per-module fixpoint AND all 335 fixtures AND the native suite
  while segfaulting the driver.

- **Do not hold the PR behind a whole-package sweep.** `internal/e2eselfhost`
  unsharded exceeds 90 minutes; `internal/e2e` no longer fits in 45. CI shards
  both and answers sooner than this box. Run the targeted `-run` legs for what
  you touched, push, open the PR, and let the sweep run alongside it. Say in
  the PR body which suites you ran and which are still in flight.

- **Do not use qemu/arm64 as a local gate.** Gate on x86-64 plus wasm locally;
  CI runs the full arm64 matrix on every push. Reach for qemu only to debug an
  arm64 failure CI has already surfaced.

- **Never pipe a test run through `tail` or `head`.** The pipeline reports
  `tail`'s exit status, which is always 0, so a failing suite is announced as a
  success and the detail is discarded. Redirect and grep:
  `go test … > run.log 2>&1; echo "EXIT=$?"`, then read `--- FAIL` from the
  file. A timed-out run also prints `FAIL` — check the `--- FAIL` count before
  reading a timeout as a breakage.

- **Exit 125 is the arena; 137 is the host.** 125 (`ExitArenaExhausted`) is a
  real, reproducible failure and almost always a leak. 137 is 128+9 — the host
  ran out of RAM; lower `FERN_BUILD_HEAVY_MB` / `FERN_BUILD_MEM_BUDGET_MB` /
  `FERN_EMIT_MEMLIMIT_MB` and retry. Do not investigate one as the other.

- **If a test SKIPs, that is a missing dependency, not a green light.** The
  pinned wasm toolchain is wasmtime v46.0.1 + wasm-tools 1.253.0; the
  session-start hook installs them under `~/.fern-wasm/`. Export them onto
  `PATH` and set `FERN_WASI_ADAPTER`.

- **Never commit to `main`.** One commit per PR, containing: the minimal
  solution (simplified — this is essential) + your `examples/proposals/<name>.fern` +
  minimal, well-designed, fast regression tests that fail if the problem comes
  back, in a general way. A regression test that only pins your exact repro is
  worth much less than one that pins the class.

That concludes your turn.
