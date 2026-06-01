# Immutability migration plan — scope + struct-update design

Date: 2026-06-01.
Status: planning. No compiler or `.fern` code changed by this doc.

## Purpose

`docs/CYCLE-COLLECTION-ANALYSIS.md` recorded the decision (2026-06-01):
**Fern is going immutable-data-structures-only — no post-construction
mutation of an already-constructed heap value.** That designs reference
cycles out of the language, so RC stays garbage-free with no cycle
collector and no `weak`-reference burden (the Koka/Roc/Lean property the
RC pivot was lifted from).

The decision's 4-step sequencing is:

1. Record the decision — **done** (`CYCLE-COLLECTION-ANALYSIS.md` §Decision).
2. Add a functional struct-update expression.
3. Migrate in-tree mutators off field assignment.
4. Flip the checker to reject field / capture mutation.

This doc covers **step-3 scoping** plus a concrete **step-2 design
sketch**. Every code claim below was verified against the file at the
cited line.

---

## 1. Mutation inventory — the language surface today

There are exactly **two** post-construction in-place-mutation paths in
the language, plus the array/map CoW paths (which are *not* mutation of
an already-shared value and are out of scope for this migration).

### 1a. Struct field assignment — `obj.field = v`

- **Checker permits it.** `*ast.Assign` (`internal/checker/checker.go:4919`)
  type-checks `n.Target` and `n.Value`, requires assignability, and
  the comment at `:4930-4939` records that `FieldAccess`, `Ident`, and
  `Index` are the three addressable target shapes. There is **no
  mutability gate** — no `mut` keyword, no `let`/`var` field
  distinction, no recursion-aware rejection.
- **IR lowers it to a raw in-place store.** `internal/ir/ir.go:9921`
  (`case *ast.FieldAccess:` inside `b.assign`): resolve the owning
  struct (`b.fieldOwner`), compute `base + rcHeader + field_offset`
  from `structFieldLayout`, evaluate the value, emit
  `payloadStoreOpFor(ft, b.ptrW)` (`ir.go:9955`). No CoW, no rc check —
  it writes the shared box directly. Compound forms (`a.v += 35`) flow
  through the same path after desugar.
- **Tests assert it.** `internal/e2e/self_host_field_assign_test.go`
  (`fieldAssignCases`, lines 17-26) — four cases run on the self-hosted
  x86-64 (`TestSelfHostFieldAssignX86_64`) and arm64
  (`TestSelfHostFieldAssignArm64`) compilers:
  - `bump-through-fn` (line 22) — `bump(b)` mutates `b.v` *through a
    function call*; caller observes `b.v == 12`. **This is the
    mutate-through-call semantic the migration must replace.**
  - `two-fields` (23), `mutate-in-loop` (24, `inc(c)` through a call),
    `compound` (25, `a.v += 35`).
  The file header comment (lines 10-16) states the intended contract:
  *"Mutation persists through the heap pointer, so a struct passed to a
  function is mutated in place."*

### 1b. Mutable closure-capture write-back — `cap = v` inside a closure

- **IR lowers it** at `internal/ir/ir.go:9958` (`case *ast.CaptureRef:`
  in `b.assign`): load `__env`, add `cr.Offset`, evaluate value, emit
  `payloadStoreOpFor(cr.Type, b.ptrW)`. The comment (`:9959-9971`)
  states the semantics directly: *"The env block is heap-allocated and
  shared by all calls to this closure — mutation persists across
  re-invocations."* `closureconv` rewrites the assigned ident to a
  `CaptureRef` during the body walk; the checker accepts it via the
  same `*ast.Assign` path as 1a.
- **Tests.** Covered by the closure-conversion / closure e2e suites
  (`TestArm64Closure*` and the x86-64 / wasm mirrors named in
  `CLAUDE.md`). No in-tree `.fern` code under `examples/` or
  `internal/stdlib/` writes back to a capture (see §2) — this path is
  exercised by Go-side fixtures only, so it has **zero `.fern`
  migration workload**; it only needs the step-4 checker rejection.

### 1c. Anything else that mutates an already-constructed heap value

Surveyed and **excluded** — these are CoW / fresh-value paths, not
shared-value mutation, and the immutability rule does not touch them:

