# Trait usage audit & migration plan

*Audit only — no code changes. Surveys where Fern's own `.fern` code (the
self-host compiler + the stdlib) could make better use of the trait system,
ranked by value × feasibility, with a phased plan.*

## TL;DR

- The trait **machinery** is mature on the **native** compiler: `Display`,
  `Debug`, `Eq`, `Ord`, `Hash`, `Default` (`core/cmp.fern`), `Iterator`
  (`core/iter.fern`), `Add`/`Sub`/`Mul`/`Div`/`Neg`/`Zero`/`Num`
  (`std/num.fern`), `From`/`Into`/`TryFrom`/`TryInto` (`std/convert.fern`),
  `Json` (`std/json.fern`), `Error` (`std/error.fern`); plus bounded generics,
  supertraits, default methods, associated types, `dyn`, and
  `@derive`. Static dispatch via monomorphisation.
- **The stdlib only `impl`s these traits for primitives.** Its own
  collection/aggregate types and `Iterator` for arrays/maps are largely
  missing — that's the safe, ships-today opportunity.
- **The self-host compiler (`examples/self_host/*.fern`) uses essentially no
  traits, and *cannot* yet** — its own frontend can't compile `trait`/`impl`
  (`checker.fern:244` "the self-host compiler has no trait / impl";
  `checker.fern:3703` "no traits yet"). So any trait adoption *in self-host
  source* is gated on first teaching the self-host frontend traits — which is
  exactly roadmap goal #1 (widen the self-host/IR subset until the AST
  fallback disappears).
- **Not everything that looks like a trait candidate is one.** The biggest
  repetition in self-host — the `o.kind == "..."` dispatch chains — is a
  *closed* sum modelled as a stringly-typed struct. The right fix there is a
  real enum + `match` (as the native side already does), **not** traits.

---

## 1. Current trait inventory (what exists today)

| Trait | Defined in | `impl`'d for | Consumed by |
|---|---|---|---|
| `Display` / `Debug` | `core/cmp.fern:29,70` | i32/i64/u32/u64/string/boolean | `print`, `assert_eq`, `@derive` |
| `Eq` / `Ord` / `Hash` | `core/cmp.fern:33,40,49` | numeric + string (+bool for Eq/Hash) | `min`/`max`/`clamp`/`sort`/`contains`/`distinct`, test runner |
| `Default` | `core/cmp.fern:58` | numerics + string + boolean | `@derive`, generic zero-values |
| `Iterator[T]` | `core/iter.fern:21` | **only `Range`** | `count`/`to_array`/`fold`/`map`/`filter` |
| `Add`…`Num` | `std/num.fern:30‑43` | i32/i64/u32/u64/f32/f64 | (currently almost nothing — see A2) |
| `From`/`Into`/`Try*` | `std/convert.fern` | — | — |
| `Json` | `std/json.fern:662` | scalars + `@derive` | serialisation |
| `Error` | `std/error.fern:19` | `dyn error.Error` users | `Result[T, dyn Error]` |

Self-host compiler source, by contrast, expresses the same *concepts* by hand:

- **~28 type predicates** as free functions in `asmcore.fern`
  (`ty_is_i32`…`ty_is_array_string`, `is_int_tag`, `is_map`, `is_option`, …;
  lines 705–1053).
- **Per-type equality, one copy each:** `ty_eq` (`asmcore.fern:1136`),
  `type_eq` (`checker.fern:147`), `value_eq` (`interp.fern:914`),
  `op_type_name_eq` (`vm.fern:303`).
- **Derive done as AST synthesis of free functions:** `synth_struct_eq` /
  `synth_struct_hash` / `synth_enum_eq` / `synth_enum_hash`
  (`parser.fern:8172‑8374`).
- **Opcode dispatch as `kind: string` tag** (`ir.fern:55`) switched on with
  200+-branch `o.kind == "..."` chains in `asm_ir.fern` / `wasm_ir.fern` /
  `asm_arm64_ir.fern`.

---

## 2. The governing constraint

The self-host compiler is *today* built by the **native** compiler (which has
traits), so in principle its source could use traits now. **But** the standing
goal is for the self-host compiler to compile itself, and it can't compile
traits. Therefore:

> Putting traits into `examples/self_host/*.fern` **before** the self-host
> frontend supports traits trades a small readability win for a regression in
> the bootstrap goal. Trait adoption in self-host source must follow
> self-host trait support, not precede it.

The stdlib already uses traits heavily, which means **the self-host compiler
cannot currently compile the trait-using stdlib modules** (`cmp`, `num`,
`json`, `convert`, `iter`, `error`) — only programs that avoid them. Closing
that gap *is* roadmap goal #1.

---

## 3. Ranked opportunities

Scored by **Value** (readability/reuse/perf) and **Feasibility today**
(does the native compiler — and, where relevant, the self-host one — support
it now).

### Track A — works under today's native compiler (ships value now)

