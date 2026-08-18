# Self-host symbol interning (#4394 lever 1) — design + phasing

Status: **design** (SH-021 decoder migration complete; this lever is now
unblocked). This note scopes #4394 lever 1 into reviewable slices with the
byte-identity + measurement gates each must clear. It is the interning analogue
of the SH-021 entries in `docs/SELF-HOST-AUDIT.md` (T2): a plan the code slices
consume, not a spec.

## What the measurement says the target is

`docs/IR-SELFCOMPILE-OOM-FINDINGS.md` is definitive about where the surviving
self-compile memory lives: the dominant strings are the **`Op.kind` / `Op.str`
fields living in the persistent op-stream arrays** (findings at
`:361`–`:368`). They are never dropped as locals/temps (`__fern_str_dec` never
sees them); they are freed only when the `Op` array itself is. String-side
*reclamation* is finished and does not move the cap — so the lever is to stop
**allocating** these strings in the first place, which is precisely #4394 lever
1 (symbol interning) for `Op.str` and #4394 lever 2 (int op-tags) for
`Op.kind`.

This note covers lever 1 (`Op.str` = mangled callee / symbol names). Lever 2 is
tracked separately.

## Where the churn is created

Two allocation sites named in the issue, both confirmed in the self-host
sources:

- **`lexer.fern` `scan_ident`** — allocates a fresh heap substring
  `l.src[begin:l.i]` per identifier *occurrence*. Every `x` in the program is a
  distinct heap string; every later pass compares them by content.
- **`flatten.fern` `mangle_bare`** — allocates `prefix + "__" + name` per
  reference rewrite (`ctx.prefix + "__" + name`, and the imported-variant /
  re-export variants). These mangled names are exactly what rides the persistent
  `Op.str` stream into codegen.

An intern table (name → i32 symbol id, names stored once) turns the
dominant compare-heavy, churn-heavy string traffic into i32 ops, and lets the
persistent op arrays hold ids instead of N copies of each name.

## Why this needed SH-021 first

Symbol ids ripple into the type system: type names (`i32`, `Foo`,
`Map[K, V]`, struct names) are identifiers too. As long as the type system
*decoded type-name strings by hand* (the magic-byte comma/bracket scans), an
interned/id representation would have broken those decoders. SH-021 removed that
coupling: every genuine canonical-type-spelling decoder now routes through the
structured `TypeRef` (`parser.parse_type_ref`) — asmcore, the checker's
resolvers + `count_type_args`, wasm's extern-sum / payload / tuple decoders,
irlower's `tuple_type_elem_tag`, flatten's tuple mangle. The remaining
string-shaped type handling is the unambiguous `[]`-suffix strips and the
internal spaceless CSV tag encodings, neither of which a symbol id disturbs. So
the type system no longer stands in the way of interning the *identifier* space.

## The interning table

A `SymTab` owned high enough to span a whole compile (created once per
top-level driver invocation, threaded like `Lex`/`EmitState`):

- `intern(name: string) -> i32` — returns the id for `name`, inserting it
  (storing the string once) on first sight. Stable within a compile.
- `name_of(id: i32) -> string` — the inverse, for the emit boundary that must
  still write a textual label / asm symbol.
- Round-trip contract: `name_of(intern(s)) == s`, and
  `intern(a) == intern(b)  <=>  a == b`.

Representation choice is a slice-0 decision to make against a benchmark, not up
front: a `Map[string, i32]` + a parallel `string[]` (id → name) is the obvious
start; a lexer-owned open-addressed table keyed on the source slice avoids even
the first substring allocation but is more code. The gate is *net allocation
measured on the 500×20 self-compile*, not elegance.

## Phasing (each slice is its own PR, byte-identical unless noted)

The migration is deliberately **additive-then-consume**, so no single slice
rewrites the whole pipeline:

