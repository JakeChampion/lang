# Project notes for Claude Code

Standing rules and orientation. **Keep this file short** — see "Keeping this
file small" at the bottom before adding to it.

## Name

The language is **Fern**. Source files are `.fern`; the CLI is `fern`
(`cmd/fern`), the LSP `fern-lsp`, the wasm playground bundle `cmd/fern-wasm`,
the stdlib doc generator `cmd/ferndoc`. Emitted runtime symbols are `__fern_*`.

The **only** things still on the old `lang` name are the Go module path
`github.com/jakechampion/lang` and the GitHub repo + Pages URLs
(`jakechampion.github.io/lang`). Both cross the GitHub-repo boundary, so they
must follow a rename of the repo itself — otherwise `go install` and the Pages
site break. Do not rename them piecemeal.

## Language direction

Fern started TypeScript-flavoured (functions, `var`, `if/else`, struct literals
all came from TS) but **that is no longer the target**. When designing syntax, a
type-system rule, error handling, or stdlib shape, look beyond TS — cribbing from
Roc, MoonBit, Rust, Zig, and Go is more productive than reaching for a TS
convention that does not fit. Treat the existing TS-shaped surface as
historical, not as a constraint to preserve, and never justify a design choice
with "this is how TS does it" when a better shape exists.

Fern is **general-purpose**. The two workloads it grew up around — small
fast-startup CLI tools and short-lived edge-function-style HTTP servers — are
where it is most polished, but they are no longer the boundary. Long-running,
allocation-heavy programs are in scope: the self-hosted compiler is exactly such
a program, and it is what drove the move from arena-and-forget to reference
counting. When a design choice trades general-purpose fitness for a narrow
edge/CLI optimisation, weigh that trade explicitly rather than assuming
short-lived-process semantics.

The reach goes below the OS as well: hosted apps, guest libraries inside someone
else's firmware, drivers, and kernels are all targets, anchored by the long-term
goal of an **entire OS written in Fern** — `docs/BARE-METAL-PLAN.md` for what that
costs and in what order. The binding constraint there is the memory model, not
codegen: an interrupt handler is a second context racing non-atomic refcounts.
Unscheduled; it constrains design choices today, it is not work in flight.

## Targets

- **ARM64 / aarch64 Linux** — the default target. qemu-aarch64 under test; real
  hardware is AWS Graviton, Raspberry Pi 4+ (64-bit), Android, Apple Silicon via
  Linux containers. Baseline: plain ARMv8-A, Advanced SIMD included.
- **ARM64 / aarch64 Darwin** — Mach-O for native Apple Silicon
  (`-target arm64-darwin`), no Linux container needed. Verified end-to-end on the
  `macos-15` CI runner; CI pins that label rather than floating `macos-latest`,
  so a runner-image roll cannot break the build without a visible commit. Only
  the latest macOS is supported.
- **WASI / WebAssembly** — exercised via wasmtime.
- **x86-64 / amd64 Linux ELF** — System V AMD64 ABI. Baseline is
  **Haswell-class 2013** (SSE4.2 + BMI1), so `popcnt` / `lzcnt` / `tzcnt` are
  assumable. Binaries are static with no runtime dispatch, so a selected
  instruction is a hard requirement, not a fast path. Note LZCNT/TZCNT fail
  **silently** below the baseline (same opcodes as bsr/bsf plus an F3 the older
  CPU ignores) where POPCNT faults. No Darwin x86-64.

Raising either CPU baseline is a project decision, not a codegen one. Per-backend
support table, version-support stance, and known limitations:
`docs/BACKEND-PARITY.md`.

The IR layer is target-agnostic; **new optimisations belong in `internal/ir`** so
all backends benefit.

**ARM32 was retired.** The codebase shipped an arm32 backend through early 2026
— it was the original target — but cross-backend parity work became untenable
and the hardware story (Pi 2/3 embedded) was poorly matched to the language's
direction. The backend, its e2e tests, and the cross-compiler / qemu wiring are
gone. **Do not add arm32-specific code back.** A comment still saying "on arm32"
or "same as arm32" is a TODO to clean up.

## Autonomy — always proceed, never ask permission

**Just start the work.** When the next step is clear (the next slice in a plan,
an obvious fix, a follow-up), begin it immediately — do NOT stop to ask "want me
to start?", "should I proceed?", "shall I do X next?", or offer a menu when one
option is clearly best. Pick the best option with your own judgement, do it, and
report what you did.

Reserve questions for genuine forks where the user's answer changes the work and
you cannot resolve it from the code or sensible defaults — not for permission,
not for sequencing you have been delegated, not for confirmation that a plan is
good. Momentum over check-ins; the PR and the report are how you keep the user
informed, not a pre-flight ask.

