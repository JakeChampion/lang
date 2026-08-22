# What the language does to the self-hosted compiler

An analysis of `examples/self_host/` (94 files, 175,194 lines) asking one
question: **which parts of Fern make writing the self-hosted compiler harder
than it needs to be?** Not "which parts of the self-host are badly written" —
that is `docs/SELF-HOST-AUDIT.md`, and its findings are largely structural
cleanups. This is the layer under that: the language features that are absent,
that are present but unusable in this program, or that are present with
semantics that push the code into a worse shape.

Method: a census of what the self-host's sources actually use, read against the
feature surface the language actually has (`docs/FEATURE-AUDIT.md`), plus
`cmd/fern` probes for every behavioural claim. The census is counted **after
`//` comments and string, f-string and char literals are stripped**, and the
counted rows are pinned by a test rather than re-grepped — see §1's method note.
The probes are in §7.

---

## 0. The diagnosis in one paragraph

Fern is a language with generics, traits, closures, `Option`/`Result`, `?`,
`for..in`, a hash map, and a 61-module stdlib. **The self-hosted compiler uses
approximately none of it.** It is written in a dialect of about fifteen
constructs — `while`, index arithmetic, `string[]`, struct literals, union-typed
`match`, and string concatenation — and the reason is not taste. Four
independent forces each remove a slice of the language from the self-host's
reach, and their intersection is roughly C-with-`match`. The forces are: an
ownership model that cannot be inferred from syntax (so every abstraction is a
new RC leak to hand-prove), a bootstrap subset that reaches no stdlib (so no
container beyond the built-in array exists), a module system where a module is a
file and cycles are illegal (so a mutually-recursive pass cannot be split), and
a self-referential fixpoint gate that makes any feature the self-host does not
already use untested on the self-host path — and therefore too risky to adopt.
That last one is the important one: it is a ratchet, and nothing turns it back
except a deliberate conversion.

---

## 1. The census

What a 175k-line compiler written in a modern language uses, counted across
`examples/self_host/*.fern` with comments and literals stripped. The `Gate`
column says what `TestSelfHostFeatureCensus` holds the row to.