- `arr.push`, `arr[i] = v`, `Map.set/delete` — route through CoW
  helpers (`isSelfMapMutation`, `ir.go:9885-9919` nested-array CoW).
  They return a (possibly fresh) handle; the surface form `m = m.set(...)`
  / `arr = arr.push(x)` is already a *reassignment of a local*, not an
  in-place edit of a shared box. These stay.
- Tuples have **no** element-assign target (immutable already —
  `CYCLE-COLLECTION-ANALYSIS.md` §1c).
- `state { }` slot writes are local-variable reassignment, not
  field mutation.

**So the only assignment-target shape this migration removes is
`*ast.FieldAccess` (1a) and `*ast.CaptureRef` (1b) in `b.assign`.**

---

## 2. In-tree `.fern` workload

Method: `grep -nE '^\s*<ident>\.<field>\s*(\+=|-=|\*=|/=|=)\s'` across
`examples/` and `internal/stdlib/`, filtering out `==`/`<=`/`>=`/`!=`
and comment lines. Statement-leading field assignment is the reliable
signal (an embedded `something.field =` inside a larger expression is a
comparison or a `var x: T = ...` declaration).

**Total: 59 field-assignment sites across 7 files.**

| File | Sites | Receiver shape | Difficulty |
|---|---|---|---|
| `internal/stdlib/std/json.fern` | 32 | parser cursor (`p.pos`, `p.error`) mutated through deeply-threaded helper calls | **HARD** |
| `internal/stdlib/std/url.fern` | 7 | local builder `u` built field-by-field, then `return Some(u)` | EASY |
| `examples/wasm/wc.fern` | 6 | local accumulator `total.*` + `acc.*` (already returned) | EASY |
| `internal/stdlib/std/stream.fern` | 4 | receiver cursor `s.pos` mutated through method calls | **HARD** |
| `internal/stdlib/std/io_buffered.fern` | 4 | receiver builder `w.data` mutated through void methods | **HARD** |
| `internal/stdlib/std/headers.fern` | 4 | receiver builder `h.names/h.values` mutated through void methods | **HARD** |
| `internal/stdlib/std/mock_platform.fern` | 2 | receiver builder `m.calls` mutated through void methods | **HARD** |

### Correction to `CYCLE-COLLECTION-ANALYSIS.md`'s premise

The decision doc states (§Correction) that there are *"~497
`obj.field = …` call sites"* and that *"the self-hosted compiler's own
passes (parser/constfold/flatten) mutate fields."* **Both claims do not
hold against the source as it stands, and the migration scope is much
smaller than the doc implies:**

- The real statement-leading field-assignment count is **59**, not ~497.
  The ~497 figure appears to count `var x: T = …` declarations and
  `a.b == c` comparisons, which the `.<field> =` substring also matches.
- The self-host `.fern` passes contain **zero** statement-leading field
  assignments. `examples/self_host/parser.fern`, `constfold.fern`,
  `flatten.fern` build their ASTs **bottom-up and immutably** (verified:
  every `<ident>.<field>` line in those files is a
  `var t: lexer.Token = p.peek()`-shaped declaration). The `__set_field`
  references that exist (`parser.fern:1181-1195`, `asm.fern:4897-4901`,
  `asm_arm64.fern`) are the self-host compiler *parsing and emitting*
  field assignment **for programs it compiles** (`obj.field = rhs` →
  `__set_field(obj, "field", rhs)`), not the self-host source using it.

This is **good news for the migration**: the self-hosted compiler — the
riskiest, fixpoint-gated component — needs **no source migration at
all**. It only needs its checker/emitter to keep accepting the
`__set_field` desugar until step 4, and at step 4 the self-host
*checker pass* (`examples/self_host/checker.fern`) must learn to reject
field assignment to match the Go checker (a parallel checker change, not
a data-shape rewrite).

### What each HARD file actually does, and why

The HARD cases share one semantic: **mutate-through-receiver/argument**,
where a void-returning function/method mutates a struct passed in, and
the *caller* observes the mutation. This is the `bump(b)` shape from
`self_host_field_assign_test.go:22`. Removing mutation means the
mutation must become *"return the new value"* and every call site must
thread it back — a signature change, not a local rewrite.

