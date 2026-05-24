# Bootstrap research — self-hosting strategies from Rust, Zig, Crystal, OCaml, Go, TinyCC

`ROADMAP-AND-SELF-HOSTING.md ▸ Part 2` covers *what's missing*
from lang to make a self-host viable (union sugar, sort,
process spawn, etc.). It does *not* cover the orthogonal
question of **bootstrap strategy** — how the Go-implemented
production compiler should hand over to the lang-implemented
one, in what stages, at what pace, with what guarantees about
trust, reproducibility, CI cost, and the langsmith
differential oracle.

This doc surveys how other languages made that transition
(Rust, Zig, Crystal, OCaml, Go, TypeScript, Pony, TinyCC,
Mes / stage0) and recommends a concrete strategy. Companion
to `ROADMAP-AND-SELF-HOSTING.md` (which is the *what*),
`PERFORMANCE-RESEARCH.md` (compile speed), and the
`examples/self_host/` work-in-progress.

## Framing — what bootstrap means here

A *self-hosted* compiler is one that compiles its own source.
Reaching self-host has three orthogonal axes:

1. **Coverage.** Which compiler-source files are written in
   the target language? Lexer? Parser? Checker? Codegen? All
   of them? `examples/self_host/` currently covers lexer,
   parser, constfold, checker, vm, printer, asm — i.e. the
   majority of the pipeline minus production codegen.

