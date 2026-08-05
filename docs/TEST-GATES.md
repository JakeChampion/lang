# Which gates prove what

A guide to picking the suites a change actually has to pass, written after two
compiler bugs shipped past three heavyweight green gates in a row.

The mechanics of running and skipping lanes are in
[CI-SIGNOFF.md](CI-SIGNOFF.md); this document is about *which* lanes carry
signal for *which* kind of change, and — more usefully — which ones look
authoritative and are not.

## The one that surprises people: the fixpoint is self-referential

The per-module / whole-compiler **fixpoint** tests compile the self-host
compiler with itself and assert gen0 and gen1 are byte-identical. That is a
strong property and it is easy to over-read. What it proves is that the
compiler **reproduces itself**. It is structurally blind to any miscompile
whose effect is confined to shapes that do not occur in the compiler's own
sources — and equally blind to one that occurs there but is *stable*, because
a consistently-wrong compiler still reproduces itself byte-for-byte.

Both bugs behind #6021 were live during green fixpoint runs. The rc
over-release corrupted the driver's freelist on nearly every program it
compiled, and the fixpoint did not care: a doubly-freed block gets recycled,
the emitted bytes are unchanged, gen0 == gen1.

So for a change to self-host lowering or to the RC/Perceus machinery:

- **`internal/e2eselfhost` is primary.** It runs *programs the compiler does
  not contain* through the self-host compiler and checks their behaviour.
- **The fixpoint is secondary.** It catches nondeterminism and
  self-compilation breakage — real failure modes, different ones.

Several comments in the tree read the other way around. They are wrong, and
#6018 is what that cost: it passed the per-module fixpoint, all 335 fixtures,
*and* the native suite while segfaulting the driver. `internal/e2eselfhost` is
what caught it.

## Gate → what it actually proves

| Gate | Proves | Blind to |
|---|---|---|
| `internal/e2e` fixtures (`TestFernFixtures`) | The NATIVE compiler is right on the corpus | Anything self-host-only; anything about *how much* it allocated |
| `TestFernFixturesSelfHost{Wasm,X86_64,Arm64}` (`FERN_SELFHOST_FIXTURES=1`) | The self-host compiler agrees with native on the corpus, on all three emitted targets. The arm64 leg is the only one where the self-host compiler produces the finished binary by itself (emit + assemble + link in-process), so it is also the gate on `arm64_native.fern` | Values >= 126 on the wasm leg, which WASI cannot express — the x86-64 and arm64 legs check those. Each leg's `testdata/selfhost-<target>-known-divergences.txt` rows, which are listed rather than fixed |
| `internal/e2eselfhost` | The self-host compiler is right on programs outside its own sources | Whole-program self-compilation; memory |
| Per-module / emit-all fixpoint | The compiler reproduces itself, deterministically | Any *stable* miscompile, including one affecting every program it sees |
| rc corpus (`rcCorpus`, all three backends) | No rc over-release on the shapes it enumerates | Shapes it does not enumerate — add one when you fix an rc bug |
| Cliff corpus (`rc_arr_push_cliff_test.go`, `rc_call_result_materialise_test.go`, `rc_cliff_bytes_test.go`) | The NATIVE compiler emits no stray retain on the accumulator shapes it enumerates — i.e. they are not quadratic — and that the crossing COUNT and its byte WEIGHT stay in step | Over-*retains* on any other shape, and every self-host emitter |
| Driver rc guard (`util.rc_underflow_guard`) | The compiler's OWN heap accounting stayed balanced while compiling | Leaks (an over-*retain* is silent), and anything outside the drivers |
| `FERN_NATIVE_ASM=1` fixtures | The in-process assembler encodes what the backend emits | The gcc path, which the fallback silently hides behind |
| Differential (`internal/e2e/diff_oracle_test.go`) | Two compilers agree on exit codes | Everything about memory — see below |
| `scripts/selfhost-emit-hashes` (manual, before/after) | A refactor of the self-host compiler was PURE: the bytes it emits for the whole fixture corpus, on all three backends, are unchanged | Anything the corpus does not exercise. It says the output did not move, never that the output is right |

**Reach for `scripts/selfhost-emit-hashes` on any mechanical refactor of the
self-host compiler.** It is the gate that fits the failure mode: whole families
of values there share one type — the `FnSigs` registries are all `string[]`,
the IR ops all `ir.Op` — so a crossed wire or a dropped argument type-checks
cleanly and surfaces only as a miscompile. The fixpoint will not catch it
(self-referential, see above) and the type checker cannot. Comparing emitted
bytes over ~1000 (fixture, target) pairs will. ~8 minutes per side.

## What nothing gates

Worth knowing so you do not assume coverage you do not have:

