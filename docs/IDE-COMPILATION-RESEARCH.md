# IDE-grade incremental compilation research

`docs/LSP-INTEGRATION-PLAN.md` ships an MVP LSP: hover, go-to-def,
diagnostics, completion, all backed by a small `compileCache` that
content-hashes whole files and re-runs `parser.Parse` →
`checker.Check` on every edit. That's the right MVP — it works,
it's small, and `cmd/lang-wasm` already builds it for the
playground.

This doc is about what comes *after* the MVP, when sub-100ms-on-
every-keystroke becomes the goal. The reference bar for that is
**rust-analyzer** — a 500k LOC Rust codebase with red-on-error
diagnostics that update faster than the cursor. The mechanisms it
uses (salsa + rowan + lossless syntax + lazy resolution) are well-
documented and largely transferable. So is the cautionary tale:
they cost 4+ years of engineering and a specific IR shape.

The question this doc answers: **given the current LSP MVP, which
of those mechanisms are worth porting, in what order, and which
have payoff thresholds the codebase won't cross for a while?**

Companion to `LSP-INTEGRATION-PLAN.md` (the *what* and the
roadmap) and `PERFORMANCE-RESEARCH.md` (compiler perf for the AOT
path). This doc is specifically about *interactive-editing perf*,
which has different shape: small edits, sub-millisecond response,
no warm-up, repeat indefinitely.

## Framing — what "IDE-grade" means

Three metrics, in order of importance:

1. **Edit-to-diagnostic latency.** Time from keystroke to red
   squiggle. Bar: < 100 ms on a medium file (~500 LOC). LSP MVP
   today: ~5-20 ms on a tiny file (playground-shape), unmeasured
   on a real-world file because no real-world files exist yet.

2. **Edit-to-completion latency.** Time from `.` to candidate
   list. Bar: < 50 ms. Today: completion is wired but goes
   through the whole pipeline.

3. **Memory ceiling.** Working set in-IDE. Bar: < 200 MB for a
   workspace of ~50 files. Today: the FIFO cache caps 16
   entries; each entry holds a full `ast.Program` + `checker.
   Info`. Workspace-shaped use is untested.

The MVP's content-hash + memoize approach works up to ~100 LOC
per file. Past that, three failure modes show up:

- **Re-parsing dominates.** Even a one-character edit re-parses
  the whole file. ~1 ms / kLOC; 5 ms on a 5k-line file. Cheap
  in isolation but adds up across rapid edits.
- **Re-checking dominates.** The checker walks the whole AST.
  ~5-50 ms / kLOC depending on type-resolution cost.
- **Cross-file invalidation cascades.** Editing one file
  invalidates every file that imports it, transitively. With
  100 files, one edit can re-check 30+.

rust-analyzer hits sub-100ms on millions of LOC because none of
those three things happen on most edits. salsa memoises queries
at the granularity of "AST of file X" → "type of symbol Y" →
"completion candidates at cursor"; an edit invalidates the
*specific* queries that depend on it, leaving the rest cached.

## What we already do well — call out so we don't drift

- **Compiler is a Go library, not a CLI subprocess.** `parser.
  Parse`, `checker.Check`, `printer.Format` are exposed as
  ordinary functions; LSP calls them in-process. This is the
  right architecture (gopls / rust-analyzer / merlin do the
  same; tsserver-via-subprocess is the wrong shape).

- **Errors are collected, not first-fail.** Per
  `LSP-INTEGRATION-PLAN.md ▸ Why this is tractable`, both the
  parser and the checker return `diag.Errors`, not panic-on-
  first-error. LSP can surface every diagnostic in one pass.

