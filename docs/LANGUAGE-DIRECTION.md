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
- The historical `number` alias was dropped in PR 1's
  follow-up cleanup; use `i32` / `i64` / `u32` etc. directly.
- Update WASM codegen: i64 ops via `i64.*` instructions.
- arm32 codegen: i64 deferred — error out with a clear message if
  used; everything else routes through existing i32 codegen.

Status:
- `i32` / `i64` shipped (PR 1 main).
- `u32` / `u64` shipped (PR 1 follow-up): checker tracks
  signedness as part of `NumberType`, IR's `Op` carries an
  `Unsigned` flag for the ops where it matters (`/`, `%`, `>>`,
  `<`, `<=`, `>`, `>=`), wasm codegen picks `_u` vs `_s`
  variants, and the i32-to-i64 cast picks `i64.extend_i32_u` for
  unsigned sources / `i64.extend_i32_s` for signed.
- Polymorphic numeric literals (PR 1 follow-up): integer
  literals are inferred against the surrounding type — `var x:
  i64 = 1` works without `1 as i64`, `f(x, 0)` resolves the
  `0` against the parameter type, `(x: u32) / 2` settles `2` to
  u32, and `(x: i64) == 0` likewise. The checker stamps a
  resolved `Width` on `*ast.NumberLit` once the context is
  known; the IR picks `i32.const` vs `i64.const` from that
  field. Out-of-range literals (`var x: i32 = 5_000_000_000`)
  are now rejected at the checker rather than silently wrapping.
- Sub-i32 widths (`i8`, `i16`, `u8`, `u16`) shipped as scalar
  types: variables and arithmetic at sub-i32 precision live in
  i32 storage at the wasm level (the wasm validator wants i32
  locals), and the cast lowering bridges widths — narrowing
  masks (`x as u8` ⇒ `i32.const 0xFF; i32.and`), unsigned
  widening is a no-op, signed widening uses
  `i32.extend8_s` / `i32.extend16_s`. Polymorphic-literal
  range checking covers all four widths.
- Memory-stride owned arrays shipped: `u8[]` / `i8[]` use
  1-byte stride, `u16[]` / `i16[]` use 2-byte stride. Loads
  pick `i32.load8_u/_s` / `i32.load16_u/_s` per element-type
  signedness; stores use `i32.store8` / `i32.store16`. Bounds
  checking goes through dedicated per-stride helpers
  (`__str_idx` reused for stride=1, new `__arr_idx_2` for
  halfwords, new `__arr_idx_8` reserved for i64/f64). Slice
  views over sub-i32 arrays and write-side `arr[i] = v` for
  sub-i32 arrays are the next follow-up.
- `f64` shipped (PR 1 follow-up): parser accepts the keyword,
  IR has `OpConstF64` + Width-aware float binary ops + the
  `OpFPromoteF32` / `OpFDemoteF64` cast ops, wasm codegen picks
  `f64.*` instructions when `Op.Width == 64`, and float
  literals (`1.5`, `3.14`) participate in the same polymorphic-
  literal flow as integer literals. `float` is still an alias
  for `f32`.

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

### PR 2.5 — Slice views (shipped)

Non-owning views over `Array<T>`. Spelled `[T]` in source —
distinct from owned `T[]` so the API surface signals "this
borrows" without needing a borrow checker.

What landed:
- `[T]` slice type. `T[]` (owned) stays for declarations.
- Slicing syntax: `arr[a:b]`, `arr[a:]`, `arr[:b]`. The fully
  unbounded `arr[:]` form is reserved (errors with a hint).
- `len(slice)` reads from `slice + 4`; `len(arr)` and
  `len(str)` keep their `base - 4` shape. The IR's `len()`
  fold picks the right offset by static type.
- Indexing: `slice[i]` goes through a new `$__slice_idx`
  runtime helper that bounds-checks against the slice's len
  and dereferences `data_ptr` before stepping. Same trap
  semantics as array indexing.