- **Allocation volume — now gated, by
  `TestSelfHostAllocDifferentialX86_64`.** Nothing used to compare how much the
  two compilers allocate, which is how they developed *opposite* cliffs
  undetected: this entry recorded `.with` through a borrowed param at 4688 MB
  native / 0 MB self-host and `.append` through a call at 4 MB native / 7006 MB
  self-host. Re-measured 2026-08-04 by the gate itself: `.with` is 31 KB native
  / 1 KB self-host at n=80 (31x, direction unchanged), and `.append` through a
  call is 3 KB native / 0 KB self-host at n=400 — the **opposite** of the figure
  above, i.e. the self-host side of that one was fixed at some point and nobody
  noticed, which is the argument for the gate rather than against it. Treat
  unmeasured allocation figures in these docs as expired.

  Quote figures the gate produced, not ones from a hand-run `fern` CLI — but
  not for the reason this note used to give. It claimed the two "compile
  through different pipelines"; they do not. Measured 2026-08-05 (#6034), the
  harness path, the harness path plus `treeshake`, and the real CLI report the
  SAME `__heap_bump_bytes()` for identical source, on both the `.append` and
  `.with` shapes.

  The numbers differ because they measure different QUANTITIES. The gate's
  figure is a per-churn DELTA — `bumpSrc` runs the churn twice and subtracts,
  so it reports only what the first churn failed to give back. A hand CLI run
  of `__heap_bump_bytes()` reports the absolute high-water mark, which includes
  startup and everything the first churn allocated fresh. Comparing the two is
  a units error, not evidence of a pipeline difference.

  The gate compares the two probes both compilers now support and asserts what
  survives a layout difference: `__arr_push_shared_count()` agreeing on ZERO vs
  NON-ZERO (it counts events, not bytes — but not exact equality, since the two
  runtimes grow capacity on different schedules), and per-churn
  `__heap_bump_bytes()` growth within a ratio. Divergences are listed in
  `internal/e2eselfhost/testdata/alloc-differential-known-divergences.txt`, and
  a listed shape that comes back within bound fails too.

  Use `__heap_bump_bytes()` and never peak RSS, which varies 12x with
  transparent hugepages (measured: 43 MB local, 552 MB on a CI runner, same
  binary and input). It returns i64 — bind it to an `i64` and narrow with an
  explicit `as i32` only where the value has to become an exit code.
- **Over-retains.** The rc detector counts over-*releases* only. A leak reads
  as a clean 0. `FERN_LEAKCHECK=1` sees that a leak happened and
  `FERN_RC_TRACE=1` names the alloc site it came from (both below, on the
  native *and* the self-host x86-64 compilers), but neither runs as part of
  any gate — you have to go looking.

## Practical rules

1. **Match the gate to the layer you touched.** Parser-time desugar → parser
   test. Checker rule → checker test. RC/lowering → the rc corpus *and*
   `internal/e2eselfhost`.
2. **A green fixpoint is not a substitute for `internal/e2eselfhost`** on any
   self-host lowering change. It was three times in a row, and it was wrong
   three times in a row.
3. **Run flaky-looking things at least 10 times.** The #6021 segfault was
   ~50%. A single-shot check passes and tells you the bug is gone.
4. **Verify which binary you are testing.** Building `fern.fern` proves nothing
   about a test that builds `asm_ir_run.fern`. Check what the test's
   `copySelfHostDriver` / `buildSelfHostBin` call actually names.
5. **Never pipe a test run through `tail` / `head`.** You get the pipeline's
   exit status — `tail`'s, always 0 — so a failing suite is reported as a pass
   and the detail is discarded. Redirect to a file and grep `--- FAIL`.
6. **`ok` in 0.3s is a SKIP, not a pass.** Usually a missing toolchain; fix the
   dependency rather than taking the green.

## Diagnostic modes

When a gate fails and the failure is a heap corruption rather than a wrong
answer, these are the tools, in the order they are usually reached for:

- **`__rc_underflow_count()`** — the counter. Exact yes/no signal for "did this
  compile over-release anything", readable from Fern. The self-host drivers
  call it themselves on every run (`util.rc_underflow_guard`).
- **`FERN_RC_UNDERFLOW_TRAP=1`** — turns each counter bump into `ud2`, so the
  process dies with SIGILL *at the offending dec* and a gdb backtrace names the
  function. This is the one that locates a bug; the counter only detects it.
- **`FERN_RC_FREE_DEBUG=1`** — quarantines freed array/map blocks and traps on
  a later touch. Complementary, not redundant: it sees an over-release that
  went through a free, and is blind to a plain dec taking a count 1 → 0 (which
  frees nothing) followed by another. Use `FERN_RC_UNDERFLOW_TRAP` for that
  case.
