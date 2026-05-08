# Language direction

Where the language is going, what shapes we've decided on, and what
we've explicitly chosen NOT to copy. Living document — update as
each phase lands.

## Positioning

Two target use cases, both short-lived:

- **CLI tools** with fast startup (cold-start measured in
  milliseconds, not seconds).
- **Edge HTTP handlers** following the `wasi:http/proxy` shape —
  Fastly Compute, Netlify Edge Functions, Unikraft Cloud,
  `wasmtime serve`. One handler invocation per request, response
  sealed at end of scope.

Both share a memory profile: programs allocate freely, then the
whole address space tears down. Means we can lean on a bump
allocator (no free, no GC), and design slices/views to share the
arena's lifetime instead of carrying ownership.

The historical TS-flavoured surface (`var`, `T[]`, `if/else`) was a
starting point, not a constraint. From here we look at Roc, MoonBit,
Rust, Zig, Odin, Hare, Gleam for design inspiration — not at TS.

## Convergent signals from cross-language research

Five-language survey (MoonBit, Roc, Zig, Odin/Hare, Gleam/Rust) —
ideas that turned up in three or more languages independently are
default-correct unless we have a specific reason to deviate.

### Strong convergence

- **`{ptr, len}` non-owning slices.** All five. Free to construct,
  share parent's lifetime. With a bump allocator the borrow-checker
  story collapses: views are arena-scoped, no further machinery.
- **Insertion-ordered IndexMap as the default `map`/`dict`.**
  MoonBit, Roc, Odin all converged. Flat `[(k,v)]` array + index
  table, single allocation, cache-friendly. Iteration order =
  insertion order. Right default for headers / JSON / env vars.
- **Sized integer types with explicit conversion.** Hare, Odin,
  Zig agree. Roc keeps it ergonomic via polymorphic literals
  (`4 : Num *` resolves from context). The combination —
  width-explicit declarations + polymorphic literals — is the
  ergonomic sweet spot.
- **Multi-return + early-exit error operator.** Some shape: Odin's
  multi-return + `or_return`, Hare's `(T | E)?`, Rust/Roc's
  `Result?`, Gleam's `use`. Match-only is universally rejected.
- **Exhaustive `match`/`switch` over tagged unions.** Universal.
  Compile-time exhaustiveness check, guards, payload destructuring.
- **`defer` (and `errdefer` for rollback)** for scope-bound
  cleanup. Pairs well with allocator-threaded code.
- **UTF-8 strings.** Universal except MoonBit (UTF-16, deliberate
  legacy). Right call for wasm + edge HTTP.

### Notably good single-source ideas worth cribbing

- **Gleam's `use`.** `use n <- result.try(parse(s))` desugars at
  parse time to `result.try(parse(s), fn(n) { <rest> })`. Pure CPS
  rewrite, zero runtime cost, generalizes `?` to with-resources,
  deferred cleanup, async, anything callback-shaped. One keyword
  unlocks many patterns.
- **Roc's lambda-set defunctionalisation.** Each call site knows
  the finite set of lambda shapes that can flow there; closures
  lower to a tagged union of capture-structs. No indirect calls,
  no boxing. Direct fit for wasm where indirect calls are slower
  and harder to inline.
- **Roc's small-string optimization.** ~23 bytes inline. HTTP
  methods, paths, JSON keys, header names mostly fit; heap traffic
  drops sharply for our targeted workloads.
- **Roc's seamless slices.** `Str.split` returns strings sharing
  the parent buffer; runtime distinguishes via a tag bit. Free
  slicing in a bump allocator.
- **Odin's `context.allocator` + `context.temp_allocator`.** A
  CLI invocation or HTTP handler is exactly one scope. Install a
  bump arena into `context.allocator` at entry, `defer
  free_all(...)` at exit. Sub-scopes can swap allocator without
  parameter churn. The two-allocator default ("permanent" vs
  "scratch") is the right shape for handlers — most allocations
  are scratch.
- **Rust's `let else`.** `let Some(x) = opt else { return; };`
  binds in the enclosing scope and forces the else branch to
  diverge. Kills pyramid-of-doom guard-clause code.
