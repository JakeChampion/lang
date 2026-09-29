# Native convergence: freeze native as the stage-0 bootstrap + oracle

**Status:** FROZEN as of 2026-09-28 — every precondition below is green;
`NATIVE-FREEZE.md` records the gate state on that day. (Policy adopted
2026-07-03 with the freeze deferred behind the preconditions.)
**Owner:** compiler / self-host.

## The question

A single language feature lands in a lot of places today:

- native `internal/ir` (+ the three native backends `codegen/{arm64,x86_64,wasmbin}`)
- native `internal/interp`
- self-host `examples/self_host/irlower.fern` (+ the three self-host backends
  `asm.fern` / `asm_arm64.fern` / `wasm.fern`)
- self-host interp

That double maintenance is the dominant tax on the project. Worse, it is
*open-ended*: every new native-only construct (the 2026-07-01/02 SSA backends
are the canonical example) widens the surface the self-host must eventually
mirror, so the two compilers can drift forever with no defined point at which
they converge. This doc forces the call — like `SSA-DECISION.md` did for the
SSA cutover — by writing down **when native stops being the product and
becomes the toolchain bootstrap**, and **what measurable gates prove parity**
before that flip.

## Decision: native becomes stage-0, but only when the parity gates close

We adopt the convergence *policy* now and defer the *freeze* until it is
earned. Rationale for splitting the two: the policy is the load-bearing part
(it reframes "native is the product" as "native is the bootstrap + oracle"),
but flipping the freeze switch while the differential gates are still sampling
rather than closed would trade one drift hazard for a worse one — a self-host
that is *declared* at parity without the tests to prove it.

### 1. The native feature-freeze point

After the Perceus port (roadmap **goal 2**) reaches parity, `internal/`
accepts only:

- **bugfixes** (correctness, never new surface),
- **oracle needs** (whatever the differential suites require to keep anchoring
  on native semantics), and
- **whatever the self-host sources require to bootstrap** — the "Go 1.4 rule":
  native must forever compile the self-host compiler's *language subset*, and
  that subset may be deliberately conservative to keep the bootstrap small.

New language features then land **self-host-first / self-host-only**, gated by
the whole-compiler fixpoint. Native is no longer where a feature is born; it is
the frozen floor the self-host stands on.

### 2. The parity contract = the differential suites, promoted from sampling to closure

The freeze is only safe once "native and self-host agree" is a *closed*
property, not a spot-check. Three suites carry that contract; each has a
concrete completion criterion:

- **Checker-codes differential** — `checker_codes_run.fern` +
  `internal/e2e/self_host_checker_codes_test.go`. Today it filters the Go
  checker's output through `selfHostImplementedCodes`
  (`self_host_checker_codes_test.go:27-85`) and asserts parity **only** on
  that set (`filterImplemented`, `:124-133`, applied at `:856`, `:979`,
  `:1045`). That filter is an open-ended contract with no completion criterion.
  **Freeze precondition: burn the filter down to empty.** As of this writing
  the Go checker emits every code the map already lists **plus six it does
  not** — the remaining gap is exactly:

  | code | meaning |
  |------|---------|
  | E023 | unknown enum (variant pattern names an undeclared enum) |
  | E032 | `use` clause inference error |
  | E044 | captured variable has an unsupported type |
  | E053 | `fip` function performs a heap allocation |
  | E060 | invalid `as?` downcast target |
  | E062 | ambiguous method on a multi-trait object |

  When `checker.fern` emits all six and `selfHostImplementedCodes` is deleted
  (the differentials comparing raw sets, unfiltered), this precondition is
  green. Each checker-port slice should shrink this table by one or more rows;
  see `docs/SELFHOST-CHECKER-PORT.md`.

