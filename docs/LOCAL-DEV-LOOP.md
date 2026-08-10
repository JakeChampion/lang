# The local dev loop

Machine-shaped facts: how long things take on a dev box, how much RAM they
want, which knobs move that, and which instrument to measure with. `CLAUDE.md`
states the *rules* that follow from this; the numbers live here so a
re-measurement updates one place.

Every figure here is dated. **Re-measure before quoting one** — several of
these have been wrong by an order of magnitude in the direction that
discourages using the tool at all, and a stale number costs an hour per
attempt.

## Fastest self-host loop: `make selfhost-cli`

Builds the self-host compiler to a native binary for this host — **~2 minutes**
once on an x86-64 host, **~13 s warm / ~40 s from a cold Go build cache on
arm64-darwin** (measured 2026-08-06 on an M-series Mac). Then ~1.3 s per
program, versus minutes per program interpreted and 90+ minutes for an
unsharded `internal/e2eselfhost`:

```
make selfhost-cli
bin/fern-selfhost -target wasm32-wasi /ABS/prog.fern $PWD/internal/stdlib -o p.wat
wasmtime run p.wat; echo $?     # oracle: ./bin/fern -interp /ABS/prog.fern
```

This is what made it practical to run all 335 fixtures through the self-host
compiler:

```
FERN_SELFHOST_FIXTURES=1 go test ./internal/e2e/ \
  -run 'TestFernFixturesSelfHost(Wasm|X86_64|Arm64)'
```

One leg per target, each with its own
`internal/e2e/testdata/selfhost-<target>-known-divergences.txt`. It found twelve
divergences on fixtures green for months, and sixteen more on x86-64.

The **arm64 leg** is the highest-value of the three: `-target arm64-linux` is the only
path where the self-host compiler produces the finished binary ITSELF (emit +
assemble + link in-process, no gcc, no wasmtime), so it is the only gate on
`arm64_native.fern`. Its first valid run measured 302/317 passing with 15 listed
rows, 12 of which are the x86-64 leg's own rows at the same measured values —
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

## Suite timings and sharding

The e2e suite is split (#4398 part 3) into `internal/e2eselfhost` (the
`TestSelfHost*` suite, ~575 files) and `internal/e2e` (everything else + ~30
residual `TestSelfHost*` legs in mixed fixture files), with the shared harness
in `internal/e2eharness` (each package re-binds the harness names via its
`harness_aliases_test.go`, so test code keeps bare identifiers like
`buildSelfHostBin`).

**`internal/e2eselfhost` unsharded exceeds 90 MINUTES** (measured 2026-07-28):
`-timeout 90m` still panicked with tests queued (`TestSelfHostStdTestE2EArm64`
16 s in). Shard it with `scripts/selfhost-shard-tests SHARD NSHARD < test-list`,
the same duration-weighted LPT partition CI uses.

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
`asm_load_run.fern` / `asm_ir_run.fern` / `wasm_ir_run.fern` / …) emits a
multi-thousand-function asm text. The harness self-limits, so **swap is
generally not needed** and the peak sits comfortably under a 16 GB host:

- The x86-64 backend flips the self-host compiler's one lowering monster
  (`irlower__lower_expr`, ~9.75M IR ops) from the inline rc fast-path to the
  `call __fern_rc_dec/inc` form (the arm64 backend's long-standing `rcInlineOK`
  mechanism, backported — behaviour-identical). That cut the `asm_ir_run` driver
  asm from ~1028 MB → ~470 MB.
- The cold driver emit runs under a **refcounted soft heap cap**
  (`withEmitMemLimit`, `FERN_EMIT_MEMLIMIT_MB`, default 3600; `<= 0` disables),
  `ir.Op` keeps its rare payload fields (Str2 / Sig / ArgTypes / CaptureSlots)
  in an `OpExt` side-table (96 B/op, was 160), and both native backends
  **release each function's IR as it is emitted** (`ip.Funcs[i] = nil` in the
  emit loop) so the IR is reclaimed incrementally instead of peaking alongside
  the output buffer. At default GOGC the emit ballooned to ~9 GB RSS of
  mostly-garbage in 134 s; capped + shrunk + released it runs ~3.7 GB in ~40 s.
  Output is byte-identical.
- Driver binaries are **assembled + linked in-process** by the pure-Go native
  assembler (`internal/native/x86_64` + `internal/native/elf` — the same
  pipeline `cmd/fern -target x86-64-linux` uses by default): ~25 s / ~2.6 GB where GNU
  `as` took ~36 s at ~4.7 GB plus a link, and the ~470 MB `.s` never touches
  disk. Any assembler error falls back to the old gcc(+lld) path automatically.
  `CachedLink` does the same for HUGE self-host-emitted asm (the stage-2
  self-compile, >= 8 MB), which previously ran GNU `as`+bfd with no memory
  reservation at all; small program links stay on gcc/bfd unchanged.
- `internal/e2eharness`'s `buildMemLimiter` is a RAM-budget weighted semaphore
  around the cold emit+link: it reserves each heavy build's estimated peak
  (`FERN_BUILD_HEAVY_MB`, default 4300) against a budget
  (`FERN_BUILD_MEM_BUDGET_MB`, default ~85% of `MemTotal`), so heavy builds
  can't stack past the host's RAM and OOM the run. Two cold driver builds fit a
  16 GB host concurrently; bigger hosts parallelise further up to the budget.