- Sub-slicing: `slice[a:b]` works, dereferencing the parent's
  `data_ptr` once before computing the new view's start.

ABI: a slice value is a heap pointer to an 8-byte struct —
`{ data_ptr: i32, len: i32 }`. `data_ptr` aliases the parent's
storage, so the bump allocator's "everything alive at scope
exit" semantics make the borrow lifetime trivial.

Element stride is fixed at 4 bytes — works for `[i32]`,
`[string]`, `[Foo]` (struct ptr), and `[T[]]` (array ptr). A
slice over `[i64]` or `[f64]` needs a stride argument and is
deferred to the unsigned-types follow-up.

Deferred:
- String slicing (`str[a:b]` returning a `[u8]` or string view).
  Splitting bytes from string is its own design pass; not
  blocking real handler code that just wants array slicing.
- `[u8]` view of strings.
- Mutating slice operations (`slice[i] = v`).
- Slice arithmetic / concatenation.

### PR 3 — Generic functions + generic structs (both shipped)

Generic functions only — generic structs are a follow-up. Same
bracket form as generic enums for declaration consistency:

```
function id[T](x: T): T { return x; }
function first[T](xs: T[]): T { return xs[0]; }

function main(): i32 {
    var a = id(42);             // T inferred = i32
    var s = first(["hi", "hi"]); // T inferred = string
    return a;
}
```

What landed:
- `[T]` / `[T, U]` after the function name in declarations.
  Same form enums use, so users learn one shape for both.
- Implicit type-arg inference at call sites — the checker
  unifies expected ParamType against actual arg types via the
  pre-existing `unifyType` helper. No explicit `f[i32](...)`
  syntax yet (would conflict with array indexing; needs
  lookahead).
- New `internal/monomorph` package runs after type-checking,
  before any IR / codegen. Walks the AST, finds every Call to
  a generic function, mangles the callee name (`id__i32`),
  records the instantiation, and clones the FuncDecl per-T.
  Original generic decls are dropped from `prog.Funcs`.
- Re-checks the rewritten program at the end of the pass so
  every cloned decl gets its own `FuncSigs` entry + body
  type-check.

Cost / trade-offs:
- Code-size grows linearly with instantiations. For our
  short-lived programs (CLI / edge handlers) and small stdlib
  surface, that's fine. A heuristic to share dictionaries for
  cold instantiations is a follow-up.
- The naïve clone-then-recheck is O(N × M) for N
  instantiations and M function bodies — fine in practice
  because both numbers are small. If recheck cost becomes an
  issue, swap for a targeted body-rewrite that only retypes
  the cloned functions.

Generic structs (also shipped, follow-up to functions):

```
struct Pair[A, B] { first: A, second: B }
struct Box[T] { val: T }

function unbox[T](b: Box[T]): T { return b.val; }
```

What landed:
- `struct Foo[A, B] { … }` parses identically to enum type
  params.
- `StructType.Args []Type` — populated for generic
  instantiations. Equality is pairwise on Args (matches
  EnumType).
- Checker infers Args from struct-literal field values via
  the same `unifyType` machinery. `StructLit.TypeArgs []Type`
  carries the inference result for the monomorphiser.
- Monomorphiser clones generic StructDecls per unique
  instantiation. After cloning + substitution, a fixed-point
  loop walks every Type slot in surviving (monomorphic)
  bodies / params / returns and mangles `StructType{Args}`
  references — catches function clones whose substituted
  param types were `Box[i32]` and need to become
  `Box__i32`. Two passes typically suffice; the loop iterates
  to catch nested instantiations.
- Field access through a generic struct's instance
  substitutes the type-args before returning the field type
  (`Pair[i32, string].first` → `i32`, not `A`).

Deferred to a follow-up:
- Explicit type args at call / type sites (`f[i32](x)` /
  `Pair[i32, string] { … }`). Needs lookahead to
  disambiguate from `arr[i]`. Inference covers the
  ergonomic baseline.
