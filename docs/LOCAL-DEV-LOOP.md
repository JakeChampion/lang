# The local dev loop

Machine-shaped facts: how long things take on a dev box, how much RAM they
want, which knobs move that, and which instrument to measure with. `CLAUDE.md`
states the *rules* that follow from this; the numbers live here so a
re-measurement updates one place.

Every figure here is dated. **Re-measure before quoting one** — several of
these have been wrong by an order of magnitude in the direction that
discourages using the tool at all, and a stale number costs an hour per
attempt.

## Toolchain: `mise install`

Every pinned tool — Go, wasmtime, wasm-tools, the WASI preview1 adapter,
actionlint, gotestsum — lives in `mise.toml`, locked to exact releases and
checksums in `mise.lock`. Install [mise](https://mise.jdx.dev), then in the
repo:

```
mise install                      # everything, ~6 s once the downloads are cached
eval "$(scripts/toolchain-env)"   # the same, plus PATH / GOROOT / FERN_WASI_ADAPTER exports
make hooks                        # route git hooks through .githooks/
```

`mise activate` in a shell profile makes the tools resolve on their own inside
the repo; `FERN_WASI_ADAPTER` still needs the `toolchain-env` line (the adapter
is a data file, not a program on PATH). To bump a version: edit `mise.toml`,
run `mise lock`, commit both files. CI installs from the same pair through
`jdx/mise-action` and `scripts/devbox` through `scripts/toolchain-env`, so a
version exists in exactly one place. The Netlify deploy sandbox cannot
bootstrap mise, so it provisions Go/Node through netlify.toml and the smoke
lane reads the wasm pins from mise.toml directly. qemu and the cross gcc are
not mise tools: apt (the session hook, on Linux) or `scripts/devbox` (on a
Mac) supply them.

## Git hooks: `.githooks/pre-push`

`make hooks` (the session hook runs it) points `core.hooksPath` at
`.githooks/` and turns **rerere** on for the clone, so a conflict you have
already resolved is replayed rather than re-asked when the same branch is
rebased onto a main that moved again. It is set per-clone because git defaults
it off and it was previously on for one machine only, out of a global
`~/.gitconfig` — no help to CI, a fresh clone, or an agent session in a
container. `rerere.autoUpdate` is left alone: replaying a resolution is safe,
staging it unread is a preference. This is the local loop only —
`auto-rebase-prs.yml` replays open PRs on a fresh runner clone, which has no
cache to hit. `pre-push` runs the part of `make lint-all` that finishes in
seconds and fails CI most often — `go build`, `go vet`, `gofmt-check`,
`fmt-check`, `actionlint`, `testnames` — about 15 s warm, 40 s with a cold
`bin/fern` (measured 2026-09-05, 4-core x86-64). `check-sources`, `deadcode` and
`freeze` stay in CI's lint lane. `git push --no-verify` skips it; it is a
convenience, not a gate.

## Perf history: `scripts/perf-history`

Every main-branch run of `perf.yml` appends its report to that commit's note
under `refs/notes/perf`; the baseline files stay the gate, the notes are the
trend. `scripts/perf-history metrics` lists what the newest note holds and
`scripts/perf-history show x86_64/map_string.ir` prints `date  sha  value` over
the last 50 main commits (`show METRIC N REV` widens or moves the window). Both
fetch the notes ref first, so they need the remote; nothing else is hosted.

## `make bootstrap`: the Go-less build, and `make distcheck`

`make bootstrap` (pinned stage0 compiling the whole compiler, then the smoke
test) is **~5 min and 6.1 GB peak RSS** on the 4-core x86-64 container (measured
2026-09-28), plus a one-time ~4 MB download. `make distcheck` — the compiler it
built recompiling itself, and that compiler doing it once more — adds **209 s +
137 s at 5.7 GB peak** and reaches a byte-identical stage3. `docs/BOOTSTRAP.md`
has the table. Do not run it alongside anything else heavy on a 16 GB box.

## Build a local compiler: `make selfhost-cli`

Builds the self-host compiler to a native binary for this host. Historical
pre-retirement timings were **~31 s warm / ~52 s from a cold Go build cache**
on a 4-core x86-64 container (measured 2026-09-01), **~13 s warm / ~40 s
cold on arm64-darwin** (measured 2026-08-06 on an M-series Mac). The resulting
compiler then took ~1.3 s per program. Re-measure this target on current source;
those measurements used the retired Go compilation pipeline.

```
make selfhost-cli
bin/fern-selfhost -target wasm32-wasi -emit asm -o p.wat /ABS/prog.fern $PWD/internal/stdlib
wasmtime run p.wat; echo $?     # oracle: ./bin/fern -interp /ABS/prog.fern
```

**Record which compiler produced the binary being measured.** `make selfhost-cli`
calls `bin/fern -target`, which now delegates to the Fern-written compiler.
The launcher selects `FERN_SELFHOST`, an installed sibling `fern-selfhost`, or
a compiler built from its embedded sources by stage0, in that order. The target
does not use Go lowering. `make bootstrap` also installs at `bin/fern-selfhost`;
the benchmark scripts may reuse that binary based on source timestamps, so its
path alone does not identify its producer or generation.

To measure the effect of a lowering change on the compiler's own generated code,
compile the same source with the base and changed compilers, then time those
stage-2 outputs. Timing only the first-generation compilers can measure the new
analysis without measuring the code it improves. The historical #8224 result
(+0.2% Ir in the Go-built compiler versus -17.6% in its own output) illustrates
why the producer matters, rather than describing today's build path:

```
./bin/fern-selfhost -g -target x86-64-linux -o /tmp/s2-new  compiler/fern.fern $PWD/internal/stdlib
/path/to/base-fern-selfhost -g -target x86-64-linux -o /tmp/s2-base compiler/fern.fern $PWD/internal/stdlib
```

~90 s and ~1.3 GB RSS each (measured 2026-09-04). Under callgrind, valgrind does
not read the `-g` `.symtab`, so resolve the hot addresses through `nm -n` on the
binary rather than reading `???:0x…` rows — and `nm` the binary you PROFILED.
Two builds do not share a layout, so resolving one run's addresses against the
other's symbol table silently names the wrong functions.

**Emitted-code size contributes to compile time.** The compiler assembles and
links its output in-process. Generated instructions now largely travel as
structured records; retained text still needs parsing. The following historical
measurement predates that handoff and is not a current per-instruction cost.
Measured 2026-09-06 on a stage-2 compile of `checker.fern` **to x86-64**:
`-emit asm` 15.86 G Ir, `-o` 21.82 G,
so assemble + link is **5.96 G — 27.3% of a full compile — over 278,058 emitted
lines, or ~21,425 Ir per line.**

That historical per-line figure is target-specific: arm64 and wasm
have their own assemblers (`arm64_native.fern`, `watbin.fern`) and their own emit
sizes, so re-measure before pricing a change against one of them. The shape of
the argument carries — all three assemble in-process — but the number does not.

The earlier advice against porting inline RC, based on an estimated 1.1% net
loss using that per-line figure, is withdrawn. The register emitters already
inline `__fern_rc_inc` and `__fern_rc_is_unique` through `ssa.rc_inlined` and
their `ssa_rc_prim` helpers; see the
[register-path retirement record](ssa-log/2026-09-22-the-stack-machines-function-driver-is-gone.md#what-the-drivers-tests-were-really-pinning).
Measure both generated-program performance and compiler assembly/link work on
the current implementation before changing that policy. The old estimate is
not evidence against an implementation that has already shipped.

This is what made it practical to run all 335 fixtures through the self-host
compiler:

```
FERN_SELFHOST_FIXTURES=1 go test ./internal/e2e/ \
  -run 'TestFernFixturesSelfHost(Wasm|X86_64|Arm64)'
```

One leg per target, each with its own
`internal/e2e/testdata/selfhost-<target>-known-divergences.txt`. It found twelve
divergences on fixtures green for months, and sixteen more on x86-64.

The **arm64 leg** exercises `arm64_native.fern` through emit, assembly and
linking. The x86-64 compiler also assembles and links in-process. The arm64 leg's first
valid run measured 302/317 passing with 15 listed rows, 12 of which were the
x86-64 leg's own rows at the same measured values —
shared frontend bugs, not arm64 ones.

Two constraints on that leg:

- **Absolute paths.** Relative ones were unopenable from an arm64-darwin binary
  until #6002 — `AT_FDCWD` is -2 on XNU, not -100.
- **Exit codes cannot carry a value >= 126.** WASI refuses anything outside
  [0..126), so wasmtime reports 1. This alone produced 14 phantom "mismatches"
  on the leg's first run.

Getting the arm64 leg live took three assembler fixes, all of which had been
mis-attributed to codegen: GAS **numeric local labels** (`b.lo 1f` … `1:`) were
not implemented at all, so every array index and string slice branched into the
ELF header (129 SIGILLs); `arm64_gas_link` had no **.text symbol** case, so
every function value resolved into .data and `blr`'d into it (23 SEGVs); and the
**literal pool held i32**, so every constant wider than 32 bits arrived wrapped
(`ldr x0, =1234567890123` loaded 1912767691). All three now REFUSE rather than
emit garbage when they cannot resolve something.

## Historical Go-compiler build profile (2026-09-02)

This profile describes the retired Go compilation pipeline. `bin/fern -target`
now invokes the self-host compiler, so neither these shares nor the Go worker
and GC tuning below describe a current target build. Profile the self-host
compiler with the tools below, and use `scripts/perf-history` for the tracked
trend. Reproducing this Go profile requires the historical compiler source.

Profile of `bin/fern -target x86-64-linux` on `compiler/fern.fern`
(4-core container, 2026-09-02, 34.9 s wall): codegen 72% — `ir.LowerWith`
32%, `ir.OptimizeCleanup` 22%, rendering the asm text 10% — the in-process
assembler 18.5% (of which parsing that text back is 14.5% and layout 3.8%),
front end 8%, and GC 28% of CPU across all of it. The codegen-to-assembler
text round trip was the largest inefficiency in that pipeline (#7993); the IR
passes were the largest cost. The profiling recipe then wrapped `run()` in
`pprof.StartCPUProfile` from a throwaway `cmd/fern` test and read
`go tool pprof -top -cum`; it does not profile today's target compiler.

Two stages of that pipeline ran on every core (#8176): `ir.LowerWith`'s
per-function body lowering (`FERN_LOWER_JOBS=N` set the worker count, `1` was
sequential) and the x86-64 assembler's line parse, which read the text in
chunks ahead of the in-order encode. Both were GC-bound rather than core-bound:
on the same container the lowering loop went 3.4 s to 2.3 s at four workers
and 3.0 s to 1.3 s with `GOGC=400`, which made allocation rate the next target.
`ir.OptimizeCleanup` was tried on the same
pool and gained nothing measurable at the default GOGC (its passes copied each
op list per round); it remained sequential.

## Historical self-host emit profile (2026-10-02)

callgrind over the self-host driver (`-g`, see below) emitting
`compiler/fern.fern` to x86-64 asm text, 2026-10-02: 271 G
instructions for the driver the pinned stage0 builds, 263 G for the one a
self-host-built compiler builds from the same source (its codegen borrows
where stage0's releases), 57 s wall on the 4-core container under other load
(73 s to a linked binary with symbols, 3.5 GB peak). Compare drivers built by
the same compiler: the input tree moves the count by under 0.02%, the
building compiler by 3%. These measurements predate the removal of the FnSigs
analysis in `40231668cf` and have not been re-measured on current main. The
retained inclusive shares below describe selected costs from that 2026-10-02
pass. The bullets omit the deleted analysis rows and are not a complete
breakdown of the totals; the optimization history afterward retains names as
measured then. Re-measure before using these shares to prioritize current
compiler work.

- The semantic lowering (`semlower.target_substitution`) was 60%: producing
  the rows 40% (`ssarc.lower` of 13.5k bodies 17%, `semsource.build_module`
  13%, the inference pass's second lowering 12%) and lambda lifting 3%.
- The backend (`asm_ir.emit_module_or_error_sub`) was 24%: the SSA emit of
  each function 12%, `asmcore.check_module` 5.5%.
- The checker was 9%.

By self cost the top rows were whole-table scans, since replaced with the
emitted asm byte-identical and the stage0-built driver's emit at 239 G
instructions (271 G before, 11.6% fewer):
the grow-flags edge resolution in `irlower.grow_param_flags_seeded` (4.8%,
a scan of all 10.8k functions per dying pass) and `semsource.known_callee`
(2.3%, 18 M calls of `method_is` scanning every known hander per
field-access callee) now go through name indexes; the units planner's
`outliving` and `anchored_after` (7% with the `used_after` tails they
drove) walk a reverse anchoring index and the rest of the block instead of
every value of the function. A second round took the callee lookup of
`asmcore.callgate_expr` (3.6%), the threader-row scan of `ssarc.caller_sigs`
(1.2%) and the record scan of `semsource.schema_of` (1.1%) through name
indexes as well: 227 G to 216 G with the stage0 pin at c891ebc
(`docs/rc-log/2026-10-02-f-three-scans-become-name-lookups.md`), and a third
round took the function-table scan of `asmcore.infer_call_named_type`
(1.5%), the per-kill copy of the escape set in `irlower.noesc_set_kill` and
the prefix-by-slice compares (`semtypes.is_env` 0.9%) and the
borrowable registry's 251 buckets (`fnsigs.param_is_borrowable` 1.7%) with
it: 214 G to 207 G on the base of 0d7a8d32
(`docs/rc-log/2026-10-02-g-a-table-scan-a-copied-set-and-sliced-prefixes.md`).
A fourth round handed the emit state to `asmcore.add_string_lit` owned,
through the sixteen emitter functions between it and the emit loop, so the
literal table is no longer copied per interned shape
(`docs/rc-log/2026-10-02-h-the-emit-state-reaches-the-literal-table-owned.md`).
What remains is spread wide: `util.hash_bucket` plus `__fern_str_eq` 5.4%
(the registries' probes), `ssa_lift.lift_impl` 3.4%, `__fern_alloc` 2.3%,
and the borrowable registry's copy-on-write store 0.9%.

To re-measure: build the driver with `-g` through the pinned stage0, run
`valgrind --tool=callgrind` on `-emit asm`, and resolve
`callgrind_annotate`'s `???:0x…` rows against `nm -n` of that same binary
(the pitfall below); `--tree=both` gives the call counts that tell a scan
from real work.

## Suite timings and sharding

The e2e suite is split (#4398 part 3) into `internal/e2eselfhost` (the
`TestSelfHost*` suite, ~575 files) and `internal/e2e` (everything else + ~30
residual `TestSelfHost*` legs in mixed fixture files), with the shared harness
in `internal/e2eharness` (each package re-binds the harness names via its
`harness_aliases_test.go`, so test code keeps bare identifiers like
`buildSelfHostBin`).

**`internal/e2eselfhost` unsharded exceeds 90 MINUTES** (measured 2026-07-28):
`-timeout 90m` still panicked with tests queued (`TestSelfHostStdTestE2EArm64`
16 s in). Shard it with `scripts/shard-tests SHARD NSHARD < test-list`,
the same duration-weighted LPT partition CI uses.

**A `-run` filter does not save you from the DEFAULT 10 m timeout**, so pass
`-timeout` even for one leg: `-run TestSelfHostWasm` takes **887 s** (measured
2026-09-02, 4-core container) and at the default panics before it finishes. The
`--- FAIL` rule below applies with a twist that makes this one read even more
like a breakage — the panic can land while the suite is still BUILDING a driver
binary, so the goroutine dump bottoms out in `e2eharness.CompileWithSelfHost`
waiting on the compiler subprocess, with no test body having run at all. The
`running tests:` header naming a single test seconds in is the tell.

**The drivers are built by the pinned stage0 compiler** (since 2026-09-29;
`internal/e2eharness/self_host_compiler.go`): the first test in a process that
needs one resolves it through `bootstrap/bootstrap.sh stage0` (downloaded once
into `build/bootstrap/`, sha256-checked), and it compiles each driver for
x86-64-linux. Measured on the 4-core container: `asm_ir_run.fern` 104 s at
4.0 GB, `wasm_ir_run.fern` 87 s at 3.6 GB, `fern.fern` 300 s at 5.7 GB — where
the Go backend emitted a driver in ~9 s — each once per source change and
shared across processes through `FERN_SELFHOST_BUILD_CACHE`. A cold driver
build is therefore the dominant cost of a single test; `FERN_SELFHOST_INTERP=1`
runs the driver under the interpreter instead when the test is not about the
driver's machine code. CI carries the built drivers between runs
(`.github/actions/selfhost-driver-cache`), keyed on the same inputs as the
harness's key, so a CI job pays a build only for a driver no earlier run
built. `STAGE0=<path>` substitutes a local compiler for the pin,
as it does for `make bootstrap`, and it is how a compiler change is tested as
the drivers' builder before the pin carries it. Every driver is held to what
the pin can compile, like `fern.fern`.

**Measured 4-way, shard 0: 48 min (green).** So sharding pays only if you run
ONE shard — four in sequence is ~3.2 h, worse than the unsharded run it
replaces. Run them in parallel only if RAM allows: each heavy driver build
reserves ~4.3 GB through `buildMemLimiter`, so a 16 GB host fits about two
concurrently, not four.

**`internal/e2e` no longer fits in one invocation at `-timeout 45m`** (measured
2026-07-29, 4-core / 15 GB host): two runs, one with the host entirely to
itself, both hit the 2700 s wall with **zero `--- FAIL` lines**. A timed-out run
panics with a goroutine dump and prints `FAIL`, but the dump shows the suite
parked in `withBuildMemory` (the `buildMemLimiter` RAM semaphore) waiting to
start a heavy driver build, or mid-`runLangInterp`. **Always check the
`--- FAIL` count before reading a timeout as a breakage.** If you do want the
whole package, give it `-timeout 90m` and expect it to be the only thing
running. Core count matters more than RAM — the semaphore serialises the heavy
builds regardless of how much memory is free.

CI does not hit either wall; it shards across the `test-e2e-*` workflows, each
well under its job timeout.

## Build memory

Every `buildSelfHostBin` / `buildBin` of a self-host driver (`asm_run.fern` /
`asm_load_run.fern` / `asm_ir_run.fern` / `wasm_ir_run.fern` / …) runs the
pinned self-host compiler over the driver's source closure in a subprocess that
peaks at 4.0 GB (`asm_ir_run.fern`) to 5.7 GB (`fern.fern`). The harness
self-limits, so **swap is generally not needed** and the peak sits comfortably
under a 16 GB host:

- `internal/e2eharness`'s `buildMemLimiter` is a RAM-budget weighted semaphore
  around each cold driver build: it reserves the build's estimated peak
  (`DriverBuildWeightMB`: 4300 MB, 6000 MB for `fern.fern`) against a budget
  (`FERN_BUILD_MEM_BUDGET_MB`, default ~85% of `MemTotal`), so heavy builds
  can't stack past the host's RAM and OOM the run. Two cold driver builds fit a
  16 GB host concurrently; bigger hosts parallelise further up to the budget.
- Self-host-emitted asm that a test links itself (`CachedLink`,
  `BuildBinArm64`) goes through gcc. A big listing (>= 8 MB; the stage-2
  self-compile at ~100 MB is the one that matters) links under a reservation
  sized to GNU as's measured peak (`gccBigLinkWeightMB`: ~6 MB per MB of asm
  plus 500 MB; 392 MB measured on the x86-64 compiler listing, 590 MB on the
  arm64 one). Small program links take no reservation.

The old in-process Go emit used `FERN_EMIT_MEMLIMIT_MB` to cap the Go heap.
That helper has been removed; it did not limit a Fern compiler subprocess.

If a build is still OOM-killed, lower `FERN_BUILD_MEM_BUDGET_MB` (fewer builds
overlap), or re-create the ephemeral
swap file (a container restart wipes it):

```
fallocate -l 8G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
```

Also keep `/` from filling — stale `/tmp/selfhost-bincache-*` dirs (~1 GB each,
one per build) pile up; `rm -rf /tmp/selfhost-bincache-*` reclaims them (they
are regenerable caches).

## Checking a test is non-vacuous: `git checkout <parent> -- <file>`, never `git stash`

A new test only proves something if it fails without the fix. To check that,
restore one file to its pre-fix state:

```sh
git checkout <parent-sha> -- compiler/ssarc.fern
go test ./internal/e2eselfhost/ -run TestYourNewCase > run.log 2>&1; echo "EXIT=$?"
git checkout HEAD -- compiler/ssarc.fern
```

**Do not reach for `git stash push <file>`.** Once the fix is committed the file
is clean, so `stash push` saves nothing and silently succeeds — and the paired
`stash pop` then applies whatever entry was already on the stack, conflicting
files nobody touched. Two changes hit this on the same day; both had to recover
an unrelated stash entry, and one recorded a `rerere` resolution for a conflict
that was never real (`.git/rr-cache`, which auto-applies to future merges — check
it if a `stash pop` ever conflicts unexpectedly).

Report the count, not the verdict: "15 of 16 fail, the sixteenth is the
deliberate control" is checkable; "verified non-vacuous" is not.

## Selecting and running a measurement: five ways to get a confident wrong answer

Every one of these produced a wrong answer that looked right, on the same day.

**Go's test cache does not know about a `.fern` file, so pass `-count=1`
whenever the change under test is Fern source.** The suites that compile this
tree's own Fern — `internal/coreutils` above all — take their inputs from
outside the package, so a rerun after editing one replays the previous
result as `ok ... (cached)`. It reads as a pass, and it reads as a pass just
as convincingly when the file has been reverted underneath it, which is how a
non-vacuity check above reports that a new test would pass without its
fix.

**`-run` is an unanchored substring match, and nearly every test in
`internal/e2eselfhost` is named `...X86_64`.** So `-run 'X86|Gas'` does not
select the fifty assembler tests, it selects most of the package: 40 minutes
instead of 2. Anchor on what follows the prefix — `-run 'TestSelfHostX86[A-Z]'`
picks `TestSelfHostX86Gas` and friends while `X86_` fails the `[A-Z]`.

**The 335-fixture corpus lives in `internal/e2e`, not `internal/e2eselfhost`,
and is gated behind `FERN_SELFHOST_FIXTURES=1`.** Skipped, it reports
`ok  github.com/jakechampion/lang/internal/e2e  0.006s` — a bare `ok` that
passes every grep for `FAIL`. The real run is ~97 s.

**`/tmp/selfhost-bincache-*` belongs to a running test process** — one per
process, from `os.MkdirTemp` in `internal/e2eharness/self_host_buildcache.go`,
held open for the whole run. Deleting one to reclaim disk while a suite is
running turns *every* test in it into `open cached bin ...: no such file or
directory`: 401 failures, none of them real. When several agents or sessions
share a box, run long suites under a private cache with
`TMPDIR=<your own dir> go test ...`, and reclaim disk from your own build
artifacts rather than from `/tmp/selfhost-bincache-*` or `/tmp/go-build*`.

**Serialize a byte-identity comparison.** Running one compile next to a heavy
suite and then `cmp`-ing produced a 421 KB "difference" between two binaries
that are in fact identical. One at a time, then compare.

## callgrind: profile the `-g` binary itself

valgrind does not read the self-host `-g` `.symtab`, so hot rows come back as
`???:0x...` and want `nm -n` to resolve. **Resolve them against the binary that
actually ran.** Building a second `-g` binary as a symbol donor and mapping the
first binary's addresses through it does not work — the two do not share a
layout, and the result is plausible-looking symbol names with offsets tens of
thousands of bytes into the wrong function. Build with `-g` and profile that.

`callgrind_annotate` is the tool for self cost; a hand-rolled parse of the
`fn=(id)` lines will double-count, because callgrind's name compression means a
bare `fn=(id)` refers back to an earlier definition and the cost lines under a
caller are inclusive.

## Arena exhaustion is exit 125; a host OOM-kill is 137

Distinguishable by status alone, which is the point — they used to share 137,
which made every occurrence a manual investigation and had three harness sites
treating genuine compiler regressions as infra.

- **125** (`ExitArenaExhausted`) — `__fern_alloc`'s bounds check fired: the
  fixed bump arena is full. A REAL failure, reproducible locally, and almost
  always a leak. 125 is clear of the 128+signal range so nothing can forge it,
  and under WASI's 126 ceiling so it survives wasmtime. Pinned across the
  self-host emitters by `internal/e2e/arena_exit_code_test.go`
  (`e2eharness.ExitArenaExhausted`).
- **137** (128+9, SIGKILL) — the host ran out of RAM. Also reads as `signal:
  killed` from the self-host compiler building a driver, or `as`/gcc dying on
  a link. Retry with a smaller budget per the knobs above. This is *total-RAM* pressure, not a
  cgroup cap (`memory.limit_in_bytes` is effectively unlimited).

**The native arena is 16 GiB** (0x400000000) in the current x86-64 and ARM64
emitters, `asm_ir.fern` and `asm_arm64_ir.fern`. The former Go `heapBytes`
implementations are retired. The Linux mmap uses `MAP_NORESERVE`, so the
reservation costs nothing until touched; only the exit-125 ceiling moves. The stage-2 self-compile
(gen1/mmc2 in the fixpoint tests) is the usual victim: the self-host-built
compiler's live set grows with every compiler-source addition, and when it hits
the arena wall the test "OOMs" on CI with no kernel OOM anywhere. Measure with
/proc RSS vs the arena size — see `docs/RC-PERCEUS-SELF-HOST-PORT.md`,
2026-07-11 entry.

## Measuring a Fern program's memory: `__heap_bump_bytes()`, not peak RSS

RSS is not comparable across hosts here. The arena is a 16 GiB `MAP_NORESERVE`
mapping, so a first touch anywhere in it maps a 2 MB huge page under
`THP=always` and a 4 KB page under `madvise`. Measured 2026-08-03: the same
binary on the same input reported **43 MB locally (madvise) and 552 MB on the CI
runner (always)** — a 12x spread with identical allocation, which failed a
100 MB RSS ceiling on a change that had just made the code 50x leaner.
`cat /sys/kernel/mm/transparent_hugepage/enabled` tells you which side of that
12x you are on.

`__heap_bump_bytes()` returns the bump allocator's high-water mark (total bytes
handed out fresh, i.e. everything the freelist could not recycle). It is exact,
host-independent, and meaningful under qemu, so it is the right gate for a
memory regression test.

**On ONE host, an A/B of peak RSS is still the sharpest instrument for an
allocation change** — the 12x above is a spread *between* hosts, and THP does
not move under you mid-session. Measured 2026-08-25 on the self-compile:
6159 / 6159 / 6159 MB before a change and 5856 / 5856 / 5857 MB after, i.e.
deterministic to the megabyte, while three interleaved wall-clock rounds of the
same pair disagreed on the sign. A third of that run is the kernel zeroing arena
pages, so bytes bumped land in `sys` and not in `user`, and a change that
deletes allocation without deleting work can be invisible to a clock. Read RSS
for the verdict and `__heap_bump_bytes()` for a gate that survives the host.

**For the self-host compiler itself, `scripts/selfhost-alloc-bench` is the
harness** — it builds a `FERN_LEAKCHECK`-instrumented compiler, compiles
`checker.fern` with it for an exact allocation count, and reads peak RSS out of
a scratch cgroup. Two details there are what separate a steady reading from a
drifting one: a **fresh** cgroup per run (a v1 memory cgroup charges page cache
to whoever faulted it in, so a reused one climbs) and emitting to `/dev/null`
(8 MB of asm is otherwise charged as cache). With both it repeats to the
megabyte. `docs/SELFHOST-SYMBOL-INTERNING.md` is the worked example.

**The alloc counts are gated; peak RSS is not.** `perf.yml`'s `alloc` lane runs
the script against `.github/alloc-baseline.txt` on every PR, but with
`FERN_ALLOC_PEAK_RSS=0`: the RSS figure is not comparable across hosts, so it
cannot be baselined against whatever runner CI hands you. Read it locally, on
one machine, A/B.

**It returns i64.** Bind it to an `i64` (`let b: i64 = __heap_bump_bytes();`);
narrowing to an exit code needs an explicit `as i32`, which is what the existing
corpus does. It used to be declared i32 while every runtime helper computed the
offset in 64 bits, and a quadratic sweep read 141 MB / 555 MB / **-2.09 GB** /
202 MB — where only the third of the four looks wrong.

## The rc==1 append cliff: only the WEIGHT ranks work

`__arr_push_shared_count()` counts crossings; `__arr_push_shared_bytes()` (i64)
sums the bytes those crossings copied. `FERN_CLIFF_REPORT=1` prints both.

**Never scope accumulator work against the count alone.** One threaded
accumulator over 20k appends copies **2.3 GB** while a hundred crossings of a
4-byte loop-depth stack copy under a kilobyte. Two rounds of `own`-conversion
work were scoped against the unweighted count and aimed at sites that could not
have paid.

**And read any figure taken before 2026-08-18 as SCALAR ELEMENTS ONLY.** The
tally lived inside `__fern_arr_push_grow` and not in the `_ptr` / `_str` /
`_move_` siblings, so `T[]` and `string[]` appends — which is what a compiler's
own accumulators are made of — crossed the cliff without bumping anything. The
"188 crossings / 812 bytes on `checker.fern`, i.e. noise" this file recorded on
2026-08-04 was that blind reading; the same workload with every variant
tallying is **281,621 crossings / 267 MB**, 88% of it `irlower.LowerState.emit`
threading its `ir.Op[]`. See `docs/PERFORMANCE-AUDIT-2026-08.md` §4d.5 for what
that copying is and §4d.6 for how it stayed invisible.

**The counters see appends and nothing else.** They are bumped by
`__fern_arr_push`'s un-share path, so a quadratic `.with` — copy-on-write via
`__fern_arr_cow_inplace` — moves neither of them. In #6911 that was the whole
difference between the numbers and the clock: `own` on the x86 assembler's state
parameter took the cliff from 21.4 GB to 3.07 GB and the compile time from 97 s
to 97 s, while lifting the code buffer out of the struct for the two `.with`
patch loops moved the cliff not at all and took the same compile to 19 s. Read a
flat cliff line as "no append regressed", never as "nothing is copying".

**And the bytes are not the cost either — the ELEMENT TYPE is.** A crossing on
a `string[]` memcpys the buffer *and* inc's every element, and the copy it
discards dec's every element back. #6911's last 3.07 GB was mostly one such
queue: 3 GB of memcpy, but 33% of the compile in the rc traffic around it, and
removing it took 19 s to 9.3 s. Rank by weight, but read the element type before
estimating what a site is worth.

**Attribute crossings by source instrumentation, not gdb.** Frame #2 resolves to
`??` above the runtime helpers — they are hand-written asm with no frame pointers
— so breaking on the counter bump cannot name the Fern caller. What works:
bracket a suspect region with `__arr_push_shared_bytes()` reads and print the
delta. Bisecting the self-host compiler that way costs about two minutes an
iteration (15 s to rebuild `bin/fern-selfhost`, 20–100 s to compile
`checker.fern`), and a probe printing only when the delta is non-zero — tagged
with the source line being assembled — attributes 36k crossings in one run.
Instrumentation is not free of observer effect: an added statement changes
liveness, and so can change which appends reuse in place. Confirm a suspected
site by *fixing* it and re-measuring an unprobed build.

## arm64 / qemu locally: debug only, never a gate

The aarch64 e2e + fixpoint tests under `qemu-aarch64` are the slow part of a
local sweep (minutes). Gate locally on the **x86-64** equivalents (fixpoint,
checker, CLI, e2e) plus the WASM tests, which give the same signal far faster;
CI runs the full arm64 matrix on every push. Reach for qemu locally only to
**debug** a specific arm64 failure CI surfaced.

This is safe because the self-host backends **share** their entire
target-independent frontend — the `Ty` type system, type inference, the
pre-codegen checker, and `EmitState` + its state methods — via
`compiler/asmcore.fern`, imported by `asm_ir.fern` (x86-64),
`asm_arm64_ir.fern`, and `wasm_ir.fern`. That half cannot drift; only the
`emit_*` instruction-selection layer is hand-maintained per target. So an
x86-64-green change is almost always arm64-green, and CI is the backstop.

When editing inference / checker / `Ty` / `EmitState`, edit `asmcore.fern` once
— it is *not* mirrored in the backends. Anything compiling those backends must
also provide `asmcore.fern`.

**Two termios tests fail under qemu and pass on real arm64.**
`TestArm64Termios` exits 21 locally — a control byte
written with `termios_set` does not read back through `termios_get` — while
`TestX86_64Termios` runs the same program on the same pty and passes, and CI's
arm64 lane, which runs natively, is green. It is qemu-user's ioctl emulation,
not a Fern defect, and it is not worth chasing: a full local `internal/e2e`
therefore reports two failures CI never shows.

## WASM toolchain

Pinned in `mise.toml` (wasmtime, wasm-tools and the preview1 adapter — the
adapter's version must equal wasmtime's). `eval "$(scripts/toolchain-env)"`
installs them and exports `PATH` + `FERN_WASI_ADAPTER`; without both the e2e
tests SKIP.

**The WASI Preview-3 async/stream/future component tests are
wasmtime-version-sensitive.** v46 changed the component-model-async ABI (async
functype tag `0x43`; non-reentrant component instances → the async-import
composer emits a sibling-nested structure). Under an older wasmtime (e.g. a
system v37/v39) they fail with `invalid leading byte (0x43)` or `cannot enter
component instance`. Use the pinned v46.

## On macOS: what runs natively, and what needs a container

Both native targets emit Linux ELF, and the e2e harness looks for `qemu-x86_64`
/ `qemu-aarch64` / `aarch64-linux-gnu-gcc` by name. macOS has none of them and
cannot: qemu's user-mode emulation is Linux-only, and Homebrew's qemu is system
emulation. So on a Mac those legs SKIP, and a SKIP reports `ok`.

Runs natively on Apple Silicon: the wasm suite (once the pinned toolchain is on
`PATH`), the `arm64-darwin` target, `-interp`, and every host-independent Go
package. Measured: `go test ./internal/e2e/ -run TestWasm` goes from 12 skips to
0 once the pinned pair is installed.

Everything else goes through **`scripts/devbox`**, a linux/arm64 container
carrying qemu-user, both cross compilers and the `mise.toml` toolchain:

```
scripts/devbox go test ./internal/e2e/ -run TestSelfHostX86_64
scripts/devbox                 # interactive shell
```

linux/arm64 on purpose: the aarch64 leg then runs natively and only x86-64 pays
emulation. This makes those legs **runnable for debugging**; it does not make
them a gate — the section above still applies, and `docs/CI-SIGNOFF.md` records
which lanes may be signed off locally as a result.

### The stage-1 / stage-2 self-compile on Apple Silicon

Both stages run natively through `-target arm64-darwin`, so the self-compile
IS reproducible on a Mac. Measured 2026-09-05 on an M-series machine from a
fresh `go build -o $B/fern ./cmd/fern` (absolute paths throughout: the
self-host CLI cannot open relative ones):

```
$B/fern    -target arm64-darwin -o $B/fern-s1 $W/compiler/fern.fern      # stage 1: 8 s, 1.3 GB RSS
$B/fern-s1 -target arm64-linux -emit asm -o $B/fern.s $W/compiler/fern.fern  # 26 s, 1.0 GB, 63.3 MB of asm (#8212's shape)
$B/fern-s1 -target arm64-darwin -o $B/fern-s2 $W/compiler/fern.fern      # stage 2: 36 s, 1.4-1.7 GB, an 11 MB Mach-O
```

The "arena exhaustion, exit 125" that #6872 / #7267 reported for the stage-2
build was not the arena: it was the string builder, which the self-host
emitters backed with a fixed 64 MiB `.bss` buffer and trapped with that exit
code when the emitted text passed it. The buffer grows now and the build
completes. `asm_load_run.fern` is no longer needed as a stand-in for
`fern.fern`.

The darwin stage 2 is a fixpoint at the emit level: `fern-s2` and `fern-s1`
produce byte-identical `-target arm64-darwin -emit asm` listings for all 471
runnable conformance cases (#8400 was `darwinize` rewriting the `:lo12:`
inside the compiler's own string literals). Stage 3 did not hold on
2026-09-05: `fern-s2` building `fern.fern` exited 125 (arena exhausted) after
20 s at 3.9-5.1 GB RSS (two runs), where `fern-s1` finished the same build in
36 s at 1.4-1.7 GB (#8479); it holds since 2026-09-29, below. The same chain
for `-target arm64-linux` in the linux/arm64
container is a full fixpoint: stage 2 builds in 174 s at 1.8 GB RSS, emits
byte-identical asm to stage 1, and compiles and runs a strbuf program
correctly.

The heap's address regime is not what breaks the darwin stage 3. XNU ignores
the arena's mmap hint and maps it above the 4 GiB `__PAGEZERO`, so on darwin
every heap pointer has a non-zero high half from the first allocation, where
Linux honours the 256 MiB hint. `FERN_HIGH_HEAP=1` makes the self-host arm64
emitter raise the hint to 8 GiB (`asm_arm64_ir.fern`), and qemu-aarch64 honours
it. The former Go `arm64codegen.Options.HighHeapProbe` is retired. Measured
2026-09-29 on the 4-core x86-64 container: an aarch64 self-host compiler
emitted with that hint compiles `fern.fern` for `arm64-linux` under qemu in
537 s at 5.4 GB peak RSS, exit 0, and its output is byte-identical to the
default-hint compiler's (517 s, 5.4 GB). The gate for the shapes is
`TestSelfHostArm64HighHeap*` (`internal/e2eselfhost`). The recipe, from a
`bin/fern-selfhost` built by `make selfhost-cli`:

```
FERN_HIGH_HEAP=1 $W/bin/fern-selfhost -target arm64-linux -emit asm -o $B/fern_hh.s $W/compiler/fern.fern $W/internal/stdlib
aarch64-linux-gnu-gcc -static -nostdlib -o $B/fern_hh $B/fern_hh.s
qemu-aarch64 $B/fern_hh -target arm64-linux -o $B/fern_s2 $W/compiler/fern.fern $W/internal/stdlib
```

What the probe does not move is the image, `.rodata` and the stack, which on
darwin also sit above 4 GiB; a truncation of one of those still needs the Mac.

Nor is it the darwin OUTPUT path: on the same container a self-host-built
x86-64 compiler compiles `fern.fern` for `arm64-darwin` in 111 s at 5.4 GB,
exit 0, byte-identical to the native-built compiler's Mach-O, and the
aarch64 self-host-built compiler does the same under qemu in 525 s at 5.5 GB.
What was left was the self-host-built compiler running ON XNU, and on
2026-09-29 the `macos-15` lane ran that stage 3 (source at ea943e9) with
`FERN_CLIFF_REPORT=1`, which prints `heap_bump_bytes` at each `sem:*` phase and
at `darwin:emitted` / `darwinized` / `assembled` / `unwind` / `linked` /
`image` (`fern.fern`), under `/usr/bin/time -l`. It passed: 56 s, 3.8 GB
maximum RSS, 5.66 GB peak footprint, 5.85 GB bumped, stage2 == stage3, with no
phase running away (5.12 GB after the semantic lowering, 5.82 GB after the
assembler). The darwin fixed point is gated by `bootstrap.yml`'s
`verify-arm64-darwin` since (`make bootstrap` + `make distcheck` from the pin,
no Go); the readout above is how to measure it by hand if it regresses:

```
FERN_CLIFF_REPORT=1 /usr/bin/time -l $B/fern-s2 -target arm64-darwin -o $B/fern-s3 $W/compiler/fern.fern $W/internal/stdlib
```
