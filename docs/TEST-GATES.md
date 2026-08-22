# Which gates prove what

A guide to picking the suites a change actually has to pass, written after two
compiler bugs shipped past three heavyweight green gates in a row.

This document is about *which* lanes carry signal for *which* kind of
change, and — more usefully — which ones look authoritative and are not.
Every lane now runs on every push; there is no way to skip one.

## The lane that runs when Actions does not

All of the below live in GitHub Actions, so a GitHub incident takes the whole
set out at once, and pushes keep landing while nothing reports. The Netlify
deploy is the one lane on a different host: it already builds the docs site on
every push, so `netlify.toml` runs `scripts/netlify-smoke-test` after the site
and a failing stage turns the deploy red.

It is a *smoke* lane and it is sized like one — one small container, single
digit minutes shared with the docs build, against the sharded tens of minutes
the real workflows get. It runs four stages, cheapest-and-broadest first:
`go build` + `go vet`; every unit-test package (`scripts/unit-test-packages`,
which `test-units` also runs, so the two cannot cover different sets); the
whole conformance corpus on interp / x86-64 / wasm; and the self-host x86-64 IR
path via one `asm_ir_run` driver build. What it cannot reach: arm64 anything
(no qemu, no cross-gcc, and an unprivileged build cannot apt-get them), the
fixpoints, the differentials, and every suite in the table below that it does
not name.

Two things about reading it. Its summary block is the whole report — it prints
per-stage PASS / FAIL / SKIP with reasons, and per-backend fixture counts for
the conformance stage, precisely so a container that quietly lost a toolchain
cannot look like a full pass. And it will SKIP late stages when the deploy
budget runs out; a skip is reported, never silently dropped, but it does mean
**a green Netlify deploy is not a claim that every stage ran** — read the
summary, not the colour. `FERN_SMOKE=off` turns the lane off once Actions is
healthy; `FERN_SMOKE=advisory` keeps it reporting without blocking a deploy.

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

### An answer is not proof the IR path produced it

A case that asserts only an exit code cannot show a shape **stayed on the IR
path**: a per-function bail can reach the same answer by another route, so the
case passes on the commit the fix has not landed on. No asm-label witness
separates the two either — a module that bailed still emits `.Lir_*` labels for
the functions that did lower.