- **Zig's hashmap fingerprint metadata.** SoA layout: array of
  values, array of fingerprint bytes (1 bit free/used/tombstone +
  ~6 bits hash). One cache line covers ~64 probe slots. Vectorizes
  cleanly.
- **Zig's `errdefer`.** Runs only on error return — perfect for
  "rollback an allocation if init fails partway."

### Things deliberately NOT cribbed

- **MoonBit's UTF-16 string internals.** Java/JS heritage. UTF-8 is
  right for wasm + edge HTTP. MoonBit themselves are migrating
  APIs Unicode-safe over time.
- **Rust's monomorphized lazy iterator chains.** Beautiful when the
  optimizer fuses them, allocation traps when it doesn't. Without
  an aggressive inliner we'd ship code that looks fast but isn't.
  Eager combinators + pipe is safer until the IR can fuse.
- **Hare's no-generics stance.** Forces users to rewrite map / set
  / vec each time. Bad fit for "small fast CLI tools" where you
  want batteries.
- **Hare's single global allocator.** Defeats the per-request
  arena story. Odin wins this one decisively.
- **Zig's no-closures stance.** Closures are valuable for HTTP
  routing / handler composition. Roc shows you can have them
  cheaply via lambda-set defunctionalisation.
- **Roc's runtime refcounting.** With a bump allocator that's
  freed on scope exit, refcounts are pure overhead. Compile-time
  uniqueness analysis can mutate-in-place when the source is
  provably dead, fall back to copy otherwise — no runtime check.
- **Odin's array swizzling / SoA programming.** Cute for gamedev,
  off-target for CLI / edge.
- **Implicit numeric widening (C / TS / JS).** Universally
  rejected by the no-GC languages we surveyed; aligned with our
  "explicit beats clever" posture.

## Shipping plan

Five PRs, each shippable. Breaking changes are fine — single user.

### PR 1 — Numeric foundation

- Replace `number` with sized integer types: `i8`, `i16`, `i32`,
  `i64`, `u8`, `u16`, `u32`, `u64`. `isize` / `usize` are aliases
  (`i32` / `u32` on wasm32, `i32` / `u32` on arm32).
- Default literal type stays `i32`. Literals are polymorphic in
  expected-type context: `let x: i64 = 1` works without a cast.
- Explicit conversion only (`x as i64`); no implicit widening.
- `f32` (existing) and `f64` (new); current `float` becomes alias
  for `f32`.
- Existing programs using `number` keep compiling — `number` stays
  as a deprecation alias for `i32` until PR 5.
- Update WASM codegen: i64 ops via `i64.*` instructions.
- arm32 codegen: i64 deferred — error out with a clear message if
  used; everything else routes through existing i32 codegen.

### PR 2 — Tuples (shipped) + slice views (deferred)

Tuples landed standalone — slices got split into a follow-up.

Tuples (in):
- `(T, U, V)` tuple type with N≥2 elements. No singleton tuples
  (avoids the trailing-comma rule); `()` is reserved for the
  function-type-of-no-args case.
- Tuple literals `(e1, e2, …)` with N≥2.
- Numeric field access: `pair.0`, `pair.1`. The lexer already
  hands `.N` back as a number; the parser routes it to a
  `FieldAccess` with the digit string as `Field`.
- Multi-return via tuples: `function divmod(a: i32, b: i32):
  (i32, i32) { return (a / b, a % b); }`.
- Codegen: tuples lower to heap-allocated records (same shape as
  structs but anonymous, addressed by position). Each element
  gets a 4-byte slot.

Tuples (deferred to a follow-up):
- Pattern destructuring `let (a, b) = pair;`. Workaround today
  is `var p = pair(); var a = p.0; var b = p.1;` — wordy but
  works. Adding `DestructureVar` cleanly is its own design pass
  (binding semantics in match arms, function params, etc.) so
  punted.

Slices (deferred to PR 2.5):
- Slice/view type `[T]` distinct from owned `Array<T>` (or
  current `T[]`). Open question: keep `T[]` for owned + `[T]`
  for view? Or unify? Lean Odin-style: `[T]` view + `Array<T>`
  owned, deprecate `T[]`.
- String slicing returns `[u8]` views.
- Views borrow lifetime from their parent (arena-scoped).

