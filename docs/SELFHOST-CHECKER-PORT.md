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

2. **Source positions (in progress).** Tokens carry `line`/`col`; the
   AST is gaining them node by node. `Diag` now carries an optional
   `line`/`col` (0 = none), emitted via `dg` (no position) / `dg_at`
   (positioned); `Par.peek_line` / `peek_col` read the current token's
   position; and `fern -check` renders `line:col: error[E0XX]: …` when a
   diagnostic has a position, else `error[E0XX]: …`. The first node to
   carry a position is `EnumDecl`, so E006-enum / E017 already print the
   exact `line:col` the Go checker does. Remaining AST nodes
   (`StructDecl`, `FuncDecl`, then the `Expr` / `Stmt` variants) follow,
   migrating their `dg(...)` emissions to `dg_at(...)` as they go — a
   `type_params`-style sweep, done incrementally so the byte-identical
   fixpoint holds at every step (codegen ignores the position fields).

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
  Enum redeclaration (E006) and duplicate variants (E017) were deferred
  here (the enum→variant grouping is dropped at parse) and later landed in
  slice 20 once that grouping was restored. Struct redeclaration (E006-
  struct) and reserved-name (E010) remain.
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
- **Slice 14 (done): equality comparison mismatch** (E041). `call_diags`
  emits E041 for an `==` / `!=` whose operands aren't the same type — the
  same rule `check_expr` applies. (Ordering ops `<` / `<=` / `>` / `>=` on
  mismatched types are E009, not E041, and aren't handled here.)
- **Slice 15 (done): unknown struct field** (E043). `call_diags` emits
  E043 for a field READ on a struct that has no such field (and no method
  of that name). A method-call callee (`obj.m(...)`) never reaches the
  field-read path — the ExprCall arm recurses only into the object — so
  method calls aren't mis-flagged. Reuses the same struct/field lookup
  `check_expr` uses, so no new false positives.
- **Slice 16 (done): slice bound must be i32** (E037). `call_diags` emits
  E037 for a present slice bound whose type isn't i32. A missing bound
  (`s[:]`, `s[a:]`) parses to an unknown placeholder and is skipped, so
  full / open slices aren't flagged. (Aside: the existing `check_expr`
  ExprSlice arm over-rejects open slices via `all_well_typed` — a separate
  pre-existing quirk, not a diagnostic, so it doesn't affect the code
  differential.)
- **Slice 17 (done): tuple field index** (E046). The field-access arm of
  `call_diags` emits E046 when a tuple field name isn't a numeric index
  ("requires a numeric index") or the index is out of range. Fires for an
  inferred tuple value (`var t = (1, 2)`), where `check_expr` yields a
  `TypeTuple`; an annotated tuple (`var t: (i32, i32)`) doesn't yet resolve
  to a `TypeTuple` (a separate self-host gap), so it's under-reported there
  — safe, and the differential corpus uses inferred tuples where the two
  checkers agree.
- **Slice 18 (done): arithmetic operand type** (E009, extending slice
  13). `call_diags`' binary arm now also flags `+` operands that aren't a
  matching string / i32 / f64 pair, and `-` / `*` / `/` / `%` operands
  that aren't matching i32 / f64 (`%` is i32-only) — the same overload
  rules `check_expr` applies, so E009 fires only where check_expr already
  rejects the operands.
- **Slice 19 (done): integer literal out of i32 range** (E047). The
  structural `var` walk emits E047 when an un-suffixed integer literal
  assigned to an `i32` var exceeds i32's max — `digits_fit_i32` compares
  the decimal digit string against "2147483647" (length, then char-by-char
  at 10 digits). Scoped to `i32`-annotated vars (other integer widths /
  contexts are under-reported — safe).
- **Slice 20 (done): enum→variant grouping + enum declaration codes**
  (E006-enum, E017). The parser desugars enums into variant structs and
  dropped the enum name, so the checker couldn't tell a variant from a
  struct. This adds an `enums: EnumDecl[]` field to `Module`
  (`EnumDecl { name, variant_names }`), populated at parse and threaded
  through `flatten`'s bundle/merge — additive, and codegen ignores it, so
  the byte-identical fixpoint holds (verified x86 + arm64). With the
  grouping, `collect_decl_diags` emits E006 for a redeclared enum and E017
  for a duplicate variant within an enum — closing the deferral from
  slice 2. This grouping is also the prerequisite for the remaining
  match/variant codes (E014/E023/E036).
