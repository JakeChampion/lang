# Language direction

Where the language is going, what shapes we've decided on, and what
we've explicitly chosen NOT to copy. Living document — update as
each phase lands.

## Positioning

Fern is a **general-purpose** language. It grew up around two
short-lived workloads it's especially good at, and those remain the
places it's most polished — but they're no longer the boundary of
what it targets.

The two workloads it grew up around:

- **CLI tools** with fast startup (cold-start measured in
  milliseconds, not seconds).
- **Edge HTTP handlers** following the `wasi:http/proxy` shape —
  Fastly Compute, Netlify Edge Functions, Unikraft Cloud,
  `wasmtime serve`. One handler invocation per request, response
  sealed at end of scope.

Both share a memory profile: programs allocate freely, then the
whole address space tears down. That profile is what *originally*
justified leaning on a bump allocator (no free, no GC) and designing
slices/views to share a scope's lifetime instead of carrying
ownership.

**But that assumption no longer bounds the language.** The clearest
proof is the self-hosted compiler: a long-running, allocation-heavy
program that is neither a CLI one-shot nor a per-request handler.
Building it is exactly what forced Fern past the arena-and-forget
model into runtime reference counting with a Perceus-style elision
pass (see the "Roc's runtime refcounting" reversal below, and
`RC-PERCEUS-PLAN.md`). So Fern now carries **two memory models** —
scope-scoped RC for the general case, with the short-lived
edge/CLI shapes reclaimed by the same RC rather than a special
arena — and general-purpose, long-running programs are a first-class
target, not an off-label use.

**The reach extends below the OS too.** Fern targets hosted
applications, guest libraries linked into someone else's firmware,
device drivers, and kernels — the language being able to own the
machine, not only run on one. The anchoring long-term goal is an
entire operating system written from the ground up in Fern, with
the self-host compiler running on it as the finish line. Epic #6506
splits the OS-dependent runtime surface from the pure one;
`BARE-METAL-PLAN.md` is what follows it, and records why the
binding constraint is the memory model (an interrupt handler is a
second context racing non-atomic refcounts) rather than the missing
inline asm — and why per-process heaps make that constraint and an
OS's natural structure the *same* design.

