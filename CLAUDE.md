# Project notes for Claude Code

## Name

The language is named **Fern**. Source files use the `.fern`
extension; the CLI is `fern` (built from `cmd/fern`), the LSP is
`fern-lsp` (`cmd/fern-lsp`), the wasm playground bundle is
`cmd/fern-wasm`, and the stdlib doc generator is `cmd/ferndoc`.
The rename also covers: internal packages `internal/fernsmith` /
`internal/fernstring`, the emitted runtime symbols `__fern_*`,
the `FERN_WASI_ADAPTER` build/test env var, the wasm JS-interop
globals (`fernCompile` / `fernInterpret` / `fernLsp`) and their
`"fern:theme"` postMessage protocol, and the WIT `fern` world
(`cmd/fern/wit/fern.wit`, `componenttype` `fern.bin`).

The **only** things still on the old `lang` name — deferred
because they cross the GitHub-repo boundary — are the Go module
path `github.com/jakechampion/lang` and the GitHub repo + Pages
URLs (`jakechampion.github.io/lang`). Both should follow a rename
of the GitHub repo itself, otherwise `go install` and the Pages
site break.

## Language direction

This language started TypeScript-flavoured (the syntax for functions, `var`,
`if/else`, struct literals, etc. all came from TS) but **that's no longer the
target**. It's evolving into its own thing. When designing new features —
syntax, type system, error handling, stdlib shape — feel free to look beyond
TS for inspiration. Fern is now a **general-purpose** language: the two
workloads it grew up around — small fast-startup CLI tools and short-lived
edge-function-style HTTP servers — remain the places it's most polished,
but they're no longer the boundary of what it's for. Long-running,
allocation-heavy programs are in scope too (the self-hosted compiler is the
proof: it's exactly such a program, and it's what drove the move from
arena-and-forget to reference counting). Cribbing from Roc, MoonBit, Rust,
Zig, Go is more productive than reaching for TS conventions when they don't
fit — and when a design choice trades general-purpose fitness for a
narrow edge/CLI optimisation, weigh that trade explicitly rather than
assuming short-lived-process semantics.

Concretely: don't justify a design choice with "this is how TS does it" if a
better shape exists. Treat the existing TS-shaped surface as historical, not
as a constraint to preserve.

## Targets

- ARM64 / aarch64 Linux (the **default** target; qemu-aarch64
  under test; real hardware: AWS Graviton, Raspberry Pi 4+ in
  64-bit mode, Android, Apple Silicon Macs via Linux containers)