## Working with PRs

**Every push of completed work gets a PR — no exceptions, never ask first.**
This includes small follow-ups, doc-only fixes, and comment corrections. Opening
it IS the expected action. The loop is:

> branch → commit → push → PR → subscribe → watch CI **and mergeability** →
> rebase-merge when green → next task

Do not stop at "pushed to the branch", do not wait after opening the PR, and do
not ask whether to merge. If CI fails, diagnose and push fixes on the same branch
until green, then merge. Subscribe to PR activity (`subscribe_pr_activity`)
without being asked — the user prefers that to manual CI polling. Pause only for
a genuine fork (an ambiguous review comment, an architectural decision).

**Open the PR EARLY.** CI is faster than this dev machine: this is a 4-core
container where `internal/e2e` runs 45+ minutes in one process, while CI shards
the same work across machines. So the moment the *targeted* suites for what you
touched are green, push and open the PR, and let any long sweep you started keep
running alongside it. Say in the PR body which suites you ran and which are still
in flight — that is the honest report, and it beats a 45-minute stall before
anyone can see the diff. This does not weaken the engineering bar; it says the PR
is the place to wait, not your terminal.

**Check mergeability on every wake-up, not just CI.** A PR can be green and
unmergeable, and webhooks do not reliably announce the transition — main moves
under you. Read `mergeable_state` alongside the checks and resolve a conflict the
same way you would a red build: without being asked.

- `dirty` = conflicts. Fix them yourself; only ask when both sides genuinely
  changed the same logic and picking one loses behaviour.
- `behind` / stale base = **rebase** onto main and force-with-lease push. Do not
  merge main in: a merge commit makes the branch un-rebase-mergeable, so the fix
  blocks the merge it was meant to unblock.
- `unstable` = mergeable, checks still running or non-required ones failing.
  Keep waiting.

**PRs here are REBASE-merged.** Each commit lands on main individually, so every
commit message is published — write them to be read, rather than leaning on a
merge-time title and body that no longer exists. Keep the branch linear; nothing
that introduces a merge commit can be rebase-merged.

Rebasing still rewrites SHAs, so content that landed via your branch reaches main
under a *different* commit than the one your branch holds. Building further on
the pre-merge commit therefore still makes git see two independent creations of
the same file — an add/add conflict, though nothing really diverged. After any
PR merges, start follow-up work from a fresh base:

```
git fetch origin main && git checkout -B <branch> origin/main
```

GitHub deletes the head branch on merge, which leaves a stale `origin/<branch>`
ref locally and makes the next `--force-with-lease` push fail with `stale info`.
`git remote prune origin` before pushing again.

If you only notice after committing, reset onto main and replay the new commits
(`git checkout -B <branch> origin/main && git cherry-pick <sha>`) rather than
hand-resolving conflict markers between a file and its own already-merged copy —
then force-with-lease push.

**GitHub can silently delete a span from a PR body or comment.** Naming both
checked shifts on one line — `<<?` then `>>?` — has already destroyed the
sentence between them, inside backticks and inside a fenced code block alike,
leaving an unterminated clause with nothing marking the loss. A lone `<` came
through escaped and intact, so the trigger is narrower than "any angle bracket"
— but do not go hunting for the boundary. Split the two operators across lines,
and read the body back after posting whenever it quotes operators or asm. A
posted comment cannot be edited afterwards; only a follow-up can correct it.

## Project goal / roadmap (what "the next task" means)

1. **DONE — full IR implementation for the entire language in the self-hosted
   compiler.** All three legacy AST→asm emitters are deleted (`asm.fern` +
   `asm_arm64.fern` #5972, `wasm.fern` #3457). Every backend routes IR-or-error,
   so a module outside the IR subset is a diagnostic naming the bail site
   (`FERN_STRICT_IR=1`), not a silent fall-through. There is no AST fallback left
   to widen the subset *against* — a construct that does not lower is now a plain
   bug report. Record: `docs/SELFHOST-AST-RETIREMENT.md`.
2. **Port the native Perceus implementation to the self-hosted compiler** —
   inc/dec insertion, borrow inference, drop specialisation, reuse analysis — so
   the self-host matches native's memory management. **Reuse is substantially
   complete**; the RECLAIM side is where the work remains. Current state, the
   live leak list, and the traps this area sets:
   `docs/rc-log/` (newest file) — its §9 predecessor in
   `docs/RC-PERCEUS-SELF-HOST-PORT.md` holds everything before 2026-08-20 — and
   `docs/SELFHOST-PERCEUS-REUSE.md`.

When a PR merges with no more specific instruction, the default next task is the
next increment toward goal 2.

**Verify tracker state against the code before picking anything up.** Issues here
have repeatedly lagged reality — #4451 / #4363 / #4346 all described work that
was already done. Check the code, not the issue.

**Native convergence policy** — reference on any native-touching work.
`docs/NATIVE-CONVERGENCE.md` governs how `internal/` (native) and the self-host
compiler converge rather than drift forever: once goal 2 reaches parity and the
freeze preconditions go green, `internal/` accepts only bugfixes, oracle needs,
and what the self-host sources require to bootstrap (the "Go 1.4 rule"). Until
the freeze fires, treat every new native-only feature as a debt entry, not a free
win, and prefer landing new surface self-host-first where the fixpoint allows.
Reference #4451 from any issue/PR adding native-only surface (`internal/ir`,
`internal/interp`, the codegen backends) or touching the differential/parity
suites, so the debt stays visible in one place.

