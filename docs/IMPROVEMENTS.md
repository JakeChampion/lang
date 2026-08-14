# Improvements drawn from fernsmith work

> **Status:** most items have landed (see the audit below). Still open: #5, #11,
> #15, #16, and #14 (tracked as #2673).

This list collects the compiler-correctness gaps, design rough edges,
and test-infrastructure improvements surfaced while building the
fernsmith generator + differential oracle (PRs #583..#620). Each
entry has a one-line claim, a brief justification, and a sketch of
the fix. Ordered by what's most worth chasing first.

Status legend: 🔥 actionable now / 🛠 small refactor /
🧪 test-infrastructure / 🎨 language-design (needs explicit buy-in).

## Status (audited 2026-05-28)

A spot-check across all 16 items found the doc significantly stale —
most concrete items have landed since it was written, but the doc was
never updated. Per-item current state:

- ✅ **Done**: #1 (`refineCallTypeArgsFromDest` lives in checker.go),
  #2 (`postSettleType` is wired through Var/Init/match paths), #3
  (`internal/interp/coverage_test.go` exists and passes for every AST
  variant), #4 (`internal/monomorph/monomorph_test.go` has tests),
  #6 (fernsmith fuzzer runs via `.github/workflows/test-fernsmith.yml`
  + `fuzz-diff.yml` + `fuzz-parse.yml`), #7 (`IsReservedName` is a
  public helper in checker.go), #8 (`evalChecked` was removed; the
  remaining `evalProgram` runs parse + checker + interp end-to-end and
  is documented as such), #9 (the `noFloats bool` was replaced
  with a structured field — comment at fernsmith.go:225), #10
  (`genericFuncs`/`genericStructs` are gone from the codebase), #12
  (the `-short`-gated `diffOracleSeeds(t)` helper landed in #1622),
  #13 (`as` extends to all types — `None as Option[i32]` type-checks and
  runs, #2669).
- 🔧 **Partially done**: #5 — the small packages called out in the fix
  sketch (`closureconv` / `shadowrename` / `treeshake`) all have
  `_test.go` now; only the giant codegen packages
  (`internal/codegen/{arm64,x86_64}`) remain without unit tests.
- ⬜ **Still open**: #11 (`gtype` is still an `int` iota in fernsmith
  — but on closer look the proposed "small refactor" understates the
  scope: making `gtype` a struct with kind+name would ripple through
  ~28 switch cases that compare against the `tI32`-style constants,
  the `numTypes` boundary check + dyn-alloc machinery, and the
  iterator in `gtype_internal_test.go`. The doc's stated motivation
  — `gtype.String()` can't see generator state — is already
  mitigated by the `g.typeName(t)` helper that callers use. **Needs
  a design call before any code lands** (effectively 🎨 rather than
  🛠), so grouped with the design-discussion bucket below), #14–16
  (language-design discussions — by their nature they require
  explicit buy-in before any code lands).

---

## 1. 🔥 Generic-call inference doesn't refine TypeArgs from destination

Found in PR #620.

```
function pick[T](cond: boolean, a: T, b: T): T { ... }
function main(): i32 {
    // Type-checks. Monomorph mangles to `pick__Result` with bare-
    // Result clone params; re-check rejects.
    var r: Result[i32, i32] = pick(true, Ok(1), Err(2));
    return 0;
}
```

