# Research roadmap — reading + implementation order

The eight `*-RESEARCH.md` docs in this directory were written
as standalone surveys; each one stands on its own. This doc
is the meta-index: **what order to read them, what order to
implement them, and which recommendations gate which other
ones.**

Read this first when picking up the research; come back to
it when deciding what to ship next.

## The 8 docs at a glance

| # | Doc | Sources surveyed | Lead recommendation |
|---|------|------------------|---------------------|
| P | `PERFORMANCE-RESEARCH.md` | BQN/CBQN, Forth, Factor, OCaml, LuaJIT, SBCL, Crystal, Nim, Julia, Vale, WUFFS, Jai, Zig, Rust | Move IR to basic-block + SSA form |
| R | `PLATFORM-RESEARCH.md` | Roc, Cloudflare Workers, Fastly Compute, wasi:http, hyper/Axum/Tower, Bun, AWS Lambda | Promote `Platform` capability bag to a 2nd handler parameter |
| B | `BOOTSTRAP-RESEARCH.md` | Rust, Zig, Crystal, OCaml, Go, TypeScript/tsgo, Pony, TinyCC | Two-implementations-forever posture; WASM snapshot bootstrap |
| I | `IDE-COMPILATION-RESEARCH.md` | rust-analyzer, Roslyn, gopls, OCaml merlin, tree-sitter, Carbon | Thread cancellation tokens through parser + checker |
| D | `DIAGNOSTIC-UX-RESEARCH.md` | Elm, Rust, ReasonML, TypeScript, Hare, codespan-reporting, ariadne, Idris | Multi-label diagnostics + structured `Diagnostic` type |
| S | `STDLIB-DESIGN-RESEARCH.md` | hyper, Bun, simdjson, serde, jiff, NodaTime, Temporal, Go time, Rust io, WASI Preview 2 | Three breaking changes now: `body: Stream[bytes]`, real `HeaderMap`, six-type date/time |
| M | `MODULE-PACKAGES-RESEARCH.md` | Cargo, Deno/jsr, Go modules, npm, Hex, Nix flakes, Swift PM, Bazel, dune | `lang.toml` + `lang.lock` with Minimum Version Selection |
| C | `CONCURRENCY-RESEARCH.md` | Go, Trio, Erlang, Rust async, Java Loom, Pony, Kotlin, Zig, OCaml 5, Verona | `concurrent { … }` block; no function coloring |

## Reading order

The recommended reading order is **R → S → C → P → I → D → B → M**.

Reasoning:

1. **R (PLATFORM)** first because it defines the handler /
   host seam — the surface every other doc references. Once
   you know what a `Platform` parameter is, the cross-doc
   references in S and C make sense.

2. **S (STDLIB-DESIGN)** next because it specifies what
   types `Platform` exposes (HTTP, JSON, date/time, I/O)
   and locks in the sync-at-language-level posture that C
   builds on.

3. **C (CONCURRENCY)** third — closes the runtime surface.
   After R+S+C, you know what handler code looks like end-
   to-end.

4. **P (PERFORMANCE)** fourth — switches focus to the
   compiler internals. SSA + escape analysis + scalar
   replacement; the architectural lever for everything
   downstream.

5. **I (IDE-COMPILATION)** fifth — applies the same
   compiler-internals lens to the *interactive* edit-
   compile-feedback loop.

6. **D (DIAGNOSTIC-UX)** sixth — the user-facing
   counterpart to I. Structured diagnostics land in the
   same plumbing the LSP uses.

7. **B (BOOTSTRAP)** seventh — only matters once
   `examples/self_host/` reaches parity. Less urgent reading.

8. **M (MODULE-PACKAGES)** last — strictly post-self-host
   concern. Read when the first non-stdlib third-party
   package is on the horizon.

## Implementation order

Reading order ≠ implementation order. Implementation is
tiered by **urgency × cost × reversibility**.

### Tier A — Breaking-change-now (cheap now, expensive later)

Land these before any handler code uses them, since once
in-the-wild handler code exists (even self-written) the
breaking change cascades.

1. **R Rec §1: Platform capability bag as 2nd handler
   parameter.** Gates almost everything else in R, S,
   and C. Single biggest single change.
2. **S Rec §1: `HttpRequest.body: Stream[bytes]`.** Was
   `string`; today's shape breaks on binary, streaming,
   large bodies.
3. **S Rec §2: Real `HeaderMap`.** Was `Map[string,
   string]`; loses duplicate headers, case-sensitive
   lookup.
4. **S Rec §4: Six-type date/time module.** Green-field
   design. Lock it in *before* any handler code uses
   dates; once Instant-vs-Date confusion lands in even
   one widely-imported helper, fixing it requires
   touching every caller.

