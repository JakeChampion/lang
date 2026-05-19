# Performance research — what makes other languages fast

Survey of performance techniques across both well-known systems
languages (Zig, Rust, OCaml) and less-mined sources (BQN/APL,
Forth, Factor, LuaJIT, SBCL, Vale, WUFFS). The framing is
deliberately compiler-and-runtime, not language-shape — there's a
separate `LANGUAGE-DIRECTION.md ▸ Outside influences we mined`
section that covers surface-syntax + type-system inspirations.

Treat this document as input to the IR / codegen roadmap.
Recommendations at the end map each idea to a concrete file under
`internal/ir/` or `internal/codegen/*` and rank them by leverage
× implementation cost.

## Framing — what "fast" means here

Two stated use cases (`LANGUAGE-DIRECTION.md ▸ Positioning`):

- CLI tools with millisecond cold-start.
- Edge HTTP handlers under `wasi:http/proxy`, Fastly Compute,
  `wasmtime serve`. One handler invocation per request.

Both are **AOT, short-lived, single-arena-per-scope**. That
profile sharply constrains which performance techniques are
worth importing:

| Technique                          | Fits cold-start AOT?      |
|------------------------------------|---------------------------|
| Monomorphisation                   | Yes — pay at compile time |
| Bump allocator                     | Yes — already there       |
| Refcount inlining (Perceus/Roc)    | Yes — opt-in per heap obj |
| In-place mutation via uniqueness   | Yes                       |
| Tracing JIT (LuaJIT, V8)           | **No** — warmup ≠ cold    |
| Generational GC                    | **No** — arena fits       |
| NaN-boxing                         | **No** — types are static |
| Tagged-immediate uniform repr      | **No** — competes w/ mono |

