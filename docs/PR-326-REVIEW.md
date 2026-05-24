# PR #326 — six-lens code review findings

PR #326 extends the pair-form (register-based) return arc from
`Option[T]` to also cover `Result[T, E]` where both type arguments
are `i32`-stack-shape. Six review agents inspected the diff
through different lenses; this document captures their findings so
they aren't lost when the PR merges. Items are tagged with a
suggested next step:

- **[fix-now]** small, bounded, fix on this PR before merge
- **[follow-up]** worth a separate PR
- **[deferred]** larger / requires design
- **[no-action]** noted, no action needed

The contradiction between the Security and Correctness reviews on
builtin-enum shadowing is recorded under "Open questions" at the
bottom.

---

## 1. End-use-case alignment — POOR

The headline finding. The stated targets are small fast-startup
CLI tools and short-lived edge-function HTTP servers; the use
case `BACKEND-PARITY.md:420-460` calls out for the pair-form arc
is "edge handlers don't allocate on the happy path of
`read_file` / `parse_json` / `http_parse_request`."

- **Zero prelude functions use `Result[i32, i32]`.** Every
  prelude `Result` signature in
  `internal/checker/checker.go:586-624` is
  `Result[string, IoError]` / `Result[Reader, IoError]` /
  `Result[Writer, IoError]`. None pass `isI32StackShape`
  (`internal/ir/ir.go:778-790`).
- **Edge handler examples use `Option`, not `Result`.**
  `examples/wasm/todo_api.fern`, `examples/native_http_handler.fern`.
- **The only `Result[i32, i32]` in the repo is the test code
  this PR adds.** `internal/e2e/x86_64_test.go:1061-1095`,
  `internal/e2e/arm64_test.go:1435-1467`.
- **The pointer-shaped follow-up is what would actually deliver
  the goal** — per the PR's own docstring at `ir.go:716-724`:
  "Other shapes (pointer-typed payloads… mixed-shape Result)
  require either the native pair-form lowering to support wider
  slots or the per-instantiation rebox machinery — both tracked
  as follow-ups."