## Engineering bar (non-negotiable)

- **Pick the gates that carry signal for what you touched** —
  `docs/TEST-GATES.md` says which suite proves what, which ones look
  authoritative and are not, what nothing gates at all, and the rc diagnostic
  modes in the order you reach for them. The headline trap: the fixpoint is
  SELF-REFERENTIAL — it proves the compiler reproduces itself and is structurally
  blind to a stable miscompile — so on self-host lowering changes
  `internal/e2eselfhost` is PRIMARY and the fixpoint secondary. #6018 passed the
  per-module fixpoint AND all 335 fixtures AND the native suite while segfaulting
  the driver.
- **Run the targeted suites before you push; leave whole-package sweeps to CI.**
  A sweep is worth *starting*, but it is not a gate you hold the PR behind. What
  must be green before the push is the set that carries signal for your change —
  including the WASM e2e tests. **A SKIP is a missing dependency to fix, not a
  green light**, and `ok` in 0.3 s is a SKIP.
- **Never regress.** When a change lands, it must not break anything outside its
  targeted set either — that is what CI on the PR is for, and driving the PR to
  green once CI reports is part of the change, not a follow-up.
- **Never pipe a test run through `tail` / `head`.** You get the pipeline's exit
  status — `tail`'s, always 0 — so a failing suite is reported as a pass and the
  detail is discarded. Redirect to a file and grep it:
  `go test … > run.log 2>&1; echo "EXIT=$?"`, then read `--- FAIL`.
- **Every new feature ships with tests.** Parser-time desugar → parser test.
  Checker rule → checker test. Runtime behaviour → e2e test. No "the next PR will
  add coverage."
- **Fix the cause, in the right place — never a side channel.** No workaround,
  no special case shielding a symptom, no threading a value to one caller as an
  extra parameter when it belongs on the shared structure. If the coherent fix
  is wider than the bug, the wider change is the fix; give it its own PR when it
  is broad, but never ship the narrow version *instead*. A tracking list, a
  known-divergence entry, or a TODO records a gap someone else owns — reaching
  for one to get your own change green is the workaround this rule forbids. When
  something genuinely blocks the correct fix, say so and stop rather than
  routing around it.
- **Fix bugs you find on the way.** If exploring for one feature surfaces a
  separate bug, fix it in the same PR with its own test rather than leaving it.

Timings, memory budgets, sharding, the arena-vs-OOM exit codes, which instrument
to measure with, and the arm64/qemu policy all live in
**`docs/LOCAL-DEV-LOOP.md`**. Read it before running anything long — several of
its numbers were previously off by an order of magnitude, and a stale one costs
an hour per attempt.

## Code comments

A comment is a brief note explaining what the code does when that is not obvious
from the implementation. It is **not a changelog**, and not a place to record the
reasoning behind a change you were just asked to make.

Write one when the code is genuinely non-obvious: a workaround for an external
bug, a non-intuitive invariant, a subtle ordering requirement. Prefer a clearer
name or a smaller function over a comment that explains awkward code.

Do not:

- **Narrate the change you just made** — "we now retry here because the stage
  was returning 502s". That belongs in the commit message or PR description.
- **Restate the code in English** — `// increment the counter`.
- **Add a header comment to a function just because you touched it.**
- **Leave "previously we did X, now we do Y" notes.**
- **Write an essay reconstructing the investigation** that led to a line of code.

That last one is the most common failure. Six lines on one field, walking through
the alternative that was not taken and what would have gone wrong otherwise:

```go
// FlushInterval is how often buffered records are written to disk.
//
// It has to be set by the caller. The zero value is not treated as "use the
// default": it disables flushing entirely, so records accumulate in memory
// until the process exits and are then lost. Nothing reports an error when
// that happens, and the buffer looks healthy right up to the point the data
// is gone.
```

The reader needs one fact, not the debugging session:

```go
// Zero disables flushing rather than selecting the default.
```

If the reason for a change matters to a future reader, keep it to one short line
at the point it applies — not a paragraph at the top of the function.

## Erasure — deletion is half the job

Coding agents add by reflex and rarely subtract. Counteract this constantly: a
diff that removes lines is at least as valuable as one that adds them.

- **Swap rule.** When a task replaces X with Y, fully deleting X is part of the
  task. No "keep the old path for compatibility" unless explicitly requested.
  "Lambda is `\x.f` now, not `λx.f`" — bad: parser accepts both; good: `λx.f` is
  gone from parser, tests, and docs. Bug fix — bad: a special-case `if` shields
  the symptom; good: the cause dies. Behaviour change — bad: old-behaviour tests
  linger or get dodged; good: deleted.
- **Comments.** A refactor that makes a comment stale deletes or rewrites it in
  the same diff. A done TODO leaves with the fix. Same for stale notes in `docs/`
  and this file. What a comment should say in the first place is above.
- **New code.** Scan for an existing concept to reuse before introducing a new
  one; find the simplest shape. If something confused you while reading, that is
  a bad abstraction — untangle it if it is in your blast radius.
- **Scope.** Full force on everything the current task touches. Deleting beyond
  that (retiring a backend, unifying parallel emit layers) is a roadmap decision
  — propose it, do not drive-by it. The backends' `emit_*` instruction-selection
  layers are deliberately parallel.
- Before finishing any task: what did this change make obsolete, and did I delete
  it?

## Subsystems with their own docs

Short pointers — the detail lives in the doc, and belongs there when you extend
it.

- **Literate programming** (`.fern.md`, tangle/weave, multi-module documents,
  importable literate libraries) — `docs/LITERATE.md`. Engine:
  `internal/literate`. When extending the chunk grammar or the remap, add cases
  at the layer you touched; the **diagnostic remap (generated line → document
  line) is the most regression-prone surface**.
- **Test runner** — `internal/stdlib/std/test.fern`, the pure-Fern TAP-13 runner
  the project migrates to once Go-side `*_test.go` files retire. Programs
  `import "std/test";` and call qualified (`test.test_new`, `test.assert_eq`,
  `test.fail`, …) with the type written `test.TestRunner`; receiver methods
  (`.it`, `.finish`) stay bare. Examples in `examples/tests/`; the Go-side gate is
  `internal/e2e/test_runner_test.go`. **When adding an assertion helper, add a
  case to `runner_self_test.fern` covering both the passing and the failing
  path** — the failure-reporting contract (predicate name in the message, actual
  + expected both quoted) is the runner's most regression-prone surface. Migration
  audit: `docs/TEST-RUNNER-MIGRATION.md`.
- **Module loading** — there is no prelude injector; a program sees only what it
  `import`s. `docs/PRELUDE-TO-MODULES.md` covers mangling, the transitive-import
  dedupe, `pub use` re-exports, and the in-memory (`modload.LoadSource`) path.
- **Capabilities** — two independent systems. `internal/platforms` gates what a
  *target* provides (the OS boundary; E066 post-tree-shake) —
  `docs/FREESTANDING-CORE.md` has the core-vs-host rule and every judgement call.
  `internal/caps` gates what a *package* may reach — `docs/PACKAGE-CAPABILITIES-BRIEF.md`.
  **A new builtin usually needs classifying in both**; a completeness test in each
  fails when one is missed. The self-host mirrors both — the target half in
  `examples/self_host/platforms.fern`, the package half in
  `examples/self_host/caps.fern` — each pinned entry-for-entry by a parity test
  in the Go package it mirrors, so **a new builtin is now four classifications**.

## Keeping this file small

This file is prepended to every turn, so its length is a permanent tax on every
task. It reached 859 lines by accumulating a changelog: each correction appended
a paragraph explaining what the previous paragraph got wrong, and closed issues
kept their narrative.

When you learn something here:

- **A measurement, an issue's status, or a war story goes in `docs/`**, not here.
  Add a pointer only if a task would go wrong without one.
- **State the current fact; do not narrate the correction.** "The arena is
  16 GiB" — not "this note used to say 8 GiB, which was wrong". Git history keeps
  the diff.
- **A closed issue's detail leaves with it.** Keep it only if it still changes
  what someone does today.
- Prefer editing an existing line over appending a new caveat under it.
