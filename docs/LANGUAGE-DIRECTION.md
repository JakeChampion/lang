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

Tuples (follow-up status):
- **Statement-level destructuring shipped.** `let (a, b) = pair;`
  binds each name to the corresponding tuple element in the
  enclosing scope. The parser routes `let` followed by `(` to
  the destructure branch (variant binding `let Some(x) = … else
  …` is unaffected); the checker requires a tuple-typed init
  whose arity matches the name list, then registers each name
  + a synthesised hidden temp as locals so the IR can do one
  evaluation followed by per-name field loads. Arity ≥ 2
  (consistent with the no-singleton-tuples rule). Mixed
  element types just work — the IR picks `Load` vs `FLoad`
  per element.
- Match-arm and function-parameter destructuring is a separate
  pass (binding semantics differ enough that they're worth
  designing on their own).

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

Element stride is fixed at 4 bytes for the i32-shaped element
types (`[i32]`, `[string]`, `[Foo]` struct ptr, `[T[]]` array
ptr). Per-stride helpers (`__slice_idx_1` for u8/i8,
`__slice_idx_2` for u16/i16, `__slice_idx_8` for i64/f64) ship
alongside, so sub-i32 and wide slices route through the right
load/store width without per-instantiation specialisation.

Shipped follow-ups:
- **String slicing** (`str[a:b]` → freshly-allocated substring).
- **Mutating slice writes** (`slice[i] = v`) — bounds-checked
  via the same `__slice_idx_N` helper as the read path, then a
  width-aware `i32.store8` / `i32.store16` / `i32.store` /
  `f32.store` / `i64.store` per element type.
- **Wide-element slices** (`[i64]` / `[f64]`) ship via the
  stride-8 helper.

