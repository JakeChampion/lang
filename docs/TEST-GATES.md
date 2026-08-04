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
| `TestFernFixturesSelfHost{Wasm,X86_64}` (`FERN_SELFHOST_FIXTURES=1`) | The self-host compiler agrees with native on the corpus, on wasm and x86-64 | **arm64 entirely** (no leg yet — blocked on #6044/#6045/#6047); and values >= 126 on the wasm leg, which WASI cannot express — the x86-64 leg checks those |
| `internal/e2eselfhost` | The self-host compiler is right on programs outside its own sources | Whole-program self-compilation; memory |
| Per-module / emit-all fixpoint | The compiler reproduces itself, deterministically | Any *stable* miscompile, including one affecting every program it sees |
| rc corpus (`rcCorpus`, all three backends) | No rc over-release on the shapes it enumerates | Shapes it does not enumerate — add one when you fix an rc bug |
| Cliff corpus (`rc_arr_push_cliff_test.go`, `rc_call_result_materialise_test.go`) | The NATIVE compiler emits no stray retain on the accumulator shapes it enumerates — i.e. they are not quadratic | Over-*retains* on any other shape, and every self-host emitter |
| Driver rc guard (`util.rc_underflow_guard`) | The compiler's OWN heap accounting stayed balanced while compiling | Leaks (an over-*retain* is silent), and anything outside the drivers |
| `FERN_NATIVE_ASM=1` fixtures | The in-process assembler encodes what the backend emits | The gcc path, which the fallback silently hides behind |
| Differential (`internal/e2e/diff_oracle_test.go`) | Two compilers agree on exit codes | Everything about memory — see below |

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

  Quote figures the gate produced, not ones from a hand-run `fern` CLI: the two
  compile through different pipelines and report different totals for identical
  source (the same `.with` shape read 48 KB / 2 KB via the CLI). The gate is
  sound either way — it compares the two COMPILERS under one consistent
  pipeline — but the two sets of numbers are not interchangeable.

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
  `FERN_RC_TRACE=1` names the alloc site it came from (both below), but
  neither runs as part of any gate — you have to go looking.

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
- **`-g`** — emits a `.symtab`, without which a gdb backtrace through a Fern
  binary is addresses only.

A worked example of the whole loop — counter to trap to backtrace to root
cause — is #6021.