- Generic constraints (`T: Eq`, `T: Hash`). Probably never —
  the lang doesn't have a trait system; users pass functions
  explicitly when they need polymorphism beyond what's
  captured by the type variable alone (Gleam's posture).

### PR 4 — Built-in `Map<K, V>` + ergonomics layer

- Built-in IndexMap-shaped `Map<K, V>`: insertion-ordered,
  flat `[(k,v)]` + Zig-style fingerprint metadata index table,
  single allocation, Wyhash hash function.
  - **First cut shipped (linear-search foundation).** Auto-
    injects a non-generic `Map` struct (i32 keys + i32 values
    only). API is `map_new(cap)` / `m.len()` / `m.has(k)` /
    `m.get(k)` / `m.set(k, v)`. Wasm runtime helpers do
    linear search.
  - **Dynamic resize shipped.** `m.set(k, v)` doubles the
    backing buffer (or jumps to 4 if cap=0) when full and
    copies the existing entries over. The bump allocator
    can't reclaim the old buffer; that pays back when the
    arena resets at scope exit (PR 5).
  - **Iteration shipped (snapshot APIs).** `m.keys()` and
    `m.values()` return fresh `i32[]` arrays containing the
    map's keys / values in insertion order. Both are
    snapshots — mutating the map afterwards doesn't affect
    the returned arrays. A non-allocating iterator
    (`m.iter()` returning a stateful cursor) is a future
    follow-up.
  - Future PRs generalise to `Map[K, V]`, swap the linear
    search for the IndexMap fingerprint table, add Wyhash,
    and ship the map-literal syntax.
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
- `if let` — **shipped**. `if let Variant(b) = expr { … } [else
  { … }]` — pattern-binding without the match ceremony.
- `let else` — **shipped**. `let Variant(b) = expr else {
  divergent };` — pattern-binding declaration whose bindings
  flow into the enclosing scope; the else branch must
  terminate the surrounding control flow. Checker enforces
  divergence via `blockDiverges` (recursive: a block diverges
  iff its last statement does; if/match diverge iff every arm
  diverges).
- `match` guards — **shipped**. Spelled `<pattern> when <bool> =>
  <body>`. Guard runs with bindings in scope; on false, the
  match falls through to the next arm. Conservative
  exhaustiveness: a guarded arm doesn't count as covering the
  variant, so a fallback arm (or `_`) is required.
- `use` syntax (Gleam-style) — **shipped**. `use IDENT [: TYPE]
  <- EXPR;` desugars at parse time to a synthesised local
  callback function whose body is the rest of the enclosing
  block; EXPR is rewritten to call with the callback appended
  as the last arg. Chains nest naturally so flat `use a <- ...;
  use b <- ...; return ...;` produces the expected closure
  tree without manual indentation. The type annotation is
  optional — when omitted, the checker reads the receiving
  function's callback-parameter signature (`fn(callback:
  (T) => U)` ⇒ binding has type `T`) and stamps the synthesised
  FuncDecl. Generic-callee inference is still TODO; for those
  callees the explicit `: TYPE` is still needed.
- **Enum methods (shipped).** `function (self: Option[i32])
  unwrap_or(fallback: i32): i32 { … }` defines a method on the
  enum type. Same hoisting + call-site rewriting as struct
  methods (`__method_<EnumName>_<MethodName>`). Receiver type
  must be a known struct or enum; non-named types still error.

### PR 5 — Memory model first-class

- `arena` as a language concept. Each scope opens an arena that
  resets at exit. CLI default = process-wide arena, freed on exit.
  HTTP handler default = per-request arena, freed when the
  response is sealed.
- `defer` keyword for scope-bound cleanup. Pairs naturally with
  the arena story.
- Document the lifetime contract for slices/views (must outlive
  the arena they reference).
  (The `number` deprecation alias has already been dropped — see
  PR 1's status block.)

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
