# Adversarial codebase review — 2026-06

> **Status:** all 17 findings are fixed (each with a regression test). The last
> one, **M1** (Map copy-on-write interp divergence,
> [#2851](https://github.com/JakeChampion/lang/issues/2851)), is now implemented —
> the interpreter does rc-based COW matching every backend (see
> `INTERP-MAP-COW-PLAN.md` and the `map_cow_*` differential cases in
> `internal/e2e/feature_differential_test.go`). The fixed findings below are
> retained as bug-fix history.

A whole-codebase adversarial review of the Fern compiler. The goal was
to **break the code**: find real correctness bugs, type-system
soundness holes, backend-parity divergences, and reference-oracle
(interp vs compiled) splits — not style nits.

Method: the codebase was swept by subsystem (backends, IR/optimiser,
frontend, modload/interp/stdlib, literate/diag/lsp/cli). Each finding
below was reproduced against the tree at commit `5a3a6c6` (probe tests
written, run, then removed; no source modified during review). The
build is clean (`go build ./...`) and all non-e2e unit packages pass.

## Status (updated after the fix pass)

All 17 findings are **fixed** (each with a regression test and the
full non-e2e suite + native e2e corpus re-run green): F1, B1, B2, I1, I2,
F3, F4, M1, M2, M3, F2, L1, L2, L3, L4, L5, B3, I3.

The last finding, **M1** (Map copy-on-write interp divergence), is now
**implemented**: the interpreter does the runtime's Perceus-style rc-based
COW. The compiled backends' COW is rc-based — they mutate a map in place
when it is unshared (rc==1) and copy only when aliased (rc>1), so a bare
`m.set(k,v)` statement mutates in place. The interpreter now tracks the
same reference count (`Map.rc`, with `retain` / `release` at the
value-flow hook points and `clone()` on the shared path), so it mutates in
place when unshared and copies when aliased — matching every backend. The
validation gate is met: the `map_cow_alias_isolation` / `map_cow_func_arg`
/ `map_cow_returned` / `map_cow_alias_then_scope_exit` differential cases
(`internal/e2e/feature_differential_test.go`) run the interp against every
backend and pass, with no new divergence. M3 (delete order) was fixed
independently. The full design + value-flow hook points are in
`docs/INTERP-MAP-COW-PLAN.md`.

The fix decisions for the three originally-deferred items were:
**F2** → require an explicit `as` cast in user code (the implicit usize
escape hatch is now gated to stdlib context); **M3** → force a single
stable cross-backend order (interp now mirrors the runtime's
swap-with-last); **M1** → make the interp match COW (now implemented, see
above).

## Summary

| # | Severity | Subsystem | Title |
|---|----------|-----------|-------|
| F1 | **Critical** | frontend | `shadowrename` drops `StructLit.Base` → shadowed struct-update base miscompiles |
| M1 | **Critical** | interp/runtime | Map copy-on-write: interp aliases, compiled backends copy (reference-oracle split) |
| B1 | **Critical** | backend (x86-64) | `usize` division/remainder truncated to 32 bits |
| I1 | **Critical** | optimiser (SSA) | SSA const-folding ignores operand width → i32 wraparound lost |
| F3 | High | frontend | Decimal integer literals overflow silently in the parser |
| F4 | High | frontend | No missing-return / fall-off-end analysis for value-returning functions |
| F2 | High | frontend | `usize` is an implicit bidirectional type wormhole (no cast required) |
| M2 | High | modload | Same-basename modules collide under mangling even when aliased |
| M3 | High | interp/runtime | Map `delete` iteration order: interp shift-down vs compiled swap-with-last |
| I2 | High | optimiser (SSA) | SSA DCE removes unused div/rem, eliminating the divide-by-zero trap |
| L1 | High | literate/diag | Diagnostic remap off-by-one for escaped chunk markers (`\<<…>>`) |
| B2 | Medium | backend (IR) | `float → usize` cast truncates to 32 bits on both natives |
| L2 | Medium | literate | `-weave -html` emits unsanitized `href` → `javascript:` XSS |
| L3 | Medium | literate/cli | Multi-file doc: unattributed error remapped through the wrong module's line map |
| B3 | Low | backend (arm64) | `OpConstStr` length `mov` fails to assemble for string literals > 64 KiB |
| I3 | Low | optimiser | Monomorphisation caps struct instantiation at 8 rounds (poor diagnostic) |
| L4 | Low | diag | `FormatRemapped` renders an out-of-range generated line as a document line |
| L5 | Low | literate | Unclosed fern fence absorbs a trailing-newline artifact as a phantom body line |

4 Critical · 6 High · 3 Medium · 4 Low.

> **Already-tracked, not counted as findings.** `docs/BACKEND-PARITY.md`
> already documents the arm64-darwin heap-pointer truncation (slice/Map
> pointer slots stored as i32) and the wide-scalar Map-key limitation on
> wasm. The review confirmed those are the known limitations and did not
> double-count them. Note, however, that F2/B1/B2 below show the
> half-implemented `usize` story has produced *new* bugs beyond the
> documented ones.

---

## Critical

### F1 — `shadowrename` drops `StructLit.Base`; shadowed struct-update base miscompiles

- **Subsystem:** frontend
- **Location:** `internal/shadowrename/shadowrename.go:295-298` (the
  `*ast.StructLit` case in `walkExpr`)
- **Scenario:**
  ```fern
  import "core/no_prelude";
  struct P { x: i32, y: i32 }
  function main(): i32 {
    var base: P = P { x: 1, y: 2 };
    {
      var base: P = P { x: 100, y: 200 };   // shadows outer base
      var updated: P = P { ...base, y: 999 };
      return updated.x;                       // should be 100
    }
  }
  ```
- **Why it's wrong:** `walkExpr`'s `StructLit` case walks `n.Fields` but
  never `n.Base`, so the `...base` spread `Ident` is never rewritten to
  the inner binding's renamed slot. IR lowering then resolves `base` to
  the **outer** slot. The interpreter (independent scoping) returns the
  correct `100`; the wasm backend returns `1` (confirmed via
  `wasm-tools print` — the struct copy reads `local.get 0`, the outer
  base — and `wasmtime run --invoke main`). Type-checks clean, since the
  checker's own walk (`checker.go:4442`) *does* visit `n.Base`.
- **Fix sketch:** add `if n.Base != nil { r.walkExpr(n.Base) }` to the
  `StructLit` case. Audit every other `walkExpr` case against the
  checker's walk for the same omission class. Add a shadowrename unit
  test + a wasm/native e2e covering shadowed spread base.

### M1 — Map copy-on-write divergence: interp aliases, compiled backends copy

- **Subsystem:** interpreter vs compiled runtime
- **Location:** `internal/interp/interp.go:104-116` (`Map` is a pointer,
  `var m2 = m1` aliases) and `interp.go:879-894` (`builtinMapSet` mutates
  in place); vs `internal/stdlib/core/map.fern:174-188`,`:371-372`
  (`__map_cow_inplace` does real COW when rc > 1).
- **Scenario** (this is the backends' own `wasm_cow_test.go` `map_set` case):
  ```fern
  import "core/no_prelude";
  import "core/map";
  function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.set("a", 1);
    var n = m;
    n = n.set("a", 999);
    if (m.get_or("a", -1) != 1)   { return 1; }  // interp FAILS here
    if (n.get_or("a", -1) != 999) { return 2; }
    return 0;
  }
  ```
- **Why it's wrong:** `fern -interp` exits `1` (m observes n's mutation);
  native/wasm exit `0` (COW isolates m, per `TestWASMNativeRcCoWInc`).
  The interp is the documented reference oracle; this is a
  reference-vs-backend split on the exact program the project's own CoW
  test pins down. Affects `set`/`delete`/`clear`.
- **Fix sketch:** give the interp value-semantics on map aliasing —
  either copy-on-assignment, or an rc/COW shim mirroring the runtime, so
  a non-last mutation through an alias doesn't bleed into the original.
  This is the higher-risk fix; needs care that arrays/strings observe
  the same rule the runtime gives them.

### B1 — x86-64 truncates `usize` division and remainder to 32 bits

- **Subsystem:** backend (x86-64) — parity gap vs arm64/wasm
- **Location:** `internal/codegen/x86_64/x86_64.go:1978`
  (`emitIntDivRem`, dispatched from `OpDivS`/`OpRemS`, lines 958-961)
- **Scenario:** `var x: usize = 5_000_000_000; var y = x / 3;` compiled
  `-target x86-64`.
- **Why it's wrong:** `w64 := op.Width == 64`. For `usize` the checker
  stamps `Width == ir.WidthPtr (-1)`, so `w64` is false and the 32-bit
  path (`cdq`/`idiv ecx`) runs, truncating the dividend to its low 32
  bits. The function's *own doc comment* (lines 1973-1975) claims usize
  uses `rax/rcx/rdx` — the code contradicts it. arm64 handles this via
  `regForWidth(op.Width)` which tests `width == 64 || width == WidthPtr`
  (`arm64.go:6622`); wasm32's usize is genuinely 32-bit so it is correct.
  x86-64 is the lone broken backend, and no e2e exercises `usize` `/`/`%`.
- **Fix sketch:** `w64 := op.Width == 64 || op.Width == ir.WidthPtr`.
  Add a `Test{X86_64,Arm64,WASM}UsizeDivRem` parity case.

### I1 — SSA constant-folding ignores operand width → i32 wraparound lost

- **Subsystem:** optimiser (SSA pipeline, `-target wasm -backend ssa`)
- **Location:** `internal/ssa/constfold.go:251-297` (`tryFold` integer
  cases) and `internal/ssa/sccp.go:423-471` (`foldIntBinary`)
- **Scenario:**
  ```fern
  function main(): i32 {
    var x = 2000000000 + 2000000000;   // i32 add
    if (x < 0) { return 1; }           // runtime: true (wraps negative)
    return 0;
  }
  ```
- **Why it's wrong:** Fern's default integer is i32 (`Op.Width == 0`).
  Both SSA folders compute every integer op in Go `int64` and store the
  full 64-bit result with no mask to the op width, so `2e9+2e9` folds to
  `4000000000` instead of the i32-wrapped `-294967296`, and `< 0` folds
  to **false** while runtime i32 is **true**. Affects
  `OpAdd/OpSub/OpMul/OpShl/OpNeg` and the unsigned compares (which use
  `uint64` instead of `uint32`). The IR-level folder (`ir/fold.go`) does
  this correctly via `int32` arithmetic — the width-awareness was simply
  dropped on the SSA side.
- **Fix sketch:** mask/convert each folded result to the op's width
  (`int32`/`uint32` for width 0/32, etc.) in both `constfold.go` and
  `sccp.go`, mirroring `ir/fold.go`. Add an SSA fold test on the wrapping
  case + the unsigned-compare case.

---

## High

### F3 — Decimal integer literals overflow silently in the parser

- **Location:** `internal/parser/parser.go:3252-3255` —
  `n = n*10 + int64(c-'0')` with no overflow detection (the hex path at
  3247 correctly uses `strconv.ParseInt(...,64)`).
- **Scenario:** `var x: i64 = 99999999999999999999999999;` (26 digits)
  wraps to `-2537764290115403777`, which *fits* i64 and slips past the
  checker's E047 range check (it only sees the already-wrapped value).
  Program type-checks and runs with a garbage value, no diagnostic.
- **Fix sketch:** parse the decimal body with `strconv.ParseInt`/
  `ParseUint` (matching the suffix's signedness) and emit a diagnostic on
  overflow, exactly like the hex path.

### F4 — No missing-return / fall-off-end analysis for value-returning functions

- **Location:** `internal/checker/checker.go:3820-3833` — `checkFunction`
  never calls the existing `blockDiverges` (`checker.go:3005`) on the body.
- **Scenario:**
  ```fern
  function f(b: boolean): Point {
    if (b) { return Point { x: 10, y: 20 }; }
    // falls off the end when b == false
  }
  ```
  Interp yields `Void` where a `Point` is expected and crashes
  (`field access on non-struct interp.Void`); a scalar return type
  silently yields `0`.
- **Fix sketch:** in `checkFunction`, require a non-void body to diverge
  on all paths via `blockDiverges`, emitting a "missing return" diagnostic
  otherwise. **Caveat:** `blockDiverges`/`stmtDiverges` does not treat
  `while (true) {}` as diverging, so it must be taught about infinite
  loops first or it will false-positive. Fix both in the same change.

### F2 — `usize` is an implicit bidirectional type wormhole

- **Location:** `internal/checker/checker.go:3553-3582` (the `assignable`
  `usize`/pointer relaxations)
- **Scenario** (both type-check with zero errors):
  ```fern
  var big: i64 = 5000000000i64;
  var ptr: usize = big;     // i64 -> usize
  var small: i32 = ptr;     // usize -> i32  (silently truncates)

  struct Big { a: i64, b: i64, c: i64 }
  var s: string = "hi";
  var p: usize = s;         // pointer-shaped -> usize
  var b: Big = p;           // usize -> arbitrary struct (reinterprets bytes)
  ```
- **Why it's wrong:** the checker correctly forbids the direct `i64→i32`
  assignment, but `assignable` makes `usize` assignable to/from any
  `NumberType` *and* any pointer-shaped type, bidirectionally and without
  a cast. So `usize` launders width changes and reinterprets pointers
  (string→struct), defeating the width-cast rule and nominal typing.
  This is the prelude's intended escape hatch, but it is exposed
  implicitly to all user code.
- **Fix sketch:** require an explicit `as` cast for user-code
  `usize`↔other conversions; restrict the implicit relaxation to
  prelude/stdlib modules (or a `@unsafe`-marked surface). Design decision
  — see "Open design questions" below.

### M2 — Same-basename modules collide under mangling even when aliased

- **Location:** `internal/modload/modload.go:607-614` (`importLocalName`
  returns the path basename), `:638`,`:683-688` (`prefixFor(...,
  mod.name)`).
- **Scenario:**
  ```
  // a/util.fern -> pub function val(): i32 { return 1; }
  // b/util.fern -> pub function val(): i32 { return 2; }
  import "./a/util" as au;
  import "./b/util" as bu;
  function main(): i32 { return au.val() * 10 + bu.val(); }
  ```
  Fails: `error[E006]: function "util__val" redeclared`. Both modules
  mangle decls with the shared basename `util`. The
  `util.fern`/`types.fern`/`helpers.fern`-per-directory layout is common,
  and the import-bound-twice guard (`modload.go:419`) doesn't catch it
  (the local names — aliases — differ).
- **Fix sketch:** derive the mangle prefix from the canonical module path
  (or a per-module unique id), not the basename. Add a modload test with
  two same-basename modules under distinct aliases.

### M3 — Map `delete` iteration order: interp vs compiled

- **Location:** `internal/interp/interp.go:896-911` (`builtinMapDelete`
  order-preserving shift-down) vs `internal/stdlib/core/map.fern:611-665`
  (`__map_delete_impl` swap-with-last). The code comments even state
  opposite contracts (`interp.go:823` "insertion order preserved" vs
  `map.fern:433` "swap-with-last reorders").
- **Scenario:** insert keys 1..4, `delete(1)`, then `keys()[0]` →
  interp `2`, compiled `4`. Any iteration after a non-last delete differs.
- **Fix sketch:** decide the contract (most languages do *not* promise
  order after delete — documenting "unspecified after delete" is cheapest),
  then make interp and runtime agree, or explicitly document the order as
  unspecified and stop asserting either in tests.

### I2 — SSA DCE removes unused div/rem, eliminating the divide-by-zero trap

- **Location:** `internal/ssa/dce.go:52-67` (`isDeadOp`) via
  `internal/ssa/opkind.go:29-42` (`IsPure`, which returns true for
  `OpDiv/OpDivU/OpRem/OpRemU`).
- **Scenario:** `var unused = 10 / get_zero();` where `unused` is never
  read — should trap at runtime; DCE deletes it and the program completes.
- **Why it's wrong:** integer div/rem trap on a zero divisor; LICM
  already refuses to hoist them for this exact reason
  (`ssa/licm.go:173-179`), and SCCP skips folding div-by-zero
  (`sccp.go:432`) — DCE is missing the same guard.
- **Fix sketch:** make `IsPure` return false for `OpDiv*/OpRem*` (or add
  a `MayTrap` predicate DCE consults), matching LICM. Add a DCE test that
  an unused trapping division survives.

### L1 — Diagnostic remap off-by-one for escaped chunk markers (`\<<…>>`)

- **Location:** `internal/literate/literate.go:351`
  (`emit(indent+deEscapeRef(bl.text), bl.litLine, len(indent))`) +
  `deEscapeRef` (`:416`); consumed by `cmd/fern/main.go:147` (`remapFor`)
  and `internal/lsp/literate.go:146`.
- **Scenario:** a chunk body line that (after indentation) begins with an
  escaped marker, e.g. `\<<literal>> = 5;`, pulled into `fn main()` at
  4-space indent. A checker error on `literal` at generated col 5 remaps
  to document col 1 (the backslash) instead of col 2.
- **Why it's wrong:** `deEscapeRef` strips the leading backslash at emit
  time, making the generated line one byte shorter to the left of every
  token, but `ColShift` records only `len(indent)`, not the removed
  backslash. Every column at/after the strip is shifted left by one in
  both `fern -check` and LSP squiggles. This is exactly the
  generated→document remap class the project calls its most
  regression-prone surface, and no test covers escape + remap.
- **Fix sketch:** account for each removed backslash in `ColShift` (track
  a per-line column delta from `deEscapeRef`). Add a literate remap test
  with an escaped marker.

---

## Medium

### B2 — `float → usize` cast truncates to 32 bits on both natives

- **Location:** `internal/ir/ir.go:7699-7708` (cast lowering, the
  `srcIsFloat && dstIsInt` branch) — shared IR, affects arm64 + x86-64.
- **Scenario:** `var f: f64 = 5_000_000_000.0; var u = f as usize;` on a
  native target loses the high bits.
- **Why it's wrong:** `realW := dstInt.NormalWidth()` returns `-1` for
  usize; the clamp `if dw < 32 { dw = 32 }` turns `-1` into `32`, never
  `64`, so `OpITruncF64{Width:32}` is emitted. The sibling int→float
  branch (line 7684) correctly resolves usize via `b.ptrW*8`. wasm32 is
  unaffected (usize is 32-bit there).
- **Fix sketch:** resolve usize to the target pointer width
  (`dstInt.IsPointerWidth()` → `b.ptrW*8`) before clamping, mirroring the
  int→float branch.

### L2 — `-weave -html` emits unsanitized `href` → `javascript:` XSS

- **Location:** `internal/literate/htmlrender.go:229` (`renderEmphasis`:
  `reLink.ReplaceAllString(seg, ...href="$2"...)`).
- **Scenario:** prose `[click](javascript:alert(document.cookie))` through
  `fern -weave -html` produces a working `javascript:` link in the
  "self-contained HTML page" the CLI advertises as browser-openable.
  `data:text/html,…` works too. `htmlEscape` runs before the regex so
  quote-breakout is prevented, but scheme injection is not.
- **Fix sketch:** allowlist URL schemes (`http`/`https`/`mailto`/relative)
  in the link rewrite; drop or neutralise others. Add an htmlrender test.

### L3 — Multi-file doc: unattributed error remapped through the wrong module's line map

- **Location:** `cmd/fern/main.go:284-288` (the `if path == "" || path ==
  e.entryAbs` branch in `entry.format`).
- **Scenario:** a `file=`-multi-module document where a *non-entry* module
  has a top-level error the checker emits with `File() == ""`
  (`diag.Filed` only fills `File()` inside a known `FuncDecl`). The error
  is routed through `e.remaps[e.entryAbs]` — the *entry* module's tangle
  line map — and lands on an unrelated `.fern.md` line. Single-file is fine.
- **Fix sketch:** when `path == ""`, don't assume the entry; either thread
  the originating module through the diagnostic, or fall back to the
  unremapped `err.Error()` rather than mapping through an arbitrary
  module's line map.

---

## Low

### B3 — `OpConstStr` length `mov` fails to assemble for literals > 64 KiB (arm64)

- **Location:** `internal/codegen/arm64/arm64.go:6762`
  (`g.emit("mov w0, #%d", len(op.Str))`).
- **Why it's wrong:** `mov w0, #N` takes a 16-bit immediate; for
  `len > 0xFFFF` the assembler rejects/truncates. The sibling
  `OpConstI32` path guards exactly this (`ldr w0, =N` for large values,
  lines 6729-6733); the string-length path does not.
- **Fix sketch:** reuse the large-immediate path (`ldr w0, =N`) for
  `len(op.Str) > 0xFFFF`.

### I3 — Monomorphisation caps struct instantiation at 8 rounds

- **Location:** `internal/monomorph/monomorph.go:283-316` (the
  `for round := 0; round < 8` loop).
- **Why it's noteworthy:** polymorphically-recursive generics
  (`struct Nest[T] { head: T, tail: Nest[Nest[T]] }`) expand without
  bound; the fixed cap stops silently and the trailing re-check surfaces
  a confusing `monomorph: re-check failed` "compiler bug" instead of a
  clear "infinitely recursive generic type" diagnostic. Terminate-with-
  wrong-error, not a miscompile.
- **Fix sketch:** detect strictly-growing instantiation chains and emit a
  proper recursion-depth diagnostic (or pre-reject polymorphic recursion
  in the checker).

### L4 — `FormatRemapped` renders an out-of-range generated line as a document line

- **Location:** `cmd/fern/main.go:143-144` (`remapFor` passes through when
  `p.Line > len(lineMap)`) feeding `internal/diag/diag.go:228`
  (`pickLine`); LSP mirror `internal/lsp/literate.go:138`.
- **Why it's wrong:** an out-of-range generated line passes through in
  *generated* coordinates and is then used to index the *document*
  source, printing a caret over an arbitrary prose line. Latent — no
  concrete parser/checker trigger was found (token positions stay within
  the tangled source) — but the fallback is incoherent.
- **Fix sketch:** when the generated line exceeds the line map, fall back
  to `err.Error()` (no caret) rather than indexing the document.

### L5 — Unclosed fern fence absorbs a trailing-newline artifact as a phantom body line

- **Location:** `internal/literate/literate.go:171-174` (the collector
  loop runs to EOF when no closing fence exists).
- **Why it's wrong:** `strings.Split(src, "\n")` yields a trailing `""`;
  with no closing fence it becomes a `bodyLine{text:"", litLine:n}`,
  giving the chunk a phantom trailing blank line tagged to a
  non-existent document line, polluting tangle output and provenance.
- **Fix sketch:** treat EOF-without-closing-fence as an error (or drop the
  trailing synthetic empty line). Add a literate test.

---

## Verified-and-cleared (checked, not bugs)

To scope the result, these candidate surfaces were checked and found
sound:

- **IR-level** `fold.go` (correct i32-width folding + div/rem skip),
  `strength.go` (width-safe identities; signed div→shift correctly
  skipped; float self-compare not folded for NaN), `dce.go` (only removes
  provably-unreachable post-terminator code), `constprop.go`.
- **TCO** (`ir/tco.go`): fires only on self `OpCallDirect` with full argc
  immediately before a return; args fully evaluated before the
  reverse-order rebind (no clobber); mutual recursion left untouched.
- **SSA** float folding (NaN/-0.0 preserved), LICM (refuses trapping
  div/rem and impure ops).
- **Checker:** match exhaustiveness correctly excludes guarded arms;
  guarded `_` doesn't satisfy exhaustiveness; direct `i64→i32` rejected;
  `for x in arr` doesn't clash with struct literals; hex-literal overflow
  caught; generic enum type-args are immutable so the covariant relaxation
  isn't exploitable; generic *struct* args require `Equal`.
- **interp:** unsigned div/cmp/shift masking and `shiftCount` mirror
  wasm/arm64/x86 width semantics; float→int saturation bounds harmless.
- **stdlib:** `parse_int_radix` wraps identically on interp + backends (no
  divergence); JSON surrogate / keyword bounds checks correct.
- **modload:** same-basename via a *single* importer is correctly rejected;
  diamond/dedupe keys on canonical path and doesn't double-add.
- **backend:** slice-header i32 pointer truncation on arm64 is the
  *documented* arm64-darwin limitation, not a latent bug; closure env
  layouts diverge between arm64 (two-word) and x86-64 (single-word) but
  each is internally consistent with its `closureconv` layout.
- **literate:** chunk-expansion cycle detection catches direct + mutual
  recursion; same-name concatenation + out-of-order resolution correct;
  non-escape single-file line map (incl. indentation `ColShift`) correct.
- **fernstring:** pack/unpack/length bit-layout consistent for both ptr
  widths.

## Open design questions (need a human call)

- **F2 (`usize` wormhole):** how much of the implicit relaxation is
  *intended* as a prelude escape hatch vs. an accident exposed to user
  code? The fix (require `as` in user code / gate behind `@unsafe` /
  restrict to stdlib modules) is a language-design decision.
- **M3 (map delete order):** pick the contract — "unspecified after
  delete" (cheapest, matches Go/Rust/Swift) vs. forcing both
  implementations to a stable order (more work, slower runtime delete).

## Suggested fix order

Cheap, clearly-correct, well-isolated fixes first (each ships with a
regression test):

1. **F1** shadowrename `StructLit.Base` — one line + audit + tests.
2. **B1** x86-64 usize div/rem — one-line predicate + parity test.
3. **B2** float→usize cast width — IR width resolution + test.
4. **I1** SSA width-correct folding — mirror `ir/fold.go` + tests.
5. **I2** SSA DCE div/rem purity — `IsPure` fix + test.
6. **F3** parser decimal overflow — `ParseInt` + diagnostic + test.
7. **L1** literate escaped-marker `ColShift` — remap fix + test.
8. **M2** modload canonical-path mangle prefix — prefix change + test.
9. **B3 / L2 / L3 / L4 / L5 / I3** — small isolated fixes + tests.
10. **F4** missing-return analysis — needs the `while(true)` divergence
    fix first to avoid false positives.
11. **M1 / M3 / F2** — reference-oracle / design-decision items; resolve
    the open questions above before implementing.