- **`ast.Walk` visitor exists.** Recently added (per the LSP
  plan's PR ordering); cursor-position → AST node lookup is
  tractable.

- **Compile cache exists at file granularity.** `internal/lsp/
  cache.go`. FIFO, content-hashed. Right shape for the MVP;
  see Rec §3 for the salsa-flavoured generalisation.

- **Same Wasm build serves browser + editor.** Avoids the
  classic LSP-vs-playground bifurcation. Cuts maintenance.

- **No tsserver-style "incremental program" abstraction
  baked in yet.** This is *good* — it means we haven't taken
  on a wrong-shape commitment we'd have to undo. The MVP is
  the right starting point.

## Single-source deep dives

### rust-analyzer — salsa, rowan, and the lossless-tree posture

Sources:
- https://rust-analyzer.github.io/blog/
- https://github.com/salsa-rs/salsa
- https://github.com/rust-analyzer/rowan
- Aleksey Kladov, "Three Architectures for a Responsive IDE"
  (2020 post; the canonical reference).

**Three load-bearing pieces:**

#### Lossless syntax tree (rowan / red-green)

The parser produces a tree that preserves *everything* — every
byte of the original source. Whitespace, comments, even
invalid characters land as `Trivia` nodes attached to the
right token. The tree is *typed* through accessor methods (a
`FnDef` node has `.name()`, `.params()`, `.body()`), but the
underlying representation is a generic `(SyntaxKind, [child])`
node.

Two virtual-tree layers:

- **Green tree**: immutable, structurally shared, content-
  hashed. A small edit produces a new green tree that shares
  90%+ of its nodes with the old one. Functional-data-
  structure style.
- **Red tree**: lazy, parent-pointer-having, cursor-friendly
  view *over* the green tree. Holds offsets and parent
  references so "what node is at offset 142?" is O(log n).
  Allocated on demand, dropped when the cursor moves.

**Why this matters for incremental.** A 1-character edit
produces a new green tree whose first divergent node is the
*token* containing that character. Everything above and around
that token is structurally shared. Memoisation keyed on green-
node identity is therefore stable across edits that don't
touch a particular subtree.

**rust-analyzer's parser is full-reparse** (not incremental).
The structural sharing in the resulting tree is what makes
edits cheap; the parser itself runs over the whole text on
every edit. This is fast enough because parsing is ~1 ms /
kLOC and the green tree's hash-cons makes downstream queries
mostly cache-hits.

#### salsa — memoised query system

Every analysis is a *query*: a pure function from inputs (the
green tree, a symbol path, etc.) to outputs (a type, a
completion list, a diagnostic). salsa caches each query's
output keyed on its inputs.

The trick: **inputs are themselves queries**. A query that
asks "what's the type of `foo`?" doesn't take "the AST" as a
parameter; it asks "what's the AST of file `foo.rs`?", which
is its own query that takes "the text of file `foo.rs`" as
input. Salsa tracks the dependency graph automatically by
recording which queries each query reads.

**Invalidation cascade.** An edit changes the text-of-file
input. salsa invalidates the AST-of-file query. Each query
that read that AST is invalidated, *but* salsa does an
**early-cutoff check**: if the AST's output content-hash is
the same after re-running (e.g. you added a comment that
didn't change the typed AST), the AST query is "validated"
without re-running its dependents. Massive saving in
practice.

**Cancellation.** Salsa queries thread a cancellation token.
When the user types another keystroke mid-query, in-flight
work is cancelled and the new edit takes over. Without this,
fast-typist UX devolves to "wait for the previous edit's
analysis to finish before the new one starts."

#### Lazy name resolution and tower of queries

rust-analyzer doesn't have a single "check this program"
pass. It has a tower of queries:

- `parse(file_id) -> Parsed`
- `module_data(file_id) -> ModuleData` (top-level decls)
- `def_map(crate_id) -> DefMap` (whole-crate name table)
- `resolve_path_in_scope(scope, path) -> Option<Def>`
- `infer_function(fn_id) -> InferenceResult`
- `diagnostics_for_file(file_id) -> [Diagnostic]`
- … hundreds more.

Each is a salsa query. Completion at a cursor doesn't
"check the whole program"; it walks up from the cursor and
asks specific queries for specific symbols. Most queries
return cached values.

**What translates:**

- **Lossless syntax tree** is the highest-leverage single
  change. Today's `ast.Node`s are stripped of trivia (the
  formatter "strips comments" per the LSP plan). A rowan-
  shaped two-layer (green + red) tree gives us:
  - Faithful formatting (no comment loss).
  - Edit-stable downstream memoisation.
  - Cursor positioning that's correct by construction.

  Cost: substantial. Roughly equivalent to a new IR layer.
  Touches parser, checker, printer, every AST consumer.

- **Salsa-shape memoisation** is the second lever. Today's
  `compileCache` keys on whole-file content; a salsa-shape
  cache keys on (query, inputs) and tracks dependencies
  automatically. The Rust crate is permissively licensed
  (Apache/MIT) but doesn't fit a Go codebase (we're not in
  Rust). A *small Go port* is feasible — the core idea is ~
  500 LOC; the polish is more. Alternatively, hand-write
  the most useful queries (parse, check, resolve_path,
  completions) without a general query framework — closer
  to gopls's approach. See Rec §3.