- ARM64 / aarch64 Darwin — Mach-O for native Apple Silicon Macs
  (`-target arm64-darwin`). No Linux container needed; clang +
  ld64 (native) or clang + lld (cross from Linux) link directly.
  Verified end-to-end on the `macos-latest` CI runner (Apple
  Silicon arm64; tracks Apple's current macOS release). Policy:
  only the latest macOS is supported — see
  `docs/BACKEND-PARITY.md` for the version-support stance and
  known limitations. All pointer-
  shaped values (string / array / struct / enum / slice /
  tuple) round-trip through 8-byte slots on arm64-darwin's
  high heap. The IR has a `WidthPtr` sentinel on
  `Op.Width` that each backend resolves to its heap-pointer
  width (4 on wasm32, 8 on arm64); `ast.IsPointerType` is
  the type-side classifier that drives stride / offset /
  store-width selection in `payloadSlotSize`,
  `structFieldLayout`, `tupleElemLayout`, `arrayElemStoreOp`,
  and `ast.ElemSizeBytesFor`. Map operations
  (`set`/`get_or`/`has`/`delete`/`iter`/`len`/`keys`/`values`)
  cover all combinations of i32 / string K/V. Closures with
  captures are lowered on every backend
  (`OpMakeClosure` / `OpMakeEnv` / `OpCallClosureDirect` —
  see `arm64.go:emitMakeClosureOrEnv` and the x86-64 mirror);
  the `ptrW`-aware capture layout from `closureconv` lines
  up with the load side (`payloadLoadOp` on CaptureRef emits
  `WidthPtr`). Test coverage: 8 `TestArm64Closure*` cases,
  matching counts on x86-64 + wasm.
- WASI / WebAssembly (currently exercised via wasmtime)
- x86-64 / amd64 Linux ELF — System V AMD64 ABI, native
  exec on x86_64 hosts + `qemu-x86_64` on non-x86 hosts.
  Six PR shape (#269–#274) covers everything from exit
  codes through arithmetic, control flow, strings + alloc,
  composite types + floats, TCP + HTTP, and the
  `ir.TailCallOptimize` pass (which is now also wired in
  arm64 + wasm — same one-line backport, every backend
  gets O(1) stack depth on self-tail recursion). End-to-
  end parity with arm64 for the edge-handler use case
  (`function handle(req): resp` → serving HTTP/1.1). No
  Darwin x86-64; Apple Silicon is the active macOS path.

The IR layer is target-agnostic; new optimisations should live in `internal/ir`
so all backends benefit.

**ARM32 was retired.** The codebase shipped an arm32 (Linux ELF)
backend through early 2026 — it was the original target and the
default — but parity work between backends became untenable and
the arm32 hardware story (Raspberry Pi 2/3 embedded) was poorly
matched to the language's stated edge-function focus. The backend,
its e2e tests, and the cross-compiler / qemu wiring were all
removed. **Do not add arm32-specific code back.** If a comment in
the codebase still says "on arm32" or "same as arm32", treat it
as a TODO to clean up.

## Autonomy — always proceed, never ask permission

**Just start the work. Never ask for permission to proceed.** When
the next step is clear (the next slice in a plan, an obvious fix, a
follow-up), begin it immediately — do NOT stop to ask "want me to
start?", "should I proceed?", "shall I do X next?", or offer a menu
of options when one is clearly best. Pick the best option using your
own judgement and ordering, do it, and report what you did. Reserve
questions for genuine forks where the user's answer changes the work
and you cannot resolve it from the code or sensible defaults — not for
permission, sequencing you've been delegated, or confirmation that a
plan is good. Momentum over check-ins; the PR + the report are how you
keep the user informed, not a pre-flight ask.

## Working with PRs

**Always open a PR for completed work — no exceptions, never ask
first.** Once a change is committed and pushed to its feature
branch, open a PR for it immediately. This includes small
follow-ups, doc-only fixes, and comment corrections — every push
of completed work gets a PR. Do NOT pause to ask "want me to open
a PR?" or "should I open a PR for this?"; opening it IS the
expected action, so just do it. The default flow is always:
branch → commit → push → PR → subscribe. Don't stop at "pushed to
the branch" — finish the loop every time.

When you open a PR, subscribe to its activity (`subscribe_pr_activity`)
without being asked. The user prefers to be alerted via the subscription
flow rather than driving manual CI checks after the fact.

**The full loop is: branch → commit → push → PR → subscribe → watch CI
**and mergeability** → auto-merge when green → move on to the next
task.** Do NOT stop and wait after opening the PR, and do NOT ask whether
to merge. Once CI is green on the PR, merge it automatically (squash) and
immediately pick up the next slice of work. If CI fails, diagnose and
push fixes on the same branch until it's green, then merge. Only pause
for the user on a genuine fork (an ambiguous review comment, an
architectural decision) — never for permission to open, merge, or
continue.

**Every PR check covers BOTH halves: is CI green, and is it still
mergeable?** A PR can be perfectly green and unmergeable, and webhooks do
not reliably announce the transition — main moves under you. So on every
wake-up read the PR's `mergeable_state` alongside its checks and resolve
a conflict the same way you'd fix a red build: without being asked.

- `dirty` = conflicts. Fix it yourself; only ask the user when both sides
  genuinely changed the same logic and picking one loses behaviour.
- `behind` / stale base = merge or rebase main in and push.
- `unstable` = mergeable, checks still running or non-required ones
  failing. Keep waiting.

**PRs here are SQUASH-merged, which is the usual cause of a conflict on
these branches.** A squash gives main a *new* SHA for content your branch
still carries under its original commit, so continuing to build on the
pre-squash commit makes git see two independent creations of the same
file — an add/add conflict, even though nothing really diverged. After
any PR merges, start follow-up work from a fresh base:

```
git fetch origin main && git checkout -B <branch> origin/main
```

If you only notice after committing, reset onto main and replay just the
new commits (`git checkout -B <branch> origin/main && git cherry-pick
<sha>`) rather than hand-resolving conflict markers between a file and
its own squashed copy — then force-with-lease push.

### Project goal / roadmap (what "the next task" means)

The standing objective is, in order:

1. **A full IR implementation for the *entire* language in the
   self-hosted compiler.** Today only a subset lowers through the IR
   path (`irlower.fern` → `asm_ir.fern`); anything outside the subset
   falls back to the legacy AST→asm emitters (`asm.fern` /
   `asm_arm64.fern` / `wasm.fern`). Each task should widen the IR
   subset until the AST fallback is never taken, so the legacy AST
   backends can eventually be retired. When a feature works in the
   native compiler and the self-host IR path but not the legacy
   AST→asm backend, that legacy gap does **not** need fixing.
2. **Then port the native Perceus implementation to the self-hosted
   compiler** (reference-counting / ownership: inc/dec insertion,
   borrow inference, drop specialisation, reuse analysis), so the
   self-hosted compiler matches the native one's memory management.

When a PR merges and there's no more specific instruction, the default
next task is the next increment toward goal 1 (then goal 2).

**Native convergence policy — reference on any native-touching work.**
`docs/NATIVE-CONVERGENCE.md` governs how `internal/` (native) and the
self-host compiler are meant to converge instead of drifting forever:
after goal 2 reaches parity and the freeze preconditions go green,
`internal/` accepts only bugfixes, oracle needs, and whatever the
self-host sources require to bootstrap ("Go 1.4 rule") — new language
features land self-host-first/only from that point on. Until the
freeze fires, treat every new native-only feature as a debt entry, not
a free win, and prefer landing new surface self-host-first where the
fixpoint allows it. Issue #4451 is the standing tracker for the freeze
preconditions — reference it from any issue/PR that adds native-only
surface (`internal/ir`, `internal/interp`, the codegen backends) or
touches the differential/parity suites, so the debt stays visible in
one place instead of tribal knowledge.

**Finding real IR-subset gaps (probing methodology — learned the hard
way).** The *per-function* IR subset is now mature: most valid
constructs (closures incl. nested, matches incl. guards/nested,
generics, `dyn` traits, `try`/`?`, tuples, iterators) already lower.
When hunting for a gap, route a probe program through the single-program
path-probe driver (`asm_pathprobe_run.fern`, which prints `ir`/`ast`) —
**but** that driver routes *invalid* programs to `ast` too, so a bare
"`ast`" verdict is NOT proof of a real gap. Always confirm the probe
program is **native-valid first** (`go build -o /tmp/fern ./cmd/fern`
then `/tmp/fern -interp prog.fern`) before treating a bail as a gap.
Most apparent "gaps" turn out to be invalid programs — wrong keyword
(match guards are `when`, not `if`), checker-rejected (a bare-ident
arm on a SCALAR match is E035 — i32 matches only accept literals or
`_`), parse errors (arrow lambdas take an EXPRESSION body, not a block:
`(x) => x+1`, not `(x) => { … }`), or missing imports (the path-probe
driver resolves no stdlib, so anything needing `std/iter`/`std/map`/…
falsely reads `ast`). The genuine remaining AST fallbacks are
documented, not probe-findable: the ~512-function merged-bundle budget
(the bootstrap self-compile, tied to the native large-tier freelist
#3425) and the **deferred wasm-IR exclusions** in `wasm_eligible`
(`wasm_ir.fern`). The per-function IR subset itself is mature: the
runtime-helper migration to Fern is complete (chr, str_concat,
i32_to_string, str_to_upper/lower, str_repeat, str_reverse, str_replace,
string_from_bytes, str_split all lower as Fern functions via the
raw-memory intrinsics), and the wasm-IR runtime gaps that were once
listed here are now **closed**: the filesystem ops
(`stat`/`read_dir`/`remove_file`/`remove_dir_all`/`temp_dir`) lower on
the wasm IR path with IR-side struct-box construction (module type-ids
via `struct_type_id`; see `TestSelfHostStatIRWasm` et al.), and the libm
transcendentals (`fexp`/`flog`/`fsin`/`fcos`/`fpow`) lower via
polynomial-approx WAT helpers (`wasm.exp_func`/`log_func`/`pow_func`/…,
the wasm siblings of the arm64 helpers, wired in `wasm_ir_run`). The
wasm-IR exclusion that genuinely REMAINS is the deferral-gate set in
`wasm_ir_deferrals_ok`: erased-wide (an i64/f64 value through a bare-
typevar param), now PARTIALLY closed (#5464) — a wide value through a
bare-typevar-RETURN PASS-THROUGH fn (`id[T](x:T):T`, #5586) OR a bare-TUPLE-
return fn (`pair[K,V](k,v):(K,V)`, #5593) lowers on the wasm IR path (erased
params/returns/locals typed i64, the uniform 8-byte slot; the caller coerces
its arg/result at the boundary — see `is_erased_typevar` / `erased_widenable`
/ `erased_passthrough_safe` (the body-safety gate that keeps `fold`-style
bodies that USE the typevar off the widened path), the `for_wasm` flag folded
into `ret_arrdyn` bit 2, the `'6'` `fn_param_sigs` flag +
`callee_param_is_erased_widened`, and the result-narrow in lower_expr).
Tuples were tractable because their wasm box is ALREADY uniform 8-byte-per-
element AND an erased `(T,T)` has byte-identical layout to a concrete
`(i64,i32)` (the reader reads each element at its concrete width from the same
`N*8` offset — no result narrow). The single-type-arg erased-wide containers —
`Option[T]`, `T[]`, AND single-typevar `Result` returns — are now CLOSED (#5464
container slice) by MONOMORPHIZING them rather than widening: parser targeted
promotion (clause (c) in `parse_func`, `has_bare_scalar_param` +
`feeds_wide_container`, guarded by `all_tp_count == 1`) promotes an erased
`some1[T](x: T): Option[T]` / `dup[T](x: T): T[]` / `okr[T](x: T): Result[T,
string]` to BOUNDED, so
`monomorphize_module` clones it per concrete instantiation
(`some1__i64(x: i64): Option[i64]` with the concrete 16B box). After cloning no
call passes a wide value through a bare-typevar param, so `module_erased_wide`
clears; `wasm_ir_run`'s `mono_ok` rescue then admits the module by judging
eligibility on the SAME monomorphised module it emits (only when the raw
verdicts both defer, so existing programs keep their exact IR/AST verdict and
byte-identical output). A bare wide LITERAL binds the clone's `T` by magnitude
(`mono_infer` → `literal_is_i64`, mirroring `infer_expr_width` in lowering), so
`some1(5000000000)` clones `some1__i64` not the truncating `__i32`. This dodges
the box-layout-shift problem (an Option is 8B/@4 for i32 but 16B/@8 for i64/f64;
an array stride is 4 vs 8): the CONCRETE clone has an unambiguous box the IR path
already lowers on every backend. Byte-identity-safe because the stdlib generics
that match (`array.intersperse` / `async.gather` — the only bare-scalar-param +
`T[]`-return shapes) are DCE'd uncalled in the bootstrap. Pinned by
`TestSelfHostErasedWideContainerWasm`. The `all_tp_count == 1` guard is also what
makes `Result` SOUND: a `Result` matched by `feeds_wide_container` is only
promoted when the type var is the fn's ONLY one, so the clone is fully concrete
(`okr__i64: Result[i64, string]`, `Result[T, T]` → `Result[i64, i64]`,
`errg[E]: Result[i32, E]` → `Result[i32, i64]`). It also blocks the
partial-promotion hazard: a multi-typevar generic where only ONE var matches
clause (c) — `scan[T, A](xs: T[], init: A, f: (A, T) => A): A[]` (A matches, T
doesn't) — would otherwise clone with an erased sibling `T`, a malformed clone
that crashes (caught by `array_hof`). The genuinely two-typevar `Result[T, E]`
(`okg[T, E](x: T): Result[T, E]`, `all_tp_count == 2`) STAYS deferred: promoting
only `T` leaves `E` erased in the clone (`Result[i64, E]`), reintroducing the
width ambiguity on the Err arm — closing it needs binding `E` from the call-site
return annotation too (a follow-up). (`c_call` is no longer deferred — FFI
`__c_call<n>` has no wasm C ABI, so it is now a clean error endpoint,
rejected before emit by `wasm_unsupported_builtin` like `subprocess` /
`timer_fd`, #4375.) (`open_file` / `writer_write` — the
open_reader/open_writer/open_appender + Writer.write streaming file I/O —
now lower on the wasm IR path: `$__fern_open_file` does path_open under
preopen fd 3, `$__fern_writer_write` fd_writes the bytes; #4372. `xs.join` is
no longer deferred
— it lowers on the wasm IR path via the `$__fern_arr_str_join` shim over
the `$__fern_str_join` WAT worker; #5328. 8-byte-VALUE maps — i64/u64 AND
f64 — now lower end-to-end, `set` / `get_or` / `values` / `get` / `iter`,
via `$__fern_map_{set,get_or,values,get,iter}_w64`, which box the 8-byte
value into an rc cell riding the i32 value column (get → a 16-byte Option,
values/iter → a fresh 8-byte-element array), selected by the `widekind` op
flag; f64 rides the same raw-byte cells as i64/u64 with an f64↔i64
reinterpret at the scalar sites. The former `module_has_wide_map_val_cached`
gate is retired. #5253.) The component-model async / readiness /
socket set that was once listed here — `poll` / `timer_fd` /
`wasm_timer_pollable` / `wasm_pollable_drop` / `tcp_*` / `subprocess` —
**has since LANDED** (the sub-issues #4315–#4320 are all closed, 2026-07):
poll / timer-pollable / block / wasm_poll / tcp_* lower on the wasm IR
path via the component-model wasi interfaces, and subprocess / timer_fd
are clean "unsupported on wasm" error endpoints (`wasm_unsupported_builtin`).
These are **no longer an avoid-list item.** Net: the tractable goal-1
IR-widening work is essentially done. **Goal 2 (the Perceus port) is ALSO
substantially complete** — despite what older notes here said, constructor
reuse is implemented and enabled in the self-host (self-overwrite,
cross-local, enum-donor, consuming-match, tuple reuse, nested-struct
fields), exercised by the byte-identical self-compile; see
`docs/SELFHOST-PERCEUS-REUSE.md`'s correction header. The remaining reuse
deltas are MARGINAL (struct reuse with enum / Map / closure / tuple
pointer fields — §3 Delta B). The real remaining frontier for retiring
the legacy AST emitters is the per-module epic's step 5 (#3457, still
OPEN) — see **`docs/SELFHOST-AST-RETIREMENT.md`** for the code-verified
slice plan (slice 1 done; **#3425 — the self-host-runtime memory leak
that gated 2/3/5 — is now CLOSED**: the native large-tier freelist was
ported to the self-host runtime, x86 `asm_ir.fern` #5609 + arm64
`asm_arm64.fern` #5614, and `TestSelfHostPerModuleFixpointX86_64`
(env-gated `RUN_PERMODULE_FIXPOINT=1`) is GREEN — a self-host-BUILT
compiler per-module-emits all 35 units with no arena OOM, gen0==gen1
byte-identical, ~7.6 GB peak/window under the 8 GiB arena. Slice 2 is
now unblocked — the only residual is the gen1 per-module fixpoint's CI
*time* (serial ~16.6 min), addressed by memory-budgeted parallel emit,
not a memory wall; 4 is a ~5k-line arm64/wasm runtime duplication that
unlocks no deletion on its own). Its wasm component-model sub-issues
(#4315–#4320) are now ALL closed, so it is **no longer blocked on them** —
verify its current blockers directly before picking it up rather than assuming it is gated.
When picking up "the next task", VERIFY tracker
state against the code first: issues #4451/#4363/#4346 have repeatedly
lagged reality (the checker-codes filter is already deleted, the
ill_typed_hint fallback already landed, the arm64 per-module
close_needs UAF is already fixed and regression-guarded).
**Caveat (2026-07): runtime-correctness probing still pays.** The
path-probe drivers only tell you WHERE a program lowers, not whether the
lowered code is RIGHT. Differential probing (native `-interp` exit code
vs the self-host-IR-compiled binary's) found a whole closure-dispatch
bug cluster the probe methodology above misses — escaping-closure /
closure-array shapes that lowered on the IR path but SIGSEGV'd or
silently miscompiled at runtime (#5001/#5007/#5009/#5026 + the
param/branch/transitive/direct-index fixes). The once-known **fn-typed
tuple elements** gap is now CLOSED end-to-end: parse_type_name only
coarsens a parenthesized `=>` type to "fn" when there is NO depth-1
comma (a tuple's fn segments coarsen individually via coarsen_fn_elems
→ "(fn, i32)"), the lift pass wraps every fn-valued tuple element into
a `__mkclo$` env box, and irlower's "clo" element tag drives env-first
`t.N(args)` dispatch + closure-local binding (self-host side pinned by
`TestSelfHostTupleFnIR*`, native side by
`internal/e2e/tuple_fn_elem_test.go`).

## Engineering bar (non-negotiable)

- **Confirm passing tests before opening a PR.** Run the full relevant
  suite locally (including the WASM e2e tests — the pinned toolchain is
  **wasmtime v46.0.1 + wasm-tools 1.253.0** (see
  `.github/actions/setup-fern/action.yml`; the `.claude/hooks/session-start.sh`
  hook installs them locally under `~/.fern-wasm/`). Export the binaries onto
  `PATH` and set `FERN_WASI_ADAPTER` to the preview1 adapter so the e2e tests
  don't SKIP). If a test SKIPs, treat that as a missing dependency to fix, not
  a green light. **The WASI Preview-3 async/stream/future component tests are
  wasmtime-version-sensitive**: v46 changed the component-model-async ABI (async
  functype tag `0x43`; non-reentrant component instances → the async-import
  composer emits a sibling-nested structure). Running them under an older
  wasmtime (e.g. a system v37/v39) fails with `invalid leading byte (0x43)` or
  `cannot enter component instance` — use the pinned v46.
  **Do NOT run `internal/e2eselfhost` unsharded — shard it.** The e2e suite
  is split (#4398 part 3) into `internal/e2eselfhost` (the `TestSelfHost*`
  suite, ~575 files) and `internal/e2e` (everything else + ~30 residual
  `TestSelfHost*` legs in mixed native/selfhost fixture files), with the
  shared harness in `internal/e2eharness` (each package re-binds the harness
  names via its `harness_aliases_test.go`, so test code keeps bare
  identifiers like `buildSelfHostBin`).
  **Measured 2026-07-28: `internal/e2eselfhost` unsharded exceeds 90
  MINUTES** — `-timeout 90m` still panicked with tests queued
  (`TestSelfHostStdTestE2EArm64` 16s in). The old "~10+ minutes, pass
  `-timeout 30m`" advice here was ~9× stale and costs you a wasted
  hour-and-a-half per attempt; it is what this note replaces. Use
  `scripts/selfhost-shard-tests SHARD NSHARD < test-list`, the same
  duration-weighted LPT partition CI uses.
  **Measured 4-way, shard 0: 48 min (green).** So sharding pays only if you
  run ONE shard — four in sequence is ~3.2 h, worse than the unsharded run it
  replaces. Run them in parallel only if RAM allows: each heavy driver build
  reserves ~4.3 GB through `buildMemLimiter`, so a 16 GB host fits about two
  concurrently, not four. For ordinary local work prefer `-run` targeting the
  tests you actually touched, and leave whole-package sweeps to CI, which
  shards across machines.
  **`internal/e2e` no longer fits in one invocation at `-timeout 45m`** —
  measured 2026-07-29 on a 4-core / 15 GB host: two runs, one of them with
  the host entirely to itself, both hit the 2700 s wall with **zero
  `--- FAIL` lines**. The old note here said "~1760 s / ~30 min"; that is
  stale, and believing it costs 45 minutes per attempt plus the time spent
  deciding whether the timeout was a regression. It is not: a timed-out run
  panics with a goroutine dump and prints `FAIL`, but the dump shows the
  suite parked in `withBuildMemory` (the `buildMemLimiter` RAM semaphore)
  waiting to start a heavy self-host driver build, or mid-`runLangInterp`.
  Always check the `--- FAIL` count before reading a timeout as a breakage.
  **Never pipe a test run through `tail`/`head`.** `go test … 2>&1 | tail -3`
  reports the PIPELINE's exit status — `tail`'s, which is always 0 — so a
  failing suite is announced as a success, and the failure detail is discarded
  with the rest of the output. Measured 2026-07-30: a `FAIL` on
  `internal/e2e` was reported as a completed-successfully background task, and
  the three surviving lines could not say which test failed (it did not
  reproduce in dedicated re-runs on either the same or the following commit, so
  the cause is now unrecoverable). Redirect to a file and grep it —
  `go test … > run.log 2>&1; echo "EXIT=$?"` — then read `--- FAIL` from the
  file.
  Prefer `-run` targeting what you touched; if you do want the whole
  package, give it `-timeout 90m` and expect it to be the only thing
  running. Core count matters more than RAM here — the semaphore serialises
  the heavy builds regardless of how much memory is free.
  CI doesn't hit the wall — it shards across the `test-e2e-*` workflows,
  each well under its job timeout.
- **Self-host driver builds are memory-heavy but the harness now self-limits
  — swap is generally NOT needed.** Every `buildSelfHostBin` / `buildBin` of
  a self-host driver (`asm_run.fern` / `asm_load_run.fern` / `asm_ir_run.fern`
  / `wasm_ir_run.fern` / …) emits a multi-thousand-function asm text. The
  peak is comfortably under a 16 GB host:
  - The x86-64 backend flips the self-host compiler's one lowering
    monster (`irlower__lower_expr`, ~9.75M IR ops) from the inline rc
    fast-path to the `call __fern_rc_dec/inc` form (the arm64 backend's
    long-standing `rcInlineOK` mechanism, backported — behaviour-identical).
    That cut the `asm_ir_run` driver asm from ~1028 MB → ~470 MB.
  - The cold driver emit runs under a **refcounted soft heap cap**
    (`withEmitMemLimit`, `FERN_EMIT_MEMLIMIT_MB`, default 3600; `<= 0`
    disables), `ir.Op` keeps its rare payload fields (Str2 / Sig /
    ArgTypes / CaptureSlots) in an `OpExt` side-table (96 B/op, was
    160), and both native backends **release each function's IR as
    it is emitted** (`ip.Funcs[i] = nil` in the emit loop) so the IR is
    reclaimed incrementally instead of peaking alongside the output
    buffer: at default GOGC the emit ballooned to ~9 GB RSS of mostly-
    garbage in 134 s; capped + shrunk + released it runs ~3.7 GB in
    ~40 s. Output is byte-identical.
  - Driver binaries are **assembled + linked in-process** by the pure-Go
    native assembler (`internal/native/x86_64` + `internal/native/elf` —
    the same pipeline `cmd/fern -target x86-64` uses by default): ~25 s /
    ~2.6 GB where GNU `as` took ~36 s at ~4.7 GB plus a link, and the
    ~470 MB `.s` never touches disk. Any assembler error falls back to
    the old gcc(+lld) path automatically. `CachedLink` does the same for
    HUGE self-host-emitted asm (the stage-2 self-compile, ≥ 8 MB) —
    which previously ran GNU `as`+bfd with no memory reservation at all
    — while small program links stay on gcc/bfd unchanged.
  - `internal/e2eharness`'s `buildMemLimiter` is a RAM-budget weighted
    semaphore around the cold emit+link: it reserves each heavy build's
    estimated peak (`FERN_BUILD_HEAVY_MB`, default 4300) against a budget
    (`FERN_BUILD_MEM_BUDGET_MB`, default ~85% of `MemTotal`), so heavy
    builds can't stack past the host's RAM and OOM the run. Two cold
    driver builds now fit a 16 GB host concurrently; bigger hosts
    parallelise further up to the budget.

  If a build is still OOM-killed (`signal: killed` / **exit 137** during
  the Go emit, or `as`/gcc on the fallback path — reads like a test failure
  but is an OOM), lower `FERN_BUILD_HEAVY_MB` / `FERN_BUILD_MEM_BUDGET_MB`
  / `FERN_EMIT_MEMLIMIT_MB`, or re-create the ephemeral swap file (a
  container restart wipes it):
  ```
  fallocate -l 8G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
  ```
  **BUT exit 137 from a *running* Fern-compiled binary is usually NOT an
  OOM-kill**: `__fern_alloc`'s bounds check deliberately `exit(137)`s when
  the fixed bump arena (x86 8 GiB, arm64 8 GiB) is exhausted — a REAL
  failure, reproducible locally, that masquerades as SIGKILL. The stage-2
  self-compile (gen1/mmc2 in the fixpoint tests) is the usual victim: the
  self-host-built compiler's live set grows with every compiler-source
  addition, and when it hits the arena wall the test "OOMs" on CI with no
  kernel OOM anywhere. Before writing off a 137 as infra, check WHICH
  process died: `as`/gcc/the Go emit during a build = host-RAM OOM (lower the
  budget knobs / add swap); a Fern binary mid-run = arena exhaustion (measure
  with /proc RSS vs the arena size — see docs/RC-PERCEUS-SELF-HOST-PORT.md,
  2026-07-11 entry). Host-RAM OOM is *total-RAM* pressure, not a cgroup cap
  (`memory.limit_in_bytes` is effectively unlimited). Also keep `/` from
  filling — stale `/tmp/selfhost-bincache-*` dirs (~1 GB each, one per build)
  pile up; `rm -rf /tmp/selfhost-bincache-*` reclaims them (regenerable
  caches).
- **Don't run arm64 / `qemu-aarch64` tests locally as a gate — let CI
  run them.** The aarch64 e2e + fixpoint tests under `qemu-aarch64` are
  the slow part of a local sweep (minutes). Locally, gate on the
  **x86-64** equivalents (fixpoint, checker, CLI, e2e) plus the WASM
  tests, which give the same signal far faster; CI runs the full arm64
  matrix on every push. Reach for qemu locally only to **debug** a
  specific arm64 failure CI surfaced, not as a pre-push gate. (The two
  self-host asm backends now **share** their entire target-independent
  frontend — the `Ty` type system, type inference, the pre-codegen
  checker, and `EmitState` + its state methods — via
  `examples/self_host/asmcore.fern`, which both `asm.fern` (x86-64) and
  `asm_arm64.fern` import. That half can't drift; only the `emit_*`
  instruction-selection layer is hand-maintained in parallel. So an
  x86-64-green change is almost always arm64-green; CI is the backstop.
  When editing inference/checker/`Ty`/`EmitState`, edit `asmcore.fern`
  once — it is *not* mirrored in the backends anymore. Anything compiling
  `asm.fern`/`asm_arm64.fern` must also provide `asmcore.fern`.)
- **Local CI sign-off (skip lanes you've already run).** CI supports
  Basecamp `gh-signoff`: after running a suite locally on the exact
  commit you're pushing, `scripts/signoff <step>` (or `--local` for the
  whole x86-64/wasm-verifiable set) posts a `signoff/<step>` commit
  status that the matching workflow's `gate` job reads and skips that
  lane. Sign-offs pin to the SHA (a new commit re-enables CI), only
  collaborators can post them (fork PRs always run), and the gate fails
  open. Don't sign off `e2e-arm64` unless you actually ran it on arm64.
  Full details + the step→lane map: `docs/CI-SIGNOFF.md`.
- **Every new feature ships with tests.** Parser-time desugar →
  parser test. Checker rule → checker test. Runtime behaviour →
  e2e test. No "the next PR will add coverage."
- **Never regress.** Re-run the full suite after every change, not
  just the targeted test for the new code.
- **Fix bugs you find on the way.** If exploring for one feature
  surfaces a separate bug (e.g. the f-string `__str_concat`
  helper-emission gap, the `for x in m { ... }` struct-lit clash),
  fix it in the same PR with its own test rather than leaving it
  for later.

## Erasure — deletion is half the job

Coding agents add by reflex and rarely subtract. Counteract this constantly:
a diff that removes lines is at least as valuable as one that adds them.

- **Swap rule.** When a task replaces X with Y, fully deleting X is part of
  the task. No "keep the old path for compatibility" unless explicitly
  requested. "Lambda is `\x.f` now, not `λx.f`" — bad: parser accepts both;
  good: `λx.f` is gone from parser, tests, and docs. Bug fix — bad: a
  special-case `if` shields the symptom; good: the cause dies. Behavior
  change — bad: old-behavior tests linger or get dodged; good: deleted.
- **Comments.** No narration comments inside function bodies. A refactor
  that makes a comment stale deletes or rewrites it in the same diff. A
  done TODO leaves with the fix. Same for stale notes in docs/ and this file.
- **New code.** Scan for an existing concept to reuse before introducing a
  new one; find the simplest shape. If something confused you while reading,
  that's a bad abstraction — untangle it if it's in your blast radius.
- **Scope.** Full force on everything the current task touches. Deleting
  beyond that (retiring a backend, unifying parallel emit layers) is a
  roadmap decision — propose it, don't drive-by it. The asm backends'
  emit layers are deliberately parallel; the legacy AST emitters retire
  via #3457, not opportunistically.
- Before finishing any task: what did this change make obsolete, and did
  I delete it?

## Literate programming

Fern supports Knuth-style literate programming via `.fern.md`
documents — see `docs/LITERATE.md`. The engine is
`internal/literate` (`Parse` → `Document`; `(*Document).Tangle`
expands the root chunk `<<*>>` and returns generated Fern source
plus a per-line provenance map; `(*Document).Weave` renders
cross-referenced Markdown). Chunks are `<<name>>=` definition
blocks referenced by `<<name>>` lines; references resolve out of
document order, same-name definitions concatenate, a reference's
indentation is prepended to its expansion, and headerless `fern`
fences are display-only (woven, not tangled).

A document can also tangle to **multiple modules**: a fence with a
`file=PATH` directive is a file-root (`internal/literate/tanglefiles.go`
— `TangleFiles` returns one `FileResult{Path,Code,LineMap,IsEntry}`
per output path; `EntryFile` resolves the compile entry). The generated
modules `import` each other normally; `loadMultiFileEntry` in
`cmd/fern/main.go` feeds them all to `modload.LoadWith` as virtual-file
overrides (keyed by path relative to the document dir) and loads from
the entry. `expandBody`/`expandChunk` are the shared recursion behind
both `Tangle` (root chunk) and `TangleFiles` (file-root bodies).

A `.fern` (or `.fern.md`) can also **import** a single-root `.fern.md`
as a library: `modload.readModuleSource` falls back to a sibling
`.fern.md` when a `.fern` import target is missing, tangles it, and
`LoadWithLiterate` returns the per-module `LiterateModule{DocPath,
DocSrc,LineMap}` provenance so the CLI's `entry.remaps` (now a
per-module `litRemap{docPath,docSrc,remap}`) maps an imported library's
diagnostics onto *its* document. Plain `.fern` wins over `.fern.md`;
importing a multi-file (`file=`) document is an error.

The CLI exposes `-tangle` / `-weave` (each takes `-o`; `-weave -html`
emits a styled HTML page), and `loadEntry` in
`cmd/fern/main.go` tangles a `.fern.md` entry in memory before the
normal compile/`-check`/`-interp` pipeline. Diagnostics are remapped
back onto the document through `diag.FormatRemapped` (the literate-only
sibling of `diag.Format`, which applies a position remap built from the
tangle line map); for multi-file docs the `entry` struct holds a
per-module `remaps` map so an error in any generated file lands on the
right `.fern.md` line. Coverage: `internal/literate/*_test.go` (incl.
`tanglefiles_test.go`), `diag` FormatRemapped tests, and
`internal/e2e/literate_test.go` + `literate_multifile_test.go` (interp
+ tangle + weave + the single- and multi-file diagnostic-remap
contracts). Examples: `examples/literate/fizzbuzz.fern.md` (single) and
`multi_module.fern.md` (multi). When extending the chunk grammar or the
remap, add cases at the layer you touch — the diagnostic-remap
(generated line → document line) is the most regression-prone surface,
mirroring the test-runner contract below.

## Test runner

`internal/stdlib/std/test.fern` is the pure-Fern test runner —
the shape the project plans to migrate to once the compiler is
self-hosted and Go-side `*_test.go` files retire. With the
auto-prelude gone (Phase 5), test programs `import "std/test";`
and call its functions qualified (`test.test_new`,
`test.assert_eq`, `test.fail`, …) with the runner type
written `test.TestRunner`; receiver methods (`.it`, `.finish`)
stay bare. Output is
TAP-13. Examples under `examples/tests/` (`arithmetic_test.fern`,
`strings_test.fern`, `runner_self_test.fern`) — the self-test
walks every assertion helper on both pass + fail paths. The
Go-side gate that keeps the runner from regressing lives at
`internal/e2e/test_runner_test.go`.

When adding NEW assertion helpers or extending the runner,
add a corresponding case to `runner_self_test.fern` covering
both the passing and failing path — the failure-reporting
contract (predicate name in the message, actual + expected
both quoted) is the runner's most regression-prone surface.

Module loading: there is no prelude injector anymore — a program
sees only what it `import`s. `modload` loads each imported
`std/`/`core/` module (and its transitive imports), mangles
non-entry decls to `<mod>__name`, and rewrites qualified call
sites; `ast.Program.LoadedStdlibPaths` records what was loaded so
a module pulled in twice (directly + transitively) dedupes rather
than redeclaring methods. That dedupe also closed an older bug
where an explicit `import "std/foo";` of a module that
transitively imports another (e.g. `std/json` → `core/int`) sent
bare-name method dispatch (`(n).to_string()`) through the mangled
`int__int_to_string` name and crashed the interpreter with "cast
from interp.Array to i32 not supported". It's fixed and guarded by
`TestInterpScriptInteropIntToStringViaMangling`
(`internal/e2e/interp_script_test.go`), which exercises the
explicit-import, transitive-import, and qualified-call shapes —
extend it if you touch the mangling / alias path.

In-memory source (stdin, REPL, the wasm playground bundle) loads
through `modload.LoadSource`, not bare `parser.Parse`, so those
paths resolve stdlib imports the same way the file-based driver
does.