Cause: variant constructors only fix the type params they have
payloads for. `Ok(1)` sets `T → i32`, leaves `E` unresolved →
`Result{}` (no args). The checker accepts the call (`assignable`
flows the destination's annotation to the resulting `Result{}`),
but the call's `TypeArgs` is stamped as `[Result{}]`. Monomorph
mangles to `pick__Result` and the cloned param/return types lack
the inner args, so the re-check fails with
"`Result has 2 type parameter(s), 0 supplied`".

**Fix**: In `internal/checker/checker.go`, after a call's
`TypeArgs` is computed but before it's returned, refine using the
destination type when present:

- `var x: T_dest = call(...)` → unify `T_dest` against the call's
  declared return type with the call's `sub` map, then re-stamp
  `TypeArgs`.
- Similarly for `return call(...)` against the enclosing fn's
  return type.

Langsmith's `skipGeneric` workaround in `expr` can drop after.

## 2. 🔥 `postSettleType`'s Call branch heuristic

Found in PR #601.

`postSettleType` rebuilds an EnumType's `Args` from a Call's args
to refresh post-`settleNumeric` payload widths. Originally fired
on *any* Call returning an EnumType, breaking non-variant
function calls like `f(p: boolean[]): Option[i32]` →
`Option[boolean[]]`.

Current fix: heuristic `isVariantCall` — callee is an Ident
starting with an uppercase letter. Matches Fern's naming
convention but it's not a guarantee.

**Fix**: Add `ast.Call.IsVariantCall bool` (mirror of
`IsPipe`). The checker sets it when it resolves a Call as a
variant constructor. `postSettleType` reads the flag. No
case-sensitivity heuristic, no risk that a future capitalised
helper name accidentally trips the branch.

## 3. 🧪 Interp silent-feature-gap coverage test

Found three times in this thread: `*ast.FString` (PR #597),
`*ast.MapLit` + Map methods (PR #610), `*ast.FuncDecl` /
`*ast.Lambda` (PR #618). Each was a `"unsupported expression %T"`
panic the fernsmith generator hit at random.

**Fix**: `internal/interp/coverage_test.go` that walks every
AST node type and asserts the interpreter accepts a minimal
example. Pattern:

```go
cases := []struct {
    name string
    node ast.Expr  // or ast.Stmt
    src  string    // minimal program exercising it
}{ ... }
```

Mirror Fern's existing `cmd/dump_wat` shape — one entry per AST
variant. Adding a new node forces a corresponding case (or an
explicit "not yet" skip with a TODO).

## 4. 🔥 Monomorph walker auto-generator (or comprehensive test)

The walker in `internal/monomorph/monomorph.go` walks the AST
looking for generic Calls + generic StructLits. Throughout this
thread it was missing seven AST shapes:

- `*ast.MapLit` (PR #614)
- `*ast.FString` (PR #614)
- `*ast.Assign` (PR #614)
- `*ast.Lambda` (PR #618)
- nested `*ast.FuncDecl` (PR #618)
- `*ast.Defer` (PR #618)
- `*ast.Switch` (PR #618)

Pattern: someone adds a new AST node, forgets to update the
walker, the bug surfaces months later when something exercises
the path.

**Fix (option A)**: Use `go/ast`-style code generation. A single
`ast.go` definition file describes each node's children; a
`go:generate` step emits the walker. Eliminates the class of bug.

**Fix (option B)**: Test that fuzzes minimal programs hitting
each AST variant and runs each pass. Catches missing cases
without code-gen tooling. Lower ceiling, lower floor.

## 5. 🧪 Tests for the `[no test files]` packages

`go test ./...` output lists these packages with no tests:
`internal/closureconv`, `internal/shadowrename`, `internal/treeshake`,
`internal/codegen/arm64`, `internal/codegen/x86_64`,
`internal/monomorph` (the last one now has tests since PR #614).

Each is a real pass with real behaviour. End-to-end tests in
`internal/e2e` exercise them indirectly, but unit-level tests
would catch regressions faster and document the expected shape.

**Fix**: Per-package `_test.go` for at least the public surface.
The codegen backends are largest; closureconv / shadowrename /
treeshake are small enough to fully cover.

## 6. 🧪 CI runs the fernsmith fuzzer

`FuzzGenerate_ParseRoundTrips` and `FuzzGenerate_ExecutionAgrees`
exist but no CI job runs them. The default `go test ./...` only
exercises seed-based deterministic sweeps, capped at 256 / 1024
seeds.

**Fix**: A CI job that runs each fuzzer for 60–120s on every PR,
with the corpus persisted across runs. Catches regressions the
deterministic sweep misses (which it has, twice in this thread).

## 7. 🛠 `IsReservedName(s string) bool` helper

The checker's `TestReservedBuiltinNamesCannotBeShadowed`
enumerates Option, Result, IoError, JsonValue, Reader, Writer,
HttpRequest, HttpResponse. The list is duplicated in the checker
init code and in the test. Langsmith has to know these too (to
avoid generating clashing names).

**Fix**: Single exported `func IsReservedName(s string) bool` in
`internal/checker` (or `internal/ast`), with all callers
consulting it. Test asserts the list matches what the checker
rejects.

## 8. 🛠 Merge `interp.evalProgram` and `evalChecked`

PR #610 added `evalChecked` (parser + checker + interp) because
the original `evalProgram` (parser + interp only) couldn't
exercise method-call rewrites. Both helpers still exist. Almost
every new test reaches for `evalChecked`.

**Fix**: Make `evalProgram` always run `checker.Check`. The few
existing tests that intentionally skip the checker (REPL-style
parser-only) can call `interp.New` + register manually.

## 9. 🛠 `noFloats` flag overloaded

The fernsmith generator's `Generator.noFloats bool` started life
as "skip f32 productions" and accumulated three meanings:

- skip f32 (float NaN/Inf edges differ across backends)
- this is the runnable / differential-oracle code path
- prefer deterministic-across-backends choices

**Fix**: Replace with a `Profile` enum or a struct of explicit
flags: `{NoFloats bool; RunnableMain bool; ...}`. Each gate site
checks the relevant flag, not an overloaded boolean.

## 10. 🛠 `genericFuncs` / `genericStructs` unification

`info.GenericFuncs map[string]*ast.FuncDecl` and
`info.GenericStructs map[string]*ast.StructDecl` flow through
parallel paths in monomorph. The instantiation loops are
near-duplicates.

**Fix**: Single `Generics map[string]GenericDecl` keyed on a sum
type. Halves the duplication; one place to fix when a new generic
form lands.

## 11. 🛠 `gtype` dual representation in fernsmith

The generator ended up with closed `iota` constants for builtins
and a sidecar `structShapes` / `enumShapes` map for dynamics.
Two lookup paths everywhere, with `g.typeName(t)` plastered over
the gap because the value-receiver `gtype.String()` can't see
generator state.

**Fix**: `gtype` becomes a struct with a kind + nominal name +
optional element. All lookups go through one helper. The
compiler's own `ast.Type` interface has the same shape — keeping
fernsmith's data model close to the compiler's would reduce
impedance.

## 12. 🧪 `go test ./...` runs ≥60s

Langsmith alone is 25s (256 seeds × parse+check+monomorph), e2e
is ~22s, ir + checker + lsp another 25s. Inner-loop iteration
gets painful.

**Fix**: `-short` mode that drops the sweeps to ~20 seeds, gated
by `testing.Short()`. The full 1024-seed sweeps move behind
`-tags=full` or `-long`. CI keeps running the full suite; dev
inner loop gets the short one.

---

## Language-design rough edges (need explicit buy-in)

The items below would change the language surface. Listed for
completeness; not picked up by the work-through below without
agreement.

## 14. 🎨 `function` keyword overloaded

Top-level decl, local decl, and anonymous expression all use
`function`. Parser tests have to spell out which form they're
testing. Local + anonymous look indistinguishable until the
parser reaches the name slot.

**Sketch**: `fn` for the expression form, `function` for the
named decl. Or use the keyword for both but require a name in
decl positions (parser disambiguates earlier).

## 15. 🎨 Variant names are globally unique

`Color { Red, Green, Blue }` and `Status { Red, Green, Blue }`
can't coexist — the checker resolves variants by bare name.
Langsmith's dynamic enums use a `__E<i>_V<j>` prefix purely to
sidestep this.

**Sketch**: Scoped variant lookup (`Color::Red` / `Color.Red`),
or per-enum disambiguation when the context clarifies which
enum a bare variant resolves to.

## 16. 🎨 `f32` half-wired

Generator avoids floats in the runnable path because Inf/NaN
edges differ across backends. CLAUDE.md retired arm32 because
parity was untenable; the same calculus may apply here. Decision
worth having explicitly: spec the IEEE edges or simplify
f32 to "you get what your platform gives".