| Construct | The language has it | Self-host uses it | Gate |
|---|---|---|---|
| Generic functions | ✅ monomorphised, with trait bounds | **8**, all `astwalk`'s fold spine | pinned |
| Generic structs | ✅ | **0** | pinned |
| Closures / lambdas | ✅ `(x: T) => e`, escaping + capturing | **40** — 4 anonymous `function(…)` exprs and 36 nested named fns (4 astwalk visitors, 17 `wasm_ir` helper-gate predicates behind `any_op`, 15 in `checker`'s collectors); 20 of the 40 capture, the gate predicates mostly do not — plus **6** arrow lambdas (3 no-op statement visitors, 3 in `constfold`'s assert probe) | pinned |
| `for x in xs` | ✅ arrays, strings, `Iterator[T]` | **307** in 2 modules — 292 in `checker.fern`, 15 in `visibility.fern`; falling as SH-022 folds collectors onto `astwalk` | floor |
| `?` error propagation | ✅ incl. `From`-converting widening | **0** | pinned |
| Hash map (`Map[K, V]`) | ✅ i32/string/`@derive(Eq, Hash)` keys | **11** spellings in 3 modules (`irverify`'s `NameIndex`, `wasm_ir`'s call set, `builtins`' mirror of `JObject`) | pinned |
| `astwalk` call sites (walkers on the shared spine) | — | **98** across 11 modules | floor |
| `enum` with payloads | ✅ multi-payload, named fields | **2 declarations** | — |
| `Option[T]` / `Result[T, E]` in return position | ✅ | **20** of 4,676 functions (0.4%) | — |
| stdlib (`std/*`, `core/*`) | 61 modules | **`std/io` only** (19 imports) | — |
| `while` + manual index | — | **4,386** loops, **4,728** `x = x + 1` | ceiling on the increments |
| `-1` as "absent" | — | **240** `return 0 - 1` | logged |
| String-tagged side tables (`"SFRRECV:"`, `"BORROW:"`, …) | — | **65** distinct tag namespaces | — |
| Magic ASCII byte constants (`== 91`, `== 44`) | — | **342** | — |
| Explicit `as` casts | — | **682** | logged |
| Hand-written AST walkers | — | **~130** over `Expr`, **~247** over `Stmt` | — |
| Wildcard `_ =>` match arms | — | **2,563** of **9,245** arrow tokens (28%) | ceiling |
| Locals with a written type annotation | inference exists | **17,320** of 17,327 (99.9%) | logged |
| Methods (`function (r: T) name(…)`) | ✅ | **276** in 9 modules | logged |

**Method.** Every counted row is taken after `//` comments and string, f-string
and char literals are stripped out. The self-host embeds whole test programs as
string literals and discusses its own syntax in prose, so a raw grep counts the
compiler talking ABOUT a construct as one that uses it — and the error is large,
not marginal: `as` appears 3,856 times raw and **682** times in code. Three
successive hand-measurements of this table disagreed with each other, in both
directions, for exactly that reason.

So the rows are no longer re-grepped. `TestSelfHostFeatureCensus`
(`internal/e2eselfhost/self_host_feature_census_test.go`) does the strip and the
count in under a second, and

```
go test ./internal/e2eselfhost/ -run TestSelfHostFeatureCensus -v
```

prints the whole table. `pinned` rows fail on any move in either direction —
they are small, and a move means this table needs editing; `floor` fails only on
a fall; `ceiling` allows ~10% of headroom over the measurement before failing;
`logged` rows are printed by the same run but not asserted. A row marked `—` is
hand-counted and not covered by the test.

The single largest file, `irlower.fern`, is **60,552 lines** and contains a
**1,704-line function** (`lower_call_method`). `LowerState`, the value threaded
through all of it, has **31 fields**, fourteen of which are `string[]` sets
carrying ownership facts.

None of this is because the author preferred it. Each row has a cause below.

---

## 2. Root causes, ranked

### 2.1 Ownership is borrowed-by-default, so it cannot be inferred

This is the big one, and `docs/OWNERSHIP-INFERENCE-PLAN.md` §3 already states
it precisely:

> in today's borrowed-by-default model the *same* syntax `match (xs)` means
> *borrow* or *consume* depending on `xs`'s declared ownership — so ownership
> cannot be inferred from syntax alone, because the annotation is the input
> that disambiguates.

The consequence is visible everywhere in `irlower.fern`. Because ownership is
not a property the compiler can read off the program, the lowering pass
reconstructs it with whole-program heuristics, and each heuristic is a
separately-proven special case carried in a string-keyed registry. The
namespaces — `SFRRECV:`, `BORROW:`, `MAPF:`, `MAPKS:`, `TUPRC:`, `RCENUM:`,
`OPTARR:`, `STRFLDF:`, and 57 more — are hand-built maps from a name to a fact,
encoded as delimited strings in `string[]`, decoded by prefix match.

`LowerState` carries eight of these fact-sets directly: `reclaimable_names`,
`aliased_names`, `borrowed_names`, `grow_exempt`, `append_inplace`, `grow_sole`,
`own_params`, `moved_names`. Each exists because a fact the programmer knew
was not expressible, so it is re-derived.

The cost is not abstract. `docs/RC-PERCEUS-SELF-HOST-PORT.md` §9's most recent
entries are a leak-by-leak grind: one entry decomposes a *single* expression
(`b.relabel(..).tag.len()`) into three independent leaks of 24, 22 and 48 bytes
per round, closes one, and names the other two as separate future slices. The
entry before it closes a fresh-string-receiver chain from 71 bytes/round to 22
and explains why the remaining 22 needs "two proofs the registry does not
carry". This is honest, careful work — and it is the shape work takes when the
type system declines to carry the information.

**The escape hatch the language already has, it throws away.** `str` (the
borrowed-string view, `internal/ast/ast.go:91`) and `char` exist as types,
carry a checker-enforced discipline — and are then **erased to `string` and
`i32` at the `LowerWith` choke point** (`internal/ir/erase_surface.go`),
immediately before the IR builder, which is exactly where Perceus lives. The
one borrow annotation the surface has is deleted just upstream of the pass that
spends thousands of lines re-inferring it. Today that erasure costs little
because `str` has no producers and its escape rule (#4814) is deferred — but it
means finishing `str` can never help RC without also un-erasing it.

### 2.2 The bootstrap subset reaches no stdlib, so there is no map

Every self-host module imports siblings and `std/io` (which exports exactly two
functions). Nothing else. The consequences:

- **A compiler that had almost no hash map.** Eleven `Map[K, V]` spellings in
  175k lines, across three modules — `irverify`'s `NameIndex`, converted in
  #6993 slice four, and `wasm_ir`'s call set. Everything else is still a linear
  scan over a `string[]` or a hand-rolled bucket table: 290 sites comparing an
  array element to a name, 114 hand-rolled
  `contains`/`index_of`/`find` helpers, and five hand-written hash tables
  (`checker.SigTable`, `irlower`'s borrow registry and `MFuncs`,
  `x86_native`'s label table, and the one now deleted). The 65 string-tag
  namespaces of §2.1 are, structurally, a hash map implemented in the string
  type. `core/map` reaches the compiler as an ordinary external import, so the
  blocker this section names was the ratchet, not the module system.
- **Reimplementation of the obvious.** `docs/SELF-HOST-AUDIT.md` SH-020 found
  `i32_to_string` in nine files. `util.fern` fixed most of that — but
  `checker.fern` still carries `itoa_nn` under a comment reading "checker.fern
  has no int→string", which has been stale since it started importing
  `./util`. Four independent copies of the correctly-rounding `parse_f64` kernel
  survive on purpose, pinned to each other by a test that asserts they are
  identical code.
- **No test framework.** `std/test` (the TAP-13 runner the project is migrating
  to) is unreachable, so each module's tests are a separate `*_run.fern` driver
  with a hand-rolled `main`: **45 drivers, 56 `main()`s**, each paired with a Go
  test that stages files into a temp dir.

### 2.3 A module is a file, and cycles are illegal

`fern -check` on two mutually-importing files: `import cycle detected`. Combined
with one-module-per-file and no package concept, a mutually-recursive compiler
pass cannot be split at all. `lower_expr` ↔ `lower_stmt` ↔ `lower_call_method`
↔ `lower_stmt_var` are irreducibly mutually recursive, so they live in one
60,552-line file, and the functions inside it grow to 1,704 lines because
splitting *them* out is the only decomposition the language permits and it
does not reduce the file.

The second cost is the test-staging tax: because there is no package, every Go
test that compiles a self-host module lists its transitive module set by hand.
**397 files in `internal/e2eselfhost/` name `util.fern` in a staging list.**
`util.fern`'s own header documents the resulting workflow — "the module grows one
helper at a time as files are converted off their local copies; keeping each
conversion small bounds the blast radius on the Go-side test staging lists".
Adding a module to the self-host is a 397-file edit.

### 2.4 The fixpoint ratchet: unused features stay untested, so they stay unused

The self-host is validated by compiling itself. `docs/TEST-GATES.md` is explicit
that this is self-referential and blind to stable miscompiles. It is also blind
to **anything the compiler's own sources do not contain** — and the docs say so,
repeatedly, each time about a different feature:

- `docs/CLOSURE-CONV-SELF-HOST-IR.md:52` — "The self-host compiler's own sources
  use no first-class functions"
- `docs/TYPED-IR-REWRITE.md:215` — "the fixpoint is blind to this: the
  self-host's own sources use no fn-typed…"
- `docs/FEATURE-AUDIT.md:3437` — "the compiler's own sources use no labeled loops"
- `docs/FEATURE-AUDIT.md:661` — "the self-host's own sources use no aliases, so
  the bootstrap fixpoint is preserved"
- `docs/FEATURE-AUDIT.md:442` — "the self-host compiler's own sources use no free
  generic with a tuple-array…"

Read together these are not five notes, they are one mechanism. A feature the
self-host does not use gets no fixpoint coverage; a feature with no fixpoint
coverage is a risk to adopt in the self-host; so it does not get used. The
ratchet turns toward the smaller subset on its own, and it has been turning for
the whole life of the project; only a deliberate conversion turns it back, and
the `for..in` row is what one looks like. **This is why the census in §1 looks
the way it does** — not because closures or generics fail on the self-host path
(they are implemented there: `irlower.fern` carries 172 `ExprLambda` sites, and
`docs/FEATURE-AUDIT.md` pins generic `Ord`-bound `sort`, the `Iterator[T]`
protocol and a generic collector to the self-host IR path on x86-64 and wasm),
but because nobody has ever had a safe first step to using them *here*.

Breaking it needs a deliberate act: pick one module, adopt one feature, and back
it with `internal/e2eselfhost` coverage (which runs programs the compiler does
*not* contain, and is the gate that actually carries signal here) rather than
with the fixpoint.

**What the first adoption cost (#6993).** `astwalk.fern` gained a generic
`fold_expr` / `fold_stmt` pair taking a fn-typed visitor, and
`collect_calls_stmt` became four lines over it — 169 lines of hand-written
`Expr` + `Stmt` recursion deleted, 164 added that every other traversal can now
reuse, so the slice itself is roughly line-neutral and the payoff is the second
conversion onward. Nothing about the lowering had to change: the shape compiles
on the self-host IR path under `FERN_STRICT_IR=1` on all three targets, and the
per-module fixpoint is unaffected. Two things it hit that the next conversion
will hit too:

- **The visitor could not be an arrow lambda — until it could.** A lambda's
  declared parameter types were resolved only when the enclosing function was
  generic (`checker.go`'s `resolveTypesInBlock` did not descend into
  expression-position lambdas), so a `parser.Expr`-annotated lambda parameter
  stayed an unresolved struct name and every use of it reported
  `expected Expr, got Expr`. The first conversion worked around it with a nested
  named function, which resolves and captures identically. #6996 then fixed the
  cause — and the bug turned out to be neither union-specific nor lambda-specific,
  reaching `var`s in expression position too. Its real cost was the ratchet's:
  nothing exercised closures here, so the one genuine bug in the path went
  unfound.
- **A visitor with no descent control cannot express every walk.**
  `collect_calls_*` converted cleanly because a node it does not record
  contributes nothing on its own, so a uniform pre-order walk is equivalent to
  the hand-written one. `collect_idents_expr` is not: a nested lambda's
  contribution is its body's reads MINUS its own bindings, and descending
  re-adds the names that subtraction removed. Slice two adds the control.

**What the second adoption cost (#6993).** `fold_expr` / `fold_stmt` became
wrappers over a `fold_expr_pruned` / `fold_stmt_pruned` pair taking a second
fn-typed parameter, `descend: (parser.Expr) => boolean`, asked after a node is
visited and before its children are. `collect_idents_expr` and
`collect_qualrefs_expr` / `_stmt` are now visitors over that spine: 260 lines of
hand-written recursion deleted against 60 added, so unlike slice one the slice
is a net subtraction, as predicted.

The prune is a predicate rather than a `(T, boolean)` return or a generic
`Visit[T]` box — the two shapes the slice was expected to choose between —
because it is asked once per AST node on the compiler's hottest walk, and a
boxed answer is an allocation per node where a predicate is a call.

Two findings, and both were only reachable by doing it:

- **`collect_qualrefs_*` did not need descent control after all — it needed a
  bug fixed.** The reason it could not share a pre-order walk was that it
  located a qualified CALL at the enclosing call node, so it had to suppress the
  callee's own field access to avoid recording twice. Native locates the same
  reference at the field access (`checkPublicFunc(mod, fa.Field, fa.P)`, the
  dot). The self-host's visibility diagnostic therefore pointed at the opening
  paren, a module name's width to the right of native's caret, and matching
  native collapses the two arms into one node case. A "we need a design decision
  here" turned out to be a divergence wearing a design decision's clothes.
- **Native mis-lowered the shape the moment a second instantiation appeared.**
  `ast.CloneBlock` shallow-copied a match arm, so every monomorphised clone
  shared one `Bindings` backing array while each got its own deep-copied body.
  shadowrename renamed the second arm's binding in the first instantiation,
  wrote it through the shared array, and rewrote only that instantiation's body
  — leaving every later clone's pattern saying `p$1` and its body saying `p`.
  The stale reference resolved to the other arm's binding of that name and
  lowering read the wrong struct: a build failure when the two arms' payload
  types differ, a segfault when they do not. `astwalk.fold_expr` binds `sl` in
  both its `ExprSlice` and `ExprStructLit` arms and now has three
  instantiations, which is exactly the trigger. Like #6996 in slice one, this
  had been sitting in the path the whole time with nothing to find it.

That is the ratchet's cost stated twice over. Neither slice needed a language
feature built; both needed one *used*, and each turned up a real bug within the
first hour of using it.

**What the third adoption cost (#6993).** The walk gained a STATEMENT visitor:
`fold_expr_nodes` / `fold_stmt_nodes` take `visit_stmt` alongside `visit_expr`,
and `fold_expr` / `fold_stmt` / the pruned pair became wrappers over them.
`collect_idents_stmt` and `collect_bound_stmt` — the last hand-written
recursions in `astwalk` — are visitors over that spine, and two near-duplicate
binder walks elsewhere (`flatten.collect_locals_stmt`,
`asmcore.callgate_bound_names`) were deleted onto it. Net −120 lines across
three modules.

The visitor has to be a PAIR rather than native's single `fn(Node) bool`
(`internal/ast/walk.go:23`) because `Expr` and `Stmt` are separate unions here
with no common supertype, and boxing them into one would cost an allocation per
node. Native folds descent control into the visitor's return; the self-host
keeps the separate predicate slice two chose, for the same reason.

What it cost, and none of it was visible from outside:

- **The no-op statement visitor can only be spelled one way, and it is the
  opposite of slice one's.** A top-level generic `skip[T]` cannot be named as a
  value at all — it monomorphises away and the reference resolves to nothing
  (#7040) — and a nested named function inside a generic keeps its declared `T`
  verbatim when the enclosing function is monomorphised, so it arrives as
  `(Stmt, T) => T` where `(Stmt, string[]) => string[]` was wanted (#7042). Both
  report `re-check failed (compiler bug)` rather than a diagnostic. The arrow
  lambda — the spelling slice one could NOT use, before #6996 fixed it — is the
  one that works. It captures nothing, and costs the same as a top-level named
  no-op (measured, 20M iterations).
- **That lambda then trapped on the self-host wasm backend.** Its `$wrapN`
  trampoline is a bare-typevar pass-through, so it took the #5464 erased
  widening and was emitted `(param i64) (result i64)` against the arity-keyed
  all-i32 `call_indirect` type: `indirect call type mismatch` on the first call.
  The trampoline is not a generic — it copies a lambda's spellings but declares
  no type parameters — so the erased-generic widening never applied to it;
  gating it on the function actually declaring the type variables is the fix.
  **`FERN_STRICT_IR=1` compiled it with exit 0 and `FERN_IR_VERIFY=1` was
  clean.** Only running the module caught it, which is `docs/TEST-GATES.md`'s
  rule about `internal/e2eselfhost` being primary, demonstrated.
- **Three live binder bugs, all of the form "two answers to what does this
  bind".** `PatVariant.at_binding` — the `n` of `n @ Tag(x)` — was known only to
  `irlower.sa_pat_binds`; every capture analysis missed it, so a lambda reading
  the binding declined the lift (`function value n not defined`). Fixing the
  binder set was half of it: `cap_type_in_stmts` also had to type the capture,
  which is the scrutinee's type. `asmcore`'s call gate did not comma-split a
  tuple destructure, so `var (g, h) = pair(); g(1)` was rejected with
  `error[E001]: call to undefined function 'g'` — a valid program refused.
  `flatten`'s copy never entered a `defer`.
- **Native had the same binder bug, in the same shape.** `modload.collectLocals`
  visited statements only, so a `defer { … }` action (a BlockExpr, i.e. an
  expression) hid its locals, and a match arm's `@` binding was not in
  `arm.Bindings`. Both left the name out of the shadow set, and the reference
  was mangled to the module decl: `undefined identifier "shadowlib__K"`, or a
  silent read of the wrong value where the types agree. It now runs over
  `ast.Walk`, so a new binding form is reachable there the moment the shared
  walk reaches it — the same consolidation as the self-host half.

**What the fourth adoption cost (#6993).** The feature was `Map[K, V]`, and the
module `irverify.fern`. Its `NameIndex` — a hand-written 1024-bucket hash (a
polynomial `bucket_of`, a counting pass, a prefix sum, a cursor, a placement
pass, over three parallel flat arrays) — is now `Map[string, i32]`, with the
value doubling as the arity and `-1` reading as both "absent" and "membership
only". **79 lines deleted against 25 added**, no public signature changed, no
call site touched, and the fixed 1024-bucket cap gone with it.

What made this the right first Map rather than a hotter one: the structure is
**never iterated**, only probed, so no iteration order can reach the emitted
output — the constraint `docs/RC-PERCEUS-SELF-HOST-PORT.md:494` puts on every
analysis here. Two things had to be preserved deliberately, and neither had a
test before this slice:

- **First-declaration-wins.** The bucket build placed names in source order and
  the probe returned the first match, so a duplicated name resolved to its first
  declaration. `Map.insert` is last-wins, so the build inserts only when absent.
  Two entries can disagree about arity and the call check reads whichever the
  index kept, which is the difference between an arity report and a false one.
- **The struct-field rebind.** `var m: Map[K, V] = ix.m;` before a map op is
  load-bearing: dispatch keys off the written `Map` type, which a bare field
  read does not carry. It costs nothing measurable.

Costs that are real but bounded: `core/map` enters the compiler's transitive
imports (an ordinary external import — no staging-list edits, and treeshake
prunes it to **+8 KB** on the built compiler), and a `Map` in a struct field is
never freed on the IR path, which here is a handful of boxes per compile because
the index is built per module, not per function.

**It is not a speed-up, and was never going to be** — it replaced a hash table
with a hash table. Measured best-of-3 through the self-host CLI: 180 ms → 184 ms
compiling `lexer.fern`, 3320 ms → 3305 ms compiling `parser.fern`. Both within
noise. The win is 79 lines and a cap: the old build allocated two
`(n_buckets + 1)`-element `i32` arrays per index no matter how few names it
held, and chained past 1024 buckets at the ~3000 names the compiler actually
declares. The scan-to-hash conversions with a real asymptotic win are still
ahead — this slice buys the right to attempt them.

The bug this one turned up was in the **wasm component wrapper**, not the
feature: `component_shape` classified a program's randomness from `random_bytes`
/ `random_i32` alone, while `module_uses_random` — deciding the core module's
import — also counts `core/map`'s `map_hash_seed`. A `Map` program therefore
emitted a core importing `wasi:random`'s `get-random-u64` wrapped in a framing
with nothing to satisfy it, and the component would not instantiate (#7078). It
had to be fixed *before* the adoption, because the compiler's own wasm builds
hit it the moment a compiler module holds a `Map`. Two adjacent divergences fell
out of the same probing and are filed rather than fixed here: a string literal
used as a comparison operand or method receiver leaks 24 B per evaluation on the
self-host where native allocates nothing (#7080), and a `Map` in a struct field
traps on native's wasm target after the second insert where all three self-host
targets are correct (#7081). The second was not a backend divergence in the end:
`core/map`'s COW column claim inc'd an inline (SSO) key's `data` word as a heap
pointer, which only the two-word string ABI reaches, and the self-host escapes
it by carrying its own `Map` runtime in `wasm_ir.fern` rather than that source.

**What the fifth adoption cost (#6993).** The first slice to consume the spine
rather than extend it. `wasm_ir`'s `component_shape` asked sixteen questions of
the module — "does it call `print`? `read_file`? `env`? …" — and each was a
separate full traversal, over a private `expr_calls` / `stmts_call` recursion
that existed only to answer them. It is now ONE walk over `astwalk.fold_stmt`
collecting a `Map[string, boolean]` of called names, and sixteen `has` probes.
**97 lines deleted against 38 added**, and the private walk is gone.

This is the one with a measured win: compiling `checker.fern` (13k lines) to a
wasm component goes **~1515 ms → ~1408 ms, about 7%**, reproducible best-of-5
across rounds. Sixteen walks of a large module is not free.

Equivalence was the thing to prove, and it is proven rather than argued: across
221 corpus programs the classification is identical, and all 35 that reach a
component emit **byte-identical** output.

The interesting finding is a NEGATIVE one, and it corrects an assumption this
document has been carrying. The deleted walk covered 8 of `Stmt`'s 12 variants
and 10 of `Expr`'s 17, with wildcard arms for the rest — `StmtDefer`,
`ExprMapLit` and `ExprFString` among them. That reads like a live blindness of
exactly the kind slice three found in `flatten`. It is not: all three are
**desugared before the compile path reaches this walk**, verified by probe on
both compilers, which is why the corpus comes out byte-identical. A partial walk
here was equivalent to a complete one *by accident of what runs before it*. The
consolidation is worth doing because that accident is not a contract — but the
honest statement is that it removed a latent hazard, not a bug.

**What the sixth adoption cost (#6993).** The feature was `for x in xs`, the
module `checker.fern`: 311 index-style `while` loops became `for` loops and that
module went 13,150 → 12,495 lines. **Behaviourally it cost nothing** —
`scripts/selfhost-emit-hashes` reports the emitted bytes identical across all
1,479 (fixture, target) pairs, the same before-and-after check slice five used
and the one that bites where the fixpoint cannot.

**It is not free in the compiler's own binary: `fern.fern` grows 3.8%**
(21,315,956 → 22,135,156 bytes on x86-64, same tree, checker.fern the only
variable). The desugar is the reason — each `for` opens with an alias of the
iterand and a captured `.len()`, two live locals plus their RC traffic where the
index form had one `i32`, times 311 loops. The elided per-element bounds check
pays some of it back and not all. That is inside `ci-check-driver-sizes`'
5% advisory tolerance, but it is most of it, and a second module converted the
same way will not fit under it: whoever does the next one should expect to
refresh `.github/selfhost-driver-sizes.txt` and should read this as the real
per-loop price of the construct, not as noise.

This is the first slice whose subject is the ratchet's own two rows rather than
a feature row: `for..in` 15 → 326, `x = x + 1` 5,039 → 4,728. Nothing had to be
built and nothing had to be fixed, which is the point — the loops were
convertible the whole time.

68 of `checker.fern`'s index loops are still `while`, and the reasons are worth
carrying to the next module because they are what a mechanical pass cannot take:
45 use the index for more than the element read (a `i > 0` separator, a `return
i`, two arrays walked in lockstep, `xs[i + 1]`), 11 bound on something other
than `xs.len()`, 7 iterate an expression rather than a path, 4 advance the index
more than once, and 1 never reads an element at all. The safe pass is a
fixpoint: converting an outer loop turns `xs[i].ys` into a path and so exposes an
inner loop the first pass could not see.

Six slices, ten bugs. The census rows that remain are not blocked on the
ratchet any more; they are blocked on their own prerequisites.

### 2.5 Types are strings

The self-host carries every type as its printed spelling: `ty: string`,
`ret_type: string`, `type_name: string` — 186 such fields — and re-parses them
on demand. SH-021 improved this: `parser.parse_type_ref` (a real `TypeRef`
tree) is now the single canonical decoder. But it still takes a `string` and
re-parses it at every call, 342 magic-ASCII comparisons remain, and
`ParamDecl.fn_param_types: string` still holds a *list* of types in one string.

This one is closest to self-inflicted — nothing in the language prevents a
`TypeRef` field. But the language does make it expensive: a recursive
`TypeRef[]` allocates on every construction under RC, there is no interning
primitive, and there is no cheap newtype, so "just use the struct" carries a
memory cost the 512-function cap (§2.6) makes real.

### 2.6 The memory cost, and what it forces

`asm_ir.emit_module_ir_gated` bails above **512 functions** per module; 512–1500
functions get rescued by a per-module concat path; and the whole compiler
(~2,040 functions) exceeds even that, so `asm_modload_run.fern` **forks and
execs itself in batches** (`spawn_batch`, `proc_fork` + `proc_exec` + `waitpid`)
to compile itself, communicating through files. A compiler that cannot hold its
own lowering in one 3.875 GiB process is paying for §2.1: the ops stream and its
`Op.str` symbol strings are never freed as locals, only when the whole array
dies (`docs/IR-SELFCOMPILE-OOM-FINDINGS.md`).

Note what the same document *rules out*: a flat AST does not help. The cost is
RC, not node representation.

---

## 3. Missing features, ranked by measured cost

Ranked by measured cost, highest first.

### 3.1 A traversal abstraction (needs usable closures)

~130 `Expr` walkers and ~247 `Stmt` walkers are hand-written, each
re-enumerating 17 `Expr` and 12 `Stmt` variants. 2,557 wildcard `_ =>` arms mean
a new AST node silently no-ops in most of them rather than failing to compile.
This is the single largest line-count and correctness cost in the tree, and the
fix is one generic visitor parameterised by a callback — which needs closures
usable in the self-host (§2.4).

### 3.2 An associative container in the bootstrap subset

The §2.2 consequence, priced: 65 hand-built string-keyed registries, 114 lookup
helpers, 290 linear-scan sites, and a linear-lookup compile time nobody has
measured because it is not the current bottleneck.

### 3.3 Character literals

**The syntax landed (#6991):** `'x'` is a `char` and `b'x'` is a `u8`, on both
compilers. The split is the point — `s[0]` yields `u8`, so byte-level code
compares against `b'['` while `'['` stays a scalar and neither converts to the
other or to i32 without a cast.

What has not moved is the tree itself: the **342** decimal byte comparisons and
the share of the **311** `as i32` casts they force are still written out, and
converting them is a separate mechanical pass. The `char`-typed stdlib
accessors (#5629 slice 5's deferred half) are still open too — the literal is
`char`'s first producer in user code, not in the stdlib.

### 3.4 Multi-return / cheap tuples

Three files carry the identical comment "Fern has no multi-return here; the
carry is prepended as a FIRST element (0 or 1) and the caller splits it off"
(`arm64_native.fern:2878`, `watbin.fern:419`, `x86_native.fern:2102`). Tuples
exist; what is missing is a tuple return that is free, so the workaround is a
sentinel element in an `i32[]`.

### 3.5 An error channel that survives state threading

`lexer.fern:46`: "Fern has no exception / multi-return-with-error idiom in this
port, so the error rides the token stream". `irlower.fern` does the same thing
at a larger scale: `LowerState` has `ok: boolean` + `fail_why: string` and **521
`if (!x.ok)` guards**, a hand-rolled error monad. `Result` + `?` exist and work
(§7) — but `Result[LowerState, Bail]` allocates a box per lowering step, and the
subset ratchet has never let anyone try it.

### 3.6 Nested containers that survive the fixpoint

`irlower.fern:22091` records that a `string[][]` `.append` "the self-host
compiler can't yet compile on itself — the fixpoint link gate caught it", so
`PreciseDrops` is two parallel flat arrays. `literate.fern:36` records the same
shape choice for a different reason: the nested array-of-chunks model
"reproducibly segfaulted / returned corrupted >8-byte strings under x86-64 and
arm64 codegen while the AST interpreter ran it correctly", with the trigger
never pinned down. That second one is an open, unexplained codegen bug being
routed around by data-model choice, and it should be pinned rather than left as
a documented workaround.

### 3.7 Field mutation

`s.n = 5` is E048 by design; every update is `T { ...old, n: v }`. For most
programs this is fine and fast (§7 — 200k struct rebuilds in 5 ms). For a
compiler it means the entire lowering state is rebuilt on every emit, which is
what makes §2.1's inference load-bearing rather than a nice-to-have.

---

## 4. Existing features that are wrong or half-built

Each of these reproduced on `cmd/fern` when this was written; the ones still
marked open reproduce today. §7 lists the probes.

### 4.1 f-string interpolants had no source positions — fixed (#6997)

Every AST node inside every f-string carried a position measured from the
interpolant's own text, so `print(f"{zzz}\n")` put the caret on line 1 column 1
— the file's first token — while the same expression outside an f-string
reported correctly. `parseExprFromText` (`internal/parser/parser.go`) re-lexes
the interpolant with a fresh parser numbering from 1:1; it now rebases every
token onto the enclosing file before parsing, so both the AST nodes and any
parse error inside the interpolant land where the reader wrote them.
`lexer.fern`'s `rebase_parts` does the same on the self-host side.

The census row it plausibly explains has not moved: the self-host still uses 235
f-strings against 11,914 string-literal `+` concatenations.

### 4.2 A failed `var` inference deleted the binding, cascading E001 everywhere — fixed (#5317)

```fern
function main(): i32 {
    var b = nosuch();     // E001: undefined identifier "nosuch"   ← correct
    return b;             // E001: undefined identifier "b"        ← spurious
}
```

With `var b: i32 = nosuch();` there was exactly one error: the recovery path for
an un-annotated `var` dropped the binding instead of poisoning it, so every
later use was a fresh, genuine-looking "undefined identifier". Against 96%
annotated locals in the self-host, that looks less like a coincidence than a
habit the compiler taught.

The binding is now registered at the erroneous type (`scope.poison`), which is
the state `s.lookup` already models as "reported, ask no further questions" —
and is what the self-host checker has always done (`check_stmt` binds whatever
`check_expr` returned, unknown included). `let (a, b) = …` and
`let S { f } = …` had the same hole, multiplied by the pattern's width, and
poison the whole group on every path that gives up. This is the *binding* half
of #5317; the tainted-symbols set that stops a type-MISMATCH fanning out is
still open.

### 4.3 The derive hints told you to write something that does not work — fixed (#6990)

```
error[E045]: map key type Key is not supported — a struct used as a key must
             derive Eq and Hash (`@derive(Eq, Hash)`)
```

Following that verbatim gave `error[E021]: @derive(Eq): unknown trait`. The same
defect ran through E041 (`Eq` / `Ord`) and E038 (`Display`): four sites naming a
bare trait name that resolves to nothing. Every one now prints
`@derive(cmp.Eq, cmp.Hash)` and names `import "core/cmp";` alongside it. This is
still the prelude-less module system's sharpest edge — `Eq` is not a name until
you import it — so a hint spelling a stdlib trait has to spell the import too.

### 4.4 `unknownTypeHint` suggested replacing two types that exist — fixed (#6990)

E064's respelling table offered "did you mean `string`?" for `str` and "did you
mean `u32`?" for `u8`. Both are real, type-checking Fern types (§7) — `str` is
`ast.StrType`, `u8` is a `NumberType` — so the branches were dead and would have
given wrong advice if they ever fired. Both entries are gone.

### 4.5 The append cliff is correct, invisible, and inexpressible

This one is **not a bug** — it is filed here because the *language* offers no way
to see it or to say what you meant.

```fern
var xs: i32[] = [];
while (i < n) { xs = xs.append(i); i = i + 1; }        // 200,000 appends: 4 ms
```

```fern
while (i < n) {
    var keep: i32[] = xs;                              // ← the only change
    xs = xs.append(i);
    if (keep.len() > n) { return 7; }                  // keep is LIVE here
    i = i + 1;
}
```

| n | time |
|---|---|
| 25,000 | 30 ms |
| 50,000 | 116 ms |
| 100,000 | 455 ms |

Exactly quadratic (4× time per 2× n), from a one-line change. And **the copies
are mandatory**: `keep` is read after the append, so it observes the old buffer
and in-place growth would corrupt it. #6024 established exactly this split —
a *dead* alias forcing a copy was the bug (fixed in #6026); a *live* one is the
semantics working.

Which is the point. The complexity class of a loop turns on whether a binding
three lines away is read again, the compiler computes that precisely
(`LowerState.aliased_names` on the self-host, `computePreciseDrops` on native),
and then it tells nobody. There is no diagnostic mode that reports which
`.append` / `.with` sites took the copying path, no annotation that says "I want
a snapshot here, charge me for it" or "this must stay in-place, error if it
cannot", and no way to assert either in a test. A performance property this
sharp should not be invisible in the source and unpinnable in CI.

The self-host stays off this cliff by shaping its *data* around it — parallel
arrays, flat `Piece` lists — rather than by writing the algorithm it wanted,
because shaping the data is the only lever the language actually gives it.

### 4.6 The variant namespace is flat, and the self-host pays for it

`Color.Red` and `Status.Red` coexist and the checker requires qualification
(E036) — fine at the surface. But the desugar underneath declares each variant
as a `StructDecl` **under its bare name**, so two enums sharing a variant name
produce two decls that name-based lookup cannot distinguish.
`ir.fern:47`'s `Op.decl: i32` field exists solely to work around this: its
comment records that "a backend resolving `str` by name gets whichever was
declared first — reading the other enum's field types and widths". An
integer index threaded through every op, to undo a namespace decision.
`docs/IMPROVEMENTS.md` item 15 has this as an open language-design question; it
already has a concrete cost.

---

## 5. What is *not* the problem

Worth stating, because effort has gone the wrong way here before.

- **Integer and float semantics.** Wrapping, total division, truncation toward
  zero, `0/0 == 0`, bit-exact `parse_f64` across four independent copies pinned
  to `strconv.ParseFloat`. Probed identical on interp and x86-64. This area is a
  genuine strength.
- **The flat-AST idea.** Measured and ruled out
  (`docs/IR-SELFCOMPILE-OOM-FINDINGS.md`): the OOM is RC, not node layout.
- **The `LowerState` rebuild.** Also measured and ruled out (same doc's 2026-06-21
  correction): clean self-reassign threading is in-place even at 45 fields and 20
  parallel arrays. The clone fires only on a read-after-thread.
- **Diagnostics as a whole.** 63 codes with `fern explain` pages, aggregated
  errors, Levenshtein hints, caret rendering. The defects in §4 are defects *in* a
  good system, not evidence against it.
- **The machine-code encoders.** `x86_native`, `arm64_native`, `elf` — small,
  single-responsibility, pinned against `llvm-mc`. `docs/SELF-HOST-AUDIT.md`
  grades them A and it is right.
- **Code quality in the self-host generally.** The comments are unusually good
  and repeatedly record real bug history. Almost everything ugly in that tree is
  ugly for a reason recorded three lines above it.

---

## 6. Recommended order of attack

Ordered by (unblocking value) ÷ (cost), not by size.

1. ~~**Fix f-string positions** (§4.1)~~ — done (#6997), on both compilers.
   Diagnostics inside an f-string can now be trusted, which removes one reason
   to keep writing `+` chains.
2. ~~**Character literals** (§3.3)~~ — the syntax is in (#6991), on both
   compilers. What remains is the mechanical pass that converts the 342 magic
   constants and the `as i32` casts they force.
3. ~~**Poison, don't delete, a failed `var` binding** (§4.2)~~ — done (#5317),
   along with the derive hints (§4.3) and the dead `unknownTypeHint` branches
   (§4.4). The rest of #5317 — suppressing the *type-mismatch* fan-out with a
   tainted-symbols set — is the same size again and still open.
4. **Break the fixpoint ratchet deliberately** (§2.4). Pick one self-host module
   and one feature — closures are the highest-value, per §3.1 — adopt it there,
   and gate it on `internal/e2eselfhost` rather than the fixpoint. This is a
   process decision more than a code change, and nothing else on this list
   compounds without it.
5. **Land the owned-by-default flip** (§2.1). Large, already planned in
   `docs/OWNERSHIP-INFERENCE-PLAN.md` §3–4, and the only thing that converts
   the RC work from an endless per-leak grind into a closed problem. Everything
   in §2.6 (the 512-function cap, the fork/exec self-compile) is downstream of
   it. While it lands, stop erasing `str` before the IR builder, or accept that
   `str` can never pay for itself.
6. **Give the bootstrap subset a map** (§2.2). Either make `core/map` reachable
   from the self-host, or land the `SymTab` from
   `docs/SELFHOST-SYMBOL-INTERNING.md` and route the 65 string-tag registries
   through it. The interning plan already exists and is unblocked.
7. **Multi-file packages, or intra-package import cycles** (§2.3). The largest
   language change here, and the only fix for a 60,552-line file and a 397-file
   staging edit. Worth scoping even if it is not worth doing yet.
8. **Surface the append cliff** (§4.5). A diagnostic mode reporting which
   `.append` / `.with` sites took the copying path, and a way to assert it in a
   test. The analysis already exists on both compilers; it has no output.
9. **Pin the nested-array codegen bug** (§3.6). `literate.fern:36` documents a
   reproducible segfault/corruption that has been routed around rather than
   fixed. A workaround with an unknown trigger is a bug with a countdown.

---

## 7. Probes

Every behavioural claim above was checked against `cmd/fern` built from this
commit, on `-interp` and `-target x86-64-linux`. Reproductions:

| Claim | Probe |
|---|---|
| §4.1 f-string positions | `print(f"{zzz}\n")` vs `print(zzz)` — both on the interpolant since #6997 |
| §4.2 cascading E001 | `var b = nosuch(); return b;` — one diagnostic, matching the annotated form |
| §4.3 derive hint | `@derive(Eq, Hash)` vs `@derive(cmp.Eq, cmp.Hash)` + `import "core/cmp"` |
| §4.4 dead hints | `var t: str = s[1:3];` and `var b: u8 = 3;` both check clean |
| §4.5 append cliff | the two loops in §4.5 at n = 25k/50k/100k |
| §2.3 import cycles | two mutually-importing modules → `import cycle detected` |
| §5 numeric semantics | `-7 % 3`, `-7 / 3`, `2147483647 + 1`, `0 / 0` — identical on both engines |
| §3.7 field mutation | `s.n = 5` → E048; `S { ...s, n: v }` × 200k → 5 ms |
| recursive ADTs, multi-payload enums, wide struct fields, fn-value arrays, `?` | all work natively — the self-host's non-use of them is not a capability gap |