### PR 3 — Generics for functions + structs

- `function map<T, U>(xs: [T], f: (T) -> U): Array<U> { ... }`
- `struct Pair<A, B> { a: A, b: B }`
- Monomorphization pass. Code-size is a wasm concern — start with
  always-monomorphize, revisit a heuristic later.

### PR 4 — Built-in `Map<K, V>` + ergonomics layer

- Built-in IndexMap-shaped `Map<K, V>`: insertion-ordered,
  flat `[(k,v)]` + Zig-style fingerprint metadata index table,
  single allocation, Wyhash hash function.
- Map literals: TBD syntax. `{ "k": v }` collides with struct
  literals. Candidates: `#{ "k": v }`, `Map { "k": v }`,
  `Map.from([("k", v)])`. Lean `Map { ... }` — it reads naturally
  and parsers cleanly.
- Pipe operator `|>` — **shipped early as a standalone PR** since
  it's a parse-time desugar with no codegen impact and the
  ergonomic win is immediate. Data-first (`x |> f(a, b)` →
  `f(x, a, b)`); chains left-associate; precedence sits between
  assignment and ternary so `1 + 2 |> f` is `f(1 + 2)`. The
  formatter round-trips the pipe form via an `IsPipe` flag on
  Call.
- `let else` / `if let`.
- `match` guards (`when n > 0`).
- `use` syntax (Gleam-style). Replaces a future `?` because it
  generalizes more.

### PR 5 — Memory model first-class

- `arena` as a language concept. Each scope opens an arena that
  resets at exit. CLI default = process-wide arena, freed on exit.
  HTTP handler default = per-request arena, freed when the
  response is sealed.
- `defer` keyword for scope-bound cleanup. Pairs naturally with
  the arena story.
- Document the lifetime contract for slices/views (must outlive
  the arena they reference).
- Drop the `number` deprecation alias.

## Deferred — not in any of the above five PRs

- **Closures via lambda-set defunctionalisation.** Currently
  closures go through a function table; works, just not optimal.
  Re-lowering them at IR-time after we have generics + monomorph
  is the natural moment.
- **Compile-time uniqueness analysis** for in-place mutation
  (Morphic-style). Real performance win for programs that build
  up + transform big structures. Needs the IR to track ownership
  paths; substantial work.
- **Iterators / lazy sequences.** Eager combinators + pipe handle
  most cases; lazy iterators want IR-level fusion to not be a perf
  trap.
- **Records (anonymous structural types).** Interesting for
  ergonomics — `({ name: "x", age: 5 })` without a struct decl —
  but not load-bearing yet. Comes back when we have generics.
- **Error effects in function signatures** (MoonBit's `raise`).
  Worth re-examining once `use` is in. May be redundant with
  `Result` + `use`.
- **`comptime` as a unified generic / const-fold mechanism**
  (Zig). Powerful but a big design swing; defer until we feel the
  pain of separate generic + const systems.
- **Async / `pollable` integration** for long-running handlers.
  The proxy world is request-response; longer-lived handlers
  (websockets, SSE) need async machinery we haven't built.
- **Trait / typeclass system.** Function-passing covers most cases
  for now (Gleam shows it's livable). Revisit when stdlib gets
  big enough that ad-hoc polymorphism hurts.
- **Standard library shaping.** json, url parsing, base64, hex,
  random — all the usual suspects for edge handlers. After PRs 1-4
  the type system is rich enough to design these properly.

## Open questions to settle as we go

- **Slice syntax**: `[T]` view + `Array<T>` owned, or keep `T[]`
  for owned and reuse `[T]` for view? Lean toward visual
  distinction.
- **Default int**: `i32` (wasm-native, current) vs `i64` (Go
  default, larger range). Lean i32 — wasm-native, existing
  programs assume it.
- **Map literal syntax**: see PR 4 candidates. Lean `Map { ... }`.
- **`Bytes` vs `[u8]`**: distinct nominal type or just a slice of
  u8? Lean `[u8]` for symmetry with other slices.
- **Closure capture semantics**: by-value vs by-reference, escape
  analysis. Currently undocumented; settle when re-lowering for
  defunctionalisation.