- **Slice 21 (done): variant declared in multiple enums** (E036). Using
  the slice-20 grouping, `ambiguous_variants` collects variant names that
  appear in 2+ enums, and `vref_stmts` / `vref_expr` (scope-threaded so a
  shadowing local isn't flagged) emit E036 for a bare, unqualified
  reference to one — a qualified `Enum.Variant` is an `ExprFieldAccess`,
  so it's not flagged. Fires only on reference, matching the Go checker.
- **Slice 22 (done): structural match-arm errors** (E026, E028).
  `match_diags` walks each `match` (no types needed): E026 when a wildcard
  `_` arm isn't last, E028 when a variant pattern repeats one an earlier
  arm already covered. Recurses into nested blocks + arm bodies.
- **Slice 23 (done): generic-struct type-argument arity** (E019).
  `type_arity_diag` compares the explicit `[...]` argument count on a
  generic-struct reference against the struct's declared type-parameter
  count (`count_type_args` + `struct_type_param_count`). Checked at
  **parameter** and **struct-field** positions — where the Go checker
  reports E019 on its own; a `var` / return annotation with the wrong
  arity additionally trips E003 / E002 in Go, so those positions are left
  out to keep the code sets matching.
- **Later: pattern matching** (E014 bad-variant-in-match, E023 — both
  need match-scrutinee enum *type* tracking, which the checker's type
  system doesn't model yet), traits (E021), maps (E045), owned-parameter
  move checking (E050/E051), then source positions, then wiring the
  self-host checker in as the gate.
- **Slice 24 (done): surface the diagnostics in `fern -check`.** The CLI
  driver's `-check` mode now prints each coded diagnostic as
  `error[E0XX]: message` on stderr (still no `line:col` prefix until
  positions are threaded), instead of only signalling well-typedness
  through the exit code. So the 24 codes built across slices 1–23 are now
  user-visible from the self-hosted CLI — the first half of "wire it in as
  the gate" (the second half, retiring the Go checker from the
  differential, waits on source positions for full output parity).
  Gated by `TestSelfHostCLIX86_64/check-bad`, which now also asserts the
  E004 code appears on stderr.
- **Slice 25 (done): source-position foundation + `EnumDecl` positions.**
  Established the position mechanism end-to-end: `Diag` gains optional
  `line`/`col` (via `dg` / `dg_at`); `Par.peek_line` / `peek_col`; the 35
  existing emissions moved to the position-less `dg(...)` helper; and
  `fern -check` prints `line:col: error[E0XX]: …` when present. `EnumDecl`
  is the first node to carry a position (captured at the `enum` keyword),
  so **E006-enum / E017 now print the exact `line:col` the Go checker
  emits** (an enum redeclared on line 2 → `2:1: error[E006]`).
  parser.fern is in the fixpoint bundle, but the new fields are
  codegen-ignored, so the byte-identical fixpoint holds (x86 + arm64).
  Gated by a new `TestSelfHostCLIX86_64/check-position`. Remaining nodes
  (`StructDecl`, `FuncDecl`, then `Expr`/`Stmt`) migrate in follow-ups.
- **Slice 26 (done): `StructDecl` positions → E007 line:col.**
  `StructDecl` gains `line`/`col`, captured at the `struct` keyword (all
  three `parse_struct_decl` returns); enum-variant + builtin structs carry
  0, and the monomorphiser / merge / flatten rewrite + clone sites
  propagate the source struct's position. **E007** (duplicate field) now
  prints `1:1: error[E007]: …`, matching Go. 13 parser + 1 flatten site
  updated; fixpoint-safe (codegen ignores the fields; x86 + arm64
  verified). Gated by `TestSelfHostCLIX86_64/check-position-struct`.
- **Slice 27 (done): `FuncDecl` positions → E006 / E018 line:col.** The
  largest sweep — `FuncDecl` gains `line`/`col` across all 33 construction
  sites (found via the compiler's own "missing field" errors).
  `parse_func_decl` captures the `function` keyword; `parse_impl_decl`, the
  monomorphiser clones, `lower_defers`, merge, and the flatten / constfold
  / ssa / wasm rewrites propagate; synthesised `@derive` methods + the
  lambda / main wrappers carry 0. **E006** (func/method redeclared) and
  **E018** (dup param) now print `2:1: error[E006]` / `1:1: error[E018]`,
  matching Go. Fixpoint-safe (x86 + arm64 verified). Gated by
  `TestSelfHostCLIX86_64/check-position-func`.
- **Slice 28 (done): `StmtVar` positions → E003 / E013 / E020 line:col.**
  First statement node. `StmtVar` gains `line`/`col`; `parse_stmt` captures
  the `var`/`let` keyword and the regular-var + tuple-destructure paths
  build via a new `s_var_at` helper (`s_var` keeps a 0 position for
  desugar / synth callers); the monomorphiser / constfold / flatten
  rebuilds propagate, the ssa lowering synthetics carry 0. **E003** /
  **E013** / **E020** now point at the `var` keyword, matching Go. (E047
  stays position-less — Go reports it at the literal, an `ExprNumber`
  position, in a later slice.) Fixpoint-safe (x86 + arm64). Gated by
  `TestSelfHostCLIX86_64/check-position-var`.
- **Slice 29 (done): `StmtReturn` positions → E002 / E012 line:col.**
  `StmtReturn` gains `line`/`col`; `parse_stmt` captures the `return`
  keyword and the parse path builds via `s_return_at` (`s_return` keeps a 0
  position for the ~24 synth / desugar callers); rebuilds propagate. E002
  (return-type mismatch) and E012 (return without value) now point at the
  `return` keyword, matching Go. Fixpoint-safe (x86 + arm64). Gated by
  `TestSelfHostCLIX86_64/check-position-return`.
- **Slice 30 (done): `StmtAssign` positions → E003-assign line:col.**
  `StmtAssign` gains `line`/`col`, captured at the `=` token (where the Go
  checker reports an assignment type error, not the lvalue) via a new
  `s_assign_at`; rebuilds propagate, ssa for-loop increment carries 0. The
  assignment case of **E003** now prints `1:42: error[E003]`, matching Go.
  Fixpoint-safe. Gated by an extended `check-position-var`.
- **Slice 31 (done): `ExprStructLit` positions → E005 line:col.**
  `ExprStructLit` gains `line`/`col`, captured at the struct-literal type
  name in `parse_primary`'s ident arm and threaded through `e_struct_lit`
  / `e_struct_update` / `parse_struct_lit_body`; generic + qualified
  callers pass 0 (safe under-report), and mono / `flatten` / `constfold`
  rebuilds propagate. **E005** (struct literal missing field) now prints
  `2:35: error[E005]`, matching Go. Fixpoint-safe (the field is
  codegen-ignored). Gated by a new `check-position-structlit`.
- **Slice 32 (done): `ExprCall` positions → E004 line:col.**
  `ExprCall` gains `line`/`col`, captured at the call's **opening paren**
  in `parse_postfix` (matching the Go parser's `Call{P: open.Pos}`, not
  the callee start) via `e_call_at`; the mono / `flatten` / `constfold`
  rebuilds propagate, and the synthetic `for`-desugar calls in `ssa`
  carry 0. **E004** (free-call arity mismatch) now prints
  `2:32: error[E004]`, matching Go. Fixpoint-safe. Gated by a new
  `check-position-call`. (E038 — per-argument type mismatch — reports at
  the *argument's* position, so it waits on leaf-expression positions.)
- **Slice 33 (done): `ExprNumber` positions → E047 + E038(number) line:col.**
  `ExprNumber` gains `line`/`col`, captured at the numeric literal in
  `parse_primary` via `e_number_at`; constfold's fold-to-constant and the
  synthetic `for`-desugar numbers in `ssa` carry 0. A new `expr_line` /
  `expr_col` helper in the checker reads a positioned node's position, so
  **E047** (literal out of range) prints `1:37: error[E047]` and the
  number-argument case of **E038** prints `2:33: error[E038]`, both
  matching Go. (E038 with a non-number argument still reports at 0 until
  the matching leaf node is positioned.) Fixpoint-safe. Gated by a new
  `check-position-number`.
- **Slice 34 (done): `ExprIdent` positions → E036 + E038(ident) line:col.**
  `ExprIdent` gains `line`/`col`, captured at the identifier token in
  `parse_primary` via `e_ident_at`; the flatten mangle/qualified-collapse
  and the mono callee rebuild propagate the original ident's position,
  and the synthetic temps in `ssa` / `wasm` carry 0. `expr_line` /
  `expr_col` gain an `ExprIdent` arm, so **E036** (ambiguous unqualified
  variant) prints `3:32: error[E036]` and the ident-argument case of
  **E038** prints `2:49: error[E038]`, both matching Go. Fixpoint-safe.
  Gated by a new `check-position-ident`.
- **Slice 35 (done): `ExprBinary` / `ExprUnary` positions → E009 + E041 line:col.**
  Both gain `line`/`col`, captured at the **operator token** (matching
  the Go parser's `Binary{P: opTok.Pos}` / `Unary{P: op.Pos}`) via
  `e_binary_at` / `e_unary_at`; the flatten + mono rebuilds propagate,
  and the synthetic `for`-desugar binaries in `ssa` plus constfold's
  can't-fold returns carry 0. **E009** (non-boolean `&&`/`||`/`!`,
  mismatched `+`/arithmetic operands) prints `1:60` / `1:38` and **E041**
  (compare mismatch) prints `1:35`, all matching Go. Fixpoint-safe. Gated
  by a new `check-position-operator`.
- **Slice 36 (done): `ExprFieldAccess` positions → E043 + E046 line:col.**
  `ExprFieldAccess` gains `line`/`col`, captured at the **dot** (matching
  the Go parser's `FieldAccess{P: dot.Pos}`) via `e_field_access_at`; the
  flatten qualified-collapse / passthrough and the mono rebuilds
  propagate, and the synthetic `.len()` / `__env` accesses in `ssa` carry
  0. `expr_line` / `expr_col` gain an `ExprFieldAccess` arm. **E043**
  (no such struct field) prints `2:55: error[E043]` and **E046** (bad
  tuple index) prints `1:48: error[E046]`, both matching Go.
  Fixpoint-safe. Gated by a new `check-position-field`. With this, every
  expression node the self-host checker reports on (number / ident /
  call / struct-lit / binary / unary / field-access) carries a source
  position.
- **Slice 37 (done): E008 + E037 line:col (checker-only).**
  No AST change — both codes are reported at an already-positioned
  sub-expression, so the emitters just call `expr_line` / `expr_col` on
  it. **E008** (if / while condition not boolean) lands on the condition
  (`1:28` for `if (5)`, `1:31` for `while (5)`) and **E037** (slice bound
  not i32) lands on the offending bound (`1:72` low, `1:74` high), all
  matching Go. Trivially fixpoint-safe (checker isn't in the bundle).
  Gated by a new `check-position-cond-slice`.
- **Slice 38 (done): `StmtBreak` / `StmtContinue` positions → E011 line:col.**
  Both gain `line`/`col`, captured at the keyword in `parse_stmt` via new
  `s_break_at` / `s_continue_at` (matching the Go parser's `Break{P}` /
  `Continue{P}`). **E011** (break / continue outside a loop) now prints
  `1:24: error[E011]` for both, matching Go. Fixpoint-safe. Gated by a new
  `check-position-break-continue`.
- **Slice 39 (done): `MatchArm` positions → E026 + E028 line:col.**
  `MatchArm` gains `line`/`col`, captured at the arm's pattern in
  `parse_match_stmt` (matching the Go checker's `arm.P`). Because the
  `-check` pipeline runs `flatten.bundle` *before* the checker, the
  flatten (and constfold / mono) MatchArm rebuilds must **propagate** the
  arm position, not zero it. **E026** (non-final wildcard `_`) prints
  `2:52: error[E026]` and **E028** (variant covered twice) prints
  `2:72: error[E028]`, both matching Go. Gated by a new
  `check-position-match-arm`.
  *Gotcha for future slices:* the **asm backend assigns struct-literal
  fields positionally** (`P { b: 9, a: 4 }` stores `9` into field `a`),
  so a struct literal's field order MUST match the declaration order —
  always append new `line`/`col` fields at the END of both the struct
  and every literal, never the front. (This bit slice 39: a blanket
  insert put them first and segfaulted the fixpoint until reordered.)
- **Slice 40 (done): E019 line:col (checker-only) — positioning arc complete.**
  E019 (generic-struct type-argument arity mismatch) is reported at the
  struct's *declaration* (Go's `sd.P`), not the use site. New
  `struct_decl_line` / `struct_decl_col` helpers look the struct up in the
  `StructDecl[]` (whose positions landed in slice 26) and the emitter
  passes them. **E019** now prints `1:1: error[E019]`, matching Go.
  Trivially fixpoint-safe (checker-only). Gated by a new
  `check-position-type-arity`.

  **With slice 40, every diagnostic the self-host checker emits carries a
  source position matching the Go checker.** The full set — E002, E003,
  E004, E005, E006, E007, E008, E009, E011, E012, E013, E017, E018, E019,
  E020, E026, E028, E036, E037, E038, E041, E043, E046, E047 — prints
  `line:col: error[E0XX]` identically to `diag.Format`. The
  source-positioning goal (slice 9 / 10 below) is achieved.
- **Slice 41 (done): E034 — heterogeneous array element type.** First
  NEW diagnostic code past the positioning arc. `call_diags`' `ExprArray`
  arm now anchors on the first element's type and flags the first later
  element whose type differs, reported at that element (Go's `el.Pos()`).
  Conservative by design — a new `is_primitive_type` guard restricts it
  to arrays where every element is a known scalar (i32 / bool / string /
  f64), so it never trips on arrays of structs / enums / nested arrays
  (whose union widening this port doesn't model). This guarantees ZERO
  false positives on real code — verified by the differential gate, which
  compiles `checker.fern` / `flatten.fern` / `bundle_run.fern` (all full
  of array literals) and sees no E034. `ExprString` gained `line`/`col`
  (appended last, per the slice-39 positional-field rule) so a string
  offender positions too: `[1, "x", 3]` → `1:36`, `["a", 1]` → `1:38`,
  both matching Go. Fixpoint-safe. Gated by a new corpus
  (`array-elem-*`), `check-position-array-elem`, and the
  `selfHostImplementedCodes` entry.
- **Slice 42 (done): E035 — variant pattern in a match on a non-enum.**
  In `stmts_call_diags`' scope-aware `StmtMatch` arm, the scrutinee is
  typed via `check_expr`; if it's a primitive (i32 / string / f64 / bool)
  and an arm is a *named* variant pattern, that's E035, reported at the
  arm (Go's `arm.P`). Conservative again — literal patterns parse to
  empty-name variants (skipped) and `true` / `false` are literal bool
  patterns (skipped), and the primitive guard means an enum / Option /
  Result scrutinee (the only kind the real bundle matches on) is never
  flagged. Verified zero false positives by running `fern -check` over
  all nine self-host modules. `match (n) { A => … }` with `n: i32` →
  `2:52: error[E035]`, matching Go's code, position, and message.
  Checker-only (trivially fixpoint-safe). Gated by new corpus
  (`match-variant-on-*`, `match-i32-wildcard-only-ok`),
  `check-position-match-nonenum`, and the `selfHostImplementedCodes`
  entry.
- **Slice 43 (done): E004 for method-call arity.** Extends the existing
  free-function arity check to method calls `obj.m(args)`: when `obj`
  types to a known struct (`TypeStruct`) and `m` resolves to a
  USER-DEFINED method via `lookup_method`, the explicit-argument count
  must equal the method's `param_types` length (the receiver is stored
  separately, so `self` is already excluded). Reported at the call's
  opening paren (Go's `n.P`). Conservative: built-in methods (`.len` /
  `.push` / `.write` …) and non-struct receivers aren't in the method
  table, so `lookup_method` returns empty and they're skipped — verified
  zero false positives by running `fern -check` over all thirteen
  self-host modules. `p.add(5)` for a 2-arg method → `3:59: error[E004]`,
  matching Go's code + position. (The self-host message — `method "add"
  expects 2 argument(s), got 1` — is clearer than Go's receiver-counting
  `function expects 3, got 2`; the gate compares codes + positions, not
  message text.) Checker-only (trivially fixpoint-safe). Gated by new
  corpus (`method-too-few-args`, `method-too-many-args`,
  `method-correct-arity-ok`) and `check-position-method-arity`.
- **Slice 44 (done): E038 for method arguments (primitive-restricted).**
  When a method call's arity matches, each argument is type-checked
  against the method's `param_types` and a mismatch is E038, reported at
  the argument (Go's `arg.Pos()`), message `argument N to "m": expected X,
  got Y`. **Restricted to primitive param-vs-arg pairs** (i32 / bool /
  string / f64): the self-host's `type_assignable` can't faithfully
  compare struct / union / qualified types the way Go's `assignable`
  does, so an UNRESTRICTED version false-positived all over the bundle.
  The primitive guard (same shape as E034) keeps it sound — verified zero
  method-E038 across all thirteen self-host modules — while still catching
  the unambiguous case (`p.add(s)` with a string where the method wants
  i32 → `3:81: error[E038]`, matching Go's code + position). Checker-only
  (trivially fixpoint-safe). Gated by new corpus (`method-arg-type-
  mismatch`, `method-arg-type-ok`) and `check-position-method-argtype`.
  *Limitation recorded:* full (non-primitive) method-arg type parity needs
  a stronger `type_assignable` (struct/union/qualified-type comparison) —
  the same type-model gap that blocks exhaustiveness (E030) and several
  other codes.
- **Slice 45 (done): type-model strengthening — array-type resolution +
  array assignability; lifts the slice-44 restriction.** Two fixes to the
  type layer:
  1. `type_from_name_with_structs_unions` now recognises the `Elem[]`
     **array suffix** (recursing on the element, wrapping in `TypeArray`,
     nesting for `X[][]`). Previously an `X[]` parameter / field type
     resolved to `unknown`, which is what made struct / array argument
     checks spuriously fire.
  2. `type_assignable` now recurses **element-wise into arrays**, and an
     empty / uninferred array literal (`unknown[]`) is assignable to any
     concrete `T[]`. Crucially, **scalar `unknown` is NOT a wildcard** —
     a genuinely unresolved value still surfaces its assignment error
     (e.g. `var s: string = badMethodCall()` stays ill-typed; the
     checker self-test `src55` guards exactly this).
  With both, the method-argument E038 check (slice 44) drops its
  primitive-only restriction and uses full `type_assignable`. Verified
  **zero** false positives across all thirteen self-host modules, and it
  now catches non-primitive mismatches Go does — e.g. passing an `i32`
  where a method wants `string[]` (`3:77: error[E038]`). Checker-only
  (fixpoint-safe). Gated by new corpus (`method-arg-array-mismatch`,
  `method-arg-empty-array-ok`); the existing `src55` self-test assertion
  guards the scalar-unknown-stays-strict invariant. This narrows the
  type-model gap that blocks struct/union argument parity and (longer
  term) exhaustiveness.
- **Slice 46 (done): E043 — struct-literal field-value type mismatch.**
  Building on the slice-45 type model, `call_diags`' `ExprStructLit` arm
  now checks each provided field's value type against the declared field
  type (via `lookup_struct` + a new `field_index` helper) and emits E043
  `field "f": expected X, got Y` at the value (Go's `f.Value.Pos()`).
  Guarded on BOTH sides by `!is_unknown` — when the declared field type
  doesn't resolve (cross-module struct types still mangle to `unknown`
  here) or the value type is unresolved, the pair is skipped. That guard
  is what makes it sound: an unguarded version produced ~370 false
  positives per module (every `tok: lexer.Token` field, whose declared
  type resolved to `unknown`); with the guard it's **zero** new false
  positives across all thirteen modules. `P { x: 1, y: "no" }` for a
  `y: i32` field → `2:48: error[E043]`, matching Go's code, position, and
  message. Checker-only (fixpoint-safe). Gated by new corpus
  (`struct-field-type-mismatch`, `struct-field-type-string-ok`) and
  `check-position-struct-field-type`. *Next type-model step:* resolve
  cross-module (mangled) struct/union names in field/param type strings,
  which would lift the `unknown` guard's coverage here and unblock the
  same comparison for struct-typed method args.
- **Slice 47 (done): resolver unification — array + union field types.**
  The type-name resolvers had drifted: only `_with_structs_unions` knew
  the `Elem[]` array suffix (slice 45), and the struct-field-type builder
  used `_with_struct_names`, which knew neither arrays nor unions. So a
  field typed `T[]` or a union (`parser.Expr`, `parser.Stmt[]`) resolved
  to `unknown`, which is why slice 46's E043 had to skip them. This slice
  (1) adds the array-suffix branch to `_with_structs`,
  `_with_struct_names`, and `_with_names_and_unions`, and (2) threads the
  module's **union names** into `collect_struct_sigs` (extracted from
  `mod.aliases` up front, before the full `UnionSig` table) so field
  types resolve through `_with_names_and_unions`. Net effect: struct
  field types now resolve to `TypeArray` / `TypeUnion` / cross-module
  `TypeStruct` instead of `unknown`, and the slice-46 field-value E043
  check covers them — e.g. `P { xs: 5 }` for an `xs: i32[]` field →
  `2:43: error[E043]: field "xs": expected i32[], got i32`, matching Go.
  Verified **zero** field-value E043 false positives across all thirteen
  modules (down from ~82/module mid-slice before the union fix).
  Checker-only (fixpoint-safe). Gated by new corpus
  (`struct-field-array-mismatch`, `struct-field-array-ok`); the checker
  self-test + full differential corpus continue to pass (the resolvers
  feed func / method / union sigs too, so this is a broad but verified
  change).
- **Slice 48 (done): E030 — non-exhaustive union match.** The marquee
  pattern-matching check, now reachable thanks to the slice-45/47 type
  model (union scrutinees resolve to `TypeUnion`). In the scope-aware
  `StmtMatch` arm: when the scrutinee types to a known union and no arm is
  a `_` wildcard, every union variant not covered by a variant pattern is
  one E030, reported at the **`match` keyword**. That required a new
  `StmtMatch` `line`/`col` (captured in `parse_match_stmt`, appended last
  per the asm positional-field rule, propagated through flatten /
  constfold / mono / ssa rebuilds — fixpoint stays byte-identical). The
  exhaustiveness logic was validated by a probe BEFORE the parser change:
  **zero** false positives across all thirteen self-host modules (their
  union matches are all exhaustive or wildcarded, and the check correctly
  recognises them via mangled variant-name matching). `match (u) { A(a)
  => … }` for `u: A | B` → `4:25: error[E030]: match is not exhaustive —
  variant B of enum U is not covered (add an arm or use `_`)`, matching
  Go's code, position, and message. Gated by new corpus
  (`union-match-non-exhaustive`, `-exhaustive-ok`, `-wildcard-ok`) and
  `check-position-exhaustiveness`. (Enum-decl exhaustiveness — distinct
  from union aliases in this port — remains future work; the self-host
  doesn't yet type enum-valued scrutinees, so those matches are skipped.)
- **Slice 49 (done): E030 for enum decls too.** Resolves the slice-48
  follow-up: `check_module` now registers every `enum` decl as a
  `UnionSig` (name = enum name, variants = the variant names), so an
  enum-typed scrutinee types to `TypeUnion` and flows through the exact
  same E030 path (and union-variant assignability) as a `type X = A | B`
  alias. Zero new machinery in the match arm. The self-host's own source
  uses **no** enum decls, so the change is inert on the bundle (verified:
  zero E030/E003/E038 across the modules) and the existing E026/E028 enum
  corpus + the union-match self-test assertions stay green. `match (e) {
  A => … }` for `enum E { A, B }` → `2:25: error[E030]`, matching Go; an
  exhaustive enum match (incl. payload variants like `Has(i32)` / `Nil`)
  stays clean. Gated by new corpus (`enum-match-non-exhaustive`,
  `enum-match-exhaustive-ok`, `enum-match-payload-exhaustive-ok`) and an
  `enum_missing_variant` CLI case. With this, E030 has full match-
  exhaustiveness parity with Go for both enum and union scrutinees.
- **Slice 50 (done): fix — bind match payloads in the diagnostic walk.**
  A real correctness bug (not a new code): the body-check path bound a
  variant pattern's payload (`TokIdent(t) => …`) to `TypeStruct(variant)`,
  but the *diagnostic* walk (`stmts_call_diags`) walked arm bodies in the
  un-extended scope, so a field access on the binding (`t.name`)
  mis-resolved and tripped a spurious read-side E043 ("struct X has no
  field"). This false-positived 8× per module on lexer's union-variant
  matches — latent today (those files aren't differential-corpus inputs)
  but a blocker for actually running the self-host checker on real
  multi-module code. The fix mirrors the body-check binding: each arm gets
  a derived scope binding `pv.binding → TypeStruct(pv.type_name)` before
  its body is walked. Read-side E043 false positives drop from 8/module to
  **zero** across all fifteen modules, while a genuine bad access
  (`A(a) => a.nope`) still flags E043 (matches Go). Checker-only
  (fixpoint-safe). Gated by new corpus (`match-binding-field-ok`,
  `match-binding-bad-field`).
- **Slice 51 (done): E014 — variant pattern not in the scrutinee's
  enum/union.** Folded into the same `StmtMatch` coverage loop as E030: a
  `PatVariant` whose name isn't in the scrutinee union's variant list is
  E014, reported at the arm. Zero false positives across all fifteen
  modules (the bundle's matches only name real variants). `match (u) { …,
  C(c) => … }` for `type U = A | B` → `5:62: error[E014]: variant "C" is
  not part of enum U`, matching Go (Go additionally emits E001 for the now-
  undefined payload binding, which this port doesn't implement, so the
  implemented-subset sets agree). Checker-only (fixpoint-safe). Gated by
  new corpus (`union-match-foreign-variant`) and
  `check-position-foreign-variant`.
- **Slice 52 (done): E029 — variant pattern qualified by the wrong enum.**
  A match arm can qualify a variant (`F.A`). Module qualifiers are mangled
  to `__` by flatten before the checker runs, so a pattern that still
  carries a `.` at check time is *enum*-qualified — a new `dot_index`
  helper splits it. In the coverage loop: if the qualifier matches the
  scrutinee union it covers the bare variant (and still E014-checks it);
  if it's a *different* known enum/union it's E029 at the arm; an unknown
  qualifier (Go's module-source mismatch, which this port doesn't model)
  is left alone. This also fixes a latent divergence — `F.A` on an `E`
  scrutinee previously emitted E014, now correctly E029
  (`3:37: error[E029]`, matching Go). Zero E014/E029 false positives
  across all fifteen modules; correctly-qualified `E.A` / `E.B` stays
  clean. Checker-only (fixpoint-safe). Gated by new corpus
  (`match-qualifier-mismatch`, `match-qualifier-correct-ok`) and
  `check-position-qualifier-mismatch`.
- **Slice 53 (done): E016 — union alias collides with a struct.**
  `collect_union_sigs` already detected and silently dropped a `type X =
  …` whose name shadows a declared struct; now `collect_decl_diags` emits
  E016 for it, at the alias's `type` keyword. `TypeAlias` gained `line`/
  `col` (captured in `parse_type_alias`, appended last per the asm
  positional-field rule, propagated through flatten + mono — fixpoint
  stays byte-identical). Only the **struct** collision is E016: a name
  shadowing an *enum* is the Go checker's E006 (enum redeclared), not
  E016, so that branch is deliberately omitted (verified — Go emits E006
  there). `type B = A | C` alongside `struct B` → `4:5: error[E016]:
  union "B" collides with a struct of the same name`, matching Go; a
  distinct alias name stays clean. Zero false positives across the
  modules. Gated by new corpus (`union-struct-name-collision`,
  `union-distinct-name-ok`) and `check-position-union-collision`.
- **Slice 5: pattern matching** (E015/E025/E027/E036), incl. remaining
  match diagnostics.
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