- **Cancellation tokens** through queries are cheap to add
  if planned for. Without them, fast-typist UX limits
  hard at ~50 ms per query.

**Considered, left:**

- *Full rust-analyzer architecture pivot.* It's a 4-year-
  investment build-out. Not justified for a single-user
  language with a working MVP. Mine specific techniques
  (lossless tree, memoised queries, cancellation), don't
  port the whole stack.

### Roslyn — the canonical red-green tree

Sources:
- https://github.com/dotnet/roslyn (the C# / VB compiler)
- Eric Lippert's blog (specifically his "Persistence,
  Facades and Roslyn's Red-Green Trees" post).
- https://learn.microsoft.com/en-us/dotnet/csharp/roslyn-sdk/

**Roslyn is the *original* red-green-tree codebase.** rowan
copied it. Worth understanding from Roslyn directly because
the design rationale is much more thoroughly documented.

**Why two trees?**

- *Green nodes* are immutable, parent-less, fully shared.
  This lets the same `MissingToken` instance appear ten
  thousand times in a file without consuming ten thousand
  allocations.
- *Red nodes* are a lazy projection of green nodes that
  carries parent pointers and absolute offsets. Created on
  demand, can be discarded.

**Roslyn's incremental parser.** Unlike rust-analyzer's
full-reparse-with-structural-sharing approach, Roslyn does
incremental *parsing*: a parse-tree-rewriter consumes the
edit (a `TextChange` with offset + length + new text) and
splices it into the existing tree, re-parsing only what
crosses syntactic boundaries. Faster than rust-analyzer's
approach for very large edits but more complex.

**Semantic model is separate.** Trees are syntactic. The
*semantic model* (types, bindings, control flow) is computed
on demand from a tree + a compilation unit. Multiple
semantic models per tree (one per analysis pass) compose
cleanly.

**What translates:**

- **Semantic model separation.** Today `checker.Info` is the
  "semantic model" — names, types, signatures. It's already
  separate from the AST. Stay there; don't conflate.

- **Red node lazy projection.** Even without going full
  red-green, lazy parent-pointer attachment (only when
  walked) saves substantial memory for the common case of
  "navigate to one cursor position, don't touch the rest."

**Considered, left:**

- *Roslyn-style incremental parsing.* The complexity isn't
  worth it for our scale. Full-reparse-with-structural-
  sharing (rowan-style) hits the same target with much less
  code.

### gopls — the pragmatic mid-tier

Sources:
- https://github.com/golang/tools/tree/master/gopls
- https://go.dev/blog/gopls-scalability (2023 post).

**gopls is the "as much salsa as you actually need" reference
point.** No red-green tree (Go's ast/token packages aren't
lossless); no general-purpose query framework (queries are
hand-coded); incremental compilation is at the **package**
level, not the symbol level.

**Architecture:**

- `Session` holds open files.
- `Snapshot` is an immutable point-in-time view; each edit
  creates a new snapshot.