- **`__arr_push_shared_count()`** — the rc==1 cliff counter, for the other
  failure mode: a compile that is CORRECT but quadratic. `__fern_arr_push_grow`
  mutates in place only at rc == 1; one stray retain upstream makes every
  append in a threaded accumulator copy the whole buffer, and nothing about the
  program's output changes. This counts the appends that copied a buffer which
  still had spare capacity — so the copy was bought by an extra reference, not
  by a full buffer. 0 on a healthy run, which makes it an assertion rather than
  a profiling curiosity. Reach for it whenever memory grows faster than the
  data does; it names the cause where `__heap_bump_bytes()` only shows the
  symptom.
- **`__arr_push_shared_bytes()`** — the same crossings WEIGHTED by the bytes
  they copied, as an i64. Use the count to answer "did anything cross"; use
  this to answer "does it matter", and **never rank work by the count alone**.
  The two readings differ by three orders of magnitude on real runs: a
  whole-module compile of `checker.fern` by the self-host compiler crosses 188
  times and copies **812 bytes** doing it (they are 4-byte loop-depth stacks in
  `irlower`'s `enter_loop`, reachable only by attribution — see below), while
  one threaded accumulator over 20k appends crosses ~20k times and copies
  **2.3 GB**. Two rounds of accumulator work were scoped against the unweighted
  count and aimed at sites that could not have paid. The compiler's own readout
  (`FERN_CLIFF_REPORT=1`, `util.arr_push_cliff_report`) prints both.
- **Attributing a crossing to a function** needs no flag: build with `-g`, find
  the counter-bump instruction inside `__fern_arr_push_grow`
  (`objdump -d --disassemble=__fern_arr_push_grow`), break on it under gdb and
  report `info symbol *(unsigned long *)$rsp` — the return address names the
  function whose append copied. That is how the 188 crossings above were traced
  to two functions in one file; `printf "%d %d", $esi, $edx` at the same
  breakpoint gives each crossing's oldLen / stride if you want the distribution
  rather than the total.
- **`FERN_LEAKCHECK=1`** — alloc/free counts and live bytes at exit. The other
  direction: what the rc detector cannot see.
- **`FERN_RC_TRACE=1`** — one stderr line per heap event:
  `rctrace <a|f> <ptr> <size> <site>`, all three numbers fixed-width 16-hex,
  `site` being the *caller's* return address. Stands to `FERN_LEAKCHECK` as
  `FERN_RC_UNDERFLOW_TRAP` stands to the underflow counter: leakcheck says a
  leak happened, this says which alloc site it came from. Pair the `a` lines
  against the `f` lines by pointer and what is left never came back; resolve
  those sites with `-g`. Run both flags together and the summary tells you how
  much to look for. Two caveats: it is per-event output, so reduce to a repro
  before reaching for it on the self-host compiler, and **`f` sites are much
  less informative than `a` sites** — every release funnels through the shared
  drop helpers, so a free line usually names `__fern_arr_dec` rather than your
  code. The alloc site is the one that locates a leak. x86-64 only.

  **Both flags work on the SELF-HOST x86-64 compiler too**, with the same
  spelling and the same output format (`asm_ir.fern`; the arm64 and wasm
  self-host backends do not have them). That is what makes them useful for the
  goal-2 (Perceus port) deltas: the remaining gap versus native is an
  allocation-VOLUME difference, and volume is exactly what nothing else gates.
  Compile one program with each compiler under `FERN_RC_TRACE=1`, pair both
  traces, and an `a` line the self-host emits where native emits none is a
  missed reuse, attributed to the construction site that missed it. Three
  differences from the native side to expect when reading self-host output:
  - Sizes come from an **8-byte-granular** heap (native rounds to 16), and
    both hooks report the same rounded size for a block, so an alloc and its
    free cancel exactly.
  - Large-tier (>=512 KiB) blocks report their **logical** size, not the
    512-KiB-rounded capacity the arena actually bumped, so `live_bytes`
    measures demand rather than footprint.
  - Allocations grown by an **append** report `__fern_arr_push`, not the
    appending function — `__fern_arr_push` allocates only on its grow path, so
    it cannot claim a site up front without leaving a stale one behind. Use the
    `__fern_arr_push_grow` breakpoint recipe above for those; construction
    sites (array/string/struct literals) do report user code.

  The self-host driver has no `-g`, so resolve its sites against the linked
  binary's symtab (`nm -n`) rather than addr2line.
- **`-g`** — emits a `.symtab`, without which a gdb backtrace through a Fern
  binary is addresses only.

A worked example of the whole loop — counter to trap to backtrace to root
cause — is #6021.