These four together are ~6 weeks of work and define the
language's user-facing shape for the next several years.

### Tier B — Foundation-now (gates everything later)

These don't break anything; they're architectural
investments that downstream recs assume.

5. **P Rec §1: Basic-block + SSA-form IR.** The single
   biggest architectural lever. Until this lands, half
   of P's other recs (escape analysis, scalar
   replacement, allocation sinking, bounds-check
   elimination, range analysis) work at half strength.
   Multi-week refactor; pays back across every backend.
6. **I Rec §1: Cancellation tokens through parser +
   checker.** 1 week of work; removes the entire class
   of fast-typist UX problems. Cheapest large win in the
   whole research set.
7. **D Rec §1+§2: Multi-label diagnostics + structured
   `Diagnostic` type.** Gates D's other recs *and* the
   LSP-side `relatedInformation` / `CodeAction` wiring.
8. **R Rec §2: `Platform` descriptor format.** TOML or
   lang-defined per-target file. Enables Tier C's mock
   platforms and Tier D's bindings.

### Tier C — Composition-on-the-foundation

Land after Tier B; each unlocks a clean expansion of
the surface.

9. **D Rec §3-§5: Suggestions, error codes, targeted
   phrasing.** Editorial work; ongoing thereafter.
10. **R Rec §3: `init()` as a recognised entry point.**
    Two-phase lifecycle (init + invoke); supports
    Lambda-shape warm starts and long-lived servers
    without forcing per-request reinitialisation.
11. **R Rec §6: Mock platform for tests.** Drops out of
    Rec §1+§2; high test-ergonomics win.
12. **S Rec §3: Schema-directed `json_parse[T](bytes)`.**
    Type-safe parsing; the modern default for typed
    payloads.
13. **S Rec §5: `Reader` / `Writer` interfaces.**
    Generalises I/O abstractions; enables stream
    composition.
14. **P Rec §2-§4: Result location semantics, escape
    analysis, scalar replacement.** Each takes 1-2 weeks
    after SSA (B-§5) lands.
15. **I Rec §3-§4: Snapshot abstraction + per-module
    caching + exported-API-hash gating.** Kicks in
    when workspace grows past ~10 files.

### Tier D — Trigger-driven (do when need arises)

These are *correct* decisions but not *urgent*. Wait for
the trigger condition.

16. **C Rec §1: `concurrent { … }` block.** Trigger: the
    first handler that wants two parallel fetches.
17. **C Rec §2-§5: No function coloring, scope-bounded
    cancellation, `Task[T]`, `select`.** Lands as a
    package with §1.
18. **R Rec §5: Multi-handler-kind recognition** (`fetch`
    + `scheduled` + `alarm`). Trigger: cron / alarm /
    websocket handler needed.
19. **R Rec §7-§8: Named bindings + service bindings.**
    Trigger: multi-environment deploys, or two handlers
    in one binary.
20. **D Rec §7-§8: Color + box-drawing + LSP wiring.**
    Visible polish; do once the engineering foundation
    is in place.
21. **S Rec §10: Outbound `plat.fetch(...)`.** Trigger:
    handler wants to call an upstream.
22. **I Rec §5: Lossless syntax tree.** Trigger: the
    formatter-strips-comments cost becomes visible, OR
    file sizes exceed ~5kLOC and edit-stable downstream
    caches matter.
23. **P Rec §5: Type-specialised array kernels.**
    Trigger: array-heavy stdlib hot paths show up in
    profiles.

### Tier E — Post-self-host

Wait until `ROADMAP-AND-SELF-HOSTING.md`'s parity
criteria are met.

24. **B Rec §1: Two-implementations-forever posture.**
    Decision, not engineering work. State publicly.
25. **B Rec §2+§4: Two-stage snapshot bootstrap + CI
    integration.** ~2 weeks together.
26. **B Rec §6: Extend langsmith to differential-test
    Go-impl vs lang-impl.** Critical for the
    two-impls-forever posture.
27. **M Rec §1-§5: Manifest, lockfile, MVS, content-
    addressed cache, workspaces.** Whole package-manager
    build-out; 2-3 months.

### Tier F — Indefinitely deferred

These are well-known good ideas that don't yet pay back
for the codebase's scale or trajectory. Worth knowing
exist; don't build.

28. P Rec §11+ — `noalias` annotations, SIMD vector
    type, cross-module inlining at link time.
29. I Rec §7+ — Full salsa-shape memoised query
    system. Carbon's bet: defer until simple approach
    proven inadequate.