- **`json.fern` (32).** `JsonParser` carries `pos` (cursor) and `error`
  (sticky flag). `p.pos = p.pos + 1` (consume) and `p.error = 1` appear
  in `parse_value` / `parse_array` / `parse_object` / literal parsers
  (`json.fern:138-420`), which call each other and all advance the same
  shared `p`. This is the deepest threading: every parse helper would
  need to return `(JsonParser, JsonValue)` (or thread `p` as a
  returned-and-rebound value) so the cursor advance is visible to the
  caller.
- **`stream.fern` (4).** `(s: Stream) read_byte/read_n/read_line/read_all`
  advance `s.pos` (`stream.fern:88,117,132,149`). The cursor advance is
  the whole point — a caller doing `s.read_byte()` twice expects two
  different bytes. Methods return only the payload today; they'd need to
  return the advanced `Stream` too (or the caller rebinds `s`).
- **`io_buffered.fern` (4).** `(w: BytesWriter) write_string/write_bytes/
  write_byte/reset` append to `w.data` (`:49,59,66,99`). Classic
  mutable builder — the accumulator is the receiver.
- **`headers.fern` (4).** `(h: HeaderMap) append/set` push to
  `h.names`/`h.values` (`:75-76,107-108`). Builder-on-receiver.
- **`mock_platform.fern` (2).** `(m: MockPlatform) record/reset` push to
  / clear `m.calls` (`:46,61`). Builder-on-receiver.

### EASY cases in detail

- **`wc.fern` (6).** Two sub-patterns, both trivial:
  - `count_chunk(chunk, acc: Counts): Counts` mutates `acc.bytes/lines/
    words` (`:24,27,32`) **and already returns `acc`**, and the caller
    already does `acc = count_chunk(line, acc)` (`:48`). The in-place
    writes are redundant given the threaded return — rewrite the body to
    build and return a fresh `Counts`; the call site is unchanged.
  - `main` accumulates `total.lines/words/bytes` (`:73-75`) — a pure
    local accumulator. Rebuild `total` from itself + `got`.
- **`url.fern` (7).** `url_parse` builds a local `u: Url` with seven
  *conditional* field writes (`:118,130,141,168,180,182,188`) then
  `return Some(u)` (`:191`). No aliasing, no through-call — a local
  builder. Cleanest rewrite: compute the seven pieces into local
  primitive vars, then **one** `Url { … }` constructor at the end.

---

## 3. Per-pattern functional-update rewrite

### Pattern A — local accumulator (EASY)

Today (`wc.fern:73-75`):
```fern
total.lines = total.lines + got.lines;
total.words = total.words + got.words;
total.bytes = total.bytes + got.bytes;
```
Rewrite with struct-update (§4):
```fern
total = Counts { ...total, lines: total.lines + got.lines,
                 words: total.words + got.words,
                 bytes: total.bytes + got.bytes };
```
Without struct-update, full reconstruction:
```fern
total = Counts { lines: total.lines + got.lines,
                 words: total.words + got.words,
                 bytes: total.bytes + got.bytes };
```
(For a 3-field struct, full reconstruction is fine; struct-update earns
its keep when the struct is wide and only a few fields change.)

### Pattern B — local conditional builder (EASY, but verbose)

`url.fern`'s `u` is written across conditionals. Two options:
- **Stage into primitives** (recommended — no struct-update needed):
  hold `scheme`, `host`, `port`, `path`, `query`, `fragment` as local
  vars, set them in the conditionals, then `Url { scheme, host, … }`
  once at the end.
- **Thread the struct** with struct-update at each conditional:
  `if (scheme_end >= 0) { u = Url { ...u, scheme: s[0:scheme_end] }; }`
  — 7 rebuilds. Works, but staging into primitives is clearer here.

### Pattern C — accumulator already returned (EASY)

`count_chunk` already returns `acc` and the caller rebinds. Drop the
in-place writes; build the return value functionally (Pattern A applied
to the function body). **Call sites unchanged.**

### Pattern D — mutate-through-receiver / void method (HARD)

This is the real cost. `headers`, `io_buffered`, `mock_platform`,
`stream`, and `json` all rely on a void method/function mutating the
receiver/argument and the caller seeing it. The functional form changes
the *contract*: the method must **return the new value**, and callers
must **rebind**.