Practical consequence for design work: when a feature would trade
general-purpose fitness for a narrow edge/CLI optimisation (leaking
because "the process exits soon", assuming single-threaded, assuming
no cycles), weigh that trade explicitly instead of assuming
short-lived-process semantics. The edge/CLI niche stays a design
*center of gravity* — the workloads we tune hardest for — not a
*ceiling*.

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
- **Roc's runtime refcounting.** ⚠️ **REVERSED — we now do exactly
  this.** The original call was: with a bump allocator freed on
  scope exit, refcounts are pure overhead; compile-time uniqueness
  analysis can mutate-in-place when the source is provably dead and
  fall back to copy otherwise, with no runtime check. That reasoning
  held only while every program was short-lived (a CLI invocation or
  one HTTP handler, where the arena tears down wholesale). It broke
  the moment we targeted a **long-running, allocation-heavy** program
  — the self-hosted compiler — where `arr = arr.push(x)` build-up
  loops are O(N²) in allocator traffic without in-place reuse
  (measured 7–60 GB on a self-compile, overflowing the bump heap).
  Pure compile-time uniqueness analysis turned out to be insufficient
  on its own at that scale, so we adopted Roc/Koka/Lean4's actual
  model: runtime RC headers + inc/dec at ownership transfers,
  copy-on-write mutation when `rc == 1`, and a Perceus-style static
  pass to *elide* the redundant inc/dec pairs (recovering most of the
  "no runtime check" win where it can prove redundancy). See
  `RC-PERCEUS-PLAN.md` (and `RC-STRINGS-PLAN.md` for string
  reclamation). This is the clearest case of the self-hosting goal
  reshaping a founding design decision: the arena-and-forget model
  remains right for the *stated* edge/CLI use case, but the compiler
  itself is not short-lived, and the two workloads want different
  memory models. We carry both — arena scopes (`arena { … }`, the
  per-handler arena) on top of RC — and lean on Perceus to keep RC
  cheap where the arena already guarantees the lifetime.
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
  (`i32` / `u32` on wasm32, `i64` / `u64` on arm64).
  **Update (#4408):** `isize` (zero uses) and `i8`/`i16`/`u16`
  (38 uses total, full per-stride backend cost) were retired —
  the surviving set is `i32`/`i64`/`u8`/`u32`/`u64`/`f32`/`f64`/`usize`.
  The status notes below describe the shipped-then-retired
  sub-i32 machinery as historical record; see
  `docs/BACKEND-PARITY.md` for the current opcode set.
- Default literal type stays `i32`. Literals are polymorphic in
  expected-type context: `let x: i64 = 1` works without a cast.
- Explicit conversion only (`x as i64`); no implicit widening.
- `f32` (existing) and `f64` (new); `float` becomes an alias —
  originally planned as f32, decided as **f64** (#5363,
  2026-07: f64 is the default and primary float).
- The historical `number` alias was dropped in PR 1's
  follow-up cleanup; use `i32` / `i64` / `u32` etc. directly.
- Update WASM codegen: i64 ops via `i64.*` instructions.
- arm64 codegen: i64 routes through 64-bit X-regs directly; no
  separate codepath needed.

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
  literal flow as integer literals. `float` is the alias for
  f64 (#5363; an unsettled literal also defaults to f64).

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
- **Function-parameter destructuring shipped.** `function f((a,
  b): (T, U))` binds the tuple elements positionally — in named
  functions (incl. methods), verbose lambdas, and arrow lambdas.
  Both parsers desugar the pattern into a synthetic
  `__ptuple_<line>_<col>` parameter of the annotated type plus a
  leading `let (a, b) = <synth>;`, so the checker / interp / IR
  reuse the statement-destructure path unchanged (a non-tuple
  annotation or an arity mismatch is the usual E024, reported at
  the parameter). A destructured param can't take a default value
  and needs a function body (not an `@import` signature).
- **Match-arm tuple patterns shipped.** `match (pair) { (0, y) =>
  …, (x, y) when x > y => …, (x, y) => … }` — in both the
  statement and expression forms. Each pattern element is a
  binder, `_`, or a literal (compared by equality; string / float
  elements use the same settled compares as literal matches);
  guards run with the arm's binders in scope. Arity is checked
  per arm (E035), element literals type-check against the
  scrutinee's element types (E035), and exhaustiveness requires
  an unguarded `_` or an unguarded all-binder arm (E030); an arm
  after an irrefutable arm is unreachable (E026). Or-patterns
  and nested patterns are not supported in tuple arms. The
  native compiler lowers them directly (checker + interp + a
  dedicated IR path mirroring the literal-match chain, with
  bindings borrowed from the tuple box like enum payload binds);
  the self-host parser desugars the whole match at parse time
  (build_tuple_match) into a destructure + flag-guarded if
  chain, so every self-host backend gets them for free. One
  self-host dispatch limit: the FIRST arm must be a tuple
  pattern (a guarded-`_`-first tuple match parses natively but
  not in the self-host compiler).
  With this, tuple destructuring covers all binding sites
  (#4406): statements, function parameters, and match arms.

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
  carries the instantiation for the monomorphiser — written
  at the construction site (`Box[i32] { val: 1 }`, which
  outranks any destination annotation) or inferred.
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
- ~~Generic constraints (`T: Eq`, `T: Hash`). Probably never —
  the Fern doesn't have a trait system.~~ **Reversed — shipped.**
  Fern now has a real trait system (`trait` / `impl Trait for
  Type`, nominal, statically dispatched via monomorphisation;
  see `docs/TRAITS.md`). Bounded generics `[T: Display + Eq]`
  are in (`FuncDecl.Bounds`), `core/cmp.fern` defines
  `Display` / `Eq` / `Ord`, and `@derive(Eq | Ord | Display)`
  synthesizes the methods. Explicit type args at call sites
  are still deferred (`E040`); inference covers the baseline.
  Users can still pass functions explicitly (Gleam's posture)
  where a trait would be overkill.

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
    `m.values()` return fresh `K[]` / `V[]` arrays containing
    the map's keys / values in insertion order. Both are
    snapshots — mutating the map afterwards doesn't affect
    the returned arrays. Wide V (i64 / u64 / f64) is handled
    by an IR-level intercept that follows the cell-pointer
    boxing and copies into a real wide-stride array. The
    non-allocating cursor iterator `m.iter()` ships separately —
    see "Cursor iteration shipped" further down for the
    `has_next` / `key` / `value` / `advance` API.
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
    cell pointer. `m.values()` is intercepted at the IR layer
    (`emitWideMapValues`) — for wide V the IR walks the
    entries, follows each cell pointer, and `__memcpy`s the
    8 payload bytes into a real wide-stride `i64[]` / `f64[]`
    result; narrow V falls through to the existing
    `__map_values_impl` Fern-prelude function.
    Trade-off: one extra alloc per insert + one extra
    indirection per read; acceptable under the bump
    allocator's per-arena reset and avoids a
    per-instantiation monomorph of the entire 1200-line
    map runtime. The longer-term direction is to migrate
    the map runtime itself to the Fern prelude (matching
    the json / url / parse_int trajectory) so the IR's
    Width-aware Store / Load picks the right ops without
    boxing — that requires a few prelude primitives the
    Fern doesn't yet expose (raw byte-buffer alloc, manual
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

> **⚠️ Superseded (2026-06-01): arenas were removed.** The
> `arena { … }` block, `arena_save`/`arena_restore`, the implicit
> per-handler arena, and the native two-cursor allocator described
> in this section have all been **deleted** — see
> `docs/ARENA-DECISION.md`. Per-request / per-scope memory is now
> reclaimed solely by **reference counting** (Perceus-style; see
> `docs/RC-PERCEUS-PLAN.md` and `docs/OWNERSHIP-INFERENCE-PLAN.md`).
> The text below is retained for historical context; everywhere it
> says "arena scope," read "the value's RC lifetime." The slice
> non-escape contract still holds, and its enforcement boundary is
> now RC ownership, not an arena reset. As of #2677 a **static escape
> check** (`E063`) backs the contract: returning a `[T]` slice that
> views function-local storage is now a checker error, in both the
> native compiler (`internal/checker` `checkSliceEscape`) and the
> self-hosted checker (`examples/self_host/checker.fern` `slc_walk`).

- **`arena { … }` block shipped.** Sugar for `arena_save() →
  body → arena_restore()` so the bump-allocator cursor snaps
  back when the block exits. Every allocation made inside
  the block is reclaimed at exit; nested blocks each get
  their own snap. Bindings declared inside an arena block
  are scoped to it (regular block-scoping, no special
  rules). Anything allocated inside the block must NOT
  escape — the language doesn't (yet) statically enforce
  this, so callers shouldn't return pointers to in-block
  allocations. **Implicit per-handler arena shipped.**
  `-target wasm32-wasi-http`'s `__http_entry` wrapper now opens an
  arena_save at the top and arena_restore right before
  exit, so every per-request allocation (the HttpRequest /
  HttpResponse structs, body strings, intermediate concat
  results, Map snapshots, etc.) gets reclaimed in one
  pointer-store at handler return. The host has already
  consumed the response body via outgoing-body writes by
  the time the restore runs — no live data references the
  freed region. CLI invocations don't need the wrapper:
  the process exits after `main`, so the OS reclaims
  everything for free.
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
- **Slice / view lifetime contract.** A `[T]` slice is a
  non-owning `{data_ptr, len}` pair into a parent owner —
  another slice, an `T[]` array, a `string`, or any heap
  buffer the bump allocator handed out. The slice **must
  not outlive its parent's arena scope.** Concretely:
  - A slice taken from a function-local array must not be
    **returned** from the function: the backing array's last
    owning reference drops when the frame unwinds, so the
    returned view dangles. This is now a **static checker
    error** (`E063`) — see "Slice escape check" below — rather
    than the discipline-only contract it used to be.
  - A slice taken from a function-local `string` / array
    must not be returned past the caller's arena unless
    the caller's arena is the same as (or wider than) the
    parent's. In practice, the request handler's outer
    arena owns everything allocated during the request, so
    slices flow freely within that scope. They become
    invalid once the handler returns and the per-request
    arena tears down. (String slices are a special case: `s[a:b]`
    copies into a fresh owned `string` via `__str_slice`, so a
    returned string slice never dangles and `E063` ignores them.)
  - The bump allocator's "everything alive at scope exit
    until the arena resets" model means the borrow-checker
    story is purely scope-based: track which arena owns the
    parent, and confine slice usage to that arena's
    lifetime.
- **Slice escape check (`E063`) — shipped (#2677).** The
  contract above is now enforced at check time, closing the
  "only real safety hole" the issue tracker flagged. A
  `return` whose value is a `[T]` slice that **provably views
  function-local storage** — a slice of an array literal, of a
  locally-declared owned array, or of a local slice binding
  that itself views local storage (chased through bindings and
  sub-slices) — is rejected. The analysis is deliberately
  conservative: anything it can't pin down to local storage is
  assumed safe, so there are **no false positives**. In
  particular it leaves alone (a) string slices, which copy into
  a fresh owned `string`; (b) slices of a parameter or receiver,
  whose backing array the caller owns and outlives the call; and
  (c) returning the owned array (`T[]`) itself, which is a move.
  The rule lives in both compilers — `checkSliceEscape` in
  `internal/checker/checker.go` and `slc_walk` /
  `slice_escape_diags` in `examples/self_host/checker.fern` —
  and the self-host port is held to byte-for-byte code parity
  with the native checker by the differential gate in
  `internal/e2e/self_host_checker_codes_test.go`. `fern explain
  E063` prints the full rationale.
- (The `number` deprecation alias has already been dropped
  — see PR 1's status block.)

## PR 6 — Examples-driven ergonomics

Writing the `examples/wasm/` showcase surfaced a clear friction
hierarchy. Each item below is a concrete change motivated by a
pain point hit while writing real programs against the current
language; ordered from biggest-leverage (touches every example)
to smallest. Status pending unless marked.

- **Heap-capture closures — shipped.** `closureconv` now accepts
  any pointer-shaped capture (string, `T[]`, `[T]`, structs,
  enums, tuples, function values) in addition to the scalar
  set (i32, i64, f32, f64, boolean). The env-block layout was
  already 4-byte-slot-per-capture, and pointer-shaped values
  are uniformly 4-byte heap references, so the change is
  largely a checker-gate relaxation:
  - `checker/checker.go` switch only rejects `VoidType` and
    `ParamType` now; everything else captures.
  - IR's `CaptureRef` lowering already emits `i32.load` for
    non-float types, which is correct for heap-ref types.
  - `exprType` + `fieldOwner` gained `CaptureRef` cases so
    `p.x` on a captured struct resolves the struct decl
    correctly.

  Lifetime is "captures must outlive the closure", same rule
  slices follow — enforced socially via the bump allocator's
  per-arena reset.

  `examples/wasm/word_freq.fern`'s `swap_pairs` is now an
  inline closure inside `sort_pairs` that captures `keys` and
  `counts` directly. `use_chain.fern` and `url_router.fern`
  unblock too (no longer scalar-only).

  **Wide captures (i64, u64, f64) — shipped.** The env-block
  layout switched from "every slot is 4 bytes" to per-stride
  (8 bytes for i64 / u64 / f64; 4 for everything else, with
  sub-i32 captures padded up to keep wide neighbours aligned-
  enough for wasm). The codegen drains captures into typed
  scratch locals (`$__cap_i32_<n>` / `$__cap_i64_<n>` / etc.)
  declared from per-type pools sized at function prelude — the
  prior i32-only pool would have failed wasm validation for any
  f32/f64/i64 capture. Compounds on the wat `__store_i64` /
  `__store_f64` shims that #218–#220 already added for the
  array-push family.

  Drive-by fix: `closureconv.rewriteExpr` was missing
  `*ast.CastExpr` and `*ast.SliceExpr` cases — `(a as i64)`
  inside a closure body left `a` as a raw `Ident` instead of
  rewriting to `CaptureRef`. Surfaced once mixed-width
  captures landed (the failure was reproducible without the
  wide-slot work too — pre-existing latent bug).

- **`?` operator for `Option` / `Result`.** `extract_text`
  in `todo_api.fern` is 22 lines of nested match for
  "match `JObject(m)`; pull `text`; match `JString(t)`; else
  `None`". With `?` it collapses to:

  ```
  function extract_text(v: JsonValue): Option[string] {
    var JObject(m) = v else return None;
    var Some(text_v) = m.get("text") else return None;
    var JString(t) = text_v else return None;
    return Some(t);
  }
  ```

  or, with the postfix form (sugar over `let-else { return
  None; }` when the surrounding return type unifies):

  ```
  function extract_text(v: JsonValue): Option[string] {
    var JObject(m) = v?;
    var JString(t) = m.get("text")??;
    return Some(t);
  }
  ```

  The `?` form requires the surrounding return type to be
  `Option[_]` / `Result[_, E]` matching the source. Same
  lowering as Rust's `?` — early-return on the failure
  variant, bind on success. Pure parse-time + checker
  desugar; no codegen change.

  **Shipped (Option + Result).** `?` is a postfix operator on
  any `Option[T]` or `Result[T, E]` source. The parser attaches
  it as part of the `parseCall` postfix loop (alongside `.field`,
  `[i]`, `(args)`) so it binds tighter than every binary
  operator — `m.get(k)? + 1` parses as `(m.get(k)?) + 1`.

  Checker rules:
  - `Option[T]?` requires the enclosing function to return
    `Option[_]`; the construct yields `T`.
  - `Result[T, E]?` requires the enclosing function to return
    `Result[_, E]` (the `E` must match exactly — no implicit
    `From` conversion). The construct yields `T`.
  The checker stamps a `TryKind` on the AST node so the IR knows
  which lowering to use.

  IR lowering:
  - Option `None`: build a fresh `None` of the function's return
    type and `return` it. None has no payload so the allocation
    is a single tag word.
  - Result `Err`: forward the SOURCE pointer through the
    enclosing return as-is. The source already carries `tag=1`
    and the `E` payload at `+4`, and the checker has verified
    `E` matches the enclosing return's `E`, so the same heap
    object satisfies both types — no reallocation.
  Both share the success-path payload load at `ptr+4`, with
  width chosen from the checker-stamped success type.

  To free `?` from the ternary, the ternary `cond ? then : else`
  was replaced with a one-line `if (cond) { then } else { else }`
  expression form (see the `if` expression note below).

- **`if (cond) { e1 } else { e2 }` as an expression — shipped.**
  Each arm holds a single expression (no statements, no
  semicolons), and the whole construct evaluates to the unified
  arm type. Lowers identically to the (now-removed) ternary —
  same typed `if/else` block in the IR, same `if (result T)` at
  the wasm level, same conditional-branch lowering on arm64. The
  statement form `if (cond) { stmts; } [else { stmts; }]` is
  unchanged; expression and statement positions are
  syntactically distinct because parseStmt dispatches `if`
  before expression parsing ever sees it.

- **`match` as an expression — shipped.** Concrete shape:

  ```
  var m = match (o) {
      Some(x) => x + 1,
      None    => 0
  };
  ```

  Mirrors the `if`-expression treatment: a separate AST node
  (`MatchExpr`) parsed from `parsePrimary`'s keyword switch, so
  the parser knows it's in expression context. Each arm body is
  a single expression — no statement block, no semicolon —
  matching the `if`-expression scope of "no surprise
  block-as-expression machinery yet". The checker reuses the
  statement-form's variant / payload / guard / exhaustiveness
  rules and adds an arm-type unification step on top.

  IR lowering: scratch slot keyed off the unified arm type,
  populated by whichever arm matched, loaded after the outer
  block. (A typed `block (result T)` would need `unreachable`
  on the fallthrough path to satisfy wasm's stack discipline;
  the slot is simpler and exhaustiveness already guarantees
  the slot is written exactly once.)

  Arm bodies that need to early-return / break / continue (the
  `_ => return None` shape from the original example) still
  need the statement-form match for now. Block-as-expression is
  a separate, larger language change.

- **String interpolation — shipped.** `f"{req.method}
  {req.path} ({len(req.body)} bytes)\n\n{req.body}"`
  desugars at parse time to the same `+` chain, with
  implicit `.to_string()` on non-string interpolants. The
  lexer scans f-strings inline (tracking brace depth so
  `f"{ {1,2} }"` parses), splits on `{...}` boundaries, and
  emits a sequence of literal-string tokens + parsed
  expression sub-trees the parser stitches back together.
  `echo_handler.fern`, `shape_area.fern`, and `wc.fern` use
  the form.

- **`for x in arr` / `for (k, v) in map` — shipped.** Both
  shapes desugar in the parser. Map form rewrites to
  `iter() / has_next() / value() / advance()`; array form
  to a C-style `for (var i = 0; i < len(arr); i = i + 1)`
  loop with `var x = arr[i]` at the top of the body.
  Detection happens by lookahead in `parseFor` — a leading
  `IDENT in` or `( IDENT , IDENT ) in` selects the foreach
  shape; everything else falls through to the C-style for.
  `word_freq.fern`, `json_pretty.fern`, `csv_to_json.fern`
  all use the form.

- **Drop `__array_append_T` from user surface — shipped.**
  `arr.push(v)` is a generic method on `T[]`. The checker
  treats `Array` like a one-type-param generic struct:
  receiver-Args flow into the `__method_<Type>_<name>`
  substitution path that Map's methods already use, so
  `string[].push(v)` checks `v` as string while
  `JsonValue[].push(v)` checks it as JsonValue.

  Lowering happens inline at the IR layer (`emitArrayPush`
  in `internal/ir/ir.go`) — one block of code emits the
  alloc + memcpy + width-correct tail store for every stride
  class (1 / 2 / 4 / 8 bytes; integer + float). Earlier
  shape used 5 nearly-identical Fern-prelude functions
  (`__array_append_string` / `_i64` / `_f64` / `_u8` /
  `_u16`), 5 mangled FuncSigs, 5 codegen aliases, 5
  treeshake aliases, and a per-stride dispatch switch in the
  checker — all collapsed into a single inline IR block.
  Adding the next `T[]` method (`concat`, `reverse`, ...)
  drops in as one IR helper instead of duplicating the
  whole stack per stride.

  All four examples (`word_freq.fern`, `csv_to_json.fern`,
  `url_router.fern`, `todo_api.fern`) and the prelude itself
  use `.push(v)` instead of the per-T helpers.

- **Module-level `state { ... }` block — REMOVED.** Fern briefly
  shipped a `state { var hits: i32 = 0; ... }` construct for
  module-global mutable variables that persisted across
  `handle()` calls (process-lifetime state for long-running
  HTTP servers). It was backed by a **two-cursor allocator** — a
  persistent heap region selected by an `__fern_alloc_mode` flag,
  with `OpPersistentSet`/`OpPersistentRestore` toggling the mode
  around state-rooted call sites so Map/T[] mutations survived
  the per-request `arena_restore`.

  The whole feature has since been removed: the `state` syntax,
  the AST node, the checker state-var table, the IR persistent-
  mode ops, and (once the arena reset was also dropped) the
  two-cursor allocator itself — both native backends now use a
  single bump cursor reclaimed by reference counting. There is
  currently **no language-level mechanism for process-lifetime
  state**; if reintroduced it would want a cycle collector
  alongside it (a `state`-rooted cycle leaks unboundedly — see
  docs/CYCLE-COLLECTION-ANALYSIS.md).
- **Numeric literal suffixes — shipped.** `42i64`, `7u8`,
  `0f32`, `1.5f64`, `42f64` (integer text + float suffix
  promotes to a float literal) all parse as the suffixed type
  directly. The lexer captures the suffix on the Number/Float
  token; the parser stamps Width + IsUnsigned at parse time so
  the literal carries a concrete type immediately, bypassing
  the polymorphic-flow machinery. The formatter preserves the
  suffix on round-trip — Width is the user-authored signal at
  format time (the format pass runs pre-checker, so settle-flow
  hasn't stamped Width yet).

  `Circle(r: f32) when r <= 0f32 =>` is now noise-free.

- **Polymorphic-int-literal promotion to float — shipped.**
  Building on the suffix work above, an unsuffixed `0` against a
  float partner now settles to that float type instead of
  erroring. Concretely, `r <= 0` works when `r: f32` — the
  literal `0` lowers as `f32.const 0.0`, not `i32.const 0`.
  Same path applies to `var r: f32 = 0`, `f(0)` where `f` takes
  `f32`, and `r * 2` arithmetic.

  Implementation: `NumberLit` gained `IsFloat` + `FloatWidth`
  fields. `settleFloat` stamps them on a polymorphic literal in
  float context; `checkExpr(NumberLit)` returns `FloatType` when
  IsFloat is set; the IR's NumberLit lowering picks
  `OpConstF32` / `OpConstF64` with `float32(Value)` /
  `float64(Value)` instead of the integer-const path. The
  binary-op handler also pre-settles a polymorphic side against
  a concrete-float partner before requireFloat fires.

  Concrete-int **variables** (e.g. `var x: i32; x + 1.5f32`)
  still error — no implicit widening. Only literals get the
  promotion.

- ~~**Bug: formatter eats `defer r.close();`**~~ — **fixed.**
  The statement-printer switch has a `case *ast.Defer` arm
  now (`internal/printer/format.go`); round-trip is covered
  by `TestFormatDeferRoundTrip`. The earlier comment scars
  across `wc.fern` / `word_freq.fern` should be cleared in a
  follow-up examples cleanup pass.

### Shipping order

Roughly biggest-leverage-first, but small wins (formatter
bug, literal suffixes) interleave when they unblock specific
example cleanup. Each item lands as its own PR:

1. ~~defer-on-method-call formatter fix~~ — **shipped.**
   `internal/printer/format_test.go:TestFormatDeferRoundTrip`
   covers `defer r.close()` survival.
2. ~~String interpolation~~ — **shipped.** `f"..."` syntax
   live in the lexer; used by `echo_handler.fern`,
   `shape_area.fern`, `wc.fern`.
3. ~~`_` wildcard in match~~ — **shipped.** Both statement
   and expression form;
   `TestWASMMatchExprWildcardArm` covers it.
4. ~~`?` operator + `var Pat = expr else { ... };` form~~ —
   **shipped (Option + Result).** Postfix `?` on the
   `parseCall` postfix loop; `let-else` lives in `parseStmt`.
5. ~~`for x in arr` / `for (k, v) in map`~~ — **shipped.**
   Both shapes desugar in the parser; covered by
   `TestWASMForEachOverArray` /
   `TestWASMForEachBreakContinue`.
6. ~~`arr.push(x)` generic dispatch~~ — **shipped.**
   Lowering folded into the IR (`emitArrayPush`) on the
   refactor PR; one block of code per stride class.
7. ~~Numeric literal suffixes + match-arm inference~~ —
   **shipped.** `42i64`, `7u8`, `1.5f64` etc. parse direct.
8. ~~Heap-capture closures~~ — **shipped.** Pointer-shaped
   captures + wide (i64 / u64 / f64) captures both live;
   `closureconv.rewriteExpr` CastExpr / SliceExpr cases
   landed alongside.
9. ~~Module-level `state { ... }` block~~ — **shipped, then
   REMOVED.** See the "Module-level `state { ... }` block —
   REMOVED" note above; the feature and its two-cursor
   allocator are gone.

Each landed item gets a follow-up commit that simplifies
one or more `examples/wasm/` programs, demonstrating the
ergonomic win in real code rather than just synthetic
tests.

## Deferred — not in any of the above five PRs

- **Closures via lambda-set defunctionalisation —
  monomorphic flow shipped.** The doc-roadmap's biggest
  deferred item lands incrementally. Roc's full
  lambda-set treatment (per-call-site tagged-union
  dispatch over the finite set of lambdas that flow into
  each function-typed slot) is still ahead, but the
  monomorphic-flow case — exactly one MakeClosure flow
  source per slot, by far the dominant pattern in
  closureconv'd output for handler / nested-function code
  — now defunctionalises at IR time, including the
  cross-function closure-factory pattern (`var f =
  makeAdder(7); f(35)`). Two flavours of monomorphic flow
  source recognised:
    1. Direct: `OpStoreLocal slot` directly preceded by
       `OpMakeClosure target=T`.
    2. Factory return: `OpStoreLocal slot` directly preceded
       by `OpCallDirect F`, where the phase-0
       `analyseReturnTargets` pre-pass found F always
       returns the same closure target T. The phase-0
       analysis walks each function's OpReturn ops and
       tracks the value-source kind: an `OpMakeClosure T`
       or an `OpLoadLocal slot` of a locally-monomorphic
       slot. Multi-target functions disqualify themselves.
  Both flavours rewrite to `OpLoadLocal slot;
  OpCallClosureDirect target` after env-load synthesis.
  Saves the function-table indirection + the wasm
  `call_indirect` runtime type check on every closure
  invocation that fits the pattern. Future follow-up: the
  multi-flow case (tagged-union dispatch over 2..N
  lambdas) for sites where multiple distinct closures can
  flow into the same slot.
- **Compile-time uniqueness analysis** for in-place mutation
  (Morphic-style). Real performance win for programs that build
  up + transform big structures. Needs the IR to track ownership
  paths; substantial work.
- **Iterators / lazy sequences.** Eager combinators + pipe handle
  most cases; lazy iterators want IR-level fusion to not be a perf
  trap.
- **Records (anonymous structural types).** Interesting for
  ergonomics — `({ name: "x", age: 5 })` without a struct decl —
  but not load-bearing yet. Generics shipped (PR 3), so this
  is no longer blocked on type-system work — pending now on
  someone hitting the friction in a real example.
- **Error effects in function signatures** (MoonBit's `raise`).
  `use` shipped + postfix `?` is in (see "use" + "Postfix `?`"
  notes in PR 6). Combined they cover the ergonomic case
  effect-rows would address. Effect rows would still be
  cleaner type-wise (no `Result[T, E]` wrapping noise on the
  caller), but the practical gap is small. Deprioritised.
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
    (`-2^31..=2^31-1`) is exact. **Migrated to the Fern
    prelude** in PR 174 — was ~190 lines of hand-written
    wat, now ~25 lines of Fern code in
    `internal/prelude/prelude.fern`.
  - **`s.parse_float()` shipped.** `string` method returning
    `Option[f64]` (originally f32; flipped with the #5363
    f64-default decision). Grammar: `[-]<digits>[.<digits>]
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
    text formatting on the float types. Trailing zeros are
    trimmed and the decimal point is dropped if the fraction is
    zero. Special values get canonical names: `NaN`, `Inf`,
    `-Inf`. Originally a fixed-digit renderer (7 fractional
    digits for f32 / 15 for f64), explicitly NOT bit-exact
    Steele/White / Ryu; it is now **shortest round-trip via
    Dragonbox** — the fewest digits that parse back to exactly
    the same float, correctly rounded, agreeing with Go's
    `strconv` digit for digit. `parse_float` is the side that
    still recovers only to within a tolerance (it is not
    correctly rounded), so a `parse_float(x.to_string())`
    round-trip still loses trailing precision on pathological
    inputs — see docs/FLOAT-SEMANTICS.md.
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

1. **Runtime core (hand-written wat).** `__fern_alloc`,
   `__str_eq`, `__str_slice`, `__slice_idx_*`, the Map's hash
   mix + open-addressing core, `arena_save` / `arena_restore`,
   the int formatter, and the wasi imports. Hand-written wat
   in `internal/codegen/wasm/wasm.go`. The IR sees only the
   call sites (`OpCallDirect "name"`); helper bodies bypass
   the IR entirely.

2. **Fern prelude (IR-routed).** `internal/prelude/prelude.fern`
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
loads/stores, hash mixing) aren't expressible at the Fern
level today.

**Performance comparison** (rough, based on the existing
helpers):

| Aspect                          | Hand-written wat   | IR-routed Fern    |
|---------------------------------|--------------------|-------------------|
| Tight loops (memcpy, scan, hash) | optimal           | within 10%, maybe |
| Tiny helpers (`s.is_empty()`)    | direct one-op     | call overhead     |
| Recursive walkers (json encoder) | one alloc per buf | match-arm + heap  |
| IR optimisations (peephole, dce) | invisible         | applied           |
| Cross-target (wasm + arm64)      | duplicate per backend | one source    |
| Maintainability                  | verbose, fiddly   | concise           |

The wins from IR-routing are **maintainability + cross-target
portability**, not raw performance. For most helpers the
generated code is comparable; for hot paths (the Map runtime,
hash mixing, the bump allocator) hand-written wat is meaningfully
faster because we control the exact ops without going through
the Fern's abstraction layers.

**Migration path** (split into per-helper PRs):

1. ✅ **Phase A: prelude infrastructure + first migration.**
   `internal/prelude/prelude.go` embeds the `prelude.fern`
   source via `//go:embed`; `injectPrelude` parses it on
   each `checker.Check` call and appends the decls to
   `prog.Funcs` (with `IsPrelude=true` for filtering in
   tests / dump tools). Method-receiver type-checking
   extended to accept `string`, `i8/i16/i32/i64/u8/u16/u32/u64`,
   and `f32/f64` receivers — built-in scalar receivers go
   through the same hoisting + dispatch as struct / enum
   methods. First migration: `s.is_empty()` — was 11 lines
   of hand-written wat, now 3 lines of Fern.
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
   tiny `__fern_alloc` + length-prefix sealer. Signatures
   registered in the checker so the prelude can call them
   like any builtin. Drives buffer-management code that
   doesn't yet have a clean Fern-level shape — the
   remaining wat string methods (`to_lower`, `to_upper`,
   `bytes`) migrated using these primitives, then the Map
   runtime followed (~1184 wat lines → ~280 Fern lines).
   The map's `__method_Map_*` calls keep their type-rich
   FuncSigs registrations (the language doesn't yet have
   generic methods on a generic struct), with a codegen
   alias that rewrites each call to its concrete `_impl`
   counterpart in the prelude — same pattern as the
   `__array_append_jsonvalue → __array_append_string`
   alias. Native backends provide `__memcpy` / `__memset` /
   `__alloc` directly (arm64 inlines them as small leaf
   functions; wasm wraps `memory.copy` / `memory.fill`).

   Drive-by escape hatch: `[u8]` slice and `u8[]` owned
   array now cast to `i32` to recover the data pointer.
   Slice cast loads the i32 at slice+0 (the data_ptr field
   of the `{data_ptr, len}` slice header); array cast is a
   no-op since an owned-array value already IS the data
   pointer (length lives at data-4). Together with the
   bulk-memory shims this is enough to express growable-
   buffer scratch code in Fern without dropping into wat.

**Why not migrate everything at once:**

- Fern is missing some primitives the prelude would want
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
  change), so the wat → Fern transition is observable only
  in the wasm size + readability of the source, not in the
  user-visible behavior.

## Open questions to settle as we go

- **~~Slice syntax~~ — settled `T[]` owned + `[T]` view.** The
  visual-distinction option won. `T[]` declares an owned array
  (heap-allocated, length prefix); `[T]` is a non-owning slice
  view of `{data_ptr, len}` into a parent owner. Methods like
  `.push` are owned-only; slicing (`a[i:j]`) yields a `[T]`
  view. Source-level disambiguation kept the parser simple and
  matches what the codegen lays out.

- **~~Default int~~ — settled `i32`.** Wasm-native and existing
  programs assume it. `i64`, `u64`, `u8`, `usize`, `f32`, `f64`
  are all available explicitly (`i16`/`u16`/`i8`/`isize` were
  retired in #4408).

- **~~Map literal syntax~~ — settled `Map { k: v, k: v }`.** Same
  shape as struct literal syntax. Heterogeneous-typed literals
  monomorphise through the auto-injected `Map[K, V]` decl.

- **~~`Bytes` vs `[u8]`~~ — settled no nominal type.** `u8[]` is
  the bytes type: owned byte arrays for the codec / crypto /
  `random_bytes` boundary, and `[u8]` for a borrowed view. No
  standalone `Bytes` newtype. (`base64_encode` / `hex_encode`
  and friends originally took a `string` used as a raw byte bag;
  #5730 moved them to `u8[]` so a `string` can carry the D9
  invariant that it is well-formed UTF-8.)

- **~~Closure capture semantics~~ — settled by-value at closure-
  creation time.** The closureconv pass evaluates each
  captured name once at the `MakeClosure` site and stores
  the value in a freshly-allocated env block; the hoisted
  function reads it via env-relative loads. For pointer-
  shaped types (`string`, arrays, slices, structs, enums,
  closures themselves) the value is the heap / wrapper
  pointer at capture time — so mutations *through* the
  pointer are visible to the closure (the closure shares
  the same underlying buffer) but reassigning the outer
  variable to a new pointer is not. For scalar types
  (`i32`, `u32`, `i64`, `f32`, `f64`, `boolean`) the value
  itself is copied; later mutations of the outer scalar
  don't reach the closure. Escape analysis (statically
  verifying the captured pointer's parent arena outlives
  the closure) is deferred — the current model leans on
  the bump allocator: closures stay valid while the arena
  that owns their captures is alive, which in practice
  means "until the request handler returns". Settle the
  formal escape rules when re-lowering for
  defunctionalisation; until then the rule is "captures
  must outlive the closure".

All five questions in this section are now resolved by what
shipped. New open questions should be appended as they
arise — the resolved ones are kept (rather than deleted) so
the trail of decisions is recoverable.

## Outside influences we mined

Periodic survey of design ideas from outside the language-
research bubble. For each, what we take, what we leave, and
why — so a future agent can re-derive the call instead of
guessing.

### TigerBeetle's TigerStyle

Source: https://github.com/tigerbeetle/tigerbeetle/blob/main/docs/TIGER_STYLE.md

TigerStyle is a tight engineering culture — NASA Power of
Ten, zero-tech-debt, statically-allocated, assertion-rich.
Not all of it transplants to a tree-walking Go compiler, but
the heart of it (proactive design, limits-on-everything,
named-with-meaning) maps cleanly onto the Fern's own
runtime + the prelude. We're already accidentally Tiger-
flavoured in several places — codifying the matches makes
future contributors arrive at the same shape without
guessing.

**Already aligned (no action — call out so we don't drift):**

- *Limits on everything.* `http_parse_request` caps at 8 KiB
  headers + 1 MiB body and returns `None` past either;
  `__map_pow2_ceil` saturates at 2^30 instead of looping
  forever; `__fern_read_line` is gated on a 4 KiB .bss
  buffer. All bounded loops are bounded explicitly.
- *Zero dependencies.* The compiler runs on Go stdlib only;
  no third-party deps in `go.mod`. Wasm helpers ship in the
  prelude rather than vendoring runtime libraries.
- *All errors handled.* Go's `if err != nil` discipline is
  enforced by `go vet`; checker errors carry source
  positions; runtime traps abort the process loudly.
- *Always motivate.* CLAUDE.md's "Engineering bar" already
  reads as a TigerStyle paragraph (zero regressions, every
  feature tests, fix bugs you find on the way).
- *Don't react to external events.* The handler model runs
  one invocation per request — the program drives, the host
  schedules. Lines up with TigerStyle's "your program
  should run at its own pace" — and is the exact reason
  edge-function targets are the long-term goal.

**Adopting:**

- *Limits-on-everything pushed into user Fern.* Add an
  `assert(cond, "msg")` builtin that traps with a source-
  positioned message in debug builds (and elides under
  `-O`). Encourages handler authors to assert preconditions
  the way the prelude already does — and gives us a natural
  place to hook future fuzz-testing.
- *Two assertions per function (target, not hard rule).*
  For codegen / IR Go code, where state-shape correctness
  matters most. Linter rule eventually; for now, soft
  target in code review.
- *Variable-naming with descending significance.* New IR
  fields and runtime locals use `latency_ms_max` shape
  (most-significant → least-significant suffix). Existing
  Go names stay; new ones follow the rule. Aligns sibling
  variables in source — `latency_ms_min` + `latency_ms_max`
  read symmetrically.
- *`source` / `target` over `src` / `dest`.* Same word
  length keeps `source_offset` + `target_offset` lined up
  in calculations. Currently mixed in the codegen — clean
  up opportunistically.
- *Centralise control flow.* "Push `if`s up and `for`s
  down." For the IR builder this means keeping the per-
  ast-node `case` switch flat in `expr()` / `stmt()` and
  pushing the imperative emit-this-then-that sequences
  down into small helpers (already partially done with
  `emitArrayPush`, `emitWideMapValues`, `structFieldLayout`
  → keep going).
- *Negative-space testing.* For every new feature, also
  test that the malformed / invalid case is rejected with
  a clean diagnostic, not a crash. Already informal — make
  it a checklist item in the PR template.

**Considered, left:**

- *Function-length hard cap (70 lines).* Some of the IR
  lowering cases legitimately need 80–120 lines (e.g.
  `emitWideMapValues`, the match-arm dispatcher). Forcing
  splits there fragments a single coherent algorithm into
  named-but-coupled helpers. Keep 70 as a soft target for
  *new* helpers; let existing well-factored long
  functions stay.
- *Zero dynamic allocation after init.* The compiler is a
  process that runs once per CLI invocation — Go's GC
  handles its memory, and dynamic allocation of AST nodes
  is correct. The *language* enforces static-after-init via
  the per-request arena; the *compiler* doesn't need to.
- *Static allocation in the language runtime.* Conflicts
  with the per-request arena model — handler code allocates
  freely, the arena resets at request end. Cheaper and
  simpler than reserving fixed-size buffers up-front.
- *No recursion.* Fern user code is fine with recursion
  (matches every modern language). The IR layer avoids
  recursion in code paths that need bounded execution (the
  tail-call optimiser exists for that), but the AST walk
  itself is naturally recursive — and shallow enough
  (handler bodies, not arbitrary user data) that the
  recursion bound is implicit in source size.

### Algebraic effects (Kyo, Koka, Eff)

Sources:
- https://getkyo.io/  
- https://github.com/getkyo/kyo (`README.md`)
- prior survey: Roc's `effects`, Koka's effect rows,
  OCaml's effect handlers.

Kyo's headline idea: every computation has type `A < S`
where `S` is a *type-level set of pending effects*. Pure
values widen to `T < Any` automatically — no
`pure`/`return` ceremony. Effects compose by intersection
(`Int < (Sync & Abort[E])`), and *handlers* discharge them
one at a time, transforming the row. The direct-style
macro lets users write `val s = Sync.defer("hello").now`
instead of nested `.map`s — looks imperative, compiles to
monadic composition.

**Why we're looking at it now.** Edge-function handlers are
exactly the workload that wants explicit effect tracking:
which handlers do IO, which can fail, which need
persistent state, which capture per-request scope.
Right now we encode this with conventions and ad-hoc
return types (`Result[T, E]`, `Option[T]`, naked types).
A row of effects could replace several of those, make
intent visible at signatures, and unlock checker rules
like "no IO from `state{}` init expressions" or "a
pure function can't suspend."

**What translates well:**

- *Pending effects as a row.* Already overlap with what
  Roc and Koka do. For Fern, a return type like
  `function handle(req): HttpResponse <io, throws[Bad
  Request]>` reads cleanly and the checker can verify
  call-site effect closure. We already track `void` vs
  result types; this is the same idea at finer
  granularity.
- *Auto-widening pure → effectful.* No need for explicit
  `pure(x)` wrapping. Already the Fern's posture —
  `i32` values flow through `Option`/`Result` without
  ceremony, and an effect row should follow the same
  rule: any `T` is `T <>` (empty row), widens up to any
  superset.
- *Direct style by default.* This is what we already
  have — `var s = http_get(url);` doesn't require `.now`
  / `.map`. Effect tracking should ride on top of the
  existing imperative-looking syntax, not introduce a
  separate `direct { }` block. Gleam's `use` rewrite +
  Kyo's auto-widen give us the right semantic shape
  without a parallel surface syntax.
- *Effects as documented capability, not category-theory.*
  Kyo explicitly avoids "cryptic operators and unnecessary
  category theory." Same posture — the user-facing story
  is "this function may suspend / throw / mutate state,"
  not "this function returns `IO (Either E A)`."

**What we'd change vs Kyo:**

- *Closed effect set, not user-defined.* Kyo's "open
  set" framing is overstated in their own docs — they
  ship a fixed `Sync`, `Async`, `Abort`, `Env`, `Var`,
  `Emit`, `Choice`, `Memo`. We'd do the same: a small
  built-in vocabulary (`io`, `throws[E]`, `suspend`,
  `state`) the checker recognises. Open extensibility
  is research-grade and not justified by the edge-
  function use case.
- *No effect handlers in user code.* Handlers are how
  Kyo discharges effects (`Abort.run(comp)`,
  `Env.run(value)(comp)`). For our targets the
  discharger is the *runtime* — `tcp_serve` discharges
  `<io>`, `arena_save`/`arena_restore` discharges
  `<state>`, the `?` operator discharges `<throws>`.
  User code consumes effects; only the runtime / the
  prelude installs handlers.
- *No macros.* Kyo leans on Scala 3 macros for direct
  style. Our parser already accepts imperative syntax;
  desugar happens at the AST → IR boundary
  (`closureconv`, the `?` operator, `use`-style CPS).
  Effect tracking is a checker-side annotation pass,
  not a syntactic transformation.

**Sketch — what an effect-annotated signature would
look like, if/when this lands:**

```
// Pure helper — no row.
function double(n: i32): i32 {
    return n * 2;
}

// Reads env vars, may suspend on syscall.
function port(): i32 <io, suspend> {
    return __port_from_env("PORT", 8080);
}

// Handler — may throw BadRequest, may IO.
function handle(req: HttpRequest): HttpResponse <io, throws[BadRequest]> {
    if (req.method != "POST") {
        throw BadRequest("method not allowed");
    }
    var body = read_body(req);  // <io, suspend> bubbles up
    return HttpResponse { status: 200, body: body };
}
```

The checker checks the effect closure: `port()` calls
`__port_from_env` which is `<io, suspend>`, so `port`'s
declared row must include both. `handle` calling `port()`
needs `<io, suspend>` in its row — and indeed it
declares `<io, throws[BadRequest]>`; the `suspend`
isn't there, so the checker rejects.

**Not committing to ship.** This is an open design
question — adding effect rows is a real surface-syntax
change, and the value depends on how many bugs the
checker catches that we wouldn't have caught otherwise.
Worth a prototype branch when we have a sufficiently
large body of handler code to measure against.

### When to revisit

When we ship native HTTP servers on arm64 (this PR /
follow-ups), there'll be real handler code in the test
suite that exercises `<io>` / `<throws>` / `<state>`
patterns. That's the right moment to prototype the
effect-row checker rules and see if they catch
anything real.

### Concurrency: structured fan-out over a stackless task runtime

Sources:
- `docs/ASYNC-IMPLEMENTATION-RESEARCH.md` (Koka / Lean 4 / Roc /
  AOT+WASM mechanics)
- `docs/CONCURRENCY-RESEARCH.md` (the menu + the chosen surface)
- `docs/ASYNC-IMPLEMENTATION-PLAN.md` (the phased build)

The standing decision (Rec §10 of `CONCURRENCY-RESEARCH.md` asks
this be recorded here once the surface lands — it has):

**The decision.** Fern gets **colorless structured concurrency**:
a `concurrent { … }` block fans out `spawn`ed tasks whose I/O
overlaps on one thread, joined with `await` inside the block's
scope. It is implemented as a **stackless CPS / state-machine**
shape (`std/task`: a task is uniformly a `Step = Done(i32) |
Wait(token, resume)`, the continuation capturing its live frame),
driven by a **single-threaded readiness reactor** that lives in
the platform/stdlib layer (the "host owns the loop" insight from
Roc, kept in-binary). No function coloring — suspension is a
property of the block, not the function signature.

```fern
concurrent {
    var a = spawn fetch(plat, url_a);
    var b = spawn fetch(plat, url_b);
    return combine(await a, await b);
}
```

**What we took.**
- *Stackless continuations* (Rust/C#/old-Zig/Koka-default): a pure
  IR/source transform — one shape that compiles unchanged on
  x86-64, arm64, and wasm32 (verified). No per-arch assembly.
- *Structured concurrency* (Trio nurseries / Swift task groups /
  Kotlin scopes): tasks are confined to the block; results don't
  leak; `select` cancels losers by simply not resuming them (RC
  reclaims the dropped continuation — one-shot, the cheap case).
- *Host-owned event loop* (Roc): the reactor is platform glue over
  one `poll` primitive, not baked into codegen.

**What we left.**
- *Stackful green threads* (Go goroutines): mandatory always-linked
  scheduler+GC, ~1 MB binary floor, per-ABI stack-switch assembly,
  no WASM story. Cold-start hostile — wrong for edge handlers.
- *Function coloring* (Rust/JS/Python async): the red/blue split
  infects the stdlib. We take Rust's *mechanism* (state machines)
  without the colored *ergonomics*.
- *A general algebraic-effect system as the way in* — see the
  effects entry above. Effects are the eventual colorless
  substrate (Koka/OCaml 5 prove direct-style async compiles AOT on
  the same Perceus RC), but they're heavier than the cheapest
  first step; concurrency arrives as a concrete surface first,
  effects subsume it later.
- *Multi-core parallelism in the language surface.* Edge handlers
  need concurrency (overlap I/O waits), not parallelism. Within a
  handler the model is single-threaded interleaving — two tasks
  share locals freely because only their suspension points
  interleave. Parallelism (if ever) is a host-scheduler concern.

**What's shipped vs pending.** The pure-Fern runtime (fan-out,
multi-await, `select`/cancellation) and the `concurrent`/`spawn`/
`await` surface are live on the **Go compiler** (interp / x86-64 /
arm64; wasm compiles). Pending: the suspending-`await` CPS
transform (drops today's `(Reactor) -> (Step, Reactor)` spawn-target
protocol leak), the real syscall reactor (`poll(2)`/`epoll`/
`wasi:io/poll` — makes I/O real), `plat.fetch` as the first real
awaitable, and the self-hosted-compiler path (blocked on the
fn-typed-enum-payload codegen gap, `docs/SELF-HOST-FN-PAYLOAD-VARIANT-GAP.md`).

**When to revisit.** (a) When a real handler needs two parallel
fetches doing *real* network I/O — that's the trigger for the
syscall reactor + `plat.fetch`. (b) When suspending `await` in
arbitrary control flow (loops, conditionals) is wanted — the full
body-splitting CPS transform. (c) When a single handler invocation
legitimately benefits from multi-core CPU parallelism (likely never
for edge handlers; possibly for CLI data-processing) — revisit the
single-threaded-within-a-handler constraint. (d) When the effect-row
work (above) matures — fold concurrency into it as one effect.