Deferred:
- `[u8]` view of strings as a non-allocating alternative to
  `str.bytes()` (which copies).
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
  - **`m.delete(k)` shipped.** Returns `true` when the key
    was present, `false` otherwise. Implementation is
    swap-with-last (O(1)); insertion order isn't preserved
    after a delete. Future PRs may add `delete_ordered`
    alongside the IndexMap layout.
  - **Map-literal syntax shipped.** `Map { 1: 10, 2: 20 }`
    parses as a `MapLit` AST node and lowers to a
    `map_new(N)` + per-entry `set` sequence. `Map {}` and
    trailing commas are accepted. Picked over `#{ ... }` /
    `Map.from([...])` for natural reading; the parser
    discriminates against the regular struct-literal path
    by the type name (`Map` is auto-injected and has no
    user-visible fields, so the brace form unambiguously
    means a map literal).
  - **Generic `Map[K, V]` shipped.** The auto-injected
    `Map` struct now carries `TypeParams=["K", "V"]`. K is
    restricted to i32-sized scalars (i32 / u32 / sub-i32
    widths) or `string`; V is restricted to pointer-sized
    types (any 4-byte storage — i32 / string / struct /
    enum / array / slice ptr). The runtime stores a
    `keyKind` tag in the buffer header so the
    open-addressing core branches between i32-eq and
    `__str_eq` without per-instantiation monomorphisation.
    Method dispatch substitutes K / V from the receiver's
    TypeArgs into the registered method signatures so
    `(m: Map[string, i32]).set(k, v)` type-checks `k` as
    string and `v` as i32. `map_new` return-type inference
    flows from the destination context (`var m:
    Map[string, i32] = map_new(8)`) so no explicit type-arg
    syntax is needed yet. Map literals infer K / V from
    the first entry's types. Map's struct is excluded from
    the monomorpher (a single helper set handles every
    instantiation via the runtime keyKind tag).
  - **IndexMap-shaped open-addressing shipped.** The
    runtime swapped from O(n) linear-scan to O(1)
    expected-time hash lookups. Buffer layout: 16-byte
    header (cap, len, keyKind, _pad), then a `cap`-sized
    bucket-index array, then a `cap`-sized `(k, v)`
    entries array stored in **insertion order**. Buckets
    are i32 entry-indices (or `-1` empty / `-2`
    tombstone). Probing is linear with tombstone-skip on
    lookup and tombstone-reuse on insert. Resize triggers
    at 75% load factor and doubles the capacity (rounded
    to the next power of 2 in `map_new`). Hash function:
    Wang's integer mix for scalar keys, FNV-1a 32-bit for
    string keys — the design supports a Wyhash-flavour
    upgrade later if better mixing on adversarial inputs
    is needed. Insertion order survives `set` / `get`;
    `delete` does swap-with-last in the entries array
    (and patches the swapped entry's bucket pointer) so
    insertion order is preserved up to the deleted slot.
    Stress-tested with 100+ entries through inserts /
    updates / interleaved deletes / re-inserts on both
    i32-keyed and string-keyed maps.
  - **Cursor iteration shipped.** `m.iter()` returns a
    `MapIter[K, V]` that holds a pointer back to the kv
    buffer plus a current entry index. The struct is
    allocated once per loop; each step is just a load
    plus arithmetic. The API is `it.has_next()`,
    `it.key()`, `it.value()`, `it.advance()`. Iteration
    walks entries in insertion order (preserved up to any
    deletes; swap-with-last `delete` reorders past the
    deleted slot).
  - **Wide V via boxing shipped.** `Map[K, i64]` /
    `Map[K, f64]` now work end-to-end. The shared wat
    helpers stay i32-stride; the IR allocates an 8-byte
    cell per `m.set(k, v)` (and per MapLit entry) and
    passes the cell pointer through the helper's V slot.
    `m.get(k)` translates the helper's `Option[i32-cell-ptr]`
    return into a fresh wide-payload `Option[V]` inline,
    `m.get_or(k, fallback)` boxes the fallback + unboxes
    the result, and `MapIter.value()` unboxes the helper's
    cell pointer. `m.values()` is rejected by the checker
    for wide V until the helper learns to map cell-pointer
    → wide-stride array; the more common shape is fine.
    Trade-off: one extra alloc per insert + one extra
    indirection per read; acceptable under the bump
    allocator's per-arena reset and avoids a
    per-instantiation monomorph of the entire 1200-line
    map runtime. The longer-term direction is to migrate
    the map runtime itself to the lang prelude (matching
    the json / url / parse_int trajectory) so the IR's
    Width-aware Store / Load picks the right ops without
    boxing — that requires a few prelude primitives the
    lang doesn't yet expose (raw byte-buffer alloc, manual
    byte-stride pokes for the bucket / entries arrays),
    so the boxing shape ships first.
  - Wide K (i64 / u64 / f64 keys) is still deferred —
    needs an 8-byte-stride entry layout (the current
    helpers hard-code 4-byte K) and a key-comparison path
    that branches on width, so it's a bigger surgery.
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
- **Wide enum payloads shipped.** Variants whose payloads
  include `i64` / `u64` / `f64` lay them out at 8-byte
  alignment in the heap object (4-byte tag, optional 4-byte
  pad, then the wide slot) and lower with `i64.store` /
  `f64.store` plus the matching loads on the match-arm /
  if-let / let-else paths. The IR's `payloadLayout` /
  `payloadStoreOp` / `payloadLoadOp` helpers compute offsets
  + ops once and the three lowering sites consume them
  uniformly. `Option[i64]` and `Option[f64]` round-trip
  bit-exact; mixed-width variants like `enum Wide { W(i64,
  i32) }` lay out their second slot after the first finishes.
  Pre-settle on the variant constructor's arg list also lets
  non-generic wide-payload variants accept bare numeric
  literals without an explicit `as i64` cast — `W(8589934592,
  7)` settles the first arg to i64 from the declared payload
  type. Contextual flow from the destination annotation back
  into a generic-enum constructor arg also shipped:
  `var o: Option[i64] = Some(1);` resolves the literal to i64
  via `settleNumeric`'s `EnumType` case, which builds the
  type-param substitution from the destination's `Args`,
  walks each arg with the substituted payload type, and
  re-stamps `VariantCallPayloads` so the IR's `emitEnumNew`
  picks the resolved (no-longer-polymorphic) payload type for
  slot sizing + store-op selection. `assignable` got a
  symmetric pairwise-args relaxation so generic enums whose
  arg types differ only by polymorphic-vs-concrete-numeric
  flow into a concretely-typed slot — `Some(Some(1))` into
  `Option[Option[i64]]` works too.

### PR 5 — Memory model first-class

- **`arena { … }` block shipped.** Sugar for `arena_save() →
  body → arena_restore()` so the bump-allocator cursor snaps
  back when the block exits. Every allocation made inside
  the block is reclaimed at exit; nested blocks each get
  their own snap. Bindings declared inside an arena block
  are scoped to it (regular block-scoping, no special
  rules). Anything allocated inside the block must NOT
  escape — the language doesn't (yet) statically enforce
  this, so callers shouldn't return pointers to in-block
  allocations. Implicit per-handler / per-CLI-invocation
  arena defaults still TBD; for now this is an explicit
  user-controlled scope.
