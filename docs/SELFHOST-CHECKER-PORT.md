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
- **Slice 5 (done): free-function call arity** (E004). `call_diags` /
  `stmts_call_diags` walk every expression (scope-threaded, so a callee
  name shadowed by a local `var` / closure is recognised as a local, not
  the function) and emit E004 when a free-function call's argument count
  doesn't match the function's declared parameter count. Conservative:
  only a name that resolves to a module function sig (and isn't a local
  binding) is checked; method and closure arity are deferred.
- **Slice 6 (done): assignment / annotated-var mismatch** (E003).
  `stmts_assign_diags` walks each body (scope-threaded) and emits E003 for
  an annotated `var x: T = v` whose init type isn't assignable to T, or an
  assignment `x = v` whose value type isn't assignable to x's declared
  type. Reuses the same `type_assignable` predicate check_stmt uses, so no
  new false positives. (The scope-threaded walks — ret_diags /
  stmts_call_diags / stmts_assign_diags — could later be unified into one
  pass.)
- **Slice 7 (done): free-function argument type** (E038). When a
  free-function call's arity matches, `call_diags` checks each argument
  against the declared parameter type (`type_assignable`) and emits E038
  per mismatch — only when arity matches (so indices line up; a wrong
  count is E004). Same conservative free-function / non-local gate as E004;
  method/closure argument types deferred. This completes the core
  type-mismatch family (E002/E003/E004/E005/E038).
- **Slice 8 (done): non-boolean condition** (E008). `stmts_assign_diags`
  now also checks each `if` / `while` condition's type and emits E008 when
  it isn't boolean (using the same `type_eq` against `bool` the checker
  uses). (Note: the self-host accepts `bool` as well as `boolean` as a
  type name; the Go checker only accepts `boolean` — a separate, minor
  leniency, not one of the ported codes.)
- **Slice 9 (done): break / continue outside a loop** (E011). `loop_diags`
  walks statements tracking an `in_loop` flag (set true in `while` / `for`
  bodies, passed through `if` / `match` / `defer`) and emits E011 for a
  `break` / `continue` reached outside a loop — so a `break` in a match arm
  is legal only when that match is inside a loop (matching the Go checker).
  Purely structural, no scope.
- **Slice 10 (done): return without value** (E012). A bare `return;`
  parses to a `punct:;` placeholder value; in a function with a declared
  (non-void) return type — the only context `ret_diags` runs in — that's
  E012. Void functions (no declared return) never reach `ret_diags`, so a
  bare `return;` there is fine, matching the Go checker.
- **Slice 11 (done): duplicate var in scope** (E013). `dupvar_diags`
  emits E013 for a `var` whose name was already declared by an earlier
  `var` in the SAME block; each block (function body, branch, loop /
  match-arm body) gets a fresh name set, so shadowing across nested blocks
  — and a `var` shadowing a parameter or loop / pattern binding — is
  allowed, matching the Go checker. Purely structural.
- **Slice 12 (done): empty array literal needs annotation** (E020).
  `dupvar_diags` (now the structural `var`-declaration walk) also emits
  E020 for an un-annotated `var x = []` whose empty-array initializer
  can't infer an element type. (E010 reserved-name was investigated and
  skipped: it doesn't fire in the standalone `-check` path the differential
  gate uses — builtins aren't injected there — so it isn't differentially
  testable.)
- **Slice 13 (done): boolean-operator operand type** (E009). `call_diags`
  now also checks that `&&` / `||` operands and a `!` operand are boolean,
  emitting E009 for a concrete non-bool operand (unknowns skipped). Scoped
  to the always-boolean operators; arithmetic / integer / float operand
  rules (also E009) and comparison mismatch (E041) follow.
- **Later: pattern matching** (E014/E025/E036), traits (E021), unknown
  field (E043), comparison mismatch (E041), tuples / maps / slices,
  owned-parameter move checking (E050/E051), then source positions.
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