Anything in the second group should be cribbed only for *ideas*
(e.g. LuaJIT's allocation sinking pass is great even AOT), not
wholesale.

## What we already do well — call out so we don't drift

Cataloguing what's right so future work doesn't accidentally
regress it.

- **IR layer is target-agnostic.** `internal/ir/` produces a
  single op stream that arm64 / arm64-darwin / wasm / x86_64
  all consume. New optimisations land once and ripple to every
  backend. This is the same architecture LLVM and Cranelift
  use, and it's the right shape.
- **Monomorphisation pass exists** (`internal/monomorph/`).
  Generic functions are specialised per call-site type
  signature; downstream passes see fully concrete code. Same
  pattern as Rust / Crystal / Julia.
- **Self tail-call optimisation across all backends**
  (`internal/ir/tco.go`). Self-recursive `return f(args)`
  rewrites to a parameter rebind + backward branch.
- **Defunctionalisation and zero-capture closure inlining**
  (`defunctionalise.go`, `inline_zero_capture.go`). Partial
  implementation of Roc's lambda-set idea — closures with no
  captures lower as direct calls.
- **Tree-shake + dead-function elimination** at link time
  (`internal/treeshake/`, `internal/ir/dead_funcs.go`).
  Important for WASM binary size and for cold-start because
  every unused export still pays for relocation processing.
- **Constant folding / propagation / copy propagation /
  strength reduction** (`constfold.go`, `constprop.go`,
  `copyprop.go`, `strength.go`). The basic optimisation
  battery is in place.
- **Bump arena lifetime model**
  (`LANGUAGE-DIRECTION.md ▸ Positioning`). Per-request /
  per-invocation arena. No GC, no free, no escape-from-scope
  cliffs.
- **Multi-stage compilation: constfold → monomorph →
  defunctionalise → inline → fold → strength → tco → dce →
  treeshake.** Same overall pipeline shape as Factor / OCaml's
  Flambda / SBCL.

## Single-language deep dives

Each section is structured as the codebase's house style
(`LANGUAGE-DIRECTION.md ▸ Outside influences we mined`): what
the source does, what translates, what we'd change or leave.

### BQN and APL (with CBQN as reference impl)

Sources:
- https://github.com/dzaima/CBQN
- Singeli: https://github.com/mlochbaum/Singeli
- Marshall Lochbaum, "Implementing a Bignum Calculator" + CBQN
  internals notes.

**What CBQN does to be one of the fastest array-language
runtimes:**

- **Element-type-specialised arrays.** A single logical array
  type, but the storage is one of `Bit` (1 bit/elem), `i8`,
  `i16`, `i32`, `f64`, plus heterogeneous fallback. Every
  primitive (`+`, `×`, `⌈`, scan, fold, …) dispatches on the
  pair `(op, elem-type)` to a hand-tuned SIMD kernel. The
  dispatch cost is amortised over the whole array — cheap if
  arrays are typical-size.
- **Singeli for the kernel matrix.** Manually writing the
  cross-product of (~50 ops) × (5 elem types) × (NEON, SSE2,
  AVX2) SIMD widths is infeasible. The CBQN authors built
  Singeli — a small typed meta-language — and *generate* the
  whole matrix from ~hundreds of LOC of templated kernel code.
- **In-place when refcount == 1.** Pure-functional surface
  (`y ← 1+x`) but the runtime checks `refcount(x) == 1` and
  mutates in place. This is the no-solver version of Roc's
  Morphic / Perceus: a runtime branch that costs ~1 cycle per
  array op, saves an entire alloc + copy on the hot path.
- **Bit-packed booleans.** `1=x` over an i32 array returns a
  bit array — 1 byte per 8 elements. Bulk ops (AND, OR, popcount)
  use word-wide bit primitives. 8× memory + 8× operation rate.
- **Identity recognition.** `+/` has identity 0, `×/` identity 1,
  `⌈/` identity -∞. Reductions on empty arrays short-circuit to
  the identity. Scans on single-element arrays elide entirely.
- **Compact bytecode.** The BQN VM has ~30 opcodes, one byte each
  (plus operand). Tight fit in i-cache; cheap dispatch.

**What translates:**

- Specialised loop primitives over `[]i32`, `[]i64`, `[]string`
  for `map / filter / fold / scan / any / all`. Hand-write
  the hot kernels rather than emitting generic `for` loops.
  CBQN's lesson: don't try to make the generic loop fast,
  recognise the high-value primitives and replace them.
- Bit-packed `[]bool` representation. The stdlib `Vec[bool]`
  (and slices of it) stores 1 bit/element. `count / any / all /
  popcount / bitand / bitor` use 64-bit words.
- Singeli-style kernel generation as the codebase grows. Once
  there are >20 specialised primitives × >3 elem types × >2
  SIMD widths, manual maintenance breaks. A small Go program
  that templates the kernels (taking primitive descriptor →
  arm64 NEON + x86_64 SSE2 + wasm-simd128 emissions) keeps the
  matrix sustainable. Could live at `internal/codegen/kernelgen/`.
- In-place-when-unique requires an explicit per-object refcount.
  *Not* a fit for the current pure-bump-arena model — but it
  becomes one if/when we add reference-counted heap objects for
  long-lived caches or seamless slices (`Roc-style`,
  `LANGUAGE-DIRECTION.md`).

**Considered, left:**

- Whole-array primitives as the *primary* surface (APL/BQN's
  spelling). Wrong for an edge-handler language — handler code
  is mostly scalar I/O glue, not numerical arrays.
- Tagged-immediate / NaN-boxing. CBQN uses these to make
  generic code (mixed `i32` / `f64` / pointer values in one
  array) cheap; with static element typing we don't need them.

### Forth

Sources:
- jonesforth (annotated): https://github.com/nornagon/jonesforth
- gforth internals, Mecrisp (Cortex-M JIT), Anton Ertl's
  "Threaded Code Variations" paper.

**What classical Forth gets right (mostly relevant to the
interpreter, not codegen):**

- **Threaded-code dispatch variants.**
  - *Direct-threaded*: each compiled word is a list of code
    addresses; the inner interpreter does `next: w ← *ip++;
    jump *w`. Two memory loads + one indirect jump.
  - *Subroutine-threaded*: each compiled word is a sequence
    of `call <primitive>; …; ret`. Looks fat but lets the
    CPU's branch predictor + return-stack buffer do their job.
    Modern superscalar CPUs run this *faster* than direct-
    threaded.
  - *Token-threaded*: 1-byte token per primitive, central
    decode + dispatch. Tightest code size; cheap in i-cache.
- **Inlining everywhere.** Forth primitives are tiny (1-3
  instructions). Inlining the dictionary is a peephole pass.
  Mecrisp / SwiftForth do this aggressively.
- **The dictionary as a linked list of words.** Each word's
  header points back to the prior word. Lookup is linear but
  the working set is tiny. Maps cleanly onto module symbol
  tables for fast linking.

**What translates:**

- **Computed-goto interpreter dispatch in `internal/interp/`.**
  Today the interpreter is a tree-walker; the IR is interpreted
  by walking `[]Op` with a `switch op.Kind`. Adding a fast
  switch-based or token-threaded dispatch loop for dev-mode IR
  execution would 2–3× the interpreter (this is the standard
  speedup from CPython's `ceval.c` computed-goto rewrite,
  Mike Pall's LuaJIT interp, Ruby YJIT). Production cold-start
  uses the AOT backends so this is a developer-tooling win,
  not a hot-path win.
- **Per-primitive native inlining.** For tiny ops
  (`OpAddI32`, `OpLoadLocal`, `OpStoreLocal`, `OpConst32`,
  `OpBranch`) the backends already emit direct sequences. The
  Forth lesson is: keep the primitive set small, optimise
  each one by hand. Already aligned (the IR op set is ~80
  ops; resist growing it).

**Considered, left:**

- Stack-based VM as the production execution model. The
  arm64 / x86_64 backends compile to register-machine native
  code, which is the right target on out-of-order CPUs. A
  stack VM only wins for size, and we already have WASM for
  that.

### Factor

Sources:
- Factor compiler design notes:
  https://docs.factorcode.org/content/article-compiler.html
- Slava Pestov's PLDI / commentary writeups.

**What Factor does that's underappreciated:**

- **Stack-effect inference.** Even though the surface looks
  dynamic, every word has a statically-known stack effect
  `( a b -- c )`. The compiler propagates these through
  quotations to discover that "this dynamic-looking pipeline
  always has shape `( i32 i32 -- i32 )`" — and then *register-
  allocates the stack away*. The stack you see in source is
  not the stack at runtime.
- **Aggressive quotation inlining.** Quotations are
  Factor's anonymous functions. They're first-class but
  almost always called at known sites (`[ 1 + ] map`). The
  compiler inlines them, exposing the body to the rest of
  the optimiser.
- **Per-type specialisation of generic words.** `+` over
  `(fixnum,fixnum)` compiles to a different sequence than `+`
  over `(float,float)`. Same idea as Julia's specialise-per-
  signature, predates Julia by a decade.
- **Modular optimiser pipeline.** Lots of small passes — high-
  level optimiser (inline + propagate + dead-code over a
  shape-flow IR), then SSA-form low-level optimiser (alias
  analysis, value numbering, escape analysis, dead-store
  elim), then register allocation. Each pass small and
  testable.

**What translates:**

- **Per-call-site closure specialisation.** Generalises
  `inline_zero_capture.go` — when a call site `f(closure_lit,
  …)` is visible, inline the closure body into `f` *for that
  call site only* (alongside the existing zero-capture path).
  This is the AOT analogue of Factor's quotation inlining.
  Roc's lambda-set defunctionalisation is the more principled
  version; the Factor approach is the pragmatic one and
  works without whole-program analysis.
- **Two-tier IR + SSA-low-level**, see Recommendations §1.

**Considered, left:**

- Concatenative surface syntax. Wrong fit — handler-style
  code is more readable in expression form.
- Stack-effect inference as a checker rule. We have a real
  type system; this is the dynamic-language workaround for
  not having one.

### OCaml (compiler perspective)

Sources:
- "Real World OCaml" compiler appendices.
- Flambda 2 design docs (Chambart, Lapeyre):
  https://github.com/ocaml-flambda/flambda-backend
- Xavier Leroy, "The ZINC experiment" + later native-code
  papers.

**What makes OCaml's compiler one of the fastest native-code
emitters in production use:**

- **Uniform value representation.** Every value is one word:
  either a tagged immediate (bottom bit = 1, top 63 bits =
  payload) or a pointer to a `(header, fields…)` block.
  Polymorphic code works without monomorphisation — `let id x
  = x` compiles once and accepts everything.
- **Block header is a tiny tag + size.** Every heap object
  has the same shape (header word + N field words). Cache-
  friendly, generic allocator, generic `compare` / GC walk.
- **No template instantiation, no header files.** The compiler
  is single-pass-typecheck + single-pass-codegen. End-to-end
  compile speed: ~250kLOC/min on commodity hardware, ~10×
  Rust's debug-mode rate.
- **Flambda 2 fold-based optimiser.** Inlining + unboxing +
  constant prop + specialisation in a single fold over the
  expression tree, with a cost model. Avoids the
  pass-explosion problem (LLVM has 80+ passes; many no-op for
  any given input).

**What translates:**

- **Fast-compile mode as a first-class goal.** OCaml's
  single-pass philosophy is a useful counterweight to the
  drift-toward-LLVM-everywhere reflex. The codebase is well-
  positioned here — Go-implemented compiler, simple type
  system, no template metaprogramming. *Resist* adding
  features that require multi-pass typing (effect rows would
  be one; bidirectional inference deeper than current would
  be another).
- **Block-shape uniform allocator.** When/if heap-allocated
  long-lived objects appear (sharing structures across
  request arenas, e.g. compiled handlers), use OCaml's
  `(header, fields[N])` layout: every heap object has the
  same shape modulo header tag + size. One allocator path,
  one walker, one comparator.
- **Cost-model-driven inliner.** `internal/ir/inline.go` uses
  a size threshold. A cost model that scores `inlining-saved-
  ops − inlining-added-ops × call-site-frequency-estimate`
  (Flambda's approach) would inline more aggressively in
  loops + less in cold paths. Sketch: each call site gets a
  cheap loop-depth annotation; inliner consults it.

**Considered, left:**

- **Tagged-immediate uniform representation as the *primary*
  data layout.** Conflicts with monomorphisation. Monomorph
  + native types (i32 stays i32, no tag bit) is strictly
  faster at runtime, at the cost of more compile-time work
  and bigger binaries. The codebase has already made this
  bet; don't revisit.

### LuaJIT (the bits that work even AOT)

Sources:
- Mike Pall's archived ML posts (intuit.com mirror).
- DynASM: https://luajit.org/dynasm.html
- LJIT-IR documentation (LuaJIT 2.1).

**LuaJIT's headline trick is a tracing JIT, which doesn't fit
cold-start. But several supporting techniques are transferable.**

- **Allocation sinking.** When a heap allocation is created and
  fully consumed within a single trace (its fields are read,
  then it's discarded without escape), the allocation is
  *removed* and the fields become scalars. Works AOT too:
  any IR-level pass over basic blocks can find `MakeStruct(a,
  b, c); .field=k; let v = .field` patterns and replace with
  scalar-only code.
- **Hand-written interpreter dispatch.** LuaJIT's interpreter
  (used when traces fall back) is hand-written assembly with
  one block per opcode, threaded with `jmp [dispatch+R*8]`.
  Outperforms gcc-compiled C interpreters. *Not* relevant to
  the production AOT backend, but again relevant for
  `internal/interp/`.
- **DynASM as a x86_64 assembler.** Macro-based, generates
  C arrays of bytes. If the x86_64 backend
  (`internal/codegen/x86_64/`) ever needs to grow, DynASM is
  the reference impl — though a hand-written byte emitter
  in Go is fine at the codebase's current scale.

**What translates:**

- **Scalar replacement of aggregates (SRoA).** New IR pass.
  Recognises struct/tuple constructors whose fields don't
  escape the function and lowers them to scalar locals. Big
  win for the codebase's struct-heavy idioms (Result, Option,
  HttpRequest, HttpResponse).
- **Allocation sinking proper.** Stronger than SRoA: pushes
  the allocation down past branches where it's unused.
  Worthwhile after SRoA lands.

**Considered, left:**

- Tracing JIT. Cold-start hostile — first request would warm
  up the JIT. No.
- NaN-boxing. Static types make it irrelevant; we know
  field representation at compile time.

### SBCL / Common Lisp

Sources:
- SBCL internals chapter
  (http://www.sbcl.org/manual/index.html).
- Christophe Rhodes, "Type-Driven Compilation in SBCL."

**Why SBCL is fast despite Lisp being nominally dynamic:**

- **Type declarations gate optimisations.** `(declare (type
  fixnum x))` turns `(+ x 1)` from a generic dispatch into a
  single ADD with unboxed registers. The compiler treats
  declarations as *assumptions*, not checks — declaring the
  wrong type is UB-equivalent.
- **DECLAIM INLINE.** Per-function, in source: "always inline
  this." Stronger than `internal/ir/inline.go`'s size-threshold
  heuristic.
- **Compiler macros.** A function can have a *companion
  rewrite rule* that the compiler tries first. `(length
  '(1 2 3))` rewrites to `3` at compile time via a compiler
  macro on `length`.

**What translates:**

- **Inline pragma.** Source-level `@inline` / `@noinline`
  decorator on a function. Overrides the inliner's heuristic.
  Cheap to implement (parser flag → IR `Func.InlineHint`).
- **Compiler-macro-style rewrite rules** for stdlib hot
  paths. `string_concat(a, b, c)` with all three known at
  compile time → single allocation with all three written in.
  Implementation: a `rewrites.go` pass that runs after
  `monomorph`, before `inline`, with hand-written
  pattern→IR rewrites for ~20 stdlib hot functions.

**Considered, left:**

- Dynamic dispatch with polymorphic inline caches. Solves a
  problem we don't have.

### Crystal

Sources:
- Ary Borenszweig, Crystal language tour + internals talks.
- https://crystal-lang.org/reference/.

**Crystal is a static Ruby that compiles to LLVM. Performance
posture is "Ruby ergonomics, Rust performance." Key tricks:**

- **Whole-program type inference with unions.** Crystal's
  type system finds the union of all possible types flowing
  through each variable. `arr = [] of (Int32 | String)`. No
  monomorphisation, but the compiler knows the closed set
  of types and emits dispatch optimised for it (cascading
  if/else, jump table).
- **Method overload resolution at compile time.** Multiple
  `def foo(x : Int32)` / `def foo(x : String)` definitions
  resolve statically based on inferred type.
- **LLVM with `-O3`, full inlining.**

**What translates:**

- **Closed-union dispatch.** Already implicit in
  `match` lowering, but the codebase could be more explicit:
  if a `match` is over a known small finite set, emit a jump
  table (`br_table` on WASM, computed branch on arm64). The
  closure tag on lambda-sets gets the same treatment.
- **Whole-program flow-sensitive type narrowing.** Factor
  also has this. After `match x { Some(n) => …, None => … }`
  inside the `Some` arm, the compiler knows `x.value` is
  unboxed `n`. Many languages still pay for the tag check
  inside the arm body — eliminating it is straightforward
  with the IR knowing about basic blocks (see Recs §1).

### Nim

Sources:
- Nim compiler internals + Araq talks.
- ORC paper (Marenkov, Rumpf):
  https://nim-lang.org/blog/2020/10/15/introduction-to-arc-orc-in-nim.html

**Why Nim is relevant: same multi-target shape as this codebase
(emits C/C++/JS, no native LLVM dependence by default).**

- **Templates and macros for compile-time codegen.** Powerful
  AST-rewriting macros run at compile time. Whole serialisation
  / JSON / HTTP-router DSLs implemented as macros.
- **ARC + ORC memory model.** Atomic-refcount-with-cycle-
  detector. The cycle detector runs only when needed
  (assignment patterns that *could* create a cycle, identified
  statically). For acyclic workloads it's pure ARC.
- **`{.inline.}` / `{.compileTime.}` pragmas** at the source
  level.

**What translates:**

- **ORC's "only-detect-cycles-where-they-could-exist" idea.**
  If we ever add reference-counted heap objects (for shared
  config, persistent maps across handler invocations), the
  cycle detector's compile-time gating means the common
  acyclic case has zero overhead. Worth knowing about ahead
  of time.

**Considered, left:**

- Templates / hygienic macros as a language feature. Big
  surface-syntax bet; mostly subsumed by the existing
  prelude + IR-level rewrites approach.

### Julia

Sources:
- Bezanson et al., "Julia: A Fast Dynamic Language for
  Technical Computing."
- `@code_native` / `@code_typed` introspection patterns.

**Julia is multiple-dispatch + JIT + LLVM. Two transferable
ideas:**

- **Specialisation per call signature.** `f(::Int, ::Int)` and
  `f(::Float, ::Float)` get distinct compiled bodies, one per
  signature actually called. This is monomorphisation done
  on-demand. We do it AOT — same idea, different timing.
- **`@inbounds` and `@simd` pragmas.** Source-level opt-outs
  from bounds checks; source-level requests for SIMD
  vectorisation. Granular control where the analysis can't
  prove the win.

**What translates:**

- **`@inbounds`-style escape hatch.** Once bounds-check
  elimination is in (see Recs §11), the cases where the
  analysis can't prove safety should have a per-loop opt-out:
  `for i in 0..len(a) (unchecked) { a[i] }`. Caller takes
  the risk; compiler emits no bounds check.

### Vale

Sources:
- https://vale.dev/ — Verdagon's design writeups.
- "Generational References" paper.

**Vale's headline technique is generational references — a novel
memory-safety mechanism with overhead in the same league as Rust
borrow checking but without lifetimes in the surface syntax.**

- Every heap object has an 8-byte generation number.
- Every reference is `(pointer, generation)`.
- Deref checks `*pointer.generation == ref.generation`. If
  the object has been freed and re-allocated, the generation
  bumps and the check fails — caught at runtime.
- Overhead measured at 5–10% vs unchecked C, much less than
  refcount.

**What translates:**

- *Not directly*, because the arena model already gives us
  memory safety for the dominant use case (everything dies at
  scope end; no use-after-free possible because nothing is
  freed until the arena is). Generational references shine
  when individual objects have *individual* lifetimes inside
  a long-lived program — opposite shape from ours.
- **Idea worth keeping in pocket**: if we ever expose
  long-lived caches or sessions across request arenas (e.g.
  a connection pool, a JIT'd query plan), generational
  references are cheaper than refcount and don't have
  refcount's cycle-detection problem.

**Considered, left:**

- Adopting them now. Premature — arena covers the use case.

### WUFFS

Sources:
- https://github.com/google/wuffs
- "Wuffs the Language" design doc.

**WUFFS = "Wrangling Untrusted File Formats Safely." A language
specifically designed for writing parsers (PNG, JPEG, GZIP, …)
with provably zero runtime panics. Transpiles to C.**

- **Bounded static analysis for bounds checks.** Each `a[i]`
  must have a proof — a chain of facts the compiler can
  verify — that `i` is in range. If the compiler can't prove
  it, the source must add a `assert i < len(a)` and the
  compiler propagates the fact forward.
- **No heap allocation.** Parsers receive pre-allocated work
  buffers from the caller. No `malloc` at all.
- **Output is C with all bounds checks erased.** Performance
  in the same league as hand-written C, with safety
  proven at compile time.

**Why this matters for edge handlers:**

The hot path for an edge HTTP handler is *parsing* — HTTP
request lines, headers, JSON bodies, URL components. These
are exactly WUFFS's target. The codebase already has
`http_parse_request` in the prelude with hand-coded bounds.

**What translates:**

- **Range analysis pass** in `internal/ir/`. Tracks each i32
  local's known min/max as a lattice. Drives bounds-check
  elimination, integer-overflow elimination, and (later)
  width-narrowing (`i32` that's provably `<256` could be
  stored as `i8` in compound types — Roc does this).
- **Assertion as a fact.** `assert(i < len(a), "…")`
  contributes `i < len(a)` to the range table for the rest
  of the scope. Cheap to wire up alongside the planned
  TigerStyle assertion builtin
  (`LANGUAGE-DIRECTION.md ▸ TigerStyle ▸ Adopting`).
- **Hand-written parser DSL for the prelude.** Long term:
  if/when we add `Cookie`, `multipart/form-data`, JSON-Pointer,
  query-string-with-arrays parsing to the prelude, a small
  WUFFS-flavoured DSL inside the lang would make them
  formally-bounded.

### Jai (Jonathan Blow)

Source: Jonathan Blow's YouTube series + community summaries
(no public binary as of this writing).

**Jai's perf-relevant ideas:**

- **Compile-time execution as a first-class primitive.** Any
  function can run at compile time, with full access to disk,
  network, etc. (subject to a permission system). Used for:
  schema → struct generation, lookup-table precomputation,
  metadata.
- **Data-oriented design support.** First-class SoA (`#soa
  []Vec3`) — `arr.x` is a slice of all xs, `arr.y` a slice
  of all ys.
- **No copy constructors / destructors / RAII.** Explicit
  init/deinit. Zero hidden control flow, in the Zig sense.

**What translates:**

- **`#soa []Struct` lowering.** For numerical hot loops
  (rare in handlers, but real in stdlib helpers like
  hashtable rehash, sorting, batch JSON encoding), SoA gives
  better cache behaviour. Implementable as a struct-layout
  hint in the IR: the array of N records is stored as
  N slices, one per field, allocated contiguously. Element
  access becomes (field-slice)[i] instead of base[i].field.

**Considered, left:**

- Full compile-time execution. We have `constfold.go`
  doing the easy cases. Going from "fold scalar arithmetic"
  to "run arbitrary lang code at compile time" is a huge
  effort and only pays off for very specific patterns
  (lookup tables, parser tables). Revisit when there's
  demand.

### Zig (perf lens — design lens already covered)

`LANGUAGE-DIRECTION.md ▸ Convergent signals` covers the
language-shape side. The *compiler*-side bits:

- **Result location semantics (RLS).** Major. Detailed in
  Recs §2.
- **`@Vector(N, T)`.** Portable SIMD builtin. Lowers to LLVM
  vector ops on native targets, to WASM `v128` on wasm.
  We can do the same with `Op.Vec(N, T)` opcodes.
- **Allocator as a value, not a global.** Already on the
  Odin-flavour roadmap.
- **Self-hosted backend skipping LLVM in debug.** Compile
  times reportedly 10× faster on the self-hosted backend.
  We're already in this regime — we don't link LLVM either.
  Stay there.

### Rust (perf lens)

`noalias` is the underappreciated win. Borrowed references
are aliasing-free by construction; the compiler tells LLVM,
which unlocks aggressive load/store reordering and
vectorisation. For our `{ptr, len}` slices into a bump arena,
we have the same property — slices from independent
constructions never alias — but no current IR/backend
mechanism communicates that. Recs §10.

Iterator fusion is the other one. `xs.iter().map(f).filter(g)
.fold(0, h)` compiles to a single loop with no intermediate
allocations because every iterator method is `inline` and the
final fold drives the loop. We don't have an iterator
protocol yet; if/when we add one, the same fusion is
achievable as long as each combinator inlines and the loop
shape is recoverable. Roc's `Task` + lambda-set is the
functional equivalent.

## Cross-cutting themes

Themes that show up in three or more of the above. By the
team's own "convergent signals" rule
(`LANGUAGE-DIRECTION.md`), these are default-correct.

1. **Specialise hot primitives by hand.** BQN/Singeli, OCaml's
   `caml_modify`, LuaJIT's tuned interpreter, Forth's
   inline-the-tiny-words pattern. The "sufficiently smart
   compiler" myth dies hard, but every fast language has a
   bunch of hand-tuned primitives that *don't* come out of
   general code paths.

2. **In-place mutation via uniqueness.** Roc (Morphic),
   BQN/CBQN (runtime refcount==1), Perceus (Koka), Lobster.
   With a bump arena the savings are smaller than with malloc
   but real — every avoided copy saves arena bytes + a memcpy.

3. **Closed sets unlock layout tricks.** Lambda-sets (Roc),
   closed sum types (MoonBit), tagged unions (Rust/Zig/Odin).
   Knowing the *finite* set of variants permits jump-table
   dispatch + tag-bit layout tricks (e.g. NonNull pointer for
   `Option[*T]`).

4. **No hidden control flow.** Zig, Roc, this codebase. Every
   call is visible; every potential allocation point is
   visible. Makes inliner + escape analysis substantially
   simpler and more accurate. Resist proposals that hide
   control flow (operator overloading, implicit `Drop`,
   exceptions, implicit conversions).

5. **AOT for cold-start, JIT for hot servers.** A discipline.
   LuaJIT / V8 / HotSpot win because they're tuning for
   long-running processes. Our use case is the opposite —
   ship native code, accept that the first instruction
   executes faster than a JIT could even decide to compile.

6. **Result location semantics.** Zig, Jai. Compound literals
   write directly to their destination. No temp + memcpy.

7. **Threaded interpreter dispatch for dev tooling.** Forth,
   LuaJIT, CPython, Ruby YJIT's baseline. Computed-goto or
   token-threaded.

8. **Range analysis drives multiple optimisations.** WUFFS,
   Rust's MIR, Crystal. Once you have it, bounds-check elim,
   overflow elim, and width-narrowing all fall out.

9. **SSA is the lingua franca of strong optimisers.** Factor,
   SBCL, LLVM, Cranelift, V8, HotSpot. Our IR is not SSA
   yet. Recs §1.

10. **Cost-model-driven inlining beats size-threshold
    inlining.** Flambda 2, modern GCC, MLIR. The fixed
    size-limit approach (current `inline.go`) overshoots in
    cold paths and undershoots in hot loops.

## Concrete recommendations

Prioritised by leverage (impact on cold-start / steady-state
perf) × cost (engineer-weeks). Each entry names the file or
new module that would change.

### 1. Move the IR to a basic-block + SSA form. *Highest leverage.*

**Cost: high (multi-week refactor).** **Impact: very high.**

Today `Func.Ops []Op` is a flat op list; control flow is
encoded as `OpBlock` / `OpBranch` markers and locals are
indexed slots. Consequences:

- `constprop.go` / `copyprop.go` work over peephole windows.
  Cross-block propagation requires manual fixed-point logic
  and tends to miss cases.
- `dce.go` is reachability-from-uses; without def-use chains,
  computing "no remaining use" is approximate.
- Loop-level optimisations (LICM, unrolling, vectorisation,
  range analysis through phi nodes) need a CFG.
- Escape analysis (Rec §3) needs def-use to see what *all*
  uses of an allocation are.

Proposed shape:

```
type Func struct {
    Name    string
    Params  []ast.Param
    Blocks  []*Block
    Entry   *Block
    // …
}
type Block struct {
    ID    int
    Ops   []Op       // pure / side-effecting; no control flow
    Preds []*Block
    Succs []*Block
    Term  Terminator // br / brif / ret / switch
}
type Op struct {
    Kind     OpKind
    Result   Value     // SSA name (per-func unique)
    Args     []Value
    // … existing fields
}
```

Locals stay only for `address-of` cases; everything else
threads SSA values. Phi insertion at block joins (standard
SSA-construction algorithm — pruned-SSA or semi-pruned;
Briggs/Cooper/Torczon Chapter 9 is the reference).

This is the single biggest architectural lever. Every other
analysis in this list is easier and stronger on SSA.

### 2. Result Location Semantics (RLS). High leverage, modest cost.

**Cost: 1–2 weeks per backend.** **Impact: high.**

Compound literals (struct, tuple, array) currently allocate
into a temp, then copy. Threading a *destination address*
through expression lowering lets the literal write fields
directly to the eventual home.

Concretely: lowering of `var x: Point = Point{ x: 1, y: 2 }`
currently produces (sketch):

```
make_struct Point        # allocates temp t
const 1 → t.x
const 2 → t.y
store t → local_x        # 16-byte memcpy
```

After RLS:

```
store_local_field local_x.x ← 1
store_local_field local_x.y ← 2
```

The destination address flows in at lowering time. This is
the textbook Zig RLS — no allocation, no copy.

Lands at the AST → IR boundary. Each `Expr` lowerer takes
an optional `dest Place`; struct/tuple/array literals
write through it. Falls back to temp if `dest == nil` (e.g.
in an `if`-expression context where the destination isn't
materialised yet).

### 3. Escape analysis + stack allocation. High leverage.

**Cost: 1–2 weeks (after SSA).** **Impact: medium-high.**

For a bump-arena language, "doesn't escape" means "can live
in the function's stack frame instead of the arena." Saves:

- A bump-pointer advance per allocation (a couple of
  cycles each, but they add up in hot loops).
- Arena footprint (smaller scratch buffer per request).
- Pointer indirection on field access — stack-allocated
  structs live in registers / known stack offsets.

Definition: an allocation *escapes* iff its address (or a
pointer derived from it) is stored into the heap, returned
from the function, passed to a function that escapes it, or
captured by a closure that escapes it. Otherwise it's
stack-allocatable.

Implementation: with SSA def-use chains, run a fixed-point
over "does this Value or any of its uses escape." Mark
non-escaping allocations and switch their codegen to
`alloca`-style stack frame slots.

### 4. Scalar replacement of aggregates (SRoA) + allocation sinking.

**Cost: 1–2 weeks (after SSA + escape).** **Impact: high.**

LuaJIT pass. When an allocation's fields are read in a
single block without the pointer escaping, the allocation
becomes scalar locals and the field accesses become
register reads.

Pattern:

```
%t = make_struct Point  # x, y
store %t.x ← 1
store %t.y ← 2
%v = load %t.x
```

becomes:

```
%v = 1
```

Plus allocation sinking: if `%t` is used only on one side of
a branch, push the allocation into that branch.

### 5. Type-specialised array kernels for stdlib hot paths.

**Cost: ongoing, ~1 day per kernel.** **Impact: medium per kernel.**

BQN/Singeli pattern. Hand-write the per-element-type SIMD
kernels for the stdlib operations that show up in handler
hot paths:

- `i32 / i64 / f64`: `sum`, `min`, `max`, `dot`, `eq_count`.
- `bool` (bit-packed): `count`, `any`, `all`, `and`, `or`,
  `xor`, `popcount`.
- `string`: `bytes_to_lower`, `find_byte`, `equals_ignore_case`.
- `bytes`: `find_substring` (Boyer-Moore-Horspool).

Lives at `internal/codegen/kernels/` with per-backend
emitters (arm64 NEON, x86_64 SSE2/AVX2, wasm-simd128). The
checker recognises calls to a small set of stdlib functions
on known-element-type slices and emits the kernel op
instead of inlining a generic loop.

If/when the kernel matrix grows past ~30 entries, a
Singeli-lite Go program at `internal/codegen/kernelgen/`
templates them per `(op, type, backend)`.

### 6. Bit-packed `[]bool` representation.

**Cost: 1 week.** **Impact: medium.**

`[]bool` stored as 1 bit per element instead of 1 byte.
Bulk ops (`any` / `all` / `count` / boolean binops) use
64-bit words. Standard reference: Roaring Bitmaps for
the sparse case; for dense the obvious word-packing is
fine.

Element access is `(arr.ptr[i>>6] >> (i & 63)) & 1`.
Assignment is `arr.ptr[i>>6] = (arr.ptr[i>>6] & ~(1<<(i&63)))
| (v<<(i&63))`. Cheap.

### 7. Cost-model-driven inliner.

**Cost: 1 week.** **Impact: medium.**

Replace `inline.go`'s size threshold with a Flambda-style
cost model: `benefit = saved_call_overhead + foldable_args ×
constant_factor − inlined_body_size × loop_depth_penalty`.

Loop depth comes from the SSA CFG (Rec §1). Source-level
`@inline` / `@noinline` (Rec from SBCL section) overrides
the cost model.

### 8. Computed-goto interpreter dispatch in `internal/interp/`.

**Cost: 2 days.** **Impact: dev-tooling only (~2-3× interp
speed).**

Today `internal/interp/` walks IR ops via `switch op.Kind`.
The Go compiler turns this into a single indirect branch
per op + a long sequence of `cmp; je` instructions for the
table.

Generating a hand-rolled dispatcher in Go isn't worth it
(no `&&label` in Go). The transferable bit is keeping the
switch *flat* (all opcodes at one level, no nested
switches), letting the Go compiler emit a jump table.
Already mostly aligned — call this out so it doesn't drift.

For a bigger win, ship a small assembly-language threaded
interpreter as a build option. Likely not worth it given the
production AOT path.

### 9. Bounds-check elimination + range analysis.

**Cost: 2 weeks.** **Impact: medium (current code rarely
hot-path-bottlenecked on bounds checks, but WUFFS-shape
parser code in the prelude will benefit).**

Range analysis tracks each i32 local's known `[min, max]`
through the SSA graph. Bounds checks become trivial when
the lattice can prove `0 ≤ i < len(a)`.

The `for i in 0..len(a) { a[i] }` pattern is the obvious
target. After the desugar, the loop induction variable has
range `[0, len(a))` by construction; the `a[i]` access
checks against that. Pure win.

Adds an `@inbounds` opt-out (Rec from Julia section) for
loops the analysis can't prove safe.

### 10. `noalias` hints on slice operations.

**Cost: 1 week.** **Impact: small but free.**

LLVM-style `noalias` annotations on `{ptr, len}` slices that
the codebase guarantees to be non-aliasing. Unlocks load/
store reordering and vectorisation in the backend.

On arm64 / x86_64, communicated via instruction scheduling
+ avoiding false dependencies in register allocation. On
WASM, `noalias` doesn't exist, but the same analysis at
IR level lets us reorder loads/stores before lowering.

### 11. SIMD vector type in the IR.

**Cost: 2 weeks.** **Impact: gates Recs §5 + future.**

`Op.Vec(N, T)` opcodes: `VecAdd`, `VecMul`, `VecSplat`,
`VecLoad`, `VecStore`, `VecReduce`. Backends lower to:

- arm64: NEON (`add v0.4s, v1.4s, v2.4s`).
- arm64-darwin: same NEON path.
- x86_64: SSE2 minimum, AVX2 where available.
- WASM: `v128` ops (already in the spec, supported by
  wasmtime + V8).

Used (a) internally by the kernel emitter for stdlib
specialisations (Rec §5), and (b) exposed as a `Vec[N, T]`
stdlib type for users who want explicit SIMD.

### 12. Cross-module inlining at link time.

**Cost: 1 week.** **Impact: medium for handler-shape code
that calls into stdlib hot paths.**

Today `inline.go` operates within a module. After treeshake
runs (which is whole-program), there's no cross-module
inline pass. A small post-treeshake inliner that fires for
hot stdlib functions would let handler code's
`http_response_set_status` calls fuse into the caller.

### 13. Lambda-set defunctionalisation extended end-to-end.

**Cost: 2 weeks.** **Impact: medium.**

Already on the roadmap (`LANGUAGE-DIRECTION.md ▸ Roc's
lambda-set defunctionalisation`). The Roc design: each call
site's closure parameter knows the *finite set* of closure
shapes that flow there; the closure parameter lowers to a
tagged union of capture-structs; the call inside the callee
becomes a `match` over the tag with direct calls in each arm.

`internal/ir/inline_zero_capture.go` is the partial start.
The full version handles non-zero captures too, requires
whole-program lambda-set inference.

Particularly important for WASM: indirect calls cross the
function-table boundary and are slower than direct calls
*and* harder to inline.

### 14. Source-level `@inline` / `@noinline` / `@inbounds`.

**Cost: 2 days (parser + IR hint flags).** **Impact: small
but pays for itself the first time a hot loop needs it.**

Annotations on function definitions and loops. Cheap to
implement, expensive to omit (eventually someone *will* need
to force a specific decision the optimiser can't make).

### 15. Compiler-macro-style rewrites for stdlib hot paths.

**Cost: ongoing, ~1 day per rewrite.** **Impact: per-rewrite.**

Pattern-driven AST/IR rewrites for known stdlib functions
when called with statically-knowable arguments. Examples:

- `string_concat(a, b, c)` with `c == "\r\n"` and all
  lengths known → single 3-source memcpy.
- `i32_to_string(n)` with `n` literal → emit the string
  constant directly.
- `map_get_or(m, k, default)` with `m` empty (e.g. fresh
  literal) → `default`.

Lives at `internal/ir/rewrites.go`, runs between
`monomorph` and `inline`.

### 16. Mutual tail-call optimisation via shared loop frame.

**Cost: 1 week.** **Impact: small (rare pattern), but unlocks
state-machine-style code without trampoline boilerplate.**

`tco.go` handles self-recursion only. Mutual TCO between a
finite set of co-recursive functions can be lowered to a
single combined `loop { switch state { … } }` shell.
Pattern shows up in lexers and protocol parsers.

### 17. PGO ingestion (later).

**Cost: 1 month.** **Impact: small for the cold-start case,
medium for long-running services.**

Long-tail. If/when there's a measurable corpus of edge-
handler binaries to profile, ingest sample profiles to bias
the cost-model inliner (Rec §7) + the kernel dispatcher
(Rec §5).

## Anti-patterns — explicit list of "do not adopt"

Reasoning recorded so a future agent doesn't re-litigate.

- **NaN-boxing / uniform-tagged-immediate value
  representation.** LuaJIT, CBQN, JS engines. Solves the
  "polymorphic value at runtime" problem we don't have
  thanks to static typing + monomorphisation.

- **Tracing JIT.** LuaJIT, V8, HotSpot, PyPy. Hostile to
  cold-start; the first request would warm up the JIT, which
  is exactly the wrong shape for edge handlers.

- **Generational GC.** OCaml, JVM, .NET. Solves the
  "objects-live-different-amounts-of-time" problem we don't
  have — arena-per-request already wins this fight.

- **C++-style header files / template instantiation per
  call site at link time.** A non-trivial reason C++ compile
  times are bad. Our monomorphisation is a *language-level*
  thing that the compiler controls, not a textual-include
  pre-processor. Stay there.

- **Implicit copy constructors / destructors / RAII.**
  Hidden control flow. Optimiser-hostile. Zig's stance,
  shared.

- **Function-coloured async/await.** Splits stdlib into red
  and blue worlds; Roc's effect rows + Gleam's `use`
  subsumes the use case without colour.

- **Refcount for short-lived values.** Roc/CBQN do this
  because their hot path is whole-program. Our arena makes
  RC pure overhead for the dominant case — RC is only
  worth it for *long-lived* objects that outlive a single
  arena, which is a minority shape.

## When to revisit

Re-survey every ~50 PRs or when a perf goal becomes blocking.
The shape of the existing optimiser fixes the "easy wins" set
— once SSA (Rec §1) lands, the remaining recommendations get
much cheaper, and several new ones (LICM, GVN/CSE, alias
analysis, loop unrolling) become straightforward additions.

The single highest-leverage milestone, by a wide margin, is
**SSA-form IR with basic blocks** (Rec §1). Until that lands,
the other optimisation recommendations are working at half
strength because they can't see across block boundaries.
After it lands, escape analysis + SRoA + bounds-check
elimination + range analysis arrive in quick succession
because they share the same lattice + def-use plumbing.
