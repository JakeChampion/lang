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

0. **Foundation — `SymTab` + tests, not yet consumed.** Add the table and its
   round-trip / equality contract with a golden driver + Go test. Consumed
   immediately by slice 1 so it does not sit as dead bundle weight against the
   ~512-function budget. Mirrors the SH-021 TypeRef foundation slice.
1. **Intern the mangled names at `mangle_bare`.** Return an id-backed handle for
   the mangled symbol; keep the *textual* `Op.str` for now by materialising
   `name_of` at the op-construction boundary. This is behaviour-preserving
   (identical `Op.str` bytes) and measurable: the persistent op arrays now share
   one string instance per unique mangled name instead of one per reference.
   Gate: the three x86 fixpoints stay byte-identical; RSS on the 500×20
   self-compile is the success metric.
2. **Carry the id on `Op` alongside `Op.str`.** Add `Op.sym: i32`; populate it
   where `Op.str` is a symbol (call targets, global refs). `Op.str` stays as the
   debug/emit spelling. Still byte-identical output.
3. **Emit from the id.** At the backends' symbol-write sites, resolve
   `name_of(op.sym)` instead of reading `op.str`; then `Op.str` can drop to
   debug-only (or be reconstructed on demand), removing the per-op string from
   the persistent array. This is the slice that actually shrinks the residual.
   Gate: byte-identical asm on all three backends + fixpoints; measured RSS drop.
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
