# What the language does to the self-hosted compiler

An analysis of `examples/self_host/` (91 files, 167,213 lines) asking one
question: **which parts of Fern make writing the self-hosted compiler harder
than it needs to be?** Not "which parts of the self-host are badly written" —
that is `docs/SELF-HOST-AUDIT.md`, and its findings are largely structural
cleanups. This is the layer under that: the language features that are absent,
that are present but unusable in this program, or that are present with
semantics that push the code into a worse shape.

Method: a census of what the self-host's sources actually use, read against the
feature surface the language actually has (`docs/FEATURE-AUDIT.md`), plus
`cmd/fern` probes for every behavioural claim. Every number below is
reproducible from the tree at the commit this landed on; the probes are in §7.

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
That last one is the important one: it is a ratchet, and it only turns one way.

---

## 1. The census

What a 167k-line compiler written in a modern language uses, counted across
`examples/self_host/*.fern`:

| Construct | The language has it | Self-host uses it |
|---|---|---|
| Generic functions / structs | ✅ monomorphised, with trait bounds | **2** (`astwalk.fold_expr` / `fold_stmt`, #6993) |
| Closures / lambdas | ✅ `(x: T) => e`, escaping + capturing | **1** (`astwalk.collect_calls_stmt`'s visitor, #6993) |
| `for x in xs` | ✅ arrays, strings, `Iterator[T]` | **0** |
| `?` error propagation | ✅ incl. `From`-converting widening | **0** |
| Hash map (`Map[K, V]`) | ✅ i32/string/`@derive(Eq, Hash)` keys | **0** |
| `enum` with payloads | ✅ multi-payload, named fields | **2 declarations** |
| `Option[T]` / `Result[T, E]` in return position | ✅ | **20** of 4,676 functions (0.4%) |
| stdlib (`std/*`, `core/*`) | 61 modules | **`std/io` only** (19 imports) |
| `while` + manual index | — | **4,573** loops, 1,979 `i = i + 1` |
| `-1` as "absent" | — | **497** `return 0 - 1` |
| String-tagged side tables (`"SFRRECV:"`, `"BORROW:"`, …) | — | **65** distinct tag namespaces |
| Magic ASCII byte constants (`== 91`, `== 44`) | — | **342** |
| Explicit `as` casts | — | **3,275** |
| Hand-written AST walkers | — | **~130** over `Expr`, **~247** over `Stmt` |
| Wildcard `_ =>` match arms | — | **2,364** of 8,606 arms (27%) |
| Locals with a written type annotation | inference exists | **17,084** of 17,727 (96%) |

The single largest file, `irlower.fern`, is **56,702 lines** and contains a
**1,634-line function** (`lower_call_method`). `LowerState`, the value threaded
through all of it, has **30 fields**, eight of which are `string[]` sets
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

- **A compiler with no hash map.** Zero `Map[K, V]` locals in 167k lines. Every
  symbol table, every "is this name in the set" test, every name→fact lookup is
  a linear scan over a `string[]`: 290 sites comparing an array element to a
  name, 114 hand-rolled `contains`/`index_of`/`find` helpers. The 65 string-tag
  namespaces of §2.1 are, structurally, a hash map implemented in the string
  type.
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
56,702-line file, and the functions inside it grow to 1,634 lines because
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
ratchet only turns toward the smaller subset, and it has been turning for the
whole life of the project. **This is why the census in §1 looks the way it
does** — not because closures or generics fail on the self-host path (they are
implemented there: `irlower.fern` carries 172 `ExprLambda` sites, and
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

- **The visitor cannot be an arrow lambda.** A lambda's declared parameter types
  are resolved only when the enclosing function is generic (`checker.go`'s
  `resolveTypesInBlock` does not descend into expression-position lambdas), so a
  `parser.Expr`-annotated lambda parameter stays an unresolved struct name and
  every use of it reports `expected Expr, got Expr` — #6996. A nested named
  function — which resolves, and captures just the same — is the spelling that
  works.
- **A visitor with no descent control cannot express every walk.**
  `collect_calls_*` converted cleanly because a node it does not record
  contributes nothing on its own, so a uniform pre-order walk is equivalent to
  the hand-written one. `collect_qualrefs_*` is not: it records a qualified call
  at the CALL's position and must then not re-record the callee's field access
  at its own. Converting that family needs the visitor to be able to say "do not
  descend", which this pair deliberately does not model yet.

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
re-enumerating 17 `Expr` and 12 `Stmt` variants. 2,364 wildcard `_ =>` arms mean
a new AST node silently no-ops in most of them rather than failing to compile.
This is the single largest line-count and correctness cost in the tree, and the
fix is one generic visitor parameterised by a callback — which needs closures
usable in the self-host (§2.4).

### 3.2 An associative container in the bootstrap subset

The §2.2 consequence, priced: 65 hand-built string-keyed registries, 114 lookup
helpers, 290 linear-scan sites, and a linear-lookup compile time nobody has
measured because it is not the current bottleneck.

### 3.3 Character literals

`'['` is a lexer error, so byte constants are decimal: 342 of them. `s[0]`
yields `u8` and comparing it to an i32-inferred local is E041, so the tree
carries 311 `as i32` casts (3,275 casts total). `char` exists as a *type* — with
no literal syntax and no producers. The cheapest high-value fix on this list.

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

Each of these reproduces on `cmd/fern` at this commit. §7 lists them.

### 4.1 f-string interpolants have no source positions

```fern
import "std/i32";
function main(): i32 {
    print(f"{zzz}\n");
    return 0;
}
```

```
g1.fern:1:1: error[E001]: undefined identifier "zzz"
    import "std/i32";
    ^~~
```

The caret is on the import. The control without the f-string reports `2:11`,
correctly. Cause: `parseExprFromText` (`internal/parser/parser.go:100`) re-lexes
the interpolant from raw text with a fresh parser whose positions start at 1:1,
and never rebases them onto the enclosing file. Every AST node inside every
f-string in every Fern program carries a bogus position.

This is a first-order defect for a project whose stated position is that "the
compiler is the only thing that teaches the user about their own language". It
also plausibly explains a census row: the self-host uses 235 f-strings against
11,914 string-literal `+` concatenations. The self-host's own lexer mirrors the same
design (`lexer.fern`'s `FStringPart { expr: string }` hands raw text on), so it
will inherit the bug when it grows position rendering.

### 4.2 A failed `var` inference deletes the binding, cascading E001 everywhere

```fern
function main(): i32 {
    var b = nosuch();     // E001: undefined identifier "nosuch"   ← correct
    return b;             // E001: undefined identifier "b"        ← spurious
}
```

With `var b: i32 = nosuch();` there is exactly one error. So the recovery path
for an un-annotated `var` drops the binding instead of poisoning it, and every
later use produces noise. Against 96% annotated locals in the self-host, this
looks less like a coincidence than a habit the compiler taught.

### 4.3 The E045 hint tells you to write something that does not work

```
error[E045]: map key type Key is not supported — a struct used as a key must
             derive Eq and Hash (`@derive(Eq, Hash)`)
```

Following it verbatim gives `error[E021]: @derive(Eq): unknown trait`. The
working spelling is `@derive(cmp.Eq, cmp.Hash)` **plus** `import "core/cmp";` —
neither of which the hint mentions. A hint that does not compile is worse than
no hint, and this is the prelude-less module system's sharpest edge: `Eq` is not
a name until you import it.

### 4.4 `unknownTypeHint` suggests replacing two types that exist

`internal/checker/checker.go:5701` offers "did you mean `string`?" for `str` and
"did you mean `u32`?" for `u8`. Both `str` and `u8` are real, type-checking Fern
types (§7) — `str` is `ast.StrType`, `u8` is a `NumberType`. These
branches are dead, and would give wrong advice if they ever fired.

### 4.5 The append cliff: one alias turns amortised O(1) into O(n²), silently

```fern
var xs: i32[] = [];
while (i < n) { xs = xs.append(i); i = i + 1; }        // 200,000 appends: 4 ms
```

```fern
while (i < n) {
    var keep: i32[] = xs;                              // ← the only change
    xs = xs.append(i);
    if (keep.len() > n) { return 7; }
    i = i + 1;
}
```

| n | time |
|---|---|
| 25,000 | 30 ms |
| 50,000 | 116 ms |
| 100,000 | 455 ms |

Exactly quadratic (4× time per 2× n), from a one-line change, with no
diagnostic, no annotation to opt out of, and no way to ask the compiler whether
a given site took the in-place path. This is the user-facing face of §2.1:
`LowerState.aliased_names` is the analysis, and when it says "aliased" the
program silently gets a different complexity class. The compiler knows; the
language gives it no way to tell you.

The self-host's own reclaim registries exist to keep *itself* off this cliff —
which is why the workaround shows up as a data-model choice (parallel arrays,
flat `Piece` lists) rather than as an algorithm choice.

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

1. **Fix f-string positions** (§4.1). Hours. Rebase interpolant positions onto
   the enclosing file in `parseExprFromText`, mirror in `lexer.fern`. Unblocks
   trusting any diagnostic inside an f-string, and removes a reason to keep
   writing `+` chains.
2. **Character literals** (§3.3). Days. Deletes 342 magic constants and a large
   share of 311 `as i32` casts on sight, and gives the already-shipped `char`
   type its first producer.
3. **Poison, don't delete, a failed `var` binding** (§4.2) and **fix the E045
   hint** (§4.3), and delete the two dead `unknownTypeHint` branches (§4.4).
   Small, independent, each removes a paper cut that has shaped self-host style.
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
   language change here, and the only fix for a 56,702-line file and a 397-file
   staging edit. Worth scoping even if it is not worth doing yet.
8. **Surface the append cliff** (§4.5). At minimum a diagnostic mode that
   reports which `.append` / `.with` sites took the copying path — the analysis
   already exists in `LowerState.aliased_names`, it just has no output.
9. **Pin the nested-array codegen bug** (§3.6). `literate.fern:36` documents a
   reproducible segfault/corruption that has been routed around rather than
   fixed. A workaround with an unknown trigger is a bug with a countdown.

---

## 7. Probes

Every behavioural claim above was checked against `cmd/fern` built from this
commit, on `-interp` and `-target x86-64-linux`. Reproductions:

| Claim | Probe |
|---|---|
| §4.1 f-string positions | `print(f"{zzz}\n")` vs `print(zzz)` — 1:1 vs 2:11 |
| §4.2 cascading E001 | `var b = nosuch(); return b;` vs the annotated form |
| §4.3 derive hint | `@derive(Eq, Hash)` vs `@derive(cmp.Eq, cmp.Hash)` + `import "core/cmp"` |
| §4.4 dead hints | `var t: str = s[1:3];` and `var b: u8 = 3;` both check clean |
| §4.5 append cliff | the two loops in §4.5 at n = 25k/50k/100k |
| §2.3 import cycles | two mutually-importing modules → `import cycle detected` |
| §5 numeric semantics | `-7 % 3`, `-7 / 3`, `2147483647 + 1`, `0 / 0` — identical on both engines |
| §3.7 field mutation | `s.n = 5` → E048; `S { ...s, n: v }` × 200k → 5 ms |
| recursive ADTs, multi-payload enums, wide struct fields, fn-value arrays, `?` | all work natively — the self-host's non-use of them is not a capability gap |