2. **Transition.** Once the lang-port is feature-complete,
   how do we hand over the production compiler? Flip overnight
   ("Crystal-style ditch the bootstrap language"), run both in
   parallel forever ("TypeScript with two impls"), or
   gradually shrink the Go side ("Go's incremental 1.4 → 1.5
   migration")?

3. **Bootstrap chain integrity.** From the source repo on a
   bare machine with only POSIX + clang + lld, how do you
   get a working compiler? Three sub-questions: (a) what
   snapshot / binary / source seed do you start from? (b) how
   many stages of recompile to reach a fixed point? (c) how
   much trust does the chain require?

For a single-user language with a Go reference compiler and
no production user code in the wild, the surface area is
manageable. The user has stated readiness for breaking
changes. The interesting design questions are *transition*
and *chain integrity* — coverage is mostly a function of
porting effort already underway.

## What we already do well — call out so we don't drift

- **The Go compiler is feature-complete and shipped.** Not
  "an MVP we're going to throw out." It produces ELF / Mach-O
  / .wasm artifacts that work on real hardware. Anything the
  lang-port can't do, the Go reference can — which means the
  lang-port can be developed without urgency.

- **Per-stage cross-validation is already wired** via
  `internal/e2e/self_host_cross_validation_test.go` and the
  per-layer `internal/e2e/self_host_*_test.go` files. The
  lang-implemented layer's output is compared against the
  Go-implemented layer's output for a corpus of inputs.
  This is the exact shape Wheeler's *Diverse Double
  Compiling* requires for trust verification, even before
  we formalise it.

- **langsmith differential oracle exists** (per
  `internal/langsmith/` and `IMPROVEMENTS.md`). Fuzzed input
  → run through both implementations → assert identical
  output. The Go reference *is* the langsmith oracle's
  reference. This is load-bearing for the transition — see
  Rec §6.

- **Pipeline-shape demos compose** (`examples/self_host/
  pipeline.fern` glues lexer → parser → constfold → checker
  → vm). The composition tests catch wiring bugs before
  the full self-host is in place.

- **External tooling dependency is acknowledged and
  documented** (`ROADMAP-AND-SELF-HOSTING.md ▸ Part 3`).
  The codebase emits `.s` and `.wat` text; clang / lld /
  wasm-tools take it from there. This is the right line —
  we don't need our own assembler.

## Single-source deep dives

### Rust — three-stage bootstrap, downloadable stage0

Sources:
- https://rustc-dev-guide.rust-lang.org/building/bootstrapping/
- https://github.com/rust-lang/rust/tree/master/src/bootstrap
- https://internals.rust-lang.org/t/the-rust-bootstrap-process/2391
- Manish Goregaokar's blog posts on rustc internals.

**The shape.** Rust's compiler is in Rust. To build it from
source, you need a Rust compiler. To break the cycle:

1. **stage0 = a downloaded pre-built rustc binary.** The Rust
   build system downloads the previous release's rustc
   (e.g. building rust 1.75 needs rustc 1.74). Cryptographic
   signature on the download.

2. **stage1 = current source compiled by stage0.** Uses the
   downloaded compiler to compile the current source's
   compiler. This stage1 binary may have *bugs from stage0*
   leaking into it (Trusting Trust attack vector); it's not
   used for distribution.

3. **stage2 = current source compiled by stage1.** Compiling
   the current source *twice* (once with stage0, once with
   the result of that) means the final compiler was
   produced by code from the current source tree. Discrepancies
   between stage1 and stage2 indicate non-determinism.

4. **Optional stage3 = compiling once more with stage2** as
   the Wheeler DDC step. Used for full-bootstrap checks in
   CI but not for distribution. The stage1 = stage2 = stage3
   property is the assertion the build system checks.

**Build-system mechanics.**

- `src/bootstrap/` is a Rust crate that orchestrates the
  three-stage build. It's *itself* compiled by stage0
  before stage1 runs.
- `config.toml` lets you pick how many stages to build.
  Local dev typically does stage1 (faster); CI runs stage2;
  Wheeler's DDC mode runs stage3.
- Cross-compilation works through `--target`; stage1
  produces compilers for other targets.

**Trust posture.** Stage0 is downloaded from rust-lang.org,
signed. The chain back to the *first* rustc-in-Rust (PR
#5946, 2014) compiles through ~60 prior stage0 releases.
Going further back, the original rustc was written in
OCaml, and that bootstrap chain isn't routinely
re-verified — Rust accepts a Trusting Trust attack would
need to have been planted in 2010 OCaml-rustc and persisted
across 14 years of recompiles, which is a high bar but not
zero.

**Bootstrappable Builds project** maintains an alternative
chain from `tinycc → gcc → rustc-mrustc → stage0 → rust` for
distros that want full source-only bootstrap. Slow (multi-
day rebuild) but verifiable.

**What translates:**

- **Stage discipline is overkill for a single-user
  language.** Three stages is the right rigour when
  distribution is to millions of users on hostile networks.
  For one user it's ceremony. **Two-stage is the sweet
  spot**: stage0 (last known good lang-compiled compiler)
  builds stage1 (current source). Comparing stage1 = stage2
  is cheap and catches non-determinism without paying for a
  full third stage on every build.

- **`stage0` as a checked-in artifact, not a download.**
  Rust downloads; Zig snapshots. For a single repo, snapshot
  is simpler and reproducible. The next-most-recent
  release's lang-compiled compiler binary lives at
  `bootstrap/stage0.<target>.<sha>.{wasm,bin}` in the repo.
  On `make bootstrap`, the build system uses this binary to
  compile current source.

  Concerns: binary size in the repo (Git LFS, or check in
  the wasm form which is smaller). Zig deals with this by
  keeping the snapshot small (it's the smallest known-good
  compiler that can produce a current compiler, not the
  full current compiler). See Zig section.

- **`make distcheck` analogue.** Wheeler's DDC step
  reformulated: take last release's compiler binary, compile
  current source, compile current source *again* with the
  output of step 1, assert byte-identical to the first
  compile. Run once per release, not per PR.

**Considered, left:**

- *Downloading stage0 from a remote URL.* For one user that's
  unnecessary infrastructure. Checked-in snapshot is fine.
- *Cross-stage feature gating ("this feature can only be
  used after stage N").* Rust does this for compiler-internal
  features. Single-user language doesn't need it.

### Zig — snapshot-based, just-rewrote-the-compiler

Sources:
- https://github.com/ziglang/zig/wiki/FAQ#how-do-i-build-zig-from-source
- Andrew Kelley, "Zig in 2023" + "Zig in 2024" posts.
- `lib/std/zig/AstGen.zig` + the surrounding bootstrap path.

**Zig's bootstrap history is the most informative for this
codebase, because it's the most similar trajectory:**

1. **2015–2020: stage1 in C++.** The original Zig compiler
   was C++ (rough equivalent of our Go-implemented compiler).
   It worked, shipped releases, and was the production tool.

2. **2020–2022: stage2 in Zig, gradually filled out.** The
   Zig-in-Zig compiler grew alongside the C++ one, behind
   feature gates. Both produced binaries; the Zig-in-Zig
   one was incomplete for years.

3. **2022: the flip.** Once the Zig-in-Zig compiler reached
   parity, the C++ compiler was **deleted entirely**. New
   bootstrap path: `zig0` (a small WASM-shaped boot loader)
   → unpacks current Zig source → compiles with itself.

4. **2024–present: stage3 / self-hosted backends.** Zig now
   has its own x86_64 and aarch64 backends, no LLVM
   dependency for debug builds. Release builds still link
   LLVM for optimisation, but the binary itself can be
   produced LLVM-free.

**Why this path is informative.** Zig and this codebase share:

- A C-family / Go-family bootstrap language they want to
  shed.
- Multi-backend ambition (LLVM optional vs always).
- Single-user / small-team origin.
- Production releases shipping during the transition.

**Zig's snapshot mechanism.** The current `zig0` binary is
checked into the source repo as `stage1/zig1.wasm.zst` (a
zstandard-compressed WebAssembly module). On `make`, the
build system:

1. Decompresses `zig1.wasm.zst` to `zig1.wasm`.
2. Runs `zig1.wasm` under a tiny C-implemented WASM
   interpreter (`stage1/wasm2c`-emitted host).
3. The decompressed Zig-in-WASM compiles current Zig source
   to native, producing a real `zig` binary.
4. From there, current `zig` recompiles itself for the
   release.

The `zig1.wasm.zst` snapshot is regenerated periodically
(every few releases) by checking in a fresh WASM-built
compiler. The intermediate WASM interpreter is ~2k LOC of
C, audit-able by one person.

**Trust posture.** The chain is: any-C-compiler → wasm2c
host → zig1.wasm → current zig binary. The "trusting trust"
attack surface is the *checked-in WASM snapshot* — if it's
compromised, the chain produces compromised compilers
forever. Mitigation: periodic Wheeler DDC re-verifications
from an older snapshot.

**What translates:**

- **The transition shape.** Zig's "develop both compilers
  in parallel, then delete the bootstrap one once parity
  hits" is exactly the path this codebase is on. Don't
  delete the Go compiler prematurely; flip when:
  - Every `internal/e2e/` test passes when run against the
    lang-compiled compiler.
  - The langsmith differential oracle (which currently
    diffs interp vs codegen of one implementation) is
    extended to diff lang-impl vs Go-impl for a corpus.
  - Cross-stage `make bootstrap; make distcheck` passes.

- **WASM as the snapshot format.** This codebase already
  emits WASM. A WASM-compiled version of the lang-port is
  the natural snapshot — small, portable across hosts,
  audit-able. The build system needs a WASM runtime to
  unpack and execute the snapshot; wasmtime is already a
  test-time dependency. Could add a tiny `wasm2c`-emitted
  host as a release-time fallback for environments without
  wasmtime.

- **Don't delete the Go compiler.** Even after self-host
  parity, the Go compiler stays useful as:
  - The langsmith differential oracle.
  - A debugging tool when the lang-compiled compiler has
    bugs (cross-check by running both).
  - A canary in CI that catches behavioural drift.

  Zig deleted their C++ stage1 because it stopped being
  maintained — three years of features the Zig-in-Zig
  compiler had and the C++ one didn't. We can avoid that
  outcome by keeping the Go side in sync, which is
  feasible *only* if both target the same IR + backends
  (which they do today).

**Considered, left:**

- *Deleting the Go compiler post-transition.* Zig deleted
  theirs; we shouldn't, because the differential oracle is
  too valuable to give up. The langsmith fuzzer's whole
  value proposition is "two implementations exist and we
  check they agree." Keep both.

### Crystal — bootstrap from Ruby, then drop Ruby

Source:
- https://crystal-lang.org/reference/1.10/man/contributing/index.html
- https://github.com/crystal-lang/crystal historical commits.

**Crystal's history:** Initially written in Ruby (~2011-2013).
The Ruby code was a working Crystal compiler that produced
LLVM IR, linked through LLVM toolchain, produced native
binaries. Crystal's *current* compiler is in Crystal.

**The transition was clean-cut.** Once the Crystal-in-Crystal
compiler reached parity, the Ruby version was deleted and the
build system switched to downloading the previous Crystal
release as the bootstrap binary. There's no intermediate
where both versions co-exist.

**Why this works for Crystal but not for us:**

- Crystal had no "Ruby reference" reason to keep the Ruby
  compiler around. Ruby is duck-typed; Crystal is statically
  typed. They share syntax-shape but no compiler-internal
  invariants. There's no value to fuzz-comparing them.
- This codebase has the langsmith differential oracle which
  *requires* two implementations.

**What translates:**

- **The downloadable-previous-release pattern.** Once the
  lang-compiled compiler is the production tool, distributing
  it via "download the most recent release as your stage0"
  is the standard pattern. Could be a tagged GitHub release
  with a single binary per target.

- **The flip itself is fast** (Crystal's flip was a single
  PR + a build system change). When the time comes, it's
  not a multi-month project.

**Considered, left:**

- *Treating the Go compiler as "the Ruby of Crystal" — a
  bootstrap to ditch.* We get more value from keeping both.

### OCaml — continuous co-evolution, classical bootstrap chain

Source:
- https://github.com/ocaml/ocaml/blob/trunk/HACKING.adoc
- OCaml's `boot/` directory + `bootstrap` script.

**OCaml's posture is the *least* like a flip-day transition.**
The OCaml compiler has always been in OCaml (going back to
~1996 — earlier in the timeline they had a bytecode-only
chain back to the original Caml Light written in C in 1990).
The "bootstrap" mode is *part of normal development*: every
change to the compiler must be testable against itself.

**The mechanism:**

- `boot/ocamlc` is a bytecode-compiled snapshot of a recent
  OCaml compiler, checked into the repo.
- On `make`, `boot/ocamlc` compiles current source's
  compiler to bytecode → `ocamlc.boot`. `ocamlc.boot`
  recompiles current source → `ocamlc`. Then `ocamlc`
  compiles `ocamlopt` (the native compiler). Etc.
- `make bootstrap` regenerates `boot/ocamlc` from current
  source — done periodically, not per commit, when language
  changes require a newer bootstrap.

**Why this scales for a 30-year-old project.**

- The bootstrap is *bytecode*, not native. Bytecode is a
  smaller, more stable target — much less likely to drift.
  A new architecture (RISC-V say) is supported by the
  bytecode compiler immediately; the native compiler can
  follow later.
- Cross-platform: the bytecode boot is platform-independent.
  One snapshot works for every supported target.

**What translates:**

- **WASM as the bytecode-analogue.** Same idea as the Zig
  takeaway. WASM is platform-independent, smaller than
  native binaries, validated-by-construction. The lang-
  compiled-to-WASM compiler is the natural "bytecode" boot.
  Lands the platform-independence of the OCaml model
  *without* needing a bytecode VM in the language proper
  (we'd use wasmtime as the unpacker).

- **Continuous bootstrap testing as the CI gate.** Every
  PR runs the lang-compiled compiler against the test
  suite. If the lang-port is behind, individual PRs may
  not be self-host-compilable — that's fine *until* the
  flip; after the flip, "the lang-compiled compiler can't
  compile its own source" is a hard CI fail.

- **`make bootstrap` regenerates the snapshot.** Done
  manually, periodically, when:
  - Language features the new code uses aren't supported
    by the old snapshot.
  - A long-bug-fix run accumulates enough that re-snapping
    is cheaper than carrying compatibility shims.

**Considered, left:**

- *Always-bytecode + sometimes-native compilers.* That's
  OCaml's choice because bytecode interpretation is
  reasonable for many of their use cases. For our cold-
  start posture, AOT-native is the production path. WASM
  is the snapshot format; native is the output.

### Go — the 1.4 → 1.5 migration (the only relevant example
of porting a working production compiler)

Source:
- https://golang.org/s/go13compiler (Russ Cox)
- https://github.com/golang/go/wiki/Go-1.5-bootstrap
- Robert Griesemer's GopherCon talks on the migration.

**Go's compiler was written in C through Go 1.4. Go 1.5
shipped a Go-in-Go compiler that had been mechanically
translated from C by a dedicated tool (`grind` + custom
post-processing). Subsequent Go releases ship a Go-in-Go
compiler.**

**The translation tool was key.** Rather than rewrite the
C compiler by hand, the Go team built a (mostly-automated)
translator from C to Go. This let them:

- Preserve the C compiler's behaviour byte-for-byte through
  the translation.
- Compare C-version-output vs Go-version-output on the same
  inputs.
- Land the migration as a *single release* (1.5) rather
  than a multi-year staged effort.

**Post-translation, the Go-in-Go compiler was hand-
optimised** for Go idioms (the auto-translated code looks
like C with `var` instead of declarations). This happened
over 1.5 → 1.7.

**Bootstrap chain post-1.5:** Building Go from source
requires a previous Go (≥ 1.4 for the initial flip; later
narrowed to ≥ 1.20 currently). The bootstrap binary is
*not* checked into the repo — you set `GOROOT_BOOTSTRAP` to
a Go installation. Reproducibility is via "download the
specific previous release."

**What translates:**

- **The translation-tool approach is *not* what we want.**
  Our Go compiler is already idiomatic Go, not a mechanical
  translation. Hand-porting (which is what `examples/self_host/`
  already does) is the right path — the target lang is
  imperative-flavoured and the Go code's structure
  translates 1:1.

- **The single-release flip is the right cadence.** Don't
  ship a half-finished lang-port as "the new compiler";
  keep the Go compiler as production until parity hits,
  then flip in a single release tag.

- **Boot binary as an externally-installed dependency.**
  Once flipped, `$LANG_BOOTSTRAP=path/to/previous/lang`
  for builds. The user can pin a specific version or
  download a tagged release. Cheaper than a checked-in
  snapshot binary, at the cost of "you need a working
  previous lang to build the new one" — which is exactly
  Go's posture and works fine.

**Considered, left:**

- *Mechanical translation tool from Go to lang.* Wrong fit;
  the existing manual port is producing readable lang code.

### TypeScript → tsgo — keep two impls forever

Source:
- https://devblogs.microsoft.com/typescript/typescript-native-port/
  (2024 announcement)
- The TypeScript repo's `internal/tsc` evolution.

**The most recent (and most informative) example for our
shape.** TypeScript's compiler has been TypeScript-in-
TypeScript for ~10 years. In 2024 Microsoft announced a Go
port (`tsgo`) targeting ~10× compile-speed wins. Both
implementations are maintained in parallel; the TypeScript
one stays canonical for spec experimentation while `tsgo`
becomes the production-perf tool.

**The interesting bit: they kept both.** Reasons (from the
announcement):

- The TS-in-TS impl is the *spec authority*. The Go port
  shadows it.
- TS-in-TS is the platform for fast iteration on new
  language features (proposal → impl in the same
  language).
- Go-in-Go is the production-perf vehicle for billion-
  line monorepos.
- Bug-for-bug compatibility is verified by running both
  against shared test suites.

**The mirror image of our situation:** TypeScript went *to*
Go for perf. We're going *from* Go for self-host. The
posture is the same — two implementations, both maintained,
both run against test corpora, diverged in implementation
but unified in behaviour.

**What translates:**

- **Two-impl-forever as a deliberate design choice.**
  Names the situation we're in (and would benefit from
  remaining in) explicitly. The Go compiler is the
  spec authority + langsmith oracle; the lang-impl
  compiler is the production cold-start path. Both
  generate the same `.s` / `.wat`. Each PR runs both.

- **Bug-for-bug compatibility as the parity criterion.**
  Not "both produce *correct* output," but "both produce
  the *same* output." This is what `internal/e2e/
  self_host_cross_validation_test.go` already checks, and
  langsmith differential oracle generalises. Lock it in.

- **Shared spec / test corpus.** Both impls consume the
  same source files and produce the same artifacts. Lives
  in `examples/` + `internal/e2e/`.

### Pony — a clean-room rewrite (cautionary tale)

Source:
- https://www.ponylang.io/blog/2017/09/the-ponyrt-rewrite/
- Pony's compiler history.

**Pony was originally a C compiler with a Pony runtime.
The team rewrote the compiler in Pony — but it was a
clean-room rewrite, not a port.**

The rewrite took ~18 months, lost track of edge-case
behaviours, and required a long bug-parity sprint
post-flip. The lesson: **port, don't rewrite**. Match the
existing compiler's behaviour line-for-line through the
port; don't take the opportunity to redesign.

**What translates:**

- **The `examples/self_host/` approach is correct.** Each
  step mirrors a specific Go source file in the existing
  compiler. The per-step `internal/e2e/self_host_*_test.go`
  tests verify the lang-impl matches the Go-impl on the
  same inputs. Keep doing this; resist the urge to
  redesign during the port.

- **No "improvements" during the port.** If the
  lang-impl spots a bug in the Go-impl, fix the Go-impl
  first, *then* port the fix. Otherwise the cross-
  validation test starts to diverge and you lose the
  oracle property.

### TinyCC — trivially self-compiles

Source:
- https://repo.or.cz/w/tinycc.git
- Fabrice Bellard's design notes.

**TCC is the gold standard for *cheap* self-hosting.** The
entire compiler is ~22k LOC of C. It compiles itself in
under a second. The bootstrap is one step: any C compiler
builds tcc; tcc rebuilds tcc; assert byte-identical.

**Why this matters as a reference point.** The cost of
the bootstrap step *should not exceed the cost of a normal
build* by more than a factor of ~2. If `make bootstrap`
on this codebase ends up taking 20 minutes, something is
wrong.

**What translates:**

- **Keep the language simple enough that bootstrap is
  cheap.** TCC's self-compile is fast because the language
  is small and the compiler is small. Our compiler is in
  the same league size-wise (`internal/` is ~50k Go LOC;
  the lang-port will be roughly the same). Bootstrap should
  be ≤2× normal build time. Watch this number as a CI
  metric.

- **The "one-step self-compile" property.** Once lang is
  self-hosting, a normal `make` should be: stage0 (snapshot)
  compiles current source → stage1; stage1 recompiles
  current source → stage2; assert stage1 = stage2. Three
  builds, all of "the same source code." That's fast and
  catches non-determinism.

### Mes / stage0 — radical bootstrappability

Sources:
- https://www.gnu.org/software/mes/
- https://github.com/oriansj/stage0
- "Reproducible Builds: Bootstrapping a programming language
  ecosystem from hex monitor" (Joshi & Sasaki).

**The Bootstrappable Builds project pushes bootstrap chains
all the way back to a hand-typed hex monitor.** Mes is a
Scheme interpreter written in C; stage0 is a chain of
progressively more capable bootstrap tools starting from a
~~500-byte hex assembler. Used by GNU Guix to provide
build chains where every binary is verifiable from source.

**Relevance to this codebase: ~zero.** Single-user, AOT,
no distro-packaging concerns. But worth knowing the
*existence* of this chain — if/when the language ships to
distros, "bootstrappable from source" becomes a checkbox
some package managers (Guix, NixOS-ish) care about, and
the cost of that check is much lower if the language was
designed with it in mind from the start.

**What we keep in pocket:**

- **Don't add language features that block bootstrappability.**
  If a feature needs an external service at build time
  (downloads from a registry, calls out to a hosted API),
  it breaks offline / reproducible builds.

- **Don't add build-time code execution beyond constfold.**
  Zig comptime + Jai compile-time-anything would be lovely
  for perf but they make bootstrap harder. The current
  `internal/constfold` is the right ceiling.

## Cross-cutting themes

1. **WASM is the right snapshot format.** Zig, OCaml (by
   analogy with bytecode), and our codebase converge. WASM
   is portable, validated-by-construction, small enough to
   check into a repo, and runs under a single host
   (wasmtime is already a test-time dependency).

2. **Two implementations are better than one — for
   diverging-by-design reasons.** TypeScript / tsgo, our
   Go-impl / lang-impl, hypothetically OCaml + a future
   port. The reasons aren't the bootstrap chain itself
   (which prefers a single canonical impl), they're the
   *diff-oracle property* and the *risk-of-bug-in-one-impl*
   mitigation.

3. **Port, don't rewrite.** Pony rewrote and paid 18
   months of bug parity. Go *translated* (mechanically),
   then iteratively cleaned up. OCaml has co-evolved
   forever. The shape of `examples/self_host/` mirroring
   the Go-impl structure file-by-file is the right model.

4. **The flip is a single release; the development is
   multi-month.** Zig, Crystal, Go all flipped in one
   tagged release after a long parallel-development phase.
   Avoid "soft flips" where some users use the new
   compiler and others the old — diff observations
   diverge, oracle property weakens.

5. **Keep the language simple enough that bootstrap is
   cheap.** TCC, OCaml. If a language feature makes the
   compiler 10x slower to build, the bootstrap chain
   inherits that cost on every contributor's machine.

6. **Stage discipline scales with distribution model.**
   Rust's three-stage rigour fits distribution-to-
   millions. For a single user, two stages (snapshot →
   self → re-self) is enough. Don't pay for stage3 every
   release.

## Concrete recommendations

Ranked by leverage × cost. Several depend on the
self-host port reaching feature parity first
(currently in progress per `examples/self_host/`).

### 1. Adopt the *two-implementations-forever* posture explicitly

**Cost: 0 (a decision, not a change).** **Impact: high.**

State publicly (in `LANGUAGE-DIRECTION.md` and / or this
doc) that the Go compiler will *not* be deleted post-
self-host. The Go-impl stays as:

- The langsmith differential oracle's reference.
- The spec-authority for behaviour comparison.
- A debug tool when the lang-impl has bugs.
- A canary in CI.

Reasoning: TypeScript / tsgo pattern works. Zig's deletion
of the C++ compiler caused them to lose a corroborating
oracle. We have an active fuzzer; killing one of its two
witnesses degrades it.

Implication: the Go-impl must not be allowed to bit-rot.
Every language feature added to the lang-impl must also
land in the Go-impl, *or* the diff oracle relaxes to
ignore that feature. Doc'd as "both impls track the same
spec; diff-oracle is the regression test."

### 2. Pick a stage model: snapshot-based, two stages

**Cost: 1 week to wire (one-time).** **Impact: gates Rec
§3, §4, §5.**

Lock in the bootstrap shape:

1. `bootstrap/stage0.wasm.zst` lives in the repo — a
   recent lang-compiled compiler, compressed.
2. `make bootstrap` decompresses, runs under wasmtime,
   compiles current source to `bin/lang` (the production
   target binary).
3. `make distcheck` runs `bin/lang` against current
   source again, byte-compares the output to step 2.
   Diff = non-determinism bug; investigate.

Two stages, not three. The Wheeler-DDC third stage runs
only on tagged release builds, not per-PR.

Snapshot regeneration: `make bootstrap-update` rebuilds
the snapshot from current `bin/lang`. Done manually, every
~50 PRs or when new language features require it.

### 3. Defer the flip until parity criteria are explicit
and met

**Cost: 0 (a decision).** **Impact: avoids the Pony
outcome.**

Parity criteria for flipping the production compiler from
Go-impl to lang-impl:

- **All `internal/e2e/*` tests pass against the lang-impl.**
- **Langsmith fuzzer**: ≥ 1M iterations with zero
  divergence on a recent run.
- **Cross-stage test passes**: lang-impl compiled by itself
  produces byte-identical output to lang-impl compiled by
  Go-impl. (Eliminates "the lang-impl miscompiles itself
  but the Go-impl masks it" class of bugs.)
- **Build time for `make all`** under 2× the current
  Go-impl build time. (TCC threshold.)
- **CI on three targets** (arm64-linux, arm64-darwin,
  x86_64-linux, wasi-http) goes green with the lang-impl
  as the primary compiler for at least one week before the
  tag.

Until all five are met, the Go-impl is the default. The
lang-impl is a `-self-host` opt-in for testers.

### 4. Add `make bootstrap` and `make distcheck` to CI

**Cost: 1 day.** **Impact: catches regressions early.**

Once the lang-impl is feature-complete:

- Every PR runs `make bootstrap` — checks that the snapshot
  can build current source.
- A nightly job runs `make distcheck` — full
  byte-comparison loop. Catches non-determinism.
- A weekly job runs a Wheeler DDC against the
  *previous-previous* snapshot — defence-in-depth.

CI cost: roughly +2 minutes per PR (assuming the
lang-compile is in the same league as the Go-compile).
Worth it.

### 5. Regenerate snapshots on a cadence, not per-PR

**Cost: ongoing, ~30 min per regeneration.** **Impact:
avoids snapshot rot.**

Regenerate `bootstrap/stage0.wasm.zst` when:

- The lang-impl uses a language feature the snapshot
  doesn't support yet. (Otherwise `make bootstrap` fails.)
- 50 PRs have accumulated since the last snapshot. (Keeps
  the snapshot fresh; reduces "fix backward-compat to old
  snapshot" busywork.)
- Pre-release. Each tagged release gets a fresh snapshot
  matching its source.

Procedure documented in `docs/BOOTSTRAP.md` (separate from
this research doc — see Rec §10).

### 6. Extend langsmith to differential-test Go-impl vs lang-impl

**Cost: 2-3 weeks.** **Impact: critical for the
two-implementations-forever posture.**

Today langsmith compares interp vs codegen of one
implementation (per `IMPROVEMENTS.md ▸ langsmith`). For
the two-impl world, extend to:

- Same source program → Go-impl → produces output A.
- Same source program → lang-impl → produces output B.
- Assert A == B.

Inputs: the existing langsmith generator's 64-seed corpus
(per `e2e: bump diff-oracle seed count to 64` commit).
Outputs: native binaries, `.wat`, `.s` text. Diff at the
output level — same bytes = pass.

Fuzz-find divergences and file them as bugs against
whichever impl is wrong. The fuzz runs catch the
"feature drift between impls" failure mode that Zig's
C++/Zig parallel-development era hit.

### 7. WASM as the snapshot format, not native

**Cost: 1 week.** **Impact: enables Rec §2, §4.**

Reasons:

- **Portable across hosts.** One snapshot works on
  Linux, Mac, x86, ARM. We support all of them as build
  hosts.
- **Validated-by-construction.** WASM bytecode can't
  contain malformed instructions. A corrupted snapshot
  fails to load, doesn't silently miscompile.
- **Smaller.** WASM is denser than native binary because
  it's typed-bytecode, not machine code.
- **Already on the supported-output list.** We emit WASM
  components for wasi-http; one extra `--snapshot` target
  flag, no new backend.

Trade-off: needs wasmtime at build time. Already a test-
time dep; promoting to build-time is cheap.

### 8. Don't add language features that block bootstrappability

**Cost: 0 (a guideline).** **Impact: long-term.**

Specifically:

- **No build-time network access.** No "download this
  package during compile." Goes with `LANGUAGE-DIRECTION
  ▸ Module system` work — packages installed via a
  separate command, vendored before build.
- **No build-time code execution beyond constfold.** Zig
  comptime + Jai compile-time-anything add bootstrap
  weight. Our current line (constfold scalar arithmetic
  + small AST-level rewrites) is fine.
- **No language-runtime dependency that requires a
  current-language toolchain to bootstrap.** E.g. if the
  lang's stdlib starts shelling out to `lang fmt`, the
  bootstrap snapshot needs `lang fmt`, which needs
  `lang`, which needs the snapshot. Avoid cycles.

### 9. Keep `clang`, `lld`, `wasm-tools` as build-time deps

**Cost: 0 (already there).** **Impact: keeps the
codebase honest.**

`ROADMAP-AND-SELF-HOSTING.md ▸ Part 3` already documents
why we have these deps. Resist the temptation to write
our own assembler / linker / wasm-tools as part of the
self-host work — that's a multi-year project that doesn't
fit the cold-start edge-handler positioning. Bootstrap
from a posix + clang + lld + wasm-tools base; that's the
floor.

### 10. Document the bootstrap procedure in `docs/BOOTSTRAP.md`

**Cost: 2 days.** **Impact: shareable runbook.**

Separate from this research doc. Covers:

- How to build from source.
- How to regenerate the snapshot.
- How to run cross-validation locally.
- How to debug a stage1 ≠ stage2 divergence.
- The two-impl posture and what to do when they diverge.

Lives at `docs/BOOTSTRAP.md`. Companion to
`ROADMAP-AND-SELF-HOSTING.md`. Written *after* Rec §2
through §5 land — the doc is a runbook for an existing
mechanism, not a design proposal.

## Anti-patterns — explicit "do not adopt"

- **Three-stage bootstrap with formal Wheeler DDC every
  PR.** Rust's discipline; overkill for one user. Two
  stages on PR, three on release tag.

- **Downloadable stage0 from a third-party URL on every
  build.** Rust pattern; introduces network dep + a
  trusted-server requirement we don't have to take.
  Snapshot in the repo is sufficient.

- **Deleting the Go compiler post-flip.** Zig pattern;
  loses the diff oracle. We're explicitly choosing the
  TypeScript-tsgo posture instead.

- **Mechanical translation tools** (Go's `grind`). Our
  `examples/self_host/` is hand-port-quality lang code,
  not mechanically-converted Go. Mechanical translation
  was right for Go (millions of lines of C) and wrong
  for us (~50k Go LOC, already idiomatic).

- **Bytecode-as-primary-output.** OCaml ships bytecode
  + native; their bytecode is a runtime artifact. We're
  AOT-only for production; WASM as a *snapshot* format is
  fine, WASM as a primary distribution format is for the
  wasi-http target only.

- **Bootstrappable Builds chain back to a hex monitor.**
  Single user, no distro story. Knowing it exists is
  enough; building toward it is wasted effort.

- **Co-installed multiple-versions-of-lang as a normal
  user experience.** Single binary per release; build
  with `LANG_BOOTSTRAP` env var pointing at a previous
  binary if you need to. Don't grow a version manager
  (rustup, ocaml-switch).

## When to revisit

- **When the lang-impl reaches parity with the Go-impl**
  (per `ROADMAP-AND-SELF-HOSTING.md ▸ Part 2`'s "Full
  lexer → IR on wasm: 4-6 weeks; Full compiler self-host
  on wasm: 6-9 weeks" estimate). At that point, Rec §2-7
  become actionable.

- **When the langsmith differential oracle catches its
  first lang-impl-vs-Go-impl divergence.** That's the
  signal Rec §6 has paid off; double down.

- **When the lang-impl's compile speed approaches the
  Go-impl's.** TCC's rule: bootstrap should be ≤2×
  normal build. If we're at 4-5×, look at
  `PERFORMANCE-RESEARCH.md ▸ Rec §1 SSA` first; if 2-3×,
  the flip becomes viable.

The framing question to answer at flip time: **what does
the post-flip CI matrix look like?** If both impls are
required to pass every PR, we're in a stable two-impl
posture. If only one is required and the other can lag,
we're in Zig's parallel-development-then-delete posture.
The doc above recommends the former; reconsider if CI
cost becomes a real constraint.