- **`defer` keyword shipped.** `defer EXPR;` schedules `EXPR`
  to run when the enclosing function exits. Multiple defers
  run in LIFO order. Each Defer node gets a synthesised
  "active" i32 local; reaching the statement at runtime sets
  the flag, and the cleanup blocks emitted before each
  return + at the end of the function run the deferred
  expression only when the flag is set. Defers registered
  inside a conditional that didn't fire are no-ops. The
  return value is evaluated before defer cleanup runs (the
  IR routes it through a temp slot), matching Go's "you
  can't mutate the return value from a defer" semantics
  without giving up named-return-value support that this
  language doesn't have anyway.
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
  - **base64 shipped.** `base64_encode(s)` /
    `base64_decode(s)` builtins, RFC 4648 standard alphabet,
    `=` padding on encode, decoding terminates at the first
    non-base64 character. Strings are treated as raw byte
    arrays so the round-trip is content-preserving.
  - **hex shipped.** `hex_encode(s)` / `hex_decode(s)`
    builtins. Lowercase output (`0-9a-f`); decode accepts
    both cases. Decoding terminates at the first non-hex
    char or odd-length tail without raising — the prefix
    length on the result reflects what was actually
    decoded so `len()` lets callers detect truncation.
    Same byte-array semantics as base64 (round-trip is
    content-preserving).
  - **`s.parse_int()` shipped.** `string` method returning
    `Option[i32]`. Accepts an optional leading `-`; rejects
    empty input, lone `-`, any non-digit character,
    embedded whitespace, and out-of-range values
    (overflow, `+`-prefixed). Internal accumulator is i64
    so the bound check against the signed-i32 range
    (`-2^31..=2^31-1`) is exact. **Migrated to the lang
    prelude** in PR 174 — was ~190 lines of hand-written
    wat, now ~25 lines of lang code in
    `internal/prelude/prelude.lang`.
  - **`s.parse_float()` shipped.** `string` method returning
    `Option[f32]`. Grammar: `[-]<digits>[.<digits>]
    [(e|E)[+-]?<digits>]`, with at least one of integer or
    fraction digits required. Mantissa accumulates into an
    i64 with a saturation cap at ~2^50 (1e15) — beyond that,
    extra digits are skipped while `exp_adj` keeps the
    magnitude correct, so very long inputs degrade gracefully
    rather than overflowing. Final value =
    `(f32) mantissa × 10^exp_adj`, sign applied via
    `f32.neg`. The float-from-decimal path is NOT bit-exact
    Steele/White / Ryu yet; close-enough-for-handler-config
    semantics. Hex / scientific formats with explicit base
    will get separate methods.
  - **`url_parse(s)` shipped.** Decomposes an absolute or
    relative URL into a `Url` struct (auto-injected, six
    fields: `scheme`, `host`, `port`, `path`, `query`,
    `fragment`). Returns `Option[Url]`; `None` only on
    completely empty input — best-effort parse otherwise
    with empty strings for missing sections and `port = 0`
    when unspecified or unparseable. No %-decoding; `query`
    and `fragment` are returned raw (callers do their own
    decode). The single-pass implementation scans for the
    boundary characters (`:` + `//`, `?`, `#`), derives
    section indices, and slices the input via
    `__str_slice` — sub-strings share the parent's lifetime
    via the bump allocator's "everything alive at scope
    exit" semantics. Drive-by IR fix: `fieldOwner` /
    `exprType` / `targetTupleType` now consult
    `b.scratchType` via the slot map, so match-arm-bound
    struct values (`Some(u) => u.scheme`) work — first time
    we exercised that path.
  - **`url_encode(s)` / `url_decode(s)` shipped.** RFC 3986
    percent-encoding. The unreserved set
    (`A-Za-z0-9-_.~`) passes through unchanged; everything
    else is emitted as `%HH` (uppercase hex). Decoding is
    forgiving — malformed `%` sequences (non-hex following,
    truncated tail) are passed through verbatim rather than
    raising. `+` is NOT translated to space; callers
    handling `application/x-www-form-urlencoded` data should
    swap `+` → ` ` before decoding.
  - **`query_parse(s)` shipped.** Splits a URL-encoded
    query string into a `Map[string, string[]]`. Pairs
    are separated by `&`; within a pair, `=` separates key
    from value. Both halves are url_decode'd before
    storage. **Duplicate keys** (`?tag=a&tag=b&tag=c`) all
    preserved — values for the same key collect into a
    `string[]` in insertion order, matching Go's
    `url.Values` and Python's `parse_qs`. A pair without
    `=` records the key with a single-element empty-string
    array. Empty input yields an empty map. Trailing `&`
    is ignored. `+` is left alone — callers handling form-
    encoded data should pre-process.
  - **`json_encode(v)` shipped.** Auto-injected `JsonValue`
    enum (variants `JNull`, `JBool(boolean)`,
    `JNumber(string)`, `JString(string)`,
    `JArray(JsonValue[])`, `JObject(Map[string,
    JsonValue])`) plus the encoder. Numbers carry their
    textual representation as a string — JSON's number
    grammar exceeds f32/f64 precision, and storing the
    digits verbatim preserves round-trip fidelity for
    parse-then-encode. Strings get the standard escape
    treatment (`"`, `\\`, `\n`, `\r`, `\t`, `\u00XX` for
    other controls); UTF-8 bytes ≥ 0x20 pass through
    verbatim — JSON allows them. The encoder uses a
    growable `[cap, len, data...]` buffer with a 2x
    doubling grow, returning a length-prefixed string
    where the buffer's `len` slot doubles as the prefix.
    `json_parse` is the inverse direction (see below).
  - **`f32.to_string()` / `f64.to_string()` shipped.** Decimal
    text formatting on the float types. Up to 7 fractional
    digits for f32 / 15 for f64 (matching IEEE 754 single /
    double precision); trailing zeros are trimmed and the
    decimal point is dropped if the fraction is zero.
    Special values get canonical names: `NaN`, `Inf`, `-Inf`.
    NOT bit-exact Steele/White / Ryu — close-enough-for-handler
    output, same trade-off as `parse_float`. Round-trip
    `parse_float(x.to_string())` recovers `x` to within f32
    epsilon for typical values; pathological cases lose
    trailing precision.
  - **`json_parse(s)` shipped.** RFC 8259 grammar
    recognizer; returns `Option[JsonValue]` (None on any
    malformed input). Recursive-descent built around a
    16-byte parser-state struct (`s`, `sLen`, `pos`,
    `error`). Numbers stored verbatim as JNumber's string
    payload — no double-parsing, perfect round-trip with
    json_encode. String escapes decoded: `\"`, `\\`, `\/`,
    `\b`, `\f`, `\n`, `\r`, `\t`, `\uXXXX` for BMP code
    points (UTF-8 1/2/3-byte encoding). Surrogate pairs
    are now combined: `😀` becomes the 4-byte
    UTF-8 sequence for U+1F600 (😀). Lone or mismatched
    surrogates fall back to U+FFFD REPLACEMENT CHARACTER,
    matching Go's `encoding/json` and most strict UTF-8
    emitters. Whitespace between tokens follows the spec.