**A1. `impl Iterator[T]` for the stdlib's own collections.** *(Value: High,
Feasibility: High.)* `Iterator` exists with a full generic combinator suite
(`map`/`filter`/`fold`/`to_array`) but is implemented for `Range` only
(`core/iter.fern:36`). Adding impls for array slices and `map` keys/values
makes the whole combinator library usable on the types people actually have,
which is the single highest leverage-per-line trait change in the codebase.

**A2. Route generic numeric code through `Num`.** *(Value: Medium,
Feasibility: Medium.)* `std/num.fern` defines `Add`…`Num` and impls them for
all six numeric types, but almost nothing *consumes* them — operators are
built-in, so the traits are currently dead weight. Either (a) add a few
generic helpers bounded on `Num`/`Ord` (a `sum[T: Num]`, `product`, generic
`abs`) to give the traits a reason to exist, or (b) document them as the
extension hook for user numeric types. Decide intent; don't leave them unused.

**A3. Tighten `@derive` reuse in the stdlib.** *(Value: Low–Med,
Feasibility: High.)* Audit std types that hand-roll `to_string`/`eq` for ones
that could be `@derive(Display, Eq, Hash)` instead — smaller surface, and it
exercises the derive path.

### Track B — self-host compiler source (gated on Phase 0)

**B0 (prerequisite). Teach the self-host frontend traits.** *(Value: High —
it's the roadmap; Feasibility: this is the work.)* Parse `trait`/`impl`/
bounds (`parser.fern`), check conformance + coherence (`checker.fern`, today
"no traits yet"), and lower trait method calls (monomorphise → mangled
receiver-method calls, the shape native already emits). Until this lands,
B1–B3 are blocked for self-host *and* the trait-using stdlib can't go through
the self-host path. **This is the recommended next roadmap slice.**

**B1. `ty_is_*` / `is_*` predicate family → methods on `Ty`.** *(Value: Med,
Feasibility: High — needs no traits.)* The ~28 free predicates in
`asmcore.fern` all `match` on the closed `Ty` enum. They don't need a *trait*
(a closed enum is the textbook case where `match` beats trait dispatch); they
want to be **receiver methods** so call sites read `t.is_i32()` not
`ty_is_i32(t)`. Pure cleanup, no dependency on B0. Listed here because it's the
change people *expect* to be "traits" but shouldn't be.

**B2. Unify per-type `Eq`/`Hash` once B0 lands.** *(Value: Med, Feasibility:
post-B0.)* `ty_eq`, `type_eq`, `value_eq` become `impl Eq for Ty/Type/Value`,
and the `synth_*_eq`/`synth_*_hash` derive machinery
(`parser.fern:8172‑8374`) can target the trait instead of synthesising bare
functions — unlocking `contains`/`distinct`/`assert_eq` for compiler types.

**B3. (Explicit non-goal as traits) `Op.kind` dispatch.** *(Value: High, but
**not** via traits.)* The 200+-branch `o.kind == "..."` chains across the IR
backends are the largest repetition in the tree, but `Op` is a closed opcode
set stored as a `kind: string` tag (`ir.fern:55`). The correct fix is to make
the opcode a **real enum/sum type and `match`** on it (mirroring native's
`OpKind`), which also kills a class of stringly-typed bugs. Trait-per-opcode
would add an open-extensibility mechanism the IR doesn't want and wouldn't
read better than an exhaustive match. Track this as a *sum-type* refactor, not
a trait one.

---

## 4. Recommended phasing

1. **Phase 1 (now, native, low risk): A1** — `Iterator` impls for arrays +
   map keys/values, with e2e tests exercising the combinators. Immediate,
   self-contained value; no bootstrap risk.
2. **Phase 2 (now, native): A2 + A3** — give `Num` a purpose (or document it);
   convert eligible std hand-rolls to `@derive`.
3. **Phase 3 (now, self-host, no trait dep): B1** — predicate family → `Ty`
   methods. Cleanup that doesn't wait on anything.
4. **Phase 4 (roadmap): B0** — self-host frontend trait support. The big one;
   unblocks the trait-using stdlib on the self-host path and is goal #1.
5. **Phase 5 (post-B0): B2** — migrate self-host per-type `Eq`/`Hash` to
   `impl`s; point the derive synthesiser at the trait.
6. **Separately (not traits): B3** — `Op.kind` string-tag → enum + `match`.

## 5. Honest caveats

- **Closed sums prefer `match`.** `Ty`, `Op`, `Value` are fixed sets owned in
  one place; exhaustive `match` is clearer and gives exhaustiveness checking.
  Reserve traits for *open* extension across types you don't own / want users
  to extend.
- **Monomorphisation cost.** Static dispatch means each instantiation emits
  code; heavy generic+bounds use grows binary size. Fine at current scale,
  worth remembering for the "small fast-startup CLI" target.
- **Bootstrap ordering is load-bearing.** Don't sprinkle traits into self-host
  source ahead of B0, or the self-host build of itself regresses.
