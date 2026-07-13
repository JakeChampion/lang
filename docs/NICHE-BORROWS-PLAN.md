# Niche-borrows execution plan

Execution plan for the ranked top-10 shortlist from
`docs/NICHE-LANGUAGE-RESEARCH.md` (2026-07-12). Living document:
each item records its **current state on main** (code-grounded,
checked 2026-07-12 — several shortlist items turned out to be
further along than the research doc assumed), the decision, the
slice plan, and exit criteria. Status markers update as PRs land.

Ordering principle: small verified-shape wins first (momentum +
they serve the porting workflow itself), then docs that lock in
contracts, then stdlib, then the checker/runtime features, then
the goal-2-adjacent memory work — which mostly *joins* existing
roadmaps (`RC-PERCEUS-PLAN.md`, `SELFHOST-PERCEUS-REUSE.md`,
`OWNERSHIP-INFERENCE-PLAN.md`) rather than starting new ones.

## Reality check vs the research doc

Grounding sweep results that adjust the shortlist:

| # | Shortlist item | Reality on main |
|---|---|---|
| 1 | Drop-guided reuse + guaranteed-reuse contract | Native reuse is **already substantial** but implements the **PLDI 2021 pairing** (`computeReuseSources`, `internal/ir/rc_analysis.go` — "reuse token" threaded from drop to alloc), not ICFP 2022 drop-guided. Struct/enum self-overwrite reuse, pair-cancellation move family, loop-body drops all merged. Self-host port: RC basics still mid-port; reuse pairing is a later slice (`SELFHOST-PERCEUS-REUSE.md`). No written user-facing reuse contract. |
| 2 | `fip`/`fbip` annotations | **`fip` already shipped natively**: contextual modifier (`fip function`, `own` param marker), checker rule E053 (no allocating constructs; fip may only call fip) — but **verify-don't-enable** (no lowering guarantee), no `fbip`, no graded `fip(n)`, **no self-host support**. |
| 3 | Platform capability split | `internal/platforms` **Phase 1 exists**: per-target `Descriptor{Capabilities, HandlerKinds, Bindings}` for all six targets — but **enforced nowhere** (only `-targets` listing + LSP completions consume it). |
| 4 | `std/peg` | Stdlib already has `regex.fern` (553-line Thompson NFA, no captures) and `stream.fern` (eager byte reader). **No PEG/grammar module**; peg complements regex (structured formats, captures, composability). |
| 5 | Must-consume marker types | Attribute infra exists (`@derive`/`@import`/`@export` in `parseAttribute`, parser.go:1033); allow-list rejects everything else. No consumption analysis. |
| 6 | Iterator fusion contract | `core/iter` cursor protocol exists; no fusion pass, no contract. Standing posture: lazy chains deferred until IR can fuse. |
| 7 | Crash-only handlers | **wasi-http already isolates** (trap = component-instance death, wasmtime survives; native traps deliberately exit 134 to match). **Native `tcp_serve` dies whole-process** on any handler trap (`std/tcp.fern:44` inline `while(true)` loop). |
| 8 | Comptime design brief | Nothing beyond the research doc's sketch; parse-time desugars (f-strings, `use`, for-in) are inlined in both parsers, not structured as CST passes. |
| 9 | Pipe topic placeholder | No placeholder support; both parsers prepend LHS as first arg (`parsePipe` parser.go:4162; `parse_pipe` parser.fern:2412); printer reconstructs from `Args[0]` + `IsPipe`. |
| 10 | `todo` keyword | No `todo`/`panic`/`unreachable` builtin. Precedent: `assert(cond, msg)` is a parser-level statement builtin desugaring to `if (!cond) { eprint(...); exit(1); }` (issue #4416). `std/test`'s `fail()` is a value, not a trap. |

## Execution phases

Each slice = one PR (branch → commit → push → PR → subscribe →
green → squash-merge), with tests at the layer touched, per the
engineering bar. Native + self-host IR path both required for
language-surface features; legacy AST→asm backend gaps are
acceptable.

### Phase A — small language-surface wins

**A1. `todo` statement builtin.** [status: shipped]
Shipped as designed, with two refinements over the sketch below:
the desugar wraps in `loop { … }` / `while (true) { … }` (native /
self-host) instead of a bare statement pair, which makes the stub
**diverge** for E052 missing-return and `let else` — so `todo;`
can be a whole non-void function body, no checker changes needed —
and the formatter round-trips the sugar via `IsTodo`/`TodoMsg` on
`ast.Loop` (deliberately better than `assert`, which formats as
its desugared body). `-check` warns per remaining entry-module
stub; runtime message is `todo: not implemented` / `todo: <msg>`
(byte-identical native vs self-host), exit 101.
Gleam-inspired typed hole, scoped first to statement position
(the porting-workflow case: stub the next match arm / function
body and keep the build green).

- Surface: `todo;` and `todo("message");` as a statement.
  Parser-level builtin exactly like `assert` (contextual — `todo`
  stays usable as an identifier), desugaring to
  `eprint("todo[: msg]"); exit(101);` (loop-wrapped) in **both**
  parsers (native `parseStmt`, self-host `parser.fern`), so every
  backend gets it for free — no checker/IR/interp surface.
  Exit code 101 distinguishes "unimplemented" from assert's 1 and
  the arena/trap 134/137 family.
- Divergence: the desugar ends with `exit()`, which the checker
  already treats as terminating for missing-return analysis (same
  property assert relies on); `todo` as the last statement of a
  non-void function must satisfy the return checker the same way
  `exit(1)` does today — verify, and if `exit` isn't
  divergence-recognised, that's a pre-existing gap to fix in the
  same PR.
- Warn on leftovers: the native checker emits a **warning**
  (not error) per `todo` (`W:` diagnostic listing file:line), so
  `-check` output inventories remaining stubs. (Self-host checker
  parity for the warning can follow; warnings don't affect the
  differential error-code gate.)
- Expression-position `todo` (typed hole settling to the expected
  type) is a **deferred follow-up** — needs bottom-type/settle
  work in two checkers; do not block the statement form on it.
- Tests: native parser test (desugar shape + identifier
  usability), checker divergence test, e2e interp + wasm run
  (exit code + stderr), self-host parser case, format round-trip.

**A2. Pipe topic placeholder `_`.** [status: shipped]
Shipped as designed: native `parsePipe` + self-host `pipe_desugar`
scan the piped call's direct args for a bare `_`; substitution
replaces prepending, `Call.PipeHole` (1-based; 0 = prepended)
drives the formatter round-trip, two holes are a parse error
(P004), nested `_` stays an ordinary identifier (checker E001),
and holes compose across nested/chained pipes (an inner pipe
consumes its own `_` before the outer scan runs).
`x |> f(a, _)` calls `f(a, x)` — completes `|>` for the minority
of callees that don't take the data first.

- Rules: `_` valid only as a **direct argument** of the piped
  call (not nested in sub-expressions), **at most one** `_`; with
  a `_` present the LHS substitutes there instead of prepending;
  `_` anywhere else remains the existing wildcard/error behavior.
- Implementation: both parsers' pipe desugar grows a scan of the
  arg list for a bare `_` ident before prepending. AST records
  the hole index (new `PipeHole int` field, default 0 == "was
  prepended") so `internal/printer/format.go`'s IsPipe
  reconstruction can round-trip `x |> f(a, _)` faithfully.
- Tests: parser (substitution, two-`_` error, nested-`_` error),
  format round-trip, e2e interp + wasm, self-host parser mirror
  cases, checker unaffected (desugar is pre-check).

### Phase B — contract & design docs (lock decisions in writing)

**B1. Reuse contract doc.** [status: shipped — `docs/REUSE-CONTRACT.md`]
`docs/REUSE-CONTRACT.md`: what Fern's RC/reuse **guarantees**
today (Koka's stance: specified, programmer-visible behavior) vs
what is opportunistic. Contents: the reuse-site taxonomy
(self-overwrite struct/enum, cross-struct/tuple pairing, array
push in-place, move family), the runtime `is_unique` guard
semantics, what taints eligibility, and the doc-anchored test
list (`general_reuse_test.go` et al.) that keeps each documented
guarantee locked. Explicitly records the PLDI-2021-vs-drop-guided
gap and links E3.

**B2. Comptime design brief.** [status: shipped — `docs/COMPTIME-BRIEF.md`]
`docs/COMPTIME-BRIEF.md`: the Zig rules (hermetic — no comptime
I/O; target-faithful — comptime observes target layout, critical
with `WidthPtr` 4-vs-8 across Fern's three backends; one
partial-evaluation mechanism, no token macros) + the Lean 4
Syntax→Expr staged-pipeline shape. Includes the near-term
actionable: new parse-time desugars should be written as explicit
CST→CST passes over the AST rather than inlined in `parseX`
functions, starting with the next desugar that lands. No comptime
implementation now — this is the brief that gates the eventual
one.

**B3. Iterator fusion contract.** [status: shipped — `docs/ITERATOR-FUSION-CONTRACT.md`]
`docs/ITERATOR-FUSION-CONTRACT.md`: the strymonas-derived
compositional guarantee to adopt *when* lazy iterators return to
the agenda — "if each operator is individually non-allocating,
the composed pipeline compiles to a single loop with no
intermediate allocations" — the operator algebra to support
(map/filter/take/zip/flat_map), which of those defeat naive
fusion, and where the pass lives (`internal/ir`, over the
cursor protocol). Records the measurement bar (hand-written loop
parity) any implementation must clear.

### Phase C — stdlib

**C1. `std/peg`.** [status: shipped]
Shipped as designed with one API adjustment: the `Pattern` enum
variants ARE the construction API (no wrapper functions needed —
`PSeq([PLit("("), PRef("b"), …])`), plus class helpers
(`peg_digit()` etc.). The matcher threads its state functionally
(struct fields are immutable — E048), which makes PEG
backtracking undo-free: a failed alternative resumes from the
pre-attempt state binding, and only the furthest-failure
watermark is merged across. Left recursion fails fast via the
8192-deep PRef bound. Verified on all four backends
(TestPegModule) + the 18-case TAP suite
(examples/tests/peg_test.fern, gated by
TestRunnerPegExamplePasses).
Pure-Fern PEG module (Janet/Rebol/Raku convergence), complement
to `std/regex` (which stays: cheap one-line matches). Scope for
slice 1: pattern constructors as an enum tree (`Lit`, `CharSet`,
`Range`, `Any`, `Seq`, `Choice`, `Star`, `Plus`, `Opt`, `Not`,
`And`, `Ref` for named rules), a recursive matcher over a
grammar `Map[string, Pattern]`, position + named **captures**
(the thing regex.fern lacks), and error position reporting.
API sketch: `peg.compile(rules) -> Grammar`,
`g.parse(input) -> Result[Match, PegError]`,
`match.capture("name") -> Option[string]`. Bounded: no
left-recursion support (documented), packrat memoisation deferred
until a real workload needs it. Tests: std-test-runner suite
(`examples/tests/peg_test.fern`) + e2e gate mirroring the
test-runner contract; exercise on two real formats (a config
grammar and HTTP request-line/headers) to keep it honest.
Doubles as a self-host IR-path workload (closures + enums +
maps + recursion).

### Phase D — platform & runtime architecture

**D1. Capability enforcement (platforms Phase 2 slice).**
[status: shipped]
Shipped with two reality adjustments: (1) the gate table lives in
`internal/platforms/enforce.go` (builtin → capability: subprocess,
stdin/read_line, the tcp/udp family, the fs family) and native +
wasm descriptors were extended to declare what their runtimes
actually wire — which surfaced that **`subprocess` is interp-only**
(no codegen backend lowers it), so no compiled target grants it
and E066 replaces the old "undefined label" assembler failure;
(2) enforcement runs in `cmd/fern run()` (not the checker) after
a pre-shake that mirrors each backend's own treeshake (dyn roots,
-shared exports, WIT-export roots, and wasi-http's drop of the
synthesised tcp_serve main), so unused imported stdlib wrappers
never trip gates and bare `-check` stays target-neutral. E066 is
positioned for entry-module call sites and position-less-but-named
for imported-module sites; `fern explain E066` documents it. The
diag catalogue-completeness gate now also scans cmd/fern +
internal/platforms. Self-host parity: not needed by construction —
E066 is emitted on the Go CLI's compile path only, never by either
checker, so the differential checker-codes gate is unaffected.
Make `internal/platforms.Descriptor.Capabilities` real: compiling
for a target rejects, at **check time**, calls to runtime
primitives the target's descriptor doesn't grant (first concrete
case: `subprocess` / blocking `read_line` under `wasi-http`).
Slice plan: (a) map capability strings → the builtin/stdlib
entry-point names they gate (table in `internal/platforms`);
(b) thread the resolved target into the check pipeline (new
optional checker input — today the checker is target-agnostic;
keep it optional so bare `-check` stays target-neutral);
(c) new error code (`E064` range) with a "this target does not
provide X; provided by: <targets>" message; (d) self-host parity
deferred with an explicit note (the differential gate covers
error codes — coordinate before adding E064 to the shared list,
or gate it behind target-supplied mode the self-host driver
doesn't take yet). Roc's model (verified) is the design
reference; this also advances `PLATFORM-RESEARCH.md` Phase 2.

**D2. Crash-only native serve.** [status: SHIPPED — design +
D2' implementation]
D2' landed per the design's chosen shape: `proc_fork` /
`proc_waitpid` native builtins (x86-64 fork/wait4; arm64
clone(SIGCHLD)/wait4 — no fork syscall on arm64; arm64-darwin BSD
fork/wait4 with the XNU child-flag normalisation,
runtime-verified by CI's macos lane), gated under the new `proc`
capability (native targets only — wasm worlds reject at check
time via E066). The interp returns -38/ENOSYS (Go cannot
bare-fork) and `tcp_serve_supervised` degrades gracefully to
single-process serving — a design amendment recorded in
CRASH-ONLY-SERVE.md. std/tcp's accept loop is factored into
`__serve_loop`; the supervisor owns the listener (backlog
survives worker deaths), logs each death's raw exit code,
backs off 100ms→5s, resets after ≥100ms-lived workers, and gives
up after 8 consecutive fast deaths. e2e: survives-handler-trap,
crash-loop-giveup, interp-fallback, and per-backend
fork/waitpid contract tests.
Decision: supervisor shape (parent owns the listener, forks the
accept-loop worker, waitpid + bounded-backoff refork, crash-loop
give-up); in-process trap recovery permanently rejected as
RC-unsound; fork-per-request deferred as an opt-in for untrusted
inputs. D2' needs new native-only `proc_fork`/`proc_waitpid`
builtins gated under a new `proc` capability (E066 machinery from
D1). wasi-http needs nothing — wasmtime already isolates.
wasi-http already has per-request isolation (wasmtime); native
`tcp_serve` dies with the request. Erlang-inspired policy:
isolate at the request boundary. Design doc first
(`docs/CRASH-ONLY-SERVE.md`) weighing: (i) supervisor process —
`fern serve` parent forks/re-execs the server child, restarts on
abnormal exit with backoff (simple, coarse: in-flight requests on
other connections die too, but the *service* survives);
(ii) fork-per-request (true isolation, expensive per request,
conflicts with keep-alive); (iii) in-process trap recovery
(longjmp/signal — **rejected up front**: RC heap state after a
mid-mutation trap is unsound to resume over a shared bump arena).
Slice 1 implements the chosen shape (expected: supervisor mode,
opt-in flag on `tcp_serve` or a `std/http` serve wrapper) + an
e2e test that a trapping handler yields a non-200/connection
reset while the server keeps answering subsequent requests.

### Phase E — type-system & Perceus-adjacent

**E1. `@must_consume` marker types.** [status: SHIPPED — native +
self-host]
Complete: native E067 (previous slice) plus the self-host port —
the attribute is stamped through the self-host parser's
struct/enum decls (must_consume field, propagated through the
flatten/bundle rewrites), the `mc_*` walk family in checker.fern
mirrors internal/checker/mustconsume.go function-for-function
(gated by mc_any_marked so unmarked modules pay nothing), and
"E067" joined selfHostImplementedCodes with 21 mc-* fixtures in
the differential codes gate — both checkers emit identical code
sets per fixture, cross-checked against the Go oracle. Port
deviations (enum-variant-as-ExprCall, value-position match's IIFE
desugar strictness, position-less lambda diags) are documented in
docs/SELFHOST-CHECKER-PORT.md. Fixpoint self-compiles green with
the new checker in the loop.
Shipped: `@must_consume` on struct/enum decls (both parsers — the
self-host parser parse-tolerates and drops it pending its checker
port), the native E067 walk (at-least-once on every path;
laundering into unmarked containers, closure capture, and
unconsumed overwrite rejected at their sites; `own` params are the
declared sinks — exempt, composing with the affine owned-param
rule for exactly-once across own boundaries), `fern explain E067`,
and 21 checker tests pinning both directions per shape. Grounding
corrections recorded in docs/MUST-CONSUME.md: the differential
gate is opt-in per code (the port lands as its own slice, the
E063 convention), and the owned-argument rule shapes how sinks
are written.
Decision: at-least-once obligation checking (E067), E063-shaped
conservative walk, consuming uses = call-arg / return / construct /
destructure, unmarked-container laundering rejected, closures
capturing marked values forbidden in slice 1; self-host checker
parity required in the same implementation PR. First queued real
user: D2's worker-lifecycle handle.
Vale-Higher-RAII/Austral-inspired linear obligation, layered on
RC (checker-only; Perceus still frees). Design doc
(`docs/MUST-CONSUME.md`) then slice 1. Sketch: `@must_consume`
attribute on struct/enum decls; a value of such a type must be
**consumed** on every control-flow path before scope exit —
consumed = passed as an owned argument, returned, or destructured
by match/let; dropping implicitly (scope exit, shadowing,
overwrite) is a new checker error. Deliberately intra-procedural
(same altitude as E063 slice-escape). First real user:
`HttpResponseWriter`-style "respond exactly once" once the
serve-side API grows one; until then the feature ships with test
types + docs. Self-host checker parity required (differential
gate) — budget for porting the walk.

**E2. `fip` completion.** [status: (a) self-host parity DONE —
absorbed upstream; (b)-(d) remain as the E2' follow-up]
Slice (a) — self-host parity for the `fip` modifier + the E053
checked subset — was shipped by the parallel checker-port
completion (#4451 freeze-precondition work, 2026-07-12: FuncDecl
carries `fip` through the self-host parser, checker.fern has the
E053 walk, and six e053-fip-* fixtures sit in the now-unfiltered
differential corpus). The remaining items are unchanged and
large: `fbip` (destructive-match mutation set — requires wiring
E053's static view to the IR's actual reuse sites so
verify-matches-enable), graded `fip(n)`, and docs. These
coordinate with the goal-2 roadmap per E3/E4 below.
Native `fip` (E053) is verify-don't-enable. Remaining, in order:
(a) **self-host parity** for the existing modifier + E053 subset
(parser + checker port — required before self-host compiler
sources can use `fip` themselves); (b) `fbip` modifier (allows
allocation-free *mutation* set: destructive match with reuse
credit — requires wiring E053's static view to the IR's actual
reuse sites so "verify" matches "enable"); (c) graded `fip(n)`;
(d) docs page (`fern explain E053` exists? verify + extend).
The ICFP 2023 paper is the reference; `OWNERSHIP-INFERENCE-PLAN.md`
is the standing home for the borrow-side interactions.

**E3. Drop-guided reuse (native evaluation).** [status: not started]
Today's `computeReuseSources` is the PLDI 2021 pairing; ICFP 2022
frame-limited/drop-guided is documented-superior (fragility under
transformation). This item is an **evaluation slice, not a
rewrite commitment**: implement the drop-guided source selection
behind a flag, run the RC fuzzer + leak detector + reuse test
suite + a self-compile allocation-count comparison, and write the
verdict into `RC-PERCEUS-PLAN.md` (adopt / keep pairing /
hybrid). Do **not** start this while the self-host port of the
*current* pairing (`SELFHOST-PERCEUS-REUSE.md`) is mid-flight
unless the verdict lands first — porting a moving target twice is
the failure mode to avoid. Sequencing with goal 2 decided when we
get here.

**E4. Self-host reuse port.** [status: standing goal-2 roadmap]
Not new work from this survey — it *is* goal 2's next frontier
(`SELFHOST-PERCEUS-REUSE.md`, `RC-PERCEUS-SELF-HOST-PORT.md`).
The survey's contribution is B1's contract doc (defines what the
port must preserve) and E3's verdict (defines which algorithm
gets ported). Proceed per the standing docs.

## Sequencing & session cadence

```
P0  plan doc (this)                          — docs PR
A1  todo statement builtin                   — small PR
A2  pipe placeholder _                       — small PR
B1+B2+B3 contract/design docs                — docs PR
C1  std/peg                                  — medium PR
D1  capability enforcement slice             — medium PR
D2  crash-only serve: design doc             — docs PR
E1  must-consume: design + slice 1           — medium PR
D2' crash-only serve: implementation         — medium PR
E2  fip: self-host parity slice              — medium PR
E2' fip: fbip / verify-enable wiring         — large
E3  drop-guided evaluation                   — large
E4  (standing goal-2 roadmap)
```

Rationale: A-slices are hours each and immediately useful to the
porting workflow; B locks contracts cheaply while they're fresh;
C is self-contained stdlib value; D1/E1 are the first
checker-surface features (medium risk, high value); the E2'/E3
memory work is deliberately last — it must coordinate with the
in-flight goal-2 roadmap rather than race it.

Per-PR gates: full x86-64 + wasm local suites (`-timeout 30m` for
whole e2e packages; wasmtime/wasm-tools from `/tmp/wt/`,
`FERN_WASI_ADAPTER` set), arm64 left to CI, swap enabled before
any self-host driver build.