## Stdlib implementation strategy: hand-written wat vs IR-routed

Two flavours of stdlib helper coexist:

1. **Runtime core (hand-written wat).** `__lang_alloc`,
   `__str_eq`, `__str_slice`, `__slice_idx_*`, the Map's hash
   mix + open-addressing core, `arena_save` / `arena_restore`,
   the int formatter, and the wasi imports. Hand-written wat
   in `internal/codegen/wasm/wasm.go`. The IR sees only the
   call sites (`OpCallDirect "name"`); helper bodies bypass
   the IR entirely.

2. **Lang prelude (IR-routed).** `internal/prelude/prelude.lang`
   — a small embedded source file parsed at checker startup
   and prepended to the user's program. Goes through the
   regular parser → checker → IR → codegen pipeline like any
   user code, so it picks up IR-level optimisations (peephole,
   dce, future inlining) and works on any backend without
   backend-specific shims. Method receivers in the prelude
   can be built-in scalar types (`function (s: string)
   is_empty(): boolean { return len(s) == 0; }`) and the
   receiver-hoisting + dispatch path treats them the same
   way as struct/enum methods.

Migration is incremental: each high-level helper moves from
wat to prelude on its own; the runtime core stays in wat
because the operations needed (memory.copy, memory.grow, raw
loads/stores, hash mixing) aren't expressible at the lang
level today.

**Performance comparison** (rough, based on the existing
helpers):

| Aspect                          | Hand-written wat   | IR-routed lang    |
|---------------------------------|--------------------|-------------------|
| Tight loops (memcpy, scan, hash) | optimal           | within 10%, maybe |
| Tiny helpers (`s.is_empty()`)    | direct one-op     | call overhead     |
| Recursive walkers (json encoder) | one alloc per buf | match-arm + heap  |
| IR optimisations (peephole, dce) | invisible         | applied           |
| Cross-target (wasm + arm32)      | duplicate per backend | one source    |
| Maintainability                  | verbose, fiddly   | concise           |