If a build is still OOM-killed, lower `FERN_BUILD_HEAVY_MB` /
`FERN_BUILD_MEM_BUDGET_MB` / `FERN_EMIT_MEMLIMIT_MB`, or re-create the ephemeral
swap file (a container restart wipes it):

```
fallocate -l 8G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
```

Also keep `/` from filling — stale `/tmp/selfhost-bincache-*` dirs (~1 GB each,
one per build) pile up; `rm -rf /tmp/selfhost-bincache-*` reclaims them (they
are regenerable caches).

## Arena exhaustion is exit 125; a host OOM-kill is 137

Distinguishable by status alone, which is the point — they used to share 137,
which made every occurrence a manual investigation and had three harness sites
treating genuine compiler regressions as infra.

- **125** (`ExitArenaExhausted`) — `__fern_alloc`'s bounds check fired: the
  fixed bump arena is full. A REAL failure, reproducible locally, and almost
  always a leak. 125 is clear of the 128+signal range so nothing can forge it,
  and under WASI's 126 ceiling so it survives wasmtime. Pinned across all five
  emitters by `internal/e2e/arena_exit_code_test.go`.
- **137** (128+9, SIGKILL) — the host ran out of RAM. Also reads as `signal:
  killed` during the Go emit, or `as`/gcc dying on the fallback path. Retry with
  a smaller budget per the knobs above. This is *total-RAM* pressure, not a
  cgroup cap (`memory.limit_in_bytes` is effectively unlimited).

**The arena is 16 GiB** (0x400000000) on every emitter — native x86-64 + arm64
(`heapBytes`) and self-host `asm_ir.fern` / `asm_arm64_ir.fern` (`heap_size`),
kept in lockstep. The mmap is `MAP_NORESERVE`, so the reservation costs nothing
until touched; only the exit-125 ceiling moves. The stage-2 self-compile
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

**It returns i64.** Bind it to an `i64` (`var b: i64 = __heap_bump_bytes();`);
narrowing to an exit code needs an explicit `as i32`, which is what the existing
corpus does. It used to be declared i32 while every runtime helper computed the
offset in 64 bits, and a quadratic sweep read 141 MB / 555 MB / **-2.09 GB** /
202 MB — where only the third of the four looks wrong.

## The rc==1 append cliff: only the WEIGHT ranks work

`__arr_push_shared_count()` counts crossings; `__arr_push_shared_bytes()` (i64)
sums the bytes those crossings copied. `FERN_CLIFF_REPORT=1` prints both.

**Never scope accumulator work against the count alone.** Measured 2026-08-04, a
whole-module compile of `checker.fern` by the self-host compiler crosses the
cliff **188 times and copies 812 bytes** doing it — 4-byte loop-depth stacks in
`irlower`'s `enter_loop`, i.e. noise — while one threaded accumulator over 20k
appends copies **2.3 GB**. Two rounds of `own`-conversion work were scoped
against the unweighted count and aimed at sites that could not have paid.

Attributing crossings to a function needs no flag: build with `-g`, break on the
counter bump inside `__fern_arr_push_grow` under gdb, and report
`info symbol *(unsigned long *)$rsp`. Details in `docs/TEST-GATES.md`.

## arm64 / qemu locally: debug only, never a gate

The aarch64 e2e + fixpoint tests under `qemu-aarch64` are the slow part of a
local sweep (minutes). Gate locally on the **x86-64** equivalents (fixpoint,
checker, CLI, e2e) plus the WASM tests, which give the same signal far faster;
CI runs the full arm64 matrix on every push. Reach for qemu locally only to
**debug** a specific arm64 failure CI surfaced.

This is safe because the self-host backends **share** their entire
target-independent frontend — the `Ty` type system, type inference, the
pre-codegen checker, and `EmitState` + its state methods — via
`examples/self_host/asmcore.fern`, imported by `asm_ir.fern` (x86-64),
`asm_arm64_ir.fern`, and `wasm_ir.fern`. That half cannot drift; only the
`emit_*` instruction-selection layer is hand-maintained per target. So an
x86-64-green change is almost always arm64-green, and CI is the backstop.

When editing inference / checker / `Ty` / `EmitState`, edit `asmcore.fern` once
— it is *not* mirrored in the backends. Anything compiling those backends must
also provide `asmcore.fern`.

## WASM toolchain

Pinned: **wasmtime v46.0.1 + wasm-tools 1.253.0** (see
`.github/actions/setup-fern/action.yml`; the `.claude/hooks/session-start.sh`
hook installs them locally under `~/.fern-wasm/`). Export the binaries onto
`PATH` and set `FERN_WASI_ADAPTER` to the preview1 adapter, or the e2e tests
SKIP.

**The WASI Preview-3 async/stream/future component tests are
wasmtime-version-sensitive.** v46 changed the component-model-async ABI (async
functype tag `0x43`; non-reentrant component instances → the async-import
composer emits a sibling-nested structure). Under an older wasmtime (e.g. a
system v37/v39) they fail with `invalid leading byte (0x43)` or `cannot enter
component instance`. Use the pinned v46.