Use `runCaptureStrictIR` instead of `runCapture` when that is the point (#6602).
It sets `FERN_STRICT_IR=1`, so a bail exits 3 naming the site and fails the test.
Most `*_ir_test.go` suites still use plain `runCapture` and have not been
audited against the flag; any that stop passing under it were pinning a bail
rather than the IR path.

## Gate → what it actually proves

| Gate | Proves | Blind to |
|---|---|---|
| `make check-sources` (lint job on every PR; check-sources.yml on every push to main) | The self-host sources and every stdlib module (each as a standalone import — `tools/stdlib_check.sh`, the CLI twin of `TestStdlibModulesImportStandalone`) type-check under the native `fern -check`. Every self-host lane needs this but asks it only implicitly, by building a driver, so a type error surfaced as a build-step failure in a job named for something else — and a merge race landed an unbuildable main with every contributing PR green (#7317). The main-push leg reports that against the merge that caused it | Whether anything RUNS — no codegen, no link, no execution. The SELF-HOST checker's verdict: this is native's, and the self-host build path filters through `filter_build_gate`, so the two can disagree by design (#7273 / #4346) |
| `internal/e2e` fixtures (`TestFernFixtures`) | The NATIVE compiler is right on the corpus | Anything self-host-only; anything about *how much* it allocated |
| `TestFernFixturesSelfHost{Wasm,X86_64,Arm64}` (`FERN_SELFHOST_FIXTURES=1`) | The self-host compiler agrees with native on the corpus, on all three emitted targets. Both Linux legs produce the finished binary by themselves (emit + assemble + link in-process), so they are also the gates on `arm64_native.fern` and `x86_native.fern`. NOTE what that does NOT gate: an assembler that DROPS an instruction still emits a plausible binary, so a green leg is not evidence the assembler is complete — `rep stosq` was silently ignored at 63,637 sites and this leg stayed green on the programs that did not depend on zeroed memory. The gate for THAT is a decoded-instruction differential against the same program built via `-target x86-64-linux -emit asm` and linked by gcc | Values >= 126 on the wasm leg, which WASI cannot express — the x86-64 and arm64 legs check those. Each leg's `testdata/selfhost-<target>-known-divergences.txt` rows, which are listed rather than fixed |
| `internal/e2eselfhost` | The self-host compiler is right on programs outside its own sources | Whole-program self-compilation; memory |
| Formatter corpus properties (`internal/printer/corpus_test.go`, plus `TestSelfHostFmtCorpusParityX86_64`) | That `-fmt` does not rewrite the program. Three properties over all 425 `.fern` files under examples/ + internal/stdlib rather than a fixture list: the formatted output still TYPE-CHECKS (where the input did), it re-parses to the same AST modulo position (the structural one — the only property that sees data loss whose output still compiles, e.g. `Box[i64]` dropping to `Box[i32]`), and formatting is idempotent. The self-host leg adds byte-parity with native over the same corpus, against an allowlist that is exact in both directions. `TestSelfHostFmtWrittenFormViaInterp` states the structural + byte-parity + type-check properties for the SELF-HOST formatter over the fixture cases without a cross toolchain, by driving `fern.fern` through the native `-interp` — every other self-host `-fmt` gate is suffixed `X86_64` and skips on any other host, so the #6802 shapes were unreachable from a dev machine while being reproducible there in ~0.35 s | Comment PLACEMENT, which no property here pins — a comment moved onto the wrong declaration still type-checks, re-parses identically (they are a side table) and is stable. Only byte-parity with native catches that, and only while native is right |
| Self-host IR verifiers (`TestSelfHostIRVerify{Structure,Stack,Fip}` and their `*CorpusClean` sweeps) | The op stream the self-host backends are handed is WELL-FORMED: local indices inside the frame, balanced scopes, in-range branch depths, call arity (`irverify.fern`), and that every op finds its operands, every scope leaves the stack where it found it, and nothing is left dangling at the function's end (`irverifystack.fern`). Its distinguishing value is that it is not self-referential — it does not care whether the compiler reproduces itself, only whether what it emitted can be lowered at all. It runs on the compile path by default (`irverifygate.fern`), which is where the arity half gets a real signature index; `FERN_IR_VERIFY=0` opts out, which is what a bisect wants when the gate itself is the suspect | Whether the ops MEAN the program: a perfectly well-formed stream can compute the wrong answer. Operand KINDS and widths, which the IR does not carry. Any function that did not lower, which has no IR to verify. Any op outside the stack pass's arity table, which is skipped and COUNTED — the corpus gate holds coverage at 100%, so a new op shows up there rather than as an unchecked function |
| `TestSelfHostFipCensusOnNativesShapes` | The self-host reuse layer's PAIRING state, measured against the exact shapes native's `internal/ir/fip_verify_test.go` pins: per function, how many constructor sites lowered fresh and how many paired into a donor box. Today R3 general pairing matches native and R1 self-overwrite / R4 consuming-match do not, so this is the gate that notices when either is ported | Whether the pairing is CORRECT — it counts sites, it does not check the donor is dead or uniquely owned. It also does not compare against native automatically: the expected counts are written down, so closing a gap fails the test and the numbers are updated by hand |
| Per-module / emit-all fixpoint | The compiler reproduces itself, deterministically | Any *stable* miscompile, including one affecting every program it sees |
| `TestSelfHostArm64DarwinMachO*` (macos.yml) | That a Mach-O the SELF-HOST toolchain assembled and ad-hoc-signed actually LOADS and runs: `arm64_native.fern`'s encoders + `macho.fern`'s container, executed by the XNU kernel. The exec half needs Apple Silicon, and it needs a driver the host can run — on darwin the `wasm_run` driver is built for `arm64-darwin` rather than as an x86-64 ELF, which is what the Linux shards use. Until #6849 the exec half ran on NO lane and an `add Xd, Xn, Xm, lsl #N` whose shift the self-host assembler silently dropped read every array element as `a[0]` | The Linux shards reach only the structural half (parse the Mach-O); they cannot launch it. And a kernel rejection is a hard failure here, not a skip — that distinction is #6042 |
| `TestSelfHostStage2FixpointArm64` (own CI job) | The arm64 emit is a fixed point of itself AND independent of the host arch: generation 2 is a real aarch64 compiler linked from generation 1's own asm, run under qemu, and the two must emit byte-identical output. Its distinguishing value is that generation 2 runs THROUGH the assembler's own output, which is what separates "the emitter is wrong" from "the assembler is wrong" — three arm64 assembler bugs were each mis-attributed to codegen first | Everything the two generations get wrong *identically*, which is the same blindness every fixpoint has. The `self` case (gen2 recompiling the whole compiler) is behind `FERN_STAGE2_SELF=1` and does not run in CI, so the span it actually gates is four inputs, not the compiler |
| rc corpus (`rcCorpus`, all three backends) | No rc over-release on the shapes it enumerates | Shapes it does not enumerate — add one when you fix an rc bug |
| Cliff corpus (`rc_arr_push_cliff_test.go`, `rc_call_result_materialise_test.go`, `rc_cliff_bytes_test.go`) | The NATIVE compiler emits no stray retain on the accumulator shapes it enumerates — i.e. they are not quadratic — and that the crossing COUNT and its byte WEIGHT stay in step | Over-*retains* on any other shape, and every self-host emitter |
| Driver rc guard (`util.rc_underflow_guard`) | The compiler's OWN heap accounting stayed balanced while compiling | Leaks (an over-*retain* is silent), and anything outside the drivers |
| `FERN_NATIVE_ASM=1` fixtures | The in-process assembler encodes what the backend emits | The gcc path, which the fallback silently hides behind |
| `RUN_SECCOMP_CORPUS=1` (`TestSeccompFixtureCorpus`) | The seccomp filter is not too TIGHT: every runnable fixture behaves identically sandboxed and not | Whether the filter DENIES anything — that is `TestSeccompFilterDenies`. A permit-all filter passes this gate trivially |
| `RUN_SHRINK_PROPERTY=1` (`TestGenBytesShrinkIsMonotonicAndValid`) | fernsmith's minimisation contract: chopping a byte off a corpus yields a program that still type-checks and is never LARGER, so a failing fuzz input reduces to a small repro. Runs 3 seeds unguarded; the env var widens it to 24 | Whether the generated programs are interesting. A generator that emitted `function main(): i32 { return 0i32; }` for every input satisfies it perfectly |
| Differential (`internal/e2e/diff_oracle_test.go`) | The NATIVE compiler's backends agree with interp on exit codes, over 2048 fernsmith-generated programs — 43% of which now carry a lambda and 6% an escaping closure | Everything about memory — see below. Anything the generator cannot emit — a shape absent from `gtype` is untested no matter how many seeds run. And whatever sits in its `knownDivergences` table |
| arm64 flat-vs-SSA differential (`internal/e2e/arm64_ssa_differential_test.go`, `TestArm64SSABackendDifferential`) | That `-target arm64-linux -backend ssa` behaves like the SHIPPING arm64 backend on the whole `examples/**` corpus (281 programs; self_host excluded), comparing exit status AND stdout. Three outcomes, not two: a clean refusal is the documented coverage subset working and is counted separately, while a refusal that is really a compiler SIGNAL, or a non-zero exit with no diagnostic, fails. Its value is that it samples a corpus — the neighbouring `arm64_ssa_test.go` is ~80 hand-written cases, so it only ever saw shapes somebody wrote down, and both defects behind #7325 (a fixed 64 KiB .bss bump heap; f32 stored into a multi-slot aggregate landing only in the last slot) shipped through that gap. Measured 2026-08-22: 156 agree / 61 refuse / 60 diverge, all 60 listed in `testdata/arm64-ssa-diff-known-divergences.txt`, which is exact in both directions | Everything the corpus does not contain, and every backend but arm64ssa: `x86_64ssa` and `-backend ssa` (wasm) have no corpus differential at all. Programs listed in `testdata/arm64-ssa-diff-stdout-unstable.txt`, which are compared on exit status only. Memory, allocation volume, and emitted-instruction count, none of which it looks at |
| Self-host checker CODE differential (`internal/e2eselfhost/self_host_checker_codes_test.go`) | That the two checkers ACCEPT AND REJECT the same programs: `TestSelfHostCheckerCodesX86_64` pins a hardcoded code set per row AND compares against the Go checker, `TestSelfHostChecker{,Bundle}DifferentialX86_64` use the Go checker as sole oracle. The hardcoded half is not redundancy — a pure differential cannot tell "both emit E072" from "neither emits anything" | What the two checkers SAY. It scrapes `E\d{3}` and discards the rest, so the two can word the same code arbitrarily differently, or identically wrongly. #6990 shipped four hints naming a spelling that does not compile, in both compilers, under a green run of this gate (#7018) |
| Self-host checker HINT-TEXT differential (`internal/e2eselfhost/self_host_checker_hint_text_test.go`) | That the two checkers give the same ADVICE where a diagnostic tells the reader what to write. Deliberately narrow: the corpus holds only messages naming a spelling, since that is the text where a difference is a bug rather than a style — forcing all prose into lockstep would fight the partial self-host checker. `hintTextDivergences` records the rows meant to differ, and is exact in both directions: a listed row that CONVERGES fails too. "Self-host says nothing" is listed as a divergence rather than skipped, so the port's remaining hint gaps are enumerable. Runs the driver under `-interp`, so unlike its `X86_64` neighbours it needs no cross toolchain and runs on every host (~5 s) | Every message outside the corpus, and whether the advice is CORRECT — two compilers can agree on a hint that does not compile. That half is `internal/checker/derive_hint_test.go` (compile the suggestion) and `internal/sourcelint/diag_suggestion_spelling_test.go` (no bare trait name in either compiler) |
| Self-host differential (`internal/e2e/diff_oracle_selfhost_test.go`, `FERN_SELFHOST_DIFF=1`) | The SELF-HOST compiler agrees with interp on exit codes, over its own 512-seed corpus, compiled through the real CLI and linked with gcc. This is the path the closure-dispatch cluster (#5001/#5007/#5009/#5026) lived on, and the only sweep that reaches it | The 57% of seeds the self-host compiler declines as not IR-eligible — a documented endpoint, but it means this leg tests less than half the corpus it samples (there is a floor on the ratio so it cannot quietly hollow out). The other two self-host targets: this leg is x86-64 only, and `-target arm64-linux` is the path where the compiler emits, assembles AND links by itself. And whatever sits in its known-divergences file |
| `scripts/selfhost-emit-hashes` (manual, before/after) | A refactor of the self-host compiler was PURE: the bytes it emits for the whole fixture corpus, on all three backends, are unchanged | Anything the corpus does not exercise. It says the output did not move, never that the output is right. **And the SSA backends entirely** — see the `--ssa` row |
| `scripts/selfhost-emit-hashes --ssa` (manual, before/after) | The same, for `ssa_x86` / `ssa_arm64` / `ssa_wasm`, which the default sweep NEVER REACHES because the SSA pipeline is opt-in behind `-ssa`. Passing `-ssa` alone is not enough: `try_ssa` is all-or-nothing and falls through to the IR path for any program outside the SSA subset, silently — so most rows would be IR bytes, identical across any SSA-backend change. This mode emits each case BOTH ways and marks a row `IR-FALLBACK` when they agree, so only rows the SSA backends really produced can gate. 421 of the 1491 rows land there (measured 2026-08-22); a run with fewer than 100 is refused rather than reported | The other 1070 rows — 941 that fell through to the IR path and 129 the SSA pipeline refused — which the markers make countable rather than invisible. Everything the default sweep is blind to, which still applies |
| `scripts/perf-bench` + `scripts/ci-check-perf` (perf.yml) | That the emitted and the retired instruction counts of the `examples/bench` corpus have not moved. Both metrics are deterministic per commit — a static count is a property of the emitted text, a retired count on a static binary repeats to the digit — so the 1% default tolerance is a significance threshold, not a noise floor, and a 1% regression is real. Baseline: `.github/perf-baseline.txt` | Everything the corpus does not contain, which today is most of the language. Wall time, which it deliberately does not measure. Whole-compiler compile time, which nothing gates. And it is ADVISORY: findings are warnings, `FERN_CI_PERF_GATE_STRICT=1` makes them fatal |
| `scripts/perf-bench-selfhost` + `scripts/ci-check-perf` (perf.yml) | That what the SELF-HOST compiler EMITS for the `examples/bench` corpus has not moved, on all THREE targets from one build — the static count needs no execution, so one binary cross-emits x86-64, arm64 and wasm. Deterministic per commit (verified over three consecutive runs), so the 1% default is a significance threshold. Its real subject is the RATIO between targets on one program: one backend drifting from the others is what an unconditionally-emitted runtime bundle looks like, which is how arm64 carried ~400 instructions of f64 transcendental runtime in every program until #2649 gated it. Baseline: `.github/perf-baseline-selfhost.txt` | Correctness — it says the output did not change size, never that it is right, so pair it with the fixpoint and `internal/e2eselfhost`. Everything the corpus does not contain. The self-host compiler's own compile TIME, which nothing gates. Per-module builds, which over-approximate needs and so hold runtime a single-unit compile drops. And it is ADVISORY, like the native lane |
| `internal/sourcelint` ambient-env gate (`TestSourcelintChildEnvIsFiltered`) | That no test in that package splices the inherited environment straight into a child process. Its tests drive CI scripts whose behaviour the environment decides, so an ambient `FERN_CI_*` value would otherwise choose what the test asserts — and a vacuous test and a passing test are byte-identical in the log. The sibling of `TestNoSilentlyCIDarkEnvGates`: that one catches a test that never RUNS, this one a test that runs and cannot FAIL | Every other tree. The e2e packages inherit at ~23 sites; most set the one variable they depend on and are fine, and sorting the rest needs reading rather than a rule — #6833 |
| AST traversal exhaustiveness (`internal/ast/walk_exhaustive_test.go`, `internal/shadowrename/exhaustive_test.go`) | That no pass with a hand-written switch over `ast.Expr` / `ast.Stmt` has fallen behind the unions. `ast.NodeKinds` is the one list of node kinds, checked against the set derived from the ast package's own source (every Node has a `Pos` method), and every kind is driven through `Walk`, the `RewriteProgramExprs` traversal, both cloners and shadowrename's two walks — each of which panics on a kind it does not name. It also pins the FIELDS: every Expr-, Stmt- and *Block-typed field must be reached by `Walk` or listed in `walkSkips` with a reason, exact in both directions, and a clone must freshly allocate everything `Walk` reaches. A new node kind fails here rather than being silently ignored by one pass — the shape of #7042, #7149 and the lambda-shadow miscompile | Passes that keep their own switch and do not test against `ast.NodeKinds`: the parser's and the checker's for-in desugars each carry one. Whether a case is CORRECT — it proves the arm exists and descends, never that it does the right thing there |
| Netlify smoke lane (`scripts/netlify-smoke-test`) | The tree compiles, the unit packages pass, the corpus runs on interp/x86-64/wasm and one self-host driver builds and compiles a program — on a host that is up when GitHub Actions is not | Everything arm64, every fixpoint and differential, and any stage it SKIPPED for budget. Its summary block says which; the deploy's colour does not |

**Reach for `scripts/selfhost-emit-hashes` on any mechanical refactor of the
self-host compiler.** It is the gate that fits the failure mode: whole families
of values there share one type — the `FnSigs` registries are all `string[]`,
the IR ops all `ir.Op` — so a crossed wire or a dropped argument type-checks
cleanly and surfaces only as a miscompile. The fixpoint will not catch it
(self-referential, see above) and the type checker cannot. Comparing emitted
bytes over 1491 (fixture, target) pairs will. **~28 minutes per side**
(measured 2026-08-22; this said ~8, from a smaller corpus).

**Any change to `examples/self_host/*.fern` must run
`TestSelfHostFeatureCensus`, whatever the change is about.** Every other gate
here is chosen by what the code TOUCHES; the census triggers on what the code is
WRITTEN IN. It pins how much of Fern the self-host uses — nested named
functions, arrow lambdas, `for..in` loops, `as` casts — so introducing a
construct moves a count UP and removing one moves it DOWN, and a refactor does
both at once. The SH-022 walker migration moved it three times in one session:
+4 nested fns and −1 `for..in` from one slice, +3 and −4 from the next, +2 from
a bug fix. Each looked unrelated to the census right up to the moment CI failed
on it. It runs in under a second — there is no reason to select around it.

**Delete `bin/fern` before building the after side.** `make selfhost-cli` is
timestamp-driven and will happily reuse a `bin/fern` built from an older commit,
while a fresh baseline worktree always builds a current one — so once `main` has
moved under you, the two sides of the gate are built by TWO DIFFERENT Go
compilers and the diff reports the intervening commits as your impurity. It
looks like a real finding: 473 of 1491 rows, spread evenly across all three
targets. The tell is scope — a change confined to one backend cannot move the
other two — and the check is one fixture re-emitted after `rm bin/fern
bin/fern-selfhost && make selfhost-cli`. Do not compare the two `bin/fern`
binaries to test this: Go embeds build paths, so a worktree copy differs from an
identical-source repo copy anyway.

**If the refactor touches `ssa_x86` / `ssa_arm64` / `ssa_wasm`, that run proves
nothing about it** — pass `--ssa`. Measured: changing one emitted string in
`ssa_arm64.fern` leaves the default sweep byte-identical and moves every arm64
row of the `--ssa` sweep. `--ssa` costs twice the emits — **~55 minutes per
side** — since it emits each case both ways to tell an SSA row from an IR
fall-through.

## What nothing gates

Worth knowing so you do not assume coverage you do not have:

- **The ORDER of the self-host checker's diagnostics, and how many times it
  reports the same code.** Every gate over checker output reduces to a set
  before comparing: the codes differential
  (`self_host_checker_codes_test.go`) takes "the sorted, de-duplicated set of
  diagnostic codes", the hint-text differential groups into "code → sorted
  unique messages", and the driver test asserts only that a `wantDiag`
  substring APPEARS on stderr. So a change that reorders diagnostics, or
  reports one twice, is invisible to all three while being plainly visible to
  a user.

  This bites the SH-022 walker migration specifically. Folding a diagnostic
  collector onto `astwalk` replaces its traversal with the shared one, and the
  two need not visit siblings in the same order; pruning it wrongly duplicates
  a report rather than losing one. `e049_expr_lambdas` (#7359) hands each
  lambda body to `e049_check_assigns` and must therefore PRUNE at
  `ExprLambda` — with the plain fold it would report every nested lambda once
  per enclosing one, and every gate above would still be green. That one was
  settled by reading the code, which is the only instrument currently
  available: **verify a diagnostic collector's order and multiplicity by hand,
  or add an order-sensitive check first.**

- **Operand-stack balance in the EMITTED native asm — now gated on arm64, by
  `TestOperandStackBalancedAcrossTwoWordDiscard`**
  (`internal/codegen/arm64/stack_balance_test.go`). Two separate things hide a
  stack-discipline break on the natives, and they cover for each other.

  First, `ir.Verify`'s stack half is *wasm validation*, deliberately: after a
  `br` the operand stack is polymorphic and `end` truncates it back to the
  frame height. A residual slot on a path that ends in a branch is therefore
  legal there and correctly not reported — but arm64 maps the operand stack
  onto `sp` and re-syncs it at no label, so the same IR leaks. `ir.Verify` is
  not, and cannot be, the gate for this class on a native backend.

  Second, the arm64 epilogue's `mov sp, x29` restores `sp` from the frame
  pointer, so the leaked slots die with the frame and the program still
  returns the right answer. #7303 leaked 2 x 16 bytes per call of every
  function whose match arm bound an unread `string`, and no exit-code
  assertion anywhere in the tree could see it. Inlining, which deletes the
  callee epilogue, turns it into five miscompiles.

  So the invariant is checked by walking the emitted text: depth through
  straight-line code, per-label agreement across branches, zero at the return
  label. Anything sp-touching the walk does not model fails the test rather
  than being skipped. There is no equivalent gate on x86-64 (which has no
  two-word values on its operand stack) or on the SSA backends.

- **A checker driver that DIED, versus one that found nothing — now
  distinguished, by `checkerDriverFault`** (`internal/e2eselfhost`). The checker
  differentials turn the driver's stdout into a code SET, and empty is a
  legitimate answer there meaning "no diagnostics". So a driver that bailed
  (`checker_modload_run.fern` returns 2 and 3 on its own failure paths) or died
  on a signal read as a *clean check*, and the differential reported a code
  disagreement naming the checker — pointing at the wrong subsystem entirely.
  The runner now fails on any exit status other than 0 or 1 and surfaces the
  driver's stderr. Only the code-set call sites need it; a site that asserts the
  exit code itself already had the signal.

- **Allocation ASYMPTOTICS — now gated, by `TestX86_64AllocScaling`**
  (`internal/e2e/alloc_scaling_test.go`). The differential below compares the
  two compilers against *each other*, so a regression that lands in the shared
  frontend, or in a stdlib function both compile the same way, moves both sides
  equally and stays green. This gate asks the other question: does a shape
  still allocate in the complexity class it is supposed to?

  It measures `__heap_bump_bytes()` for one churn at `n` and at `2n` in
  separate cold processes and bounds the RATIO, not the bytes. A recorded
  byte budget rots — every legitimate change to a header size, a growth
  schedule or the SSO threshold moves every number at once, so budgets get
  re-recorded in bulk without being read and a real regression rides in with
  the batch. Constant factors cancel out of a ratio entirely. Measured on the
  current corpus: linear shapes sit at **2.00–2.06x** per doubling and the
  quadratic calibration case at **3.78x**, so the 2.20x bound has no tuning
  problem.

  The corpus carries a deliberate `wantQuadratic` **calibration case** (naive
  `s = s + x` left-fold) asserted to stay ABOVE the bound. If it ever drops
  below, either `+` grew a builder representation or the gate stopped
  discriminating — and in the second case every other row would be passing
  vacuously, which is the failure mode a green suite cannot otherwise report.
  Both failure directions are mutation-checked.

  This is the gate for the asymptotic class of bug that leaves answers
  correct: the naive substring search that went quadratic on repetitive input
  (2.655s → 0.014s) and the merge sort that copied the whole array every pass
  were both invisible to every correctness suite. ~1 s for the whole corpus.

  It is **x86-64 only**, and that is a real blind spot rather than a
  detail: a backend that allocates differently from the others cannot
  fail it. `docs/ALLOCATION-OBSERVABLE.md` states the same observable as
  a cross-backend conformance contract (`AL-01` / `AL-02` in
  `spec/semantics.md`), and the first run of its cases found the wasm
  backend never reclaiming a short-lived string — 32 fresh bytes per
  loop iteration, unbounded, where both natives are flat (#6423). Being
  a conformance case rather than a Go test, it also survives the freeze
  this document's own framing is organised around.

- **Allocation volume between the two compilers — gated by
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
- **The self-host's own x86-64 assembler, on any real program.**
  `-target x86-64-linux` assembles and links IN-PROCESS (`x86_native.fern` +
  `elf.fern`) — but every `internal/e2eselfhost` program test asks the driver
  for `-emit asm` and hands the text to gcc, and so does the x86-64 fixtures
  leg. What reaches `x86_native.fern` is `TestSelfHostX86{Encode,Gas,Capstone}`
  (byte-level, one instruction at a time) plus a few small `-o` CLI tests
  (transitive deps, `pub` visibility, a rip-relative store). No rc-shaped or
  allocation-shaped program runs through it at all.

  So an encoding bug there is invisible to the whole corpus, and one was:
  `testl %ecx, %ecx` was assembled as `testq %rcx, %rcx` on the argument that
  the operands are zero-extended and ZF agrees. SF does not — a 32-bit -1 read
  by `movl` is a large positive at 64 bits — so the `js` on every "negative rc =
  immortal, skip" arm of the rc runtime fell through to the decrement path, and
  a program that handed any immortal block to `__fern_str_free` tripped the
  over-release detector (#6544). Both compilers agreed through gcc.

  **When a change touches asm the runtime emits, run the program BOTH ways** —
  `bin/fern-selfhost -o prog` and `-emit asm` into gcc — and treat a
  disagreement as the assembler until proven otherwise. Adding the shape to
  `TestSelfHostX86GasGroundTruth` (bytes from `as` + objdump, never from a
  reading of the manual) is what keeps it fixed.
- **Over-retains.** The rc detector counts over-*releases* only. A leak reads
  as a clean 0. `FERN_LEAKCHECK=1` sees that a leak happened and
  `FERN_RC_TRACE=1` names the alloc site it came from (both below, on the
  native *and* the self-host x86-64 compilers), but neither runs as part of
  any gate — you have to go looking.
- **A syntactic form native accepts and the self-host does not.** Nothing
  enumerates the language's surface, so parity is only ever tested at the
  shapes some case happens to use. `conformance/cases/` is the one corpus that
  reaches the self-host on all three targets — and whether a form appears
  there is up to whoever added it. A Go-inline e2e test does not count:
  `internal/e2e/param_pattern_test.go` builds its sources as Go string
  literals, so the struct-pattern and `@` parameters it covers are invisible
  to every self-host leg, and those two forms have been native-only since they
  landed.

  The failure this hides is not a rejection, which someone would notice. It is
  a self-host parser that reads the form as something ELSE and compiles a
  different program: a literal in a variant payload (`Some(0)`) was discarded
  outright, leaving a bare `Some` arm that matched everything — native said 2,
  the self-host binary said 1, and the corpus was green because no case wrote
  the form. Three pattern loops in the same file spun forever on input native
  rejects cleanly, for the same reason.

  So when you add a syntactic form, **write a `conformance/cases/` entry for
  it** and check the self-host actually agrees; a green fixture leg is a claim
  about the shapes in the corpus and nothing wider. `bin/fern-selfhost -fmt`
  is the cheap probe — ~3 ms, and it prints the AST the self-host really
  built, which separates "rejected" from "silently read as something else".

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
   `copySelfHostDriver` / `buildSelfHostBin` call actually names. Same trap when
   quoting a SIZE: the three are tens of megabytes apart, and
   `.github/selfhost-driver-sizes.txt` names `fern.fern` as the one meant by
   "the self-host binary".
5. **Never pipe a test run through `tail` / `head`.** You get the pipeline's
   exit status — `tail`'s, always 0 — so a failing suite is reported as a pass
   and the detail is discarded. Redirect to a file and grep `--- FAIL`.
6. **`ok` in 0.3s is a SKIP, not a pass.** Usually a missing toolchain; fix the
   dependency rather than taking the green.
7. **A `-run` name that matches nothing is exit 0.** Deleting a test does not
   remove it from the workflow that names it, so the lane keeps listing
   coverage it stopped having. `test-e2e-arm64`'s cross-host job ran 15 of the
   17 tests in its regex for months, including the arm64 stage-2 fixpoint its
   55-minute budget was sized for — deleted with the AST emitters in #5972,
   still named in the regex, still weighted at 900s in
   `.github/selfhost-test-weights.txt` (#6310). `make testnames` now fails on
   a name in `.github/` that resolves to no test; when you retire a test, run
   it before assuming the workflows followed.
8. **A wrong shard WEIGHT fails the shard, and only by timeout.** An entry that
   badly understates a test pushes its bucket past the 18-minute
   `-test.timeout`, so an unrelated PR goes red for a scheduling error — twice
   so far (#5914, #6823). The `verify` job now audits the declared weights
   against the durations the shards report and prints a corrected-weights block
   in its job summary (`scripts/ci-test-weights`); it is advisory, so read the
   summary rather than waiting for a red. Weight pessimistically — the same test
   has measured 482 s and 738 s on consecutive runs.
9. **A parent reports PASS when every one of its subtests SKIPs.** So rule 6
   does not save you: there is nothing in the log to read. `TestStdArrayEqual`
   asserted nothing for its whole existence that way — the interp oracle
   skipped, which took the x86-64 and wasm legs with it (#6840). Two habits
   close it: an oracle helper for hand-written cases FAILS on a gap rather than
   skipping (`runInterpByte` / `interpStdout`; only generator-fed corpora get
   `runInterpByteOrSkip` / `interpStdoutOrSkip`), and each toolchain-gated
   backend leg goes in its own sub-test so a missing tool cannot abort the legs
   after it.
10. **A corpus walk that selects nothing PASSes with no sub-tests at all**, which
   reads exactly like a clean run. Every fixture-driven walk needs a floor:
   `runSelfHostFixtureLeg`, `forEachRunnableFixture`, `TestSeccompFixtureCorpus`,
   `TestFernFixtures` and the diff-oracle legs all fail on zero now. The selector
   is a token in each case's `backends` file, not a target name, so a rename
   quietly empties the set (#6849). The generator-fed sweeps carry the same floor
   as a RATIO — `TestNumericProperty_Differential` fails when under 80% of its
   seeds reach the oracle, because a wall of skips nobody totals is the same
   vacuum in slower motion.
11. **Most lanes cannot tell you whether a given test RAN.** They use gotestsum's
   `pkgname-and-test-fails` formatter, which prints one line per package plus
   failures — so a PASS, a SKIP and a cached replay are indistinguishable in the
   log, and "did test X actually run?" has no answer from that lane at all. The
   whole point of rule 6 is defeated when even the elapsed time is per-package.
   Where a lane exists SPECIFICALLY to establish that something runs, give it
   `--format standard-verbose` and `-count=1` (the latter defeats a cached PASS
   being replayed without executing the binary) and have the test log what it
   covered. `test-e2e-arm64.yml`'s backend-agreement step does exactly that: it
   reported `ok internal/e2e (64ms)` for a two-backend compile-link-run, which
   was unreadable either way, and now prints the case count it compared. This is
   NOT fixed lane-wide — the other lanes still hide passes — so on any question
   of the form "is this test running in CI" read the test's own output, not the
   lane's colour, and add the output if it is not there.
12. **A test that needs TWO backends on one host runs on neither single-backend
   lane.** `TestF64TranscendentalBackendsAgree` had compared nothing since it was
   written: the catch-all `test-e2e-other` lane owns its name, and each of that
   lane's arches has one of the two toolchains. It now runs on
   `test-e2e-arm64.yml`'s `cross-host` job (x86 host + aarch64 cross toolchain +
   qemu-user), which sets `FERN_REQUIRE_CROSS_BACKENDS=1` so a lane that loses a
   toolchain goes RED instead of quietly back to skipping.

## The IR path-probe tells you WHERE, never whether it is RIGHT

Route a probe program through the single-program path-probe driver
(`asm_pathprobe_run.fern`) and it prints `ir` or `ast`. Two ways that misleads.

**A bare `ast` verdict is not proof of a gap.** The driver routes *invalid*
programs to `ast` too. Always confirm the probe is **native-valid first**
(`go build -o /tmp/fern ./cmd/fern && /tmp/fern -interp prog.fern`) before
treating a bail as a real gap. Three filed issues were wrong for exactly this
reason. The single-program drivers now WARN on stderr when a program has imports
they cannot resolve (#6004); that warning means the verdict describes a broken
program and says nothing about the language.

Most apparent "gaps" are invalid programs:

- **Wrong keyword** — match guards are `when`, not `if`.
- **Checker-rejected** — a bare-ident arm on a SCALAR match is E035; i32 matches
  accept only literals or `_`.
- **Parse errors** — **every arrow-lambda parameter must be type-annotated**:
  `(x: i32) => x+1`, never `(x) => x+1` (P001). The annotation is what tells the
  parser it is looking at a lambda rather than a parenthesized expression or a
  tuple literal, decided before the matching `)` is reached. (`spec/grammar.ebnf`
  claimed the annotation was optional until the grammar was corrected to match.)
  The body is an EXPRESSION, but a `{ … }` block IS one — a value block
  (`(x: i32) => { x + 1 }`, and `(x: i32): i32 => { return x; }` since #6858) —
  so a block body is not itself the parse error this note used to call it.
- **Missing imports** — the path-probe driver resolves no stdlib, so anything
  needing `std/iter` / `std/map` / … falsely reads `ast`.

**And a green `ir` verdict says nothing about runtime correctness.** Differential
probing (native `-interp` exit code vs the self-host-IR-compiled binary's) found
a whole closure-dispatch bug cluster this methodology misses — escaping-closure /
closure-array shapes that lowered on the IR path but SIGSEGV'd or silently
miscompiled at runtime (#5001 / #5007 / #5009 / #5026, plus the param / branch /
transitive / direct-index fixes). Probe for the path, then probe differentially
for the answer.

## Diagnostic modes

When a gate fails and the failure is a heap corruption rather than a wrong
answer, these are the tools, in the order they are usually reached for:

- **`-sanitize` / `FERN_SANITIZE=1`** — start here. One compile-time flag over
  all three heap detectors below (over-release, use-after-free, leak census),
  for when you do not yet know which one will fire. A clean run prints no
  `fern-sanitizer:` line at all and leaves exit code and stdout untouched, so
  it drops into an existing harness; a finding is a named message plus a
  backtrace, and the two fatal checks exit 124. Both natives carry all three,
  with identical message text and status, so a finding does not depend on which
  backend built it. The **self-host x86-64 compiler reads `FERN_SANITIZE=1`
  too** (emit-time, like its `FERN_LEAKCHECK` / `FERN_RC_TRACE` ports), giving
  the census and the over-release report — which is what puts the mode on the
  long-running allocation-heavy program it was argued for. Full contract,
  costs, and what it does *not* catch:
  `docs/SANITIZER.md`. The individual flags below remain the right tool once
  you know what you are chasing.
- **`FERN_IR_VERIFY=1`** — reach for this one FIRST when the self-host compiles
  a program that then segfaults, or emits a wasm module wasmtime will not
  validate. Every self-host backend runs the two IR verifiers over each
  function before emitting it, so a local index outside the frame, an unclosed
  scope, a branch with no target, or an operand-stack imbalance exits 4 naming
  the function and the op index — at the point the lowering produced it, rather
  than wherever the machine code happens to fall over. It checks call ARITY
  too, against an index of the emitted unit's declared functions; a callee it
  does not carry — a runtime helper, another unit's function — is skipped, as
  is a function holding an op the stack pass cannot model. Callee RESOLUTION
  is not in the gate: it needs the whole import closure, so it runs from
  `asm_modload_run -verifyprovided`.

  ON by default; observation only, so the emitted code is byte-identical
  either way and a diagnosis made under it is about the same compiler that
  failed without it. `FERN_IR_VERIFY=0` opts out, which is what a bisect wants
  when the gate itself is the suspect. It was opt-in until #6994: its stack
  pass allocated enough inside a WASM-HOSTED compile to reach a latent
  premature free, corrupting the WAT that compiler emitted — a fault the gate
  surfaced rather than caused, and fixed by rc-heading the strings the wasm
  WASI helpers hand back.
- **`__rc_underflow_count()`** — the counter. Exact yes/no signal for "did this
  compile over-release anything", readable from Fern. The self-host drivers
  call it themselves on every run (`util.rc_underflow_guard`).
- **`FERN_RC_UNDERFLOW_TRAP=1`** — makes each counter bump fatal *at the
  offending dec*: `fern-sanitizer: rc over-release (double free)` on stderr,
  the #5538 frame-pointer backtrace under it, exit 124. This is the one that
  locates a bug; the counter only detects it. (It used to be a bare `ud2` and
  a SIGILL you had to be under gdb to interpret — `break __fern_report` gets
  you that stop back if you want the live registers.)
- **`FERN_RC_FREE_DEBUG=1`** — quarantines freed array/map/box blocks and
  reports `fern-sanitizer: use-after-free` on a later touch. Complementary,
  not redundant: it sees an over-release that went through a free, and is
  blind to a plain dec taking a count 1 → 0 (which frees nothing) followed by
  another. Use `FERN_RC_UNDERFLOW_TRAP` for that case — and note the
  over-release guard sits ahead of the free, so a drifting count usually
  reports as a double free before it can dangle. Nothing is recycled in this
  mode (a reused block would overwrite its own poison), so the heap only
  grows; the release is still *counted*, so `FERN_LEAKCHECK` stays accurate
  with both on.
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
  direction: what the rc detector cannot see. Under `-sanitize` the same
  counters also produce a one-line verdict (`fern-sanitizer: leak <K> bytes in
  <N> blocks`) when the balance is positive, so a leak needs no number read.
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
  binary is addresses only. It is also what resolves the abort backtrace the
  natives print under a fatal abort (#5538); `-backtrace=false` /
  `FERN_BACKTRACE=0` suppresses that walk at compile time, so leave it alone
  while debugging.

A worked example of the whole loop — counter to trap to backtrace to root
cause — is #6021.
