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
TS for inspiration. The stated use cases are small fast-startup CLI tools and
short-lived edge-function-style HTTP servers, so cribbing from Roc, MoonBit,
Rust, Zig, Go is more productive than reaching for TS conventions when they
don't fit.

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
→ auto-merge when green → move on to the next task.** Do NOT stop and
wait after opening the PR, and do NOT ask whether to merge. Once CI is
green on the PR, merge it automatically (squash) and immediately pick up
the next slice of work. If CI fails, diagnose and push fixes on the same
branch until it's green, then merge. Only pause for the user on a genuine
fork (an ambiguous review comment, an architectural decision) — never for
permission to open, merge, or continue.

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
wasm-IR exclusions that genuinely REMAIN are all the async / readiness /
socket set — `poll` / `timer_fd` / `wasm_timer_pollable` /
`wasm_pollable_drop` / `tcp_*` / `subprocess` — which need the
component-model wasi interfaces the bare core-wasm+preview1 backend
doesn't wire; these are actively worked in parallel, **avoid**. Net: the
tractable goal-1 IR-widening work is essentially done; the next frontier
is **goal 2** (the Perceus port — reuse analysis is the remaining large,
memory-safety-critical piece; inc/dec, borrow, drop-specialisation, and
per-type struct-drop / field-reclaim slices already landed).

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
  **Pass `-timeout 30m` when running a *whole* e2e package in one `go test`
  invocation:** the e2e suite is split (#4398 part 3) into
  `internal/e2eselfhost` (the `TestSelfHost*` suite, ~575 files) and
  `internal/e2e` (everything else + ~30 residual `TestSelfHost*` legs in
  mixed native/selfhost fixture files), with the shared harness in
  `internal/e2eharness` (each package re-binds the harness names via its
  `harness_aliases_test.go`, so test code keeps bare identifiers like
  `buildSelfHostBin`). Either package run unsharded takes ~10+ minutes
  (incl. arm64/qemu + wasm), just over `go test`'s default 600s `-timeout`,
  so the default aborts it with a `panic: test timed out` that reads like a
  failure but is not one. CI doesn't hit this — it shards by test-name
  regex across the `test-e2e-*` workflows (the selfhost lane round-robins
  the union of both packages' `TestSelfHost*` lists), each well under its
  10-min job timeout.
- **Self-host driver builds peak at ~16–18 GB RAM — enable swap if they
  get OOM-killed.** Every `buildSelfHostBin` / `buildBin` of a self-host
  driver (`asm_run.fern` / `asm_load_run.fern` / `asm_ir_run.fern` /
  `wasm_ir_run.fern` / …) assembles a multi-thousand-function `.s`, and
  `as` alone spikes to ~8 GB; with the `go test` package compile plus a
  second concurrent `as`, the peak crosses ~16 GB. On the ~15 GB-RAM
  container with **no swap** this trips the kernel OOM-killer: the build
  dies with `signal: killed` / **exit 137** (reads like a test failure but
  is an OOM — not a real failure) and can even bounce the whole container.
  **BUT exit 137 from a *running* Fern-compiled binary is usually NOT an
  OOM-kill**: `__fern_alloc`'s bounds check deliberately `exit(137)`s when
  the fixed bump arena (x86 8 GiB, arm64 8 GiB) is exhausted — a REAL
  failure, reproducible locally, that masquerades as SIGKILL. The stage-2
  self-compile (gen1/mmc2 in the fixpoint tests) is the usual victim: the
  self-host-built compiler's live set grows with every compiler-source
  addition, and when it hits the arena wall the test "OOMs" on CI with no
  kernel OOM anywhere. Before writing off a 137 as infra, check WHICH
  process died: `as`/gcc during a build = OOM (rerun/swap); a Fern binary
  mid-run = arena exhaustion (measure with /proc RSS vs the arena size —
  see docs/RC-PERCEUS-SELF-HOST-PORT.md, 2026-07-11 entry).
  It is *total-RAM* pressure, not a cgroup cap (`memory.limit_in_bytes` is
  effectively unlimited), so swap fixes it. Re-create swap each session if
  self-host builds start OOM-ing (it's ephemeral — a container restart
  wipes it):
  ```
  fallocate -l 8G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
  ```
  Watch with `free -m` mid-build: a healthy run pushes a few GB into swap
  at the assembler peak and completes. Also keep `/` from filling — stale
  `/tmp/selfhost-bincache-*` dirs (~1 GB each, one per build) pile up;
  `rm -rf /tmp/selfhost-bincache-*` reclaims them (regenerable caches), and
  a swapfile needs the free space.
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
