# Porting the type checker to the self-host (retiring the Go checker)

Goal: grow `examples/self_host/checker.fern` to parity with the Go
checker (`internal/checker/checker.go`, ~6700 LOC, 50 stable diagnostic
codes E001–E051) so the Go checker can be retired as the strict gate.

This is the **standalone type-checker port**. It is distinct from
`docs/SELFHOST-CHECKER.md`, which describes a narrow Option/Result guard
embedded in the asm emitter; this document is about the full
`checker.fern` rule set + coded diagnostics.

## Where we are

`checker.fern` (≈2000 LOC) already walks the whole `parser.Module`:
primitives, vars, binary/unary ops, string concat, arrays, structs +
fields, methods + receiver dispatch, closures, unions, function
signatures + arity/arg-type checks, struct-literal field checks. Errors
were reported only as a lenient `TypeUnknown(reason)` sentinel surfaced
through the single boolean `ModuleTypes.all_well_typed`. It is wired into
`fern.fern` and `pipeline.fern` as a pass/fail gate.

As of slice 1 it also returns `ModuleTypes.diags: Diag[]` — a list of
coded diagnostics (`Diag { code, message }`) carrying the same `E0XX`
codes the Go checker emits.

`checker.fern` is **not** in the native fixpoint bundle (lexer + parser +
asm + flatten + bundle_run), so it can evolve freely without fixpoint
risk; the regression gate is the Go-vs-self-host **differential** on
emitted diagnostic codes.

## The two gaps

1. **Diagnostic codes.** Convert boolean/sentinel reporting into coded
   `Diag`s and grow coverage to all 50 rules: declarations
   (E006/E007/E010/E013/E016/E017/E018/E019), types/assign/return
   (E002/E003/E004/E005/E012/E020/E038/E041/E047), control flow
   (E008/E011/E022), pattern matching
   (E014/E015/E023/E025/E026/E027/E028/E035/E036), generics (E019/E040),
   traits (E006/E021 conformance/coherence/object-safety/derive),
   `?` operator (E042), slices (E037), tuples (E024/E046), maps
   (E001/E045), closures (E044), and owned-parameter move checking
   (E050/E051).

2. **Source positions.** Tokens carry `line`/`col`, but the parser's AST
   nodes (`ExprBinary`, `StmtVar`, …) drop them, so diagnostics can't yet
   say `line:col`. Full `line:col: error[E0XX]` parity needs positions
   threaded from tokens into the AST — a front-end-wide change touching
   every AST struct and `parse_*` production (a `type_params`-style sweep
   across all backends' struct literals). Deferred behind code coverage:
   matching the **set of codes** a program triggers is the first parity
   milestone; precise spans are the second.

## Slice plan

Each slice is one PR, gated by the differential test
`internal/e2e/self_host_checker_codes_test.go`: a corpus of small
programs run through both checkers, asserting the self-host emits the
same code(s) the Go checker does — restricted to
`selfHostImplementedCodes`, which grows per slice.

- **Slice 1 (done): the diagnostic mechanism + first declaration rules.**
  Added `Diag { code, message }` and `ModuleTypes.diags`; emit E007
  (duplicate struct field) and E018 (duplicate parameter) from the decl
  tables. `all_well_typed` now also reflects `diags`. Added
  `checker_codes_run.fern` (prints one code per line) and the
  differential gate.
- **Slice 2 (done): function / method redeclaration** — E006 for a
  free function (same name, both receiver-less) and a method (same name +
  receiver type), flagging the redeclaration site like the Go checker.
  Struct / enum redeclaration (E006) and duplicate variants (E017) are
  deferred: the parser desugars enums into variant structs and drops the
  enum name, so a `mod.structs` entry can't be told apart from a variant
  — that grouping must be recovered first. Duplicate var in scope (E013)
  and reserved-name (E010) also remain.
- **Slice 3 (in progress): return-type mismatch** (E002). `ret_diags`
  walks each function body — recursing through if / while / for / match /
  defer sub-bodies and threading scope so a return sees locals declared
  earlier in its block — and emits E002 for every return whose value type
  isn't assignable to the declared return type. It reuses the same
  `type_assignable` predicate `check_stmt` already uses, so it fires
  exactly where the checker already flagged the function ill-typed (no new
  false positives) and only attaches the stable code. Remaining
  type-mismatch codes — E003 (assignment), E004 (arity), E038 (argument
  type) — follow in the next slices via the same per-function diagnostic
  collection.
- **Slice 4 (done): struct literal missing field** (E005). `slit_diags` /
  `stmts_slit_diags` recurse through every expression in a function body /
  top-level statements (and into nested blocks + lambda bodies), and for
  each non-update struct literal emit E005 for every declared field the
  literal omits. The struct table is module-global, so this needs no
  scope; struct-update literals (`Name { ...base, … }`) and
  non-struct/variant type names are skipped (conservative — no false
  positives).
- **Slice 4: control-flow + conditions** (E008, E011, E012).
- **Slice 5: pattern matching** (E014/E015/E025/E026/E027/E028/E036),
  incl. exhaustiveness.
- **Slice 6: traits** (E021 conformance/coherence/object-safety/derive).
- **Slice 7: `?` / slices / tuples / maps / literal-fits** (E042, E037,
  E024/E046, E045, E047).
- **Slice 8: owned-parameter move checking** (E050/E051).
- **Slice 9: source positions** — thread token line/col into the AST and
  upgrade every `Diag` to a span, reaching `line:col` parity.
- **Slice 10: wire as the gate** — `fern.fern` prints
  `line:col: error[E0XX]: msg` from the self-host checker; the Go checker
  leaves the differential gate.

## Differential testing

`internal/e2e/self_host_checker_codes_test.go` compiles
`checker_codes_run.fern` with the Go-built bundle compiler, runs it over
a corpus, and asserts the printed code set equals what Go's
`checker.Check` (formatted through `diag.Format`) reports for the same
source, intersected with `selfHostImplementedCodes`. New rules extend the
corpus and that set in lockstep.