**Verdict**: OK as a symmetry/correctness fix (any reason
`Result[i32, i32]` should heap-box when `Option[i32]` doesn't?),
poor as use-case alignment. **[follow-up]** — pointer-shaped
pair-form payloads is the next high-leverage piece of this arc.

---

## 2. Security

**One medium-severity gap** + clean otherwise.

### 2.1 Variant-order trust on shadowed builtin enum (med) **[open question]**

`pairFormVariantsFor` (`internal/ir/ir.go:748-762`) matches solely
on the type-name `"Option"` / `"Result"` and on
`isI32StackShape` of the type arguments. It never consults
`info.Enums[name]` to confirm the variant decl is in canonical
order. If a user can write
`enum Option[T] { None, Some(T) }` (swapped order), the heap-box
construction path uses `varIdx` from the user's decl (Some=1)
while pair-form lowering hard-codes `OpMakeSomeI32 → tag=0`.
Consumers reading the tag would interpret the variant
incorrectly.

The Correctness reviewer disputed this — claims the names are
reserved at `checker.go:86-88` and variant names are globally
unique at `checker.go:431`, so the shadow is structurally
impossible.

**Resolution: verify against the checker before deciding.** See
"Open questions" below.

### 2.2 Memory safety — clean

- Tag/payload offsets (tag@0, payload@4) unchanged; new
  `OpMakeOkI32` / `OpMakeErrI32` share codegen with
  `OpMakeSomeI32` / `OpMakeNoneI32` at
  `internal/codegen/x86_64/x86_64.go:689,704`,
  `internal/codegen/arm64/arm64.go:3644,3657`,
  `internal/codegen/wasm/wasm_ir.go:874,896`.
- `isI32StackShape` correctly excludes pointer-width / usize
  types — no pointer truncation risk.
- No new pointer arithmetic or raw stores introduced. Wasm
  sandbox guarantees unaffected.

---

## 3. Correctness

The dispatch logic agrees with itself on tag values, monomorph
clones make generics concrete before IR sees them, and helper-
return paths are correctly rejected from pair-form. A few small
gaps:

### 3.1 Lost arity guard in `isVariantLiteralExpr` (low) **[fix-now]**

Old `isSomeOrNoneExpr` enforced `len(c.Args)==1` for `Some`,
`==0` for `None`. New `isVariantLiteralExpr` (`ir.go:890-903`)
only checks `names[id.Name]`. **Same gap in `pairFormVariantOf`
(`ir.go:868`)**: returns `(name, nil)` for a no-arg `Ok(...)` —
the `case "Ok"/"Err"/"Some"` branch then calls `b.expr(nil)`
which panics. The checker rejects bad arity upstream
(`checker.go:2740`) so this is unreachable today, but the
defensive guard was load-bearing for a "what if the analysis
drifts" reason. Re-add per-variant arity checks.

### 3.2 Function-shadowing risk (low) **[follow-up — pre-existed for `Some` too]**

A user can define `function Ok(x: i32): Result[i32, i32] { ... }`.
The checker resolves `Ok(x)` at the call site by consulting
`FuncSigs` first (`checker.go:1661`, `:2739`) so `Ok(x)` is
treated as a function call, not a variant constructor — but the
pair-form lowering at `ir.go:1672-1693` consults *only* the
textual ident name. Net: a function returning
`Result[i32, i32]` whose body is `return Ok(helper())` where
`Ok` is shadowed by a user function would mis-lower the
recursive-style call.

Pre-existed for `Some` before this PR — this PR doubles the
surface area. Fix in a follow-up by consulting
`b.info.FuncSigs[id.Name]` / locals in `isVariantLiteralExpr`
before treating as a variant.

### 3.3 Silent fallthrough in return-lowering switch (low) **[fix-now]**

The new `switch variantName` in `stmt()` Return-handling
(`ir.go:1672-1691`) has no `default`. If `isVariantLiteralExpr`
ever drifts from `pairFormVariantOf`, a bogus literal would
silently emit `OpReturnPair` with no payload prep. Add
`default: panic(...)` to make the invariant load-bearing.

### 3.4 Test gaps (low) **[follow-up]**

Added tests cover only happy paths. Missing:

- Mixed-shape `Result[i32, string]` — assert ineligibility (no
  `OpMakeOkI32`).
- Nested `Result[Result[i32, i32], i32]` — assert outer is
  ineligible.
- Pair-form `Result` returned from a monomorphised generic
  helper — assert `OpReturnPair` survives.
- `let Ok(v) = divide(...) else { ... }` (LetElse) —
  `TestLowerLetElseOnPairFormCallSkipsRebox` only covers Option.
- Closure returning `Result` — assert `fn.IsLocal` keeps it out.
- Function-side mixed return paths: `return Ok(...)` on one
  branch + `return helper()` on the other — assert pair-form
  rejected.

---

## 4. Extensibility

The design scales **poorly** to a third pair-form enum.

### 4.1 Variant dispatch is name-keyed, not shape-keyed **[deferred]**

`pairFormVariantsFor` switches on the literal strings
`"Option"` / `"Result"` (`ir.go:752-770`); the lowering switches
on `"Some"/"None"/"Ok"/"Err"` (`ir.go:1672-1693`). A user-defined
`enum Foo { Bar(i32), Baz }` with the same i32 shape can never
opt in.

**Fix**: drive eligibility off the enum *declaration* (look up
`b.info.Enums[name]`, accept "exactly two variants, exactly one
carries a single i32-shaped payload, the other is nullary").
`varIdx` from `lookupVariant` already gives tag 0 vs 1 — no name
table needed.

### 4.2 Op family multiplies per enum × per width **[deferred]**

Today: 4 ops (`OpMakeSomeI32`, `OpMakeNoneI32`, `OpMakeOkI32`,
`OpMakeErrI32`) for 2 enums at i32. Adding i64 / string / ptr
and one user enum is a Cartesian explosion. Better: single
`OpMakePairLit` with an `I32`=tag field and a width discriminant
(or fold width into the existing `WidthPtr` sentinel pattern
already used elsewhere in the IR — see `CLAUDE.md`).

### 4.3 i32-only is wired into many places **[deferred — coupled with use-case alignment]**

Scratch slots are unconditionally `OpStoreLocal`/`OpLoadLocal`
(i32 storage) in match/iflet/letelse
(`ir.go:1414-1437, 1497-1513, 1866-1878`). The repack is
hard-coded to 8 bytes / `[ptr+4]` for the payload
(`ir.go:1208-1229, 3814`). Widening will need width-parameterised
slot ops plus a `payloadStoreOp(t)` selector at every site.

### 4.4 `pairVariants` cached as `map[string]bool` is name-coupled state **[deferred]**

`ir.go:967, 1073-1077`. After fix #4.1 this collapses to a
single `payloadVariantTag int32` (which tag carries the
payload) — simpler and width-ready.

### 4.5 Test coverage doesn't pin negative cases **[follow-up]**

`ir_test.go:482-555` covers Result happy path but nothing
asserts that `Result[i32, string]` (mixed width) or a
user-defined two-variant enum *falls through* to heap-box.
Without those, the width assumption could silently break later.

### 4.6 N-variant / N-payload generalisation is blocked by the "pair" assumption **[no-action]**

`OpReturnPair` literally returns two i32s; the target ABIs
(wasm multi-value, SysV rax+rdx, AAPCS x0+x1) are 2-slot by
design. Sums-of-products would need `OpReturnN` or fall back to
heap-box. Acceptable — the *machinery* is Option/Result-specific
by construction, but the *eligibility analysis* could still
generalise.

---

## 5. Performance

The PR is **structural**, not perf-positive on its own.

### 5.1 Codegen still heap-allocates on all three backends **[deferred — step 4 of the arc]**

`OpMakeOkI32` / `OpMakeErrI32` / `OpMakeSomeI32` still call
`__fern_alloc(8)` and store `{tag, payload}`:

- x86_64: `internal/codegen/x86_64/x86_64.go:689-714` ("Native
  fallback — same heap-box shape")
- arm64: `internal/codegen/arm64/arm64.go:3644-3666` ("Native
  fallback: alloc 8 bytes")
- wasm: `internal/codegen/wasm/wasm_ir.go:874-908` (also allocs;
  comment at `:357-368` confirms wasm function sig stays
  `(result i32)` not `(result i32 i32)`)

`OpReturnPair` is identical to `OpReturn` on natives
(`x86_64.go:680-688`, `arm64.go:3635-3643`); on wasm it
collapses to plain `return` (`wasm_ir.go:865`).

### 5.2 Real saving lives only at scrutinee position **[no-action — by design]**

The only saved work today is the `suppressPairRebox` path at
`ir.go:3825-3837` — when a pair-form call feeds directly into
`if let` / `match` / `let else`, `OpCallDirectPair` extracts
`[ptr+0]` (tag) and `[ptr+4]` (payload) inline
(`x86_64.go:1614-1618`, `arm64.go:4591-4595`,
`wasm_ir.go:944-950`) instead of the heap-rebox-then-reload
dance. Net saving: avoids `__fern_alloc(8) + 2 stores + 2 loads`
per pair-form call where the consumer is the scrutinee.
Measurable on hot `match parse(req) { ... }` loops.

### 5.3 Detection is conservative — misses idiomatic shapes **[follow-up]**

`allReturnsAreVariantLiteral` only accepts
`Return{Value: variant-literal}`. Misses:

- **Tail-call returns**: `return helper(x)` where helper is
  itself pair-form — currently emits `OpCallDirectPair` →
  `emitRepackPairAsHeapBox` → `OpReturn` → caller re-extracts.
  Alloc-store-load round trip the analysis could see through.
- **Ternary / if-expression returns**:
  `return cond ? Ok(x) : Err(y)` (`ir.go:817-839`).
- **Match-arm expressions**.

These are exactly the shapes edge handlers tend to be written
in.

### 5.4 Per-call map allocation in `pairFormVariantsFor` **[fix-now — trivial]**

`ir.go:759, 765`: hoist the two variant maps to package-level
vars: `var optionVariants = map[string]bool{"Some":true,"None":true}`
/ `resultVariants`. Saves one allocation per eligibility check.

### 5.5 Compile-time cost — negligible **[no-action]**

`findPairFormFuncs` is O(AST size), runs once. No quadratic
blow-up on Result-heavy programs.

### 5.6 Wasm function sig deliberately stays `(result i32)` **[deferred — independent perf win]**

`internal/codegen/wasm/wasm_ir.go:357-374`. Switching to
`(result i32 i32)` and dropping the heap-box would land the
actual win on wasm independently of natives. Independent
follow-up; not blocked on native ABI work.

---

## 6. Maintainability

Mostly good — the rename from `isSomeOrNoneExpr` /
`allReturnsAreSomeOrNone` to `isVariantLiteralExpr` /
`allReturnsAreVariantLiteral` is a clear win now that there are
two variant families. A few small drifts to clean up:

### 6.1 Stale doc-comment on `findPairFormFuncs` **[fix-now]**

`ir.go:686-696` still names only `Option[T]` / `Some` / `None`
and calls Result a "future step in this arc" — that future is
now. Update.

### 6.2 Contradictory test header comment **[fix-now]**

`ir_test.go:495-499` header claims "Mixed Result on a heap-form
scrutinee still works (no pair-form caller-side; the heap-box
function-side path covers it)" — but the test then asserts
`OpCallDirectPair` IS present and bans `OpAlloc` / `OpMatchTag`.
The comment looks copy-pasted from a different intended test.
Either rewrite the comment to match the assertions or write the
test the comment describes.

### 6.3 Result match-skip-rebox test is a near-clone of Option's **[follow-up]**

`ir_test.go:486-554` vs `:450`. Given the eligibility logic is
now table-driven by `pairFormVariantsFor`, an IR test
parameterised over both shapes would cover both with one body.
As-is, both Option and Result tests will drift independently.

### 6.4 `pairVariants` field comment wording **[fix-now — trivial]**

`ir.go:961-969`: says "Empty when thisIsPair is false" — the
zero value is `nil`, not an empty map.

### 6.5 e2e tests structurally clone each other **[no-action]**

`TestX86_64ResultPairForm` / `TestArm64ResultPairForm` mirror
each other. That's the cross-backend convention here, and each
exercises a distinct backend's codegen for the same source.
Acceptable.

### 6.6 Commit message **[no-action]**

Above bar — explains both function-side and caller-side
changes, names the four `OpMake*I32` ops, lists the test
additions and what each pins, notes prior-PR backend-lowering
dependency.

---

## Open questions

### Q1. Are `Option` / `Result` actually reserved against user shadowing?

The Security and Correctness reviews disagreed. Resolve by
reading:

- `internal/checker/checker.go:86-88` — does this reserve the
  *type names* `Option` / `Result`?
- `internal/checker/checker.go:431` — does this enforce
  global-uniqueness of *variant names* `Some` / `None` /
  `Ok` / `Err`?
- `internal/checker/checker.go:333-340` — the security review
  cited this as "skips injecting the builtin when the user has
  declared the name first."

If both type-name and variant-name are reserved (Correctness's
position), finding 2.1 is **[no-action]**. If only one is
reserved, finding 2.1 needs a fix in either the checker or
`pairFormVariantsFor`.

### Q2. Should we land the low-risk fixes on PR #326 or open a follow-up?

`[fix-now]` items: 3.1, 3.3, 5.4, 6.1, 6.2, 6.4. None are
behaviour-changing (defensive guards, comment fixes, an
allocation hoist) — small enough to add to the existing PR
without re-triggering substantive review. User decision.

---

## Cross-cutting summary

The biggest single insight across the six lenses: **this PR is
structurally clean but doesn't move the needle on the
edge-handler use case** because every real prelude `Result`
return is pointer-shaped. The headline arc-step worth doing
next is **pointer-shaped pair-form payloads** (use-case
alignment + extensibility 4.3 are the same problem). The
extensibility / maintainability findings (4.1, 4.2, 4.4, 6.3)
point to a refactor where eligibility runs off
`info.Enums[name]` shape rather than nominal name match —
worth doing in the same follow-up so we don't entrench the
name-table approach further.

Performance finding 5.3 (extend detection to tail-call /
ternary / match-expression returns) is an orthogonal cheap win
that should fire on any pair-form enum and is worth its own
small PR.