The wins from IR-routing are **maintainability + cross-target
portability**, not raw performance. For most helpers the
generated code is comparable; for hot paths (the Map runtime,
hash mixing, the bump allocator) hand-written wat is meaningfully
faster because we control the exact ops without going through
the lang's abstraction layers.

**Migration path** (split into per-helper PRs):

1. ✅ **Phase A: prelude infrastructure + first migration.**
   `internal/prelude/prelude.go` embeds the `prelude.lang`
   source via `//go:embed`; `injectPrelude` parses it on
   each `checker.Check` call and appends the decls to
   `prog.Funcs` (with `IsPrelude=true` for filtering in
   tests / dump tools). Method-receiver type-checking
   extended to accept `string`, `i8/i16/i32/i64/u8/u16/u32/u64`,
   and `f32/f64` receivers — built-in scalar receivers go
   through the same hoisting + dispatch as struct / enum
   methods. First migration: `s.is_empty()` — was 11 lines
   of hand-written wat, now 3 lines of lang.
2. **Phase B: migrate higher-level stdlib.** `parse_int`,
   `parse_float`, `s.repeat`, `url_encode`, `url_decode`,
   `query_parse`, `url_parse`, `json_encode`, `json_parse`,
   `f32.to_string`, `f64.to_string` — all shipped on a
   per-PR cadence. Phase B continued with the string
   methods: `starts_with`, `ends_with`, `contains`,
   `index_of`, `trim` (slice-based, no allocation), then
   `to_lower`, `to_upper`, `bytes` (allocation-based,
   built on the new `__alloc_u8` primitive), then `split`
   and `replace` (variable-length result, built on
   `__array_append_string` + concat). The supporting
   `__is_ascii_ws` and `__bytes_eq` wat helpers retired
   together with the migrations — every string method
   that has a wat helper has now moved, except
   `as_bytes` (which still needs slice-header alloc the
   prelude doesn't yet expose).
3. **Bridge functions for wasm intrinsics shipped.**
   `__memcpy(dst, src, n)`, `__memset(dst, b, n)`, and
   `__alloc_u8(n): u8[]` are thin wat-shim wrappers around
   wasm's bulk-memory `memory.copy` / `memory.fill` plus a
   tiny `__lang_alloc` + length-prefix sealer. Signatures
   registered in the checker so the prelude can call them
   like any builtin. Drives buffer-management code that
   doesn't yet have a clean lang-level shape — the
   remaining wat string methods (`to_lower`, `to_upper`,
   `bytes`) migrated using these primitives, then the Map
   runtime followed (~1184 wat lines → ~280 lang lines).
   The map's `__method_Map_*` calls keep their type-rich
   FuncSigs registrations (the language doesn't yet have
   generic methods on a generic struct), with a codegen
   alias that rewrites each call to its concrete `_impl`
   counterpart in the prelude — same pattern as the
   `__array_append_jsonvalue → __array_append_string`
   alias. Backends without bulk-memory (eg arm32 today)
   trip an "unsupported" path during codegen; wat is the
   only consumer for now.

   Drive-by escape hatch: `[u8]` slice and `u8[]` owned
   array now cast to `i32` to recover the data pointer.
   Slice cast loads the i32 at slice+0 (the data_ptr field
   of the `{data_ptr, len}` slice header); array cast is a
   no-op since an owned-array value already IS the data
   pointer (length lives at data-4). Together with the
   bulk-memory shims this is enough to express growable-
   buffer scratch code in lang without dropping into wat.

**Why not migrate everything at once:**

- Lang is missing some primitives the prelude would want
  for the most aggressive helpers: `i32 ↔ f32` bit-cast (for
  the float formatter's exponent bit pattern), `memory.copy`
  shim (for the json buffer). Add these incrementally.
- The IR inliner now handles small functions with internal
  control flow + non-recursive direct calls (extended in
  the post-Map-runtime cleanup so the migrated prelude's
  prefix / scan / hash helpers actually inline). Bodies up
  to 80 ops with internal `if` / `while` / `return` substitute
  cleanly; the wrapper-block + br-translation shape lets
  early returns fall through to the caller's continuation.
  Recursion + closure-call ops still disqualify. Compounds
  for every hot prelude helper — `__substr_eq`, `__map_hash`,
  the small char classifiers — that previously paid full
  call overhead per use.
- Each migration removes per-PR risk: the migrated helper is
  validated against the existing test suite (no behavior
  change), so the wat → lang transition is observable only
  in the wasm size + readability of the source, not in the
  user-visible behavior.

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