- `Package` is the unit of caching. Each Snapshot has a
  map of file-set → Package.
- A type-check is per-Package; same as `go build`'s.
- File edits invalidate the package(s) the file belongs to;
  reverse-dependency packages are *not* invalidated unless
  exported symbols changed.

**Key win: re-using `go/types`.** gopls doesn't have its own
type checker. It calls Go's stdlib `go/types` package, which
is the same one `go build` and `go vet` use. The behaviour
is therefore identical, by construction.

**The exported-API-hash optimisation.** When file X is
edited but X's exported API (the public symbols' signatures)
doesn't change, packages that import X don't need re-type-
checking — only X itself does. gopls computes a hash of the
exported API on each re-check and skips invalidation when
the hash is stable.

**What translates:**

- **Snapshot abstraction.** Today the LSP holds open files
  in a map keyed by URI. Wrap that in a `Snapshot` struct:
  immutable, point-in-time, copy-on-write. Each edit
  produces a new snapshot. Queries take a `Snapshot` as
  input. Makes the cache invalidation discipline explicit.

- **Per-module (vs per-file) caching.** Today the LSP
  caches per-file. The codebase has a module system per
  `PRELUDE-TO-MODULES.md`. Cache the *whole module's*
  checker result keyed on the file-set + content hashes
  of all member files. Invalidate the module when any of
  its files changes.

- **Exported-API-hash gating.** When a module's edited
  files don't change the module's exported signatures,
  dependent modules don't re-check. Same trick as gopls;
  ~30% of edits are private-only.

- **Re-using the same type-checker for `lang build` and
  the LSP.** Already true — both go through
  `internal/checker`. Stay there. Resist the urge to
  fork a "fast IDE checker."

**Considered, left:**

- *Per-symbol caching* (rust-analyzer's grain). gopls
  doesn't do it; the trade-off isn't worth it until file
  sizes pass ~10k LOC. We're far below that.

### OCaml merlin — per-file checker with caches at module boundaries

Sources:
- https://github.com/ocaml/merlin
- Frédéric Bour's talks on merlin's architecture.

**Merlin is the OCaml IDE engine.** Predates LSP; speaks its
own protocol but bridged to LSP via `ocaml-lsp-server`. The
architecture is informative because OCaml's separate
compilation model makes per-file caching natural.

**Key idea: `.cmi` files as the cache.** OCaml's compilation
produces `.cmi` (compiled module interface) per file —
serialised, contains exported types. Other files that
depend on this one import the `.cmi`, not the source.

Merlin uses this directly:

- Open file → parse → type-check → write `.cmi`.
- Other open file → parse → type-check using cached
  `.cmi`s of imports.
- File X edited → invalidate X's `.cmi` → reverse-deps
  that read it are flagged for re-check.

**Why this works in OCaml and translates here.** OCaml's
module system is explicit + nominal: file `foo.ml` defines
module `Foo` with a flat namespace. Same as this codebase's
`import "std/http"` shape per `PRELUDE-TO-MODULES.md`. The
`.cmi`-as-cache analogy is direct.

**What translates:**

- **Cache compiled module interfaces, not source ASTs.**
  Today the LSP cache holds whole `ast.Program` graphs.
  Most consumers (completion, hover, definition) only need
  the *exported* symbols + their types. Caching a
  module-interface struct (closer to `checker.Info` minus
  function bodies) cuts the working set substantially.

  Concrete shape: `ModuleInterface { exports: map[string]
  Symbol, imports: [ModuleId], file_hash: u64 }`.
  ~500 bytes per module typical, vs ~50 KB for a full AST.

- **Reverse-dep tracking.** When module X's interface
  changes, the set of modules to re-check is exactly X's
  reverse-deps. Build a small reverse-dep graph as
  modules are loaded.

**Considered, left:**

- *On-disk `.cmi` files.* Merlin persists them; gopls
  doesn't. For an in-memory LSP serving small workspaces,
  in-memory cache is enough. Persistence buys us cold-
  start speed if the IDE relaunches; worth doing later.

### tree-sitter — incremental parsing for syntax-only tools

Sources:
- https://tree-sitter.github.io/tree-sitter/
- Max Brunsfeld's Strange Loop talk.

**tree-sitter is *the* reference for incremental parsing,
but it's syntax-only — no type checking. Used by Atom,
Neovim, GitHub Search, many syntax-highlighters.**

**The algorithm:** GLR (generalised LR) parser with
**reusable nodes**. On edit, the parser identifies which
nodes were "in" the edit region and re-parses only those;
nodes outside the edit region are spliced into the new
tree directly.

**Error recovery is built in.** GLR keeps multiple parse
states active; a token that breaks one continuation keeps
the others alive. The parser produces a "best-effort" tree
for syntactically-broken input — useful when the user is
mid-typing.

**What translates:**

- **Error-tolerant parsing is necessary for IDE-grade UX.**
  Today's parser (per `internal/parser/parser.go`, ~2900
  LOC) collects errors but I'd want to confirm: does it
  produce a usable tree past a syntax error? If a typo at
  line 5 prevents parsing line 6's function body, hover at
  line 50 won't work. Need to check. Recommendation: see
  Rec §6.

