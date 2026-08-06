# Settled rulings — do not re-report

Read this in **Phase 0** of a proposal (`docs/FERN-PROPOSALS.md`). Everything here has been
decided. Filing it again costs the maintainer a context switch and gets closed.

A ruling is *settled*, not *sacred*. If you have an argument that the ruling
never considered, make that argument — but say up front which ruling you are
reopening and what is new. "I hit this and it annoyed me" is not new; the
ruling already knows it is annoying.

Add a row when a maintainer settles something. Cite the issue/PR that settled
it, so the next reader can find the reasoning rather than the verdict.

---

## Language design

**ARM32 is retired and is not coming back.** The backend, its e2e tests and the
cross-compiler/qemu wiring were all deleted in early 2026 — parity work across
backends became untenable and the Raspberry Pi 2/3 story was a poor match for
what the language is for. Do not propose restoring it, and do not add
arm32-specific code. A stale "on arm32" comment in the tree is a cleanup TODO,
not evidence the backend still exists.

**"TypeScript does it this way" is not an argument.** Fern started
TS-flavoured and that is history, not a constraint. Look to Roc, MoonBit,
Rust, Zig and Go instead. See `CLAUDE.md` §"Language direction".

**No tracing garbage collector, and no weak references.** Reference counting
with cycle-freedom *enforced by the checker* is the design — Option A.2 in
`docs/CYCLE-COLLECTION-ANALYSIS.md`, adopted and shipped. Cycles are no longer
constructible (E048 and friends), so a cycle collector would collect nothing.
That document's body describes a constructible cycle and is **historical**;
read its update header first.

**No exceptions.** Errors are `Result` plus `?`. Proposals to widen where `?`
can appear are fair game; proposals to add `throw`/`catch` are not.

**No implicit prelude.** A program sees only what it `import`s. The auto-prelude
was removed deliberately (Phase 5 of the module work); "I had to import
`std/string` to use a string method" is the design working.

**Struct fields are immutable after construction (E048).** This is one of the
three enforcement points that make cycles unconstructible. It is not an
oversight in the checker.

## Targets and platform support

**Only the current macOS release is supported.** CI pins a specific recent
label (`macos-15`) rather than tracking `macos-latest`, so a runner image roll
cannot break the build without a visible commit. Pinning CI to an *older* label
to dodge a break is explicitly not supported — see `docs/BACKEND-PARITY.md`
§version-support.

**No Darwin x86-64.** Apple Silicon is the macOS path.

**The x86-64 CPU baseline is Haswell-class 2013 (SSE4.2 + BMI1) and the arm64
baseline is plain ARMv8-A.** Binaries are static with no runtime dispatch, so a
selected instruction is a hard requirement. Raising or lowering either baseline
is a project decision, not something a proposal settles.

**A wasm leg cannot round-trip an arbitrary exit code.** Two separate reasons,
often confused:

- WASI refuses anything outside `[0..126)`, so a value >= 126 is reported as 1.
  This produced 14 phantom "mismatches" on the self-host fixture leg's first
  run.
- A `-target wasm` CLI component lowers `main`'s return through
  `wasi:cli/run`'s `result<_, _>`, so under plain `wasmtime run` **every**
  nonzero value collapses to exit 1 — 3, 7 and 125 all report 1.

The x86-64 and arm64 legs check exact values. On wasm, compare stdout.

**`__c_call<n>` (FFI), `subprocess` and `timer_fd` are clean errors on wasm, by
design.** There is no wasm C ABI to call into. `wasm_unsupported_builtin`
rejects them before emit; that is the intended endpoint, not a missing feature.

**The Go module path (`github.com/jakechampion/lang`) and the Pages URLs still
say `lang`, deliberately.** They cross the GitHub-repo boundary; renaming them
before the repo itself is renamed breaks `go install` and the docs site.
Everything else is already `fern`.

## Measurement and gates

**RSS is not a memory metric here.** The arena is a 16 GiB `MAP_NORESERVE`
mapping, so identical allocation reads 43 MB under `THP=madvise` and 552 MB
under `THP=always` — a 12x spread that once failed a ceiling on a change that
had just made the code 50x leaner. Use `__heap_bump_bytes()` (bind it to an
`i64`). An issue whose metrics section quotes RSS will be asked to re-measure.

**The unweighted append-cliff count does not rank work.**
`__arr_push_shared_count()` counts crossings; `__arr_push_shared_bytes()` sums
what they copied. A whole-module compile crosses 188 times and copies 812
bytes — noise — while one badly-threaded accumulator copies 2.3 GB. Two rounds
of optimisation were scoped against the count and aimed at sites that could not
have paid.

**The fixpoint is not the primary gate for a lowering change.** It proves the
compiler reproduces itself and is structurally blind to a *stable* miscompile.
`internal/e2eselfhost` is primary; see `docs/TEST-GATES.md`. #6018 passed the
per-module fixpoint, all fixtures and the native suite while segfaulting the
driver.

**Wall-clock measured in a sandbox container is not a project metric.** Four
cores, shared host, no isolation. Report host-independent counters and let CI
produce timings.

**Exit 125 is arena exhaustion, not an OOM.** 137 is the host OOM-killer
(128+9). They are deliberately distinct so that a genuine compiler regression
is not filed as infrastructure flake, and vice versa.

## Compiler architecture

**The legacy AST→asm emitters are deleted and are not coming back.** All three
(`asm.fern`, `asm_arm64.fern`, `wasm.fern`) are gone; every backend routes
IR-or-error. A construct that does not lower is a plain bug report naming the
bail site (`FERN_STRICT_IR=1`), not a reason to reintroduce a fallback path.

**The backends' `emit_*` instruction-selection layers are deliberately
parallel.** Unifying them is a roadmap decision, not a drive-by. What is
*shared* — the `Ty` type system, inference, the pre-codegen checker,
`EmitState` — lives in `examples/self_host/asmcore.fern` and must be edited
there exactly once.

**New language surface should land self-host-first.** Under
`docs/NATIVE-CONVERGENCE.md`, `internal/` eventually accepts only bugfixes,
oracle needs, and what the self-host sources require to bootstrap. A
native-only feature is a debt entry against #4451, not a free win.

## Method

**A hack that hides a symptom is not a fix.** Stated in `docs/FERN-PROPOSALS.md` Phase 4
and repeated here because it is the most common rejection. If the root cause
lives in a deeper layer, it is fixed in that layer, however large that is.

**A row in a `known-divergences.txt` is already reported.** Deleting a row is
an excellent proposal; re-filing one is not a proposal at all.

**A `*-PLAN.md` in `docs/` usually means the problem is known.** Check before
filing; then argue with the plan if you disagree with it.