- **Fuzz-diff execution oracle** — `FuzzGenerate_ExecutionAgrees`
  (`internal/e2e/diff_oracle_test.go`), run by `.github/workflows/fuzz-diff.yml`,
  alongside the `TestDifferential*` corpus. This becomes the **standing
  regression net**: post-freeze it is the mechanism that catches a self-host
  behavioural divergence from native semantics, so it must stay in CI on every
  push and its corpus must grow with the language, never shrink.

- **SH-057-class semantic gaps** — the enumerated deep semantic divergences
  (mutable scalar captures / #2850 and its siblings). These are freeze
  **blockers**, not filtered-away deltas. They live in one place (this section)
  so "are we at parity?" has a single checklist rather than tribal knowledge.
  Freeze precondition: this list is empty. Known open entries at adoption:
  - ~~mutable scalar captures across closure boundaries (SH-057 / #2850)~~ —
    **closed 2026-08-02 (#5988)**. Worth keeping the note: #2850 was closed once
    already, on the compiled path, while the self-host interpreter still captured
    by value. A class is only closed when EVERY engine implements it.

  Add a row when a new class is found; strike it when a differential test
  pins the fix.

### 3. `internal/interp` is the long-term keeper — even post-freeze

Native `internal/interp` is **not** frozen out of existence. It is the
semantics reference every differential test anchors on, it is cheap to carry
(~3.9k lines), and it is the oracle that makes goal-2 correctness checkable at
all. Post-freeze it keeps receiving the same bugfix / oracle-need updates as
the rest of `internal/`. Retiring the native *backends* is on the table once
the self-host backends reach parity; retiring the native *interpreter* is not.

### 3a. Backend retirement has its own prerequisites, distinct from the freeze

The freeze stops native being where features land. Retiring the native
*backends* is a strictly later and larger step, and it is worth writing down
what it actually needs, because "goal 2 is nearly done" does not imply
"the backends can go":

1. **A bootstrap that does not need them.** ~~`make selfhost-cli` builds the
   self-host compiler with `./bin/fern` — the native backends. There is no
   checked-in stage0 snapshot and no `make bootstrap` / `make distcheck`.~~
   **Closed on Linux (2026-09-28):** `make bootstrap` takes a pinned earlier
   compiler (`bootstrap/stage0.lock`, a release asset per host, sha256-pinned),
   compiles the current compiler with it, smoke-tests the result and installs it
   — no Go, no native backend on the path, proven by a CI job with no Go setup.
   `make distcheck` then has that compiler recompile the compiler, and the
   result do it once more: stage2 and stage3 are byte-identical, at 5.7 GB peak
   on a 16 GB host, in the same CI job. The publish job pins that self-built
   fixed point on both Linux hosts, so the pin a refresh leaves behind is a
   self-built binary; the first pin published this way still has one
   native-built generation in its ancestry (stage2 is stage1's output, and
   stage1 is the native candidate's), the one after it none. What is NOT
   closed: arm64-darwin, whose stage2
   exhausts the arena compiling the compiler (#8479), so its pin is still the
   native-built candidate. `docs/BOOTSTRAP.md`. Ruled out on Linux (heap
   above 4 GiB under qemu, the darwin output path) and measured on every
   `macos-15` round with the compiler's per-phase heap readout:
   `docs/LOCAL-DEV-LOOP.md`, "The stage-1 / stage-2 self-compile on Apple
   Silicon".
2. **Every target self-contained on the self-host side.** ~~As of this
   writing `-target x86-64-linux` stops at GAS text and needs an external
   assembler + linker, where `-target arm64-linux` links in-process.~~ **Closed:**
   `-target x86-64-linux` now assembles and links in-process via `x86_native.fern`
   + `elf.fern`, so both Linux targets produce a finished binary with
   nothing on `$PATH`. `-target x86-64-linux -emit asm` is the escape hatch that still
   emits text.
3. **A decision on the oracle.** ~~`BOOTSTRAP-RESEARCH.md §1` recommends
   *two-implementations-forever* precisely so the fuzz-diff oracle keeps two
   witnesses; that directly contradicts deleting the native backends.~~
   **Decided 2026-09-28: the native backends are not witnesses, and they go
   with the next step after the freeze.** The oracle is `internal/interp`
   (§3), which the differential suites anchor on and which stays. `BOOTSTRAP-RESEARCH.md §1`'s
   recommendation is superseded for the backends and holds for the
   interpreter. A gate that compares self-host codegen output against native
   codegen output (byte-identical emit comparisons, the native half of the
   leak and alloc-count matrices) therefore goes with the backends or is
   re-anchored on the interpreter; that is scope for the deletion PRs, not a
   reason to keep a backend.
4. **The non-compiler consumers.** ~~`internal/wasm/playground` and
   `cmd/fern-wasm` are built on native codegen; the browser playground would
   need the self-host compiler compiled to wasm instead.~~ **Moved
   2026-09-28:** the playground compiles, checks, interprets, shows assembly
   and builds cli/run components on `web/playground.wasm`, the self-host
   compiler built by itself (`PLAYGROUND-SELFHOST-WASM.md`, top), and since
   the same day its wasi:http panes too (#6636, `-target wasm32-wasi-http`).
   What is still on the Go toolchain is the language server (#6641), all
   `cmd/fern-wasm` now carries. **Measured 2026-09-01
   (#6643) — `docs/PLAYGROUND-SELFHOST-WASM.md`:** this is not size- or
   memory-bound. The self-host compiler already runs *as* wasm — a stdin-driven
   wasm-emitting driver is 2.3 MB (614 KB gzipped) against the playground
   bundle's 28.5 MB (6.3 MB), and compiles a program under wasmtime in 1.9 s at
   104 MiB peak. Two of the three blockers that measurement found are closed: the
   nested-arithmetic miscompile was a use-after-free in the compiler's own gate
   passes (#7948), and `internal/codegen/wasmbin` has the `strbuf_*` lowerings it
   was missing (#7951). The third — the driver — compiles now:
   `examples/self_host/playground_run.fern` reads a program on stdin, resolves
   its `std/…` imports out of an embedded bundle through a sealed
   `modloader.Overlay`, and emits a wasm module. Hosted in wasm with **no
   preopens at all** it produces output byte-identical to the natively-hosted
   build. What remains is not compilation: the playground also interprets, and
   `examples/self_host/interp.fern` implements no I/O builtins at all, so its
   output pane has no self-host counterpart — a second missing consumer beside
   the LSP. The native toolchain still cannot compile `fern.fern` for wasm,
   so this artifact still has only one witness — but the blocker is no longer a
   missing lowering. #7947 landed `sleep_ms` and emptied
   `providedMissingLowering`; what refuses now is `write_file_exec`, which needs
   `fsmode`, and E066 declines it because the component-model filesystem has no
   permission bits (#6133). That is a target property rather than a gap, so this
   witness is not one to wait for.

## Freeze preconditions (all must be green before native is frozen)

**Read the live state from `make freeze`, not from this list.** The rows below
are the *definitions*; `tools/freeze_gate.sh` derives every mechanically-checkable
one from the tree and runs in CI on every push. An audit on 2026-08-02 found
this list, the tracker (#4451) and `SELFHOST-PERCEUS-REUSE.md` all carrying
stale claims, and all three stale in the same direction — more pessimistic than
the code. Prose does not re-check itself. Precondition 1 is the only one the
gate cannot cheaply measure — `make distcheck` is its criterion and needs ~6 GB
and ten minutes — so the gate reads it off the CI wiring and otherwise prints
UNVERIFIABLE rather than guessing.

1. Roadmap **goal 2** complete — the Perceus port (inc/dec, borrow inference,
   drop specialisation, reuse analysis) at parity in the self-host compiler.
   **Criterion: `make distcheck` green.** Every other precondition got a
   mechanical definition and went green; this one said "at parity" and left the
   rest to prose, so the gate can only ever print UNVERIFIABLE no matter how
   much work lands. `distcheck` is the measurement that fits: the self-built
   compiler compiling `fern.fern` byte-identically (stage2 == stage3) is the
   whole compiler's own source, the one
   configuration nothing else gates — the per-module fixpoint compiles it eight
   units per process, and §2's suites oracle behaviour, not reclaim. It is
   also §3a's precondition 1 for retiring the backends, so the two stop being
   argued separately. **GREEN as of 2026-09-28**: the self-built compiler
   recompiles the compiler at 5.7 GB peak on a 16 GB host and reproduces
   itself byte for byte (stage2 == stage3), and the check runs in CI on both
   Linux hosts (`docs/BOOTSTRAP.md`). On 2026-09-02 the same step was
   OOM-killed at 13.9 GB, which was the RECLAIM gap goal 2 was about.

   The generated leak matrix is necessary but not sufficient: it reached
   150/150 `clean clean` on both ISAs while `distcheck` was still OOM-killed,
   because it covers the shapes someone enumerated rather than the compiler's
   own code.
2. #3451 / #3457 complete (the bootstrap-budget / bundle prerequisites).
   **GREEN as of 2026-08-02.** #3457 is closed: all three legacy AST→asm
   emitters are deleted — `asm.fern` + `asm_arm64.fern` (#5972) and `wasm.fern`
   (#5983) — every backend routes IR-or-error, and the ~512-function
   merged-bundle budget went with them. #3451 remains open, but only on step 6
   (#3458, incremental codegen / per-module object cache). That is build
   performance, and self-host-only, so it does not bear on the question this
   precondition asks — whether native has stopped being where features land.
   This row's stated end condition was always "ending with the legacy AST
   emitters deleted", and that is met, so the gate scores it on the emitters and
   the IR-or-error routing rather than on the epic being closed. (It was
   previously recorded as HALF green, and #4451 called it "the sole substantial
   remaining gate" while citing a wasm component-model chain, #4315–#4320, that
   had already closed in full.)
3. **Checker-codes filter empty** — `selfHostImplementedCodes` deleted, the
   six-code gap above closed, all three differentials comparing unfiltered
   sets. **GREEN as of 2026-07-12.**
4. **SH-057-class semantics closed** — the blocker list in §2 is empty, each
   former entry pinned by a differential test. **GREEN as of 2026-08-02**: the
   one listed entry, mutable scalar captures (SH-057 / #2850), is closed on both
   engines. The compiled path had it via `box_mutated_scalar_captures`; the
   INTERPRETER still captured by value and returned 8 where the reference says
   49 — #2850 had been closed on the compiled half alone. Fixed in #5988 and
   pinned by `TestSelfHostMutableScalarCaptureInterp`, which oracles every case
   against the native interpreter, plus a row in the cross-validation corpus.

None were date-driven; the last one went green on 2026-09-28 and the freeze
is in force (`NATIVE-FREEZE.md`).

## What a freeze does NOT change

- The differential suites keep running forever — they are the regression net,
  not a one-time gate.
- `internal/interp` keeps getting bugfixes (§3).
- The self-host compiler's *language subset that native must bootstrap* stays
  conservative on purpose; a self-host-only feature is allowed to be
  un-bootstrappable by native as long as it is not on the bootstrap path.

## Maintenance contract now the freeze is in force

- `internal/` accepts bugfixes, oracle needs, and what the self-host sources
  require to bootstrap (§1). A new native-only feature is an exception to
  argue for on #4451, not a free win: it widens the surface the self-host
  must mirror. New language surface lands self-host-first.
- Keep the three differential suites green in CI (they already run).
- `tools/freeze_gate.sh` keeps running in CI so a precondition that regresses
  fails a PR rather than a future audit. When one does, amend its row here
  and the check together.
- New checker rules land self-host-side; `selfHostImplementedCodes` stays
  deleted.