Today (`headers.fern:74-77`):
```fern
pub function (h: HeaderMap) append(name: string, value: string) {
    h.names  = h.names.push(name.to_lower());
    h.values = h.values.push(value);
}
// caller:  h.append("X", "1");   // mutation observed via h
```
Functional form:
```fern
pub function (h: HeaderMap) append(name: string, value: string): HeaderMap {
    return HeaderMap { ...h, names:  h.names.push(name.to_lower()),
                             values: h.values.push(value) };
}
// caller:  h = h.append("X", "1");   // rebind
```
Every call site of every migrated method/function must change from
`recv.m(args);` to `recv = recv.m(args);` (and the method must grow a
return type). For `json.fern` the cursor-and-error parser this means
multi-value returns threaded through the recursive-descent call graph —
the single largest rewrite in the tree.

**The HARD/EASY split is exactly the void-return-mutate-through-call vs
local-accumulator distinction.** Easy = the mutated value is a local the
same function owns and returns/uses. Hard = the mutation is *observed by
a different stack frame* (the `bump(b)` semantic from
`self_host_field_assign_test.go:22`), which only worked because structs
have reference semantics — the exact property immutability removes.

---

## 4. Design sketch — functional struct-update expression (step 2)

**No spread/struct-update syntax exists today** (verified: no
`spread` / `...` / `StructUpdate` in `internal/parser/*.go` or
`internal/ast/*.go`). It must be built.

### Proposed syntax