0. **Foundation — `SymTab` + tests, not yet consumed.** _(Landed:
   `examples/self_host/symtab.fern` + `symtab_run.fern` +
   `TestSelfHostSymTab`.)_ The table and its intern / name_of / lookup / count /
   round-trip contract, `string[]`-backed via `util.index_of_str` (a map-backed
   index is the benchmarked follow-up behind this same contract; `core/map` is
   no longer novel in the compiler's own sources — #6993 slice four put
   irverify's `NameIndex` on one). It is a standalone module
   `fern.fern` does not import, so it is inert to the main self-compile (only its
   test driver pulls it in) — it costs nothing against the ~512-function bundle
   budget until slice 1 consumes it. Mirrors the SH-021 TypeRef foundation slice.

   _Complementary churn cleanup (landed, independent of the id plumbing):_
   `flatten.mangle_bare`'s own-decl branch re-concatenated `prefix + "__" + name`
   on every reference; it now resolves to a **precomputed shared instance** from a
   `RewriteCtx.declnames_mangled` parallel array (mirroring the existing
   `ivar_mangled` idiom), so N references to a decl share one mangled string body
   instead of N. Byte-identical (fixpoint-proven) and it reduces *allocation
   performed* at the mangle churn site #4394 names — but note it is a **transient
   churn** reduction, not a cut to the persistent `Op.str` residual (the mangled
   name is still a string on the op stream, just a shared one), and the standard
   single-module 500×20 benchmark does **not** exercise it (an entry module is not
   mangled — `flatten_qualified` uses empty declnames); the win lives in
   multi-module bundling (the real self-compile). It does not use `SymTab` (a
   declname→mangled map is a parallel array, not the name→id→name intern table);
   the id-based slices below are the path to the residual itself.
   The `mangle_bare` **heap** churn this slice targeted is now captured by the
   landed cleanup above — see the sharpened finding below.

### What the heap residual actually is (investigated 2026-07-05, post-#4612)

Probing the op-construction path sharpened the target and de-risked the id
slices:

- **Runtime-helper callee names** (`__fern_str_dec`, `__fern_rc_inc`, … — the
  hot, high-fan-in ones) are Fern **string literals**, which are `.rodata`-backed
  (the backends' own `str_lit_label` / "literal is data in `.rodata`" paths):
  evaluating `"__fern_str_dec"` yields a value pointing at shared `.rodata`
  bytes, **no heap body**. So the hot helper names contribute ~zero heap residual.
- **Mangled user-callee names** are the heap part. irlower does not
  re-concatenate them — it passes the AST identifier (`cid.name` / `id.name`)
  straight into `op_call_direct` — and those AST names are now the **shared
  instances** the landed `mangle_bare` dedup produced. So the call-op `Op.str`
  **heap bodies are already deduped**.

Net: the remaining `Op.str` residual is not string *bodies* (helpers are
`.rodata`, mangled bodies are deduped) but the **per-op `str` pointer field
itself** — 8 bytes × every op in the persistent arrays. Only shrinking the `Op`
box removes it, which is the id conversion below.

### Remaining work — why the naive id conversion does NOT win, and what does

The originally-sketched "add `Op.sym: i32`, emit from it" does **not** shrink the
residual, for two compounding reasons this investigation surfaced:

1. **`Op.str` is double-duty.** It holds both `const_str`'s literal *value* and a
   call/global op's *name*. `const_str` genuinely needs a string, and Fern structs
   have no optional fields, so **`str` cannot be removed from `Op`**. Adding a
   `sym: i32` for call ops therefore makes every op box carry *both* `str` and
   `sym` — the box **grows** ~4–8 bytes × every op in the persistent arrays: a
   straight memory *regression*.
2. **The call-name bodies are already gone.** Per the finding above, hot helper
   names are `.rodata` (no heap) and mangled user-callee bodies are deduped
   (#4612). So a call-op `sym` removes no string body — there is nothing left to
   remove there. The box growth in (1) is unoffset.

So the real residual is the **`Op.str` pointer slot × N ops**, and the only way to
reclaim it is to drop `str` **entirely** — which requires **every** `Op.str` use,
including `const_str` values, to become an id/handle, not just call names. That is
a materially bigger and different change than "intern the symbols":

- It needs a table that interns **arbitrary string constants** (user literals),
  not just the symbol name space — closer to a general constant pool than the
  `SymTab` name→id table.
- Or it needs `Op` split so only the ops that *use* a string carry one (a variant
  / tagged representation), so the common ops shrink — an IR representation change,
  not an interning slice.

**Recommendation:** do **not** pursue the naive per-op `sym` id — it regresses.
The tractable, already-landed wins on this axis are the SH-021 decoder migration
(smaller, correcter type decode) and the `mangle_bare` body dedup (#4612). A
genuine `Op.str`-slot reclamation is an **IR-representation** change (constant
pool for all string operands, or an op-variant split) that should be scoped as its
own design against a measured multi-module-self-compile RSS baseline before any
code — the 215-constructor churn only pays off if the box actually shrinks, which
neither the partial `sym` nor a body-dedup achieves. Lexer interning (slice 4)
remains independently worth measuring for *allocation performed* (churn), separate
from the persistent-residual question.

The pre-populated read-only `SymTab` insight still holds for whichever
representation is chosen (the call-target name space — module function names plus
the fixed runtime-helper set — is known before lowering, so no table need thread
through the lowering/emit recursion), which is why the `SymTab` foundation stays a
useful building block.

### Measuring peak RSS — methodology (learned the hard way)

Any of the residual-reclamation work is gated on a trustworthy peak-RSS number for
the self-host compiler compiling a fixed module. Getting one is harder than it
looks; the traps, so the next attempt skips them:

- **`getrusage`/`ru_maxrss` is USELESS for this runtime.** The self-host runtime's
  bump allocator pre-reserves a large fixed arena, and `ru_maxrss` (via Go's
  `cmd.ProcessState.SysUsage()` or otherwise) comes back at ~the arena size
  (~7.2 GB here) **independent of the workload** — 100×20 and 1000×20 both report
  the same number. It measures the reservation, not touched pages. Do not use it.
- **Sampled `/proc/<pid>/status:VmHWM` under-reports.** VmHWM is the true peak-RSS
  high-water mark, but the compile is sub-second, so even a tight busy-loop poll
  misses the transient peak (1000×20 sampled *lower* than 500×20 — impossible for
  a real peak). Usable only as a rough lower bound.
- **`cgroup v2 memory.peak` is the right tool but was unavailable here** (`/sys/fs/cgroup`
  is a bare tmpfs, no delegated sub-cgroup, no `systemd-run`). On a host with
  cgroup2 delegation, run the driver in a scratch cgroup and read `memory.peak` —
  that is kernel-tracked, not sampled, and ignores the untouched arena.
- Rough IR-path data point from the (unreliable, lower-bound) VmHWM samples:
  `asm_ir_run` compiling a single-module 500-fn × 20-stmt arithmetic program peaks
  around **~190 MB** touched, ~48 MB at 100×20 — same order as the OOM findings'
  `asm_run` (AST path) 484 MB at 500×20, which used `getrusage` on a runtime build
  where the arena did not dominate the reading. Treat these as indicative, not
  authoritative, until a `memory.peak`-based harness exists.
- Generator note: function names cannot be `f32`/`f64` (float type keywords → P001);
  and a deep `fn_i → fn_{i+1}` call CHAIN is not representative (superlinear compile
  cost) — use independent functions calling one shared leaf.
4. **Intern at the lexer (`scan_ident`).** The deepest change and the largest
   churn source; do it last, once the id plumbing exists, so identifiers are ids
   from birth. Needs care that the functionally-threaded `Lex` state does not
   trade string-alloc for table-copy churn — a mutable/shared table, benchmarked.

## Gates (non-negotiable, per the engineering bar)

- **Byte-identity** on every slice that claims it (all but the ones that
  explicitly change the op representation), proven by the three x86 self-compile
  fixpoints (bootstrap / load / modload) plus the WASM matrix in CI.
- **Measurement**: each consuming slice reports peak RSS on the 500×20
  self-compile (the `docs/IR-SELFCOMPILE-OOM-FINDINGS.md` methodology). A slice
  that does not move the metric — like the string-reclamation work that
  preceded it — is called out as such rather than assumed a win.
- **Tests**: `SymTab` contract (golden + Go), plus the existing fixpoint /
  differential suites for the codegen-touching slices.

## Relationship to the native side

Per `docs/NATIVE-CONVERGENCE.md`, interning is a self-host-internal
representation change; it does not add native-only language surface, so it is
not freeze-relevant debt. It should land self-host-first (there is no native
oracle need for an interning table).