- **GLR-style incremental parsing.** Hard to retrofit. Not
  recommended for the codebase's scale — the full-reparse
  approach is fine up to ~10k LOC. Worth knowing the
  technique exists if/when file sizes blow up.

- **Use tree-sitter as a *syntax-highlighter* feed.**
  Tangential: tree-sitter grammars exist for editor
  highlighting; writing one for lang would give us
  GitHub syntax highlighting, Neovim treesitter support,
  etc., for free. Doesn't change the LSP architecture
  but is high-value-for-low-cost.

**Considered, left:**

- *tree-sitter as the LSP's parser.* Wrong shape; the lang
  parser is in Go alongside the rest of the compiler, and
  tree-sitter is C with bindings. Two parsers = two
  things to keep in sync.

### Carbon's IDE-first compiler design

Sources:
- https://github.com/carbon-language/carbon-lang
- Chandler Carruth's CppNow 2023 "Toolchain Architecture
  for IDE" talk.

**Carbon (Google's C++ successor) is the most recent attempt
at "design the compiler IDE-first from day one."** Useful as
a sanity check on whether the techniques above are still the
right answer for a 2026 language.

**Key positions:**

- **Same compiler binary for `carbon build` and `carbon-ide`.**
  No fork. Same as gopls's posture.
- **Parser produces a tree with explicit error nodes.** No
  "best-effort" — errors are first-class.
- **Type-checking is push-based, not query-based.** Carbon's
  designers explicitly rejected the salsa model — too much
  complexity, infrastructure cost. Instead: a single
  type-checker pass with very fast cancellation and
  re-runs. Bet: hardware is fast enough that re-doing the
  type-check on edits is fine if the type-check itself is
  sub-millisecond per kLOC.
- **Explicit module interfaces** (like OCaml `.cmi`,
  TypeScript `.d.ts`).
- **Memory pools per query.** Allocate everything in an
  arena tied to a specific compile invocation; drop the
  arena when invalidated. Avoids GC pressure entirely.

**The "fast checker" bet** is the most interesting departure.
Carbon's argument: salsa is necessary if the checker is slow.
If the checker is sub-millisecond per kLOC, just re-run it
on every edit. Less infrastructure, simpler code.

**Whether this works for lang:** depends on
`PERFORMANCE-RESEARCH.md ▸ Rec §1 SSA`. After SSA + the
other passes, the checker (which doesn't depend on SSA, but
benefits from clean IR layering) should hit < 5 ms / kLOC.
At that point, re-running on every edit is < 100 ms for
20 kLOC, which fits the IDE budget.

**What translates:**

- **The "fast checker, no salsa" bet is plausible for our
  scale.** Worth deferring the salsa-shape investment
  until measurements show the simple approach hits a wall.
  Pure cost-engineering call.

- **Memory pools per query.** Already aligned with the
  codebase's arena-per-request model. The LSP's per-query
  arena can use the same machinery.

- **Explicit module interfaces.** Same idea as the OCaml
  `.cmi` recommendation. Aligns Carbon + Merlin + gopls
  on the same point — high signal.

**Considered, left:**

- *Push-based type-checking* over a centralised pull-based
  query system. Less mature than salsa; less obvious it
  scales to multi-million LOC. For our scale, both work.

### Aleksey Kladov's "Three Architectures for a Responsive IDE"

Source: https://rust-analyzer.github.io/blog/2020/07/20/
three-architectures-for-responsive-ide.html

**The canonical theory piece.** Names three shapes:

1. **"Map-reduce"** — re-do everything on every edit, lean
   on parallelism + cancellation. Cheap to build; doesn't
   scale past a single-digit-MB workspace. (TypeScript's
   `tsserver` pre-2020.)

2. **"Incremental"** — full salsa-shape memoised query
   graph. Maximum scale; maximum complexity. (rust-analyzer,
   Roslyn.)

3. **"Lazy"** — compute on demand at the cursor; don't
   eagerly compute anything else. Compose-with-everything
   answers. Doesn't pre-warm caches; can be slow on first
   query of a file. (IntelliJ's older architectures.)

Kladov's argument: most production IDEs end up as a hybrid.
Our LSP MVP is "map-reduce, debounce edits." That's fine
for now; the question is which axis to invest in first.

**For this codebase the axis is clear:**

- *Memory* is mostly fine (working set is small).
- *Latency* is currently fine for small files; will degrade
  at scale.
- *Cancellation* is missing — fast-typist UX will hit this
  first.

So: **start with cancellation**, then add per-module caching
(gopls-shape), then evaluate whether salsa-shape is needed
based on profile data.

## Cross-cutting themes

1. **Lossless syntax trees are universal.** Roslyn, rust-
   analyzer, tree-sitter, Swift's libIDE. The case is so
   clear that every project building IDE infrastructure
   *from scratch* in the last 10 years has adopted some
   form of it. Our parser strips comments today; the cost
   of fixing this compounds the longer we wait.

2. **Per-module is the sweet-spot caching grain.** Not
   per-file (too coarse for modules with many files; too
   fine for cross-file analysis); not per-symbol (overhead
   exceeds gains until very large codebases). OCaml
   merlin, gopls, Carbon converge.

3. **Cancellation tokens through long-running queries.**
   Universal once latency matters; missing in our MVP.
   The earlier this lands, the cheaper.

4. **Reuse the same type-checker for build + IDE.**
   gopls, Carbon, merlin. Forking an "IDE-mode checker"
   loses correctness guarantees; resist.

5. **Exported-API-hash gating for cross-module
   invalidation.** gopls + merlin. ~30% of edits skip
   downstream re-check.

6. **Error-tolerant parsing is required, not optional.**
   tree-sitter, rust-analyzer, Roslyn. The user's
   half-typed function body must produce a tree that
   lets the rest of the file's analysis proceed.

7. **Memory pools per query / per snapshot.** Carbon,
   merlin (via `.cmi` files). Arena allocation amortises
   the cost of "drop a stale cache entry": just throw
   the arena away. Aligns naturally with the codebase's
   existing per-request arena model.

## Concrete recommendations

Ordered by leverage × cost.

### 1. Thread cancellation tokens through long-running queries

**Cost: 1 week.** **Impact: high for fast-typist UX.**

Today's pipeline is `parser.Parse → checker.Check → diagnostics`,
all synchronous. When a new edit arrives mid-check, the LSP
either waits for the previous check (latency spike) or
discards its output (wasted CPU).

Add a `context.Context` (Go-idiomatic cancellation) through
the parser + checker. At each top-level statement boundary,
check `ctx.Err()`. On cancel, return early with a sentinel
error the LSP ignores.

Threads:

- `parser.Parse(ctx, src)` checks ctx at top-level stmt
  boundaries.
- `checker.Check(ctx, prog)` checks ctx between each
  top-level decl.
- LSP `updateDoc` cancels the previous in-flight ctx
  before launching the new one.

Cheap to wire, no architectural change.

### 2. Add end-positions to AST nodes

**Cost: 1 week.** **Impact: gates Rec §4, §5 + a clutch of
LSP features.**

Per `LSP-INTEGRATION-PLAN.md ▸ Significant gaps to close`,
AST nodes carry only start positions. Hover, semantic
tokens, go-to-def all need end positions for correct
ranges. Listed as a gap; just blocking on it.

Mechanical change to `internal/ast` (every node gets an
`End() Position`); parser populates as it builds. ~1 week of
careful editing.

### 3. Snapshot abstraction + per-module caching

**Cost: 2 weeks.** **Impact: high once workspace size
exceeds ~10 files.**

Replace `compileCache`'s per-file FIFO with a per-module
cache keyed on `(module-path, hash-of-all-member-files)`.
Wrap in a `Snapshot` struct that's immutable per edit:

```
type Snapshot struct {
    files     map[uri]string             // open buffers + on-disk
    modules   map[modulePath]*ModuleResult
    parent    *Snapshot                  // structural sharing
}
type ModuleResult struct {
    iface     *ModuleInterface           // exported symbols
    info      *checker.Info              // for hover/def
    diags     []Diagnostic
}
```

Each edit produces a new Snapshot that copy-on-writes the
edited file + invalidates dependent modules.

### 4. Reverse-dep tracking + exported-API-hash gating

**Cost: 1 week.** **Impact: medium-high once modules count
exceeds ~10.**

When module X is edited:

- Compute X's new `ModuleInterface`.
- Hash it.
- If the hash equals the previous interface's hash,
  reverse-deps don't need re-checking — only X itself.

Reverse-deps maintained as a `map[modulePath][]modulePath`
populated lazily during compilation.

### 5. Lossless syntax tree (red-green or rowan-shape)

**Cost: 4-6 weeks. Touches parser, AST, checker, printer.**
**Impact: gates faithful formatting, future incremental
parsing, edit-stable downstream caches.**

Big lift. Two layers:

- **Green tree.** Immutable, structurally shared, every
  byte of source preserved (including whitespace and
  comments). Stored as `(kind, [children])` generic node;
  typed accessors layered on top.
- **Red tree.** Lazy projection with parent pointers and
  absolute offsets. Allocated on demand.

Parser changes shape: build the green tree, then layer a
typed view. Checker reads through the typed view. Printer
walks the green tree and emits trivia faithfully.

The formatter-strips-comments bug (per the LSP plan)
disappears as a consequence.

**Probably defer until** the workspace + file-size growth
makes the lossy-AST cost real. Worth the prep work
(end-positions, error-tolerant parsing) ahead of time.

### 6. Audit + fix error-tolerant parsing

**Cost: 2 weeks.** **Impact: medium.**

Question to answer first: does the current parser produce
a usable tree past a syntax error at line N? If not, the
LSP's hover/completion/diagnostics for lines > N silently
break whenever the user is mid-typing.

Audit by:

- Constructing a battery of half-typed inputs (function
  definitions truncated mid-body, missing `}` on if,
  partial `match` arms).
- Running parser, asserting:
  - Some tree is produced.
  - Subsequent valid declarations are recognised.
  - The error is positioned correctly.

Fix any cases that fail. The parser likely already
recovers at top-level boundaries (function definitions);
the gaps are typically in expression position.

### 7. Hand-written query memoisation for the highest-traffic
queries

**Cost: 2 weeks.** **Impact: medium; alternative to full
salsa.**

Without going full salsa, pick the 5-10 queries the LSP
hits most:

- `module_interface(module_path)` — exported symbols.
- `module_diagnostics(module_path)` — error list.
- `type_at_position(uri, pos)` — for hover.
- `definition_at_position(uri, pos)` — for go-to-def.
- `completions_at_position(uri, pos)` — for completion.

Memoise each by hand keyed on its inputs. Invalidate
manually on edits. Less general than salsa but ~10× less
code.

### 8. Defer general salsa-shape query system

**Cost: 0 (a deferral).** **Impact: avoids a multi-month
investment we don't yet need.**

Carbon's bet — fast checker, simple invalidation, no
salsa — is plausible for our scale. Don't build the
infrastructure until measurements show:

- Median edit latency > 50 ms on a real workspace.
- And per-module caching (Rec §3 + §4) doesn't fix it.

Once both conditions hold, revisit. Likely cost: 2-3
months. Likely benefit: 5-10× latency improvement on
large workspaces.

### 9. tree-sitter grammar for syntax highlighting

**Cost: 1 week.** **Impact: low for LSP perf, high for
broader ecosystem (GitHub highlighting, Neovim
treesitter, etc.).**

Write a `tree-sitter-lang` grammar (separate repo or
under `editors/`). Used by GitHub Linguist, Neovim,
Helix, Zed, many syntax highlighters. Doesn't replace
the LSP parser.

### 10. Persistent on-disk module-interface cache

**Cost: 2 weeks.** **Impact: low; cold-start improvement
only.**

After Rec §3, persist the `ModuleInterface` cache to disk
(`.lang/cache/<module>.iface`). On LSP launch, hydrate
the cache from disk → instant warm-state.

Defer until LSP startup latency measurably matters.

## Anti-patterns — explicit "do not adopt"

- **Forked "IDE-mode checker."** gopls, Carbon, merlin all
  refused. Behavioural drift between build-mode and IDE-mode
  produces uncatchable bugs.

- **Subprocess-driven LSP** (tsserver pre-2017). In-process
  is the right call; both the codebase and the broader
  ecosystem are aligned. Wasm-in-browser is the modern
  analogue.

- **Full salsa-shape memoised query system *before*
  measurements prove the simple approach hits a wall.**
  Carbon's bet stands until proven wrong.

- **Symbol-level caching** (rust-analyzer's grain). Past
  the per-module sweet spot for our scale; pays off only at
  100k+ LOC files, which we're not approaching.

- **Speculative parsing / type-checking** (predict what
  the user is about to type). Worth a single sentence:
  some IDEs (IntelliJ in particular) pre-warm caches based
  on user trajectory. Not worth the complexity unless cold
  caches become a measurable bottleneck.

- **Custom LSP wire-format parser** instead of just
  hand-rolling. The MVP already does the right thing —
  ~10 message types, no `go.lsp.dev/protocol` dep. Resist
  the temptation to "professionalise" by pulling in a
  general protocol library.

- **Per-edit re-formatting / re-linting from inside the
  LSP.** These are user-triggered commands, not edit-time
  feedback. Don't let them eat the edit-latency budget.

## When to revisit

- **When the first real workspace exists** (currently the
  playground + a couple of example files). Real workspaces
  give measurements; measurements drive prioritisation.

- **When the lossy-AST cost becomes visible** — comment
  loss, format-round-trip non-determinism, position-
  reporting bugs. At that point, Rec §5 (lossless syntax
  tree) jumps in priority.

- **When a contributor opens a 5k-line .lang file.** Edit
  latency past 50 ms triggers Rec §3 + §4 + §7 in that
  order.

- **When the language picks up enough users that bug
  reports about "completion is slow" start arriving.**
  Until then, Carbon's bet — fast-checker, simple
  invalidation, no salsa — is the right default.

The single highest-leverage *cheap* recommendation is
**Rec §1 (cancellation tokens)**: 1 week of work, removes
the entire class of fast-typist UX problems. Land that
first; everything else is conditional on workspace size
and feature growth.