```fern
Foo { ...old, field: newval, other: x }
```
- A leading `...<expr>` "base" inside an otherwise-normal struct literal.
- `...old` must be the **first** element (keeps the parser's
  field-loop simple and reads naturally as "start from `old`, then
  override"). Exactly one base permitted.
- `old` must have type `Foo` (the same struct as the literal's type
  name) — no cross-type spread in v1.
- Overrides may name **any subset** of fields; un-named fields are
  copied from `old`. (Contrast plain `Foo { … }`, which requires the
  full field set.)

Chosen over alternatives: `Foo { old with field: x }` (extra keyword),
`{ ...old, field: x }` typeless (loses the nominal type the checker
needs for layout). `...` matches the array/JS spread the surface
already evokes and is unambiguous after `{`.

### Parser

In `parseStructLit` (`internal/parser/parser.go:2747`): before the
field loop, if the next tokens are `...`, parse `...<expr>` as the
base and stash it. Add a `Base ast.Expr` field to `ast.StructLit`
(`internal/ast/ast.go:884`) — `nil` for today's plain literals, so all
existing code is unaffected. The field loop is unchanged; a trailing
`}` after just `...old` (no overrides) is legal (a pure copy).

### Checker

In the `*ast.StructLit` case (`internal/checker/checker.go:5116`):
- If `Base != nil`, type-check it; require its type `== StructType{TypeName}`
  (error `E003`-style "struct-update base must be Foo, got …").
- For each override `FieldInit`, type-check `Value` against the declared
  field type (same as today's per-field check).
- **Relax the completeness check** when `Base != nil`: a plain literal
  must name every field; a struct-update literal may name a subset — the
  rest are copied from the base. Reject an override naming a field the
  struct doesn't have (existing check).
- Generics: carry `Base`'s `TypeArgs` to the result (the base already
  fixes the instantiation).

### IR lowering

Mirror the existing `*ast.StructLit` emit (`internal/ir/ir.go:6921`),
which already does: `OpAlloc(size + rcHeaderBytes)` → store `rc=1` at
`[base+0]` → per-field `base + rcHeader + offset` store. For
struct-update:
1. Evaluate `Base` once into a temp slot.
2. Alloc the new box + rc header exactly as today.
3. For each field: if overridden, store the override value (with the
   same `needsRcIncOnAlias` gate as today, `ir.go:6975`); else **load
   the field from the base** (`base + rcHeader + offset`) and store it
   into the new box — and **rc-inc** copied pointer-shaped fields
   (`ast.IsPointerType`), because the new struct co-owns them (same
   aliasing rule the field-init path already applies). Non-pointer
   fields are a plain load/store.
4. Drop the base temp at end of statement (its own rc-dec), balancing
   the incs on the copied pointer fields.

The store widths come from `payloadStoreOpFor(ft, b.ptrW)` /
`structFieldLayout` exactly as the field-assign path used — so
arm64/x86-64/wasm parity is automatic (the `WidthPtr` sentinel resolves
per backend, per `CLAUDE.md`).

### Open questions (do not resolve here)

- **Spread position**: first-only (proposed) vs anywhere. First-only is
  simpler and sufficient; revisit only if a real use wants
  `Foo { field: x, ...old }` last-wins semantics.
- **Cross-type / structural spread** (copy shared-named fields from a
  different struct): out of scope for v1; nominal-same-type only.
- **Nested update sugar** (`Foo { ...old, inner.field: x }`): no — the
  user writes `inner: Inner { ...old.inner, field: x }` explicitly.
- **rc/move interaction**: when the base is a dead local at its last
  use, can the lowering *move* (steal the box, overwrite the changed
  field, skip the copy) instead of alloc-and-copy? A nice optimisation,
  but it reintroduces in-place mutation under the hood — keep it out of
  v1 (semantics first; the `markConstructionMoves`/Perceus reuse
  analysis can add it later, after step 4 makes uniqueness provable).
- **Interpreter parity**: the interpreter has its own StructLit eval;
  it needs the matching base-copy path so `-interp` and the native
  backends agree (add to the interp's struct-lit handler).

---

## 5. Recommended migration order

Easy first (validates the new struct-update expression on low-risk
code); self-host / fixpoint-gated last.

1. **Step 2 — build struct-update** (`...old` in `StructLit`): parser +
   AST field + checker + IR (all backends) + interpreter, with
   parser/checker/e2e tests per the engineering bar. Land before any
   migration so the rewrites have the tool.
2. **`wc.fern`** (Pattern A + C, 6 sites). Lowest risk — an example,
   not stdlib, and `count_chunk` already threads its return. Proves the
   expression end-to-end on a real program.
3. **`url.fern`** (Pattern B, 7 sites). Local builder; stage into
   primitives + one constructor. Pure-stdlib but no public method
   signature changes (it's a free function returning `Option[Url]`).
4. **`mock_platform.fern`** (Pattern D, 2 sites). Smallest HARD file;
   test-only consumer, so the `m = m.record(...)` rebind churn is
   contained to test code — a good first HARD case to shake out the
   "method grows a return type, callers rebind" pattern.
5. **`io_buffered.fern`** + **`headers.fern`** (Pattern D, 4+4). Builder
   methods; convert each void method to return the new value, then fix
   every in-tree caller to rebind. Do these together — they share the
   builder shape and likely share callers (HTTP path).
6. **`stream.fern`** (Pattern D, 4). Reader cursor; methods return the
   advanced `Stream` alongside their payload (or a small `(value,
   Stream)` tuple). Touches the Reader-convention call sites
   (`wc.fern`'s `count_reader` reads via `r.read_line()` — note
   `wc.fern` uses the fd-backed `Reader`, not `Stream`, so confirm which
   readers are affected before rewiring).
7. **`json.fern`** (Pattern D, 32). Last and hardest: thread
   `(JsonParser, …)` through the recursive-descent graph. Most sites,
   deepest call graph, public API (`json.parse`). Do it only once the
   pattern is proven on 4-6.
8. **Self-host**: no `.fern` source migration (per §2 correction). The
   self-host work is in **step 4**, not step 3 — the self-host
   `checker.fern` pass must start rejecting field assignment in lockstep
   with the Go checker, and the byte-identical fixpoint tests must be
   re-baselined once the Go compiler stops emitting the field-store
   lowering. Sequence this **after** all step-3 stdlib migration so the
   fixpoint compiles a tree that no longer contains field assignment.

---

## 6. Step-4 checker enforcement + tests to update

Once §5 lands, flip the checker to reject the two mutation targets:

- In `*ast.Assign` (`internal/checker/checker.go:4919`): when
  `n.Target` is `*ast.FieldAccess` (or, post-closureconv, a captured
  ident that becomes `*ast.CaptureRef`), emit a new error
  (e.g. `E0xx`: "fields are immutable after construction; use
  `Foo { ...old, field: v }`"). The diagnostic should point at the
  struct-update expression as the fix.
- The IR `b.assign` cases at `ir.go:9921` (FieldAccess) and `ir.go:9958`
  (CaptureRef) become dead for valid programs; keep them as a
  belt-and-braces internal error or delete them once the checker
  guarantees they're unreachable.
- Self-host `checker.fern` gets the parallel rule (step 5/8 above).

### Tests that need updating (the regression-prone surface)

- **`internal/e2e/self_host_field_assign_test.go`** — this asserts the
  behaviour being **removed**. All four `fieldAssignCases` (incl. the
  `bump-through-fn` mutate-through-call) must be either deleted or
  **inverted** into checker-rejection tests (assert the new error code).
  This is the single most important test to flip — it currently encodes
  the contract the migration reverses.
- **Closure-capture write-back tests** (`TestArm64Closure*` and x86-64 /
  wasm mirrors): any case that writes back to a capture must move to a
  checker-rejection assertion. (Capture *reads* stay valid.)
- **New checker tests** (positive): struct-update type-checks (subset
  override, full copy, wrong base type rejected, unknown field
  rejected, generic base). Per the engineering bar: parser-test for the
  `...old` desugar, checker-test for each rule, e2e for the runtime
  copy + rc balance on all backends.
- **New negative-control tests**: immutable recursive trees still
  compile — `JsonValue` construction and bottom-up AST building (the
  self-host parser's own shape) must remain accepted, proving the rule
  only bans *post-construction* mutation, not recursive types.
- **Self-host fixpoint / byte-identical tests**: re-baseline after the
  Go compiler stops emitting the field-store lowering (`ir.go:9921`).
- **`docs/RC-PERCEUS-PLAN.md:71-77` and Open Question #9 (`:2003-2004`)**:
  update the stale "no mutable struct fields / cycle-free by accident"
  lines to "cycle-free by enforcement" once step 4 lands (called out in
  `CYCLE-COLLECTION-ANALYSIS.md` follow-ups).

---

## Appendix: evidence index

| Claim | Evidence (verified) |
|---|---|
| Checker accepts FieldAccess assign target, no mut gate | `internal/checker/checker.go:4919-4939` |
| Field assign lowers to raw in-place store | `internal/ir/ir.go:9921-9956` |
| Capture write-back lowers to shared-env store | `internal/ir/ir.go:9958-9983` |
| Field-assign behaviour asserted (incl. through-call) | `internal/e2e/self_host_field_assign_test.go:17-93` |
| 59 statement-leading field-assign sites, 7 files | grep over `examples/`, `internal/stdlib/` |
| `json.fern` cursor/error mutation (32) | `internal/stdlib/std/json.fern:138-420` |
| `url.fern` local builder (7) | `internal/stdlib/std/url.fern:118-191` |
| `wc.fern` accumulator, `count_chunk` returns acc (6) | `examples/wasm/wc.fern:24-48,73-75` |
| `stream.fern` receiver cursor (4) | `internal/stdlib/std/stream.fern:88,117,132,149` |
| `io_buffered.fern` receiver builder (4) | `internal/stdlib/std/io_buffered.fern:49,59,66,99` |
| `headers.fern` receiver builder (4) | `internal/stdlib/std/headers.fern:75-76,107-108` |
| `mock_platform.fern` receiver builder (2) | `internal/stdlib/std/mock_platform.fern:46,61` |
| Self-host `.fern` passes do NOT mutate fields | zero statement-leading hits in `examples/self_host/*.fern`; all `<ident>.<field>` lines are `var t: T = …` |
| `__set_field` is the self-host *desugar/emit*, not usage | `examples/self_host/parser.fern:1181-1195`, `asm.fern:4897-4901` |
| No struct-update / spread syntax exists yet | no `spread`/`...`/`StructUpdate` in `internal/parser/*.go`, `internal/ast/*.go` |
| StructLit AST node | `internal/ast/ast.go:884-896` |
| StructLit parses (field loop) | `internal/parser/parser.go:2747-2779` |
| StructLit type-check | `internal/checker/checker.go:5116` |
| StructLit IR emit (alloc + rc header + per-field store) | `internal/ir/ir.go:6921-6975` |
| Pointer-field rc-inc on field init (aliasing) | `internal/ir/ir.go:6959-6975` (`needsRcIncOnAlias`) |
</content>