30. C Rec §6: `Channel[T]`. Defer until streams need
    bounded backpressure.
31. M Rec §9+ — Central registry, signing
    infrastructure. Single-user; no ecosystem to
    secure.

## Dependency graph

Edges read "A is a prerequisite of B":

```
R-§1 (Platform parameter)
 ├─ R-§3 (init)
 ├─ R-§6 (mock platforms)
 ├─ R-§7 (bindings)
 │   └─ R-§8 (service bindings)
 ├─ S-§10 (plat.fetch)
 └─ C-§1 (concurrent {…} uses plat.*)

P-§1 (SSA IR)
 ├─ P-§2 (RLS)
 ├─ P-§3 (escape analysis)
 ├─ P-§4 (SRoA + allocation sinking)
 ├─ P-§7 (cost-model inliner)
 ├─ P-§9 (bounds-check elimination)
 └─ P-§11 (range analysis)

I-§1 (cancellation tokens)
 └─ enables fast-typist UX, no dependents

I-§2 (end positions)
 └─ I-§4 (snapshot abstraction)
     └─ I-§7 (hand-written memoisation)
 └─ I-§5 (lossless tree)

D-§2 (structured Diagnostic)
 ├─ D-§1 (multi-label)
 ├─ D-§3 (suggestions)
 ├─ D-§4 (error catalogue)
 └─ D-§8 (LSP relatedInformation/CodeAction)

S-§1 (Stream[bytes] body)
 └─ S-§3 (json_parse[T] uses bytes)
S-§5 (Reader/Writer)
 └─ S-§8 (MemoryWriter)
 └─ all stream-shaped stdlib

B-§1 (two-impls-forever)
 └─ B-§2 (snapshot bootstrap)
     ├─ B-§4 (CI integration)
     └─ B-§5 (snapshot regeneration)
 └─ B-§6 (differential oracle)

M-§1 (lang.toml)
 └─ M-§2 (lang.lock)
     └─ M-§3 (MVS)
     └─ M-§4 (content-addressed cache)
     └─ M-§5 (workspaces)
     └─ M-§6 (vendoring)
```

Cross-doc edges (the load-bearing inter-doc dependencies):

- **R-§1 → C-§1**: Concurrency surface uses `plat.fetch`,
  `plat.kv`, etc.
- **R-§1 → S-§10**: Outbound HTTP goes through
  `plat.fetch`.
- **R-§2 (descriptor format) → M-§8 (per-target sections)**:
  Same per-target metadata, same TOML schema.
- **P-§1 (SSA) → I-§4 (snapshot abstraction)**: salsa-
  shape memoisation needs def-use chains; not a hard
  block but a substantial enabler.
- **D-§2 (structured Diagnostic) → I-§8 (LSP code-actions)**:
  IDE quick-fixes need machine-applicable suggestions.
- **B-§6 (differential oracle) ← langsmith fuzzer**:
  Existing infrastructure already in `internal/langsmith/`.

## One-recommendation-per-doc, ranked by absolute leverage

If you only do *one thing per doc*, do these:

1. **R**: Platform capability bag (Rec §1).
2. **P**: Basic-block + SSA-form IR (Rec §1).
3. **S**: Six-type date/time module (Rec §4).
4. **I**: Cancellation tokens through parser+checker (Rec §1).
5. **D**: Structured `Diagnostic` type (Rec §2).
6. **C**: `concurrent { … }` block + no function coloring (Recs §1+§2 as a pair).
7. **B**: Two-implementations-forever posture (Rec §1).
8. **M**: `lang.toml` + `lang.lock` with MVS (Recs §1+§2+§3 as a triple).

The cheapest single high-impact change: **I Rec §1**
(cancellation tokens — 1 week, removes a whole class of
LSP UX problems).

The single change with the longest leverage tail:
**P Rec §1** (SSA IR — multi-week refactor, but unlocks
~6 other P recs and pays back across every backend
indefinitely).

The single change most expensive to defer:
**S Rec §4** (six-type date/time). Date/time is what
*every* language gets wrong; getting the shape right
*before* code accretes against the wrong shape is the
defining version of this category of decision.

## When to revisit this doc

- **After each Tier A item lands.** They're the
  load-bearing breaking changes; the rest of the plan
  rebalances around what shipped.
- **When a research doc gets superseded by an
  implementation.** Mark it `IMPLEMENTED →
  <link-to-impl-doc>` in the table above.
- **When a new research topic arrives.** Add to the
  table; re-rank the tiers.

The tiers are *opinionated rankings*, not commandments.
If profile data, real usage, or a contributor's
preference reorders things, reorder them. The dependency
graph is the actual constraint; everything else is
priority-call.
