# Porting the type checker to the self-host (retiring the Go checker)

> **Open follow-ups tracked in GitHub:**
> [#4363](https://github.com/JakeChampion/lang/issues/4363) (small unported
> codes) and [#4346](https://github.com/JakeChampion/lang/issues/4346)
> (silent `all_well_typed` rejections), plus #4344/#4345. The E021/E060/E062
> trait-conformance family is fully ported (#4347, closed; generic-bound
> conformance has its own follow-up issue). The old coarse tracker #2857 is
> closed. This doc is a living progress log — verify the latest slice before
> picking up an item.

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
- **Slice 54 (done): E052 — missing return.** A common, high-value check
  and a pure checker-side control-flow analysis (no AST change). A new
  `block_exits(stmts)` mirrors the Go checker's `funcBodyExits`: only the
  LAST statement matters — `return` exits; an `if` exits iff both arms do
  (a one-armed `if` falls through); a `while (true)` is divergent (breaks
  ignored — a breakable loop needing a value keeps a trailing return the
  surrounding block catches); a `match` exits iff every arm body does.
  `switch` / `if let` are already desugared to if/else by the parser, so
  no special case is needed. A non-void function whose body doesn't
  `block_exits` is E052 at the function declaration. Validated by probe
  first: **zero** false positives across all fifteen modules (every
  bundle function ending in `while (true)`, an exhaustive returning
  `match`, an if/else, or a trailing `return` is recognised as exiting).
  `function f(): i32 { var x = 1; }` → `1:1: error[E052]`, matching Go's
  code, position, and message. Gated by new corpus (`missing-return`,
  `missing-return-one-armed-if`, `return-while-true-ok`,
  `return-if-else-ok`) and `check-position-missing-return`. (Corpus uses
  `boolean`, not the self-host-only `bool` alias, so the Go side doesn't
  add a stray E008.)
- **Slice 55 (done): E021 — method receiver references an unknown type.**
  The first slice of the broad E021 family. The self-host grammar now
  parses `trait` / `impl … for …` / `dyn`, and the impl-conformance slices
  have since landed (#4347: E021 for an impl method with the wrong
  signature, and for an impl that omits a required trait method); the
  remaining pieces are coherence / object-safety. A new `receiver_type_ok`
  validates
  each method's receiver in `collect_decl_diags`: a primitive, a declared
  struct / enum / union alias is fine; a generic / `Map` / array (`X[…]`)
  or `dyn Trait` receiver is skipped conservatively; anything else is
  E021 at the method declaration. `function (r: Nope) m()` →
  `1:1: error[E021]: method receiver references unknown struct "Nope"`,
  matching Go; struct and builtin (`i32`) receivers stay clean. Zero
  false positives across all fifteen modules (every bundle method's
  receiver is a declared struct). Checker-only (fixpoint-safe). Gated by
  new corpus (`method-unknown-receiver`, `method-struct-receiver-ok`,
  `method-builtin-receiver-ok`) and `check-position-bad-receiver`.

  *The impl table had to be mangled before conformance could work across
  modules.* `flatten.rewrite_module_bodies` rewrites every type spelling in
  an imported module — including each method's receiver type (`Num` →
  `num__Num`) — but passed `mod.impls` through verbatim, so an
  `impl Trait for Type` block kept its source spelling. An EMPTY impl is
  resolved by looking for an inherent method on `impl_type` ("empty impls
  adopt the existing method"), and under the stale spelling there was none:
  every required method of every empty impl in an imported module read as
  missing — and since a bundle's diagnostics are compared as a SET, one bad
  decl poisoned the whole program's code set. Real programs hit it once
  `core/cmp` gained `impl Display/Eq/Ord/Hash for bigint.BigInt`, which
  #6314 put in `std/string`'s closure: four spurious E021s on anything
  reaching `core/bigint`, which `import "std/array"` does.
  `flatten.mangle_impls` (#6398) maps `impl_type` through
  `rewrite_type_name` — trait names are global and `method_names` are bare,
  so only the target moves. The checker side does NOT work as an
  alternative: the receiver is `bigint__BigInt` and the target
  `bigint.BigInt`, so stripping the qualifier still leaves them unequal.
  Only the checker reads `impl_type`, so this is checker-only and
  fixpoint-safe. Pinned by
  `TestSelfHostCheckerModloadEmptyImplMangledX86_64`, which covers both
  spellings that were broken — an empty impl on the impl-ing module's own
  struct (bare, mangled by prefix) and one on another module's struct
  (qualified, mangled by the module map) — plus the whole
  stdlib-importing half of the bundle differential corpus.

  *Note on the remaining codes:* this note originally listed E040, E015
  multi-payload patterns, E027 match guards, and E022 let-else as blocked
  on missing language features — all four have since shipped (E040 via the
  `type_param_count` / call-type-arg parser work; E015/E027 with the
  multi-binding + `when`-guard parser support; E022's `let else` as a
  parser desugar surfacing E035 today). E031 (match/if-expression arm-type
  unification) has also since shipped — see Slice 70 below: the value
  match/if desugars to an IIFE, but the checker now recognises that shape
  and unifies the arm types. E045 (map literal key type) shipped too — see
  Slice 71: the literal desugars to a `map_new[_i32]().insert()` chain, and
  the checker recognises that chain to check the first key's type. E025
  (switch-on-float / case-value type) shipped too — see Slice 72: `switch`
  now parses to a real `StmtSwitch` node (desugared to the if/else chain
  only at emit), so the checker sees the shape. The genuinely-remaining
  codes are E044 (typed closure captures — Go only emits it for a captured
  `void`/generic-placeholder value, near-unreachable), E023 (unknown-enum
  scrutinee — already surfaces as E035 here), and E032 (`use` binding
  inference) / E053 (`fip` allocation analysis) — each needing a language
  feature or analysis the self-host doesn't yet model end-to-end.
  (E050/E051 owned-parameter move checking and E049 captured-reference
  reassignment are now done — see below.)
- **Slice 73 (done): E003 false positive on an annotated map var.** A
  `var m: Map[K, V] = Map { … }` (including the empty `Map {}`)
  false-positived E003: the literal desugars to `map_new[_i32](n)…`, whose
  key/value type the self-host leaves `unknown` (it doesn't infer K / V from
  a literal's entries), and `type_assignable` had no map arm — so the
  `unknown`-keyed map wasn't assignable to the annotated `Map[K, V]`. Added
  a `TypeMap` arm that treats an `unknown` key / value side as a wildcard
  (mirroring the empty-array `unknown[]` rule), with concrete sides checked
  for assignability. Pure false-positive removal — `type_assignable` only
  ever returned false for map→map before. Gated by three corpus cases
  (annotated empty / string-keyed / i32-keyed map vars, all clean under Go
  + self-host). Checker-only. (Closes the follow-up noted in Slice 71.)
- **Slice 72 (done): E025 — switch scrutinee / case value types.** Unlike
  the other desugar-blocked codes, a `switch`'s lowered `scrut == value`
  comparisons can't be told apart from a hand-written if-chain, and the
  existing E041 (`==` on mismatched types) actively fires on them — so
  recognising the desugar was impossible. Instead `switch` now parses to a
  real **`StmtSwitch`** node (a transient node like `StmtDefer`): the type
  checker sees it intact, and `desugar_switches_module` (run from
  `module_with_builtins` for the emit paths, and on-the-fly in `eval_stmt`
  for the interpreter) lowers every `StmtSwitch` to the same nested
  if/else-if chain the backends already consume — so no backend lowers one
  at runtime (their arms exist only for exhaustiveness, mirroring
  `StmtDefer`). `switch_diags` emits E025 for a float scrutinee or a case
  value whose type isn't equal to the scrutinee's, and `block_exits` learns
  that a switch exits only with a `default` whose every arm exits (E052
  parity). The other coded body-walkers (E001/E002/E003/E005/…), which
  don't know `StmtSwitch`, run on a **switch-flattened diagnostic form**
  (`switch_diag_block`: scrutinee + case values as expression statements,
  each body as an `if (true) { … }` block — no synthetic `==`, so E041
  never mis-fires) so case-body / scrutinee / case-value diagnostics are
  still caught. The fixpoint bundle uses no `switch`, so the parser change
  is byte-identical (verified). Gated by 8 corpus cases (float → E025,
  case-type → E025, i32/string/multi-value ok, no-default → E052,
  all-return ok, an undefined name in a case body → E001), cross-checked
  against Go, plus the existing switch e2e suites (x86/arm64/wasm/interp)
  and the byte-identical stage-2 fixpoint.
- **Slice 71 (done): E045 — map literal key type.** A map literal
  `Map { k: v, … }` is desugared by the parser to
  `map_new[_i32](n).insert(k0, v0).insert(…)…`; the FIRST key fixes the
  map's key type, which must be **i32 or string** (the only key kinds the
  runtime's FNV-hash / open-addressing compare supports). The check folds
  into the existing scope-threaded `call_diags` pass (no new wiring): when a
  call's callee is `<base>.insert(...)` whose `<base>` is the literal's base
  constructor (`mlit_is_base` — a bare `map_new` / `map_new_i32` call), that
  first `insert`'s key argument is typed and, if it isn't i32/string, E045
  is reported at the key — matching the Go checker, which checks
  `MapLit.Entries[0].Key`. Recognising the desugared chain (rather than a
  preserved literal node) means a hand-written `map_new(n).insert(floatKey,
  …)` would also be flagged, but that's pathological (the runtime can't key
  on a float anyway) and never appears in real code; for actual map
  literals the check is faithful. Map programs need `import "core/map";`
  (Go reports its own E001 "Map operations require import" otherwise — a
  Go-only rule the self-host doesn't model, so such cases stay out of the
  corpus). Reachable Go contract note: the mixed-key / mixed-value E045
  paths the Go source suggests don't actually fire (polymorphic-numeric
  settling), so the implemented + tested surface is the unsupported-first-
  key-type case. Gated by 4 corpus cases (float key → E045; string-key,
  i32-key, used-map → clean), cross-checked against Go. Checker-only;
  checker.fern isn't in the fixpoint bundle. (Found on the way, left for a
  follow-up: `var m: Map[K,V] = Map {}` false-positives E003 because
  `type_assignable` has no Map arm — independent of E045.)
- **Slice 70 (done): E031 — match/if-expression arm-type unification.** A
  `match` / `if` used in VALUE position is desugared by the parser into an
  immediately-invoked closure — `(function(): RT { <match|if with
  returning arms> })()` — so the type system never saw a real
  match/if-expression node and the arm-type consistency check (E031) was
  missing. New passes (`mx_stmts` / `mx_expr`, scope-threaded like
  `ret_diags`) recognise that IIFE shape (a 0-arg call of a 0-param lambda
  whose body is a single `StmtMatch` / `StmtIf`), type each arm's result
  expression — binding match-payload names via `variant_binding_type`, and
  recursing an `else if` chain — and emit E031 when the arms aren't
  mutually compatible. The compatibility predicate (`mx_arm_compatible`)
  mirrors the Go checker's `unifyIfArms`: arms coexist when they're equal
  (`type_eq`), assignable either way (empty-array vs typed array; a struct
  that's a union member arm), both numeric (i32/f64 — the self-host
  analogue of Go's polymorphic-numeric widening), element-wise-compatible
  **tuples / arrays / maps** (recursive), or two struct values of the SAME
  **enum** family. The enum-family rule is the subtle one and the reason it
  isn't a trivial port: the Go checker types an enum variant constructor as
  its ENUM type, so `A(1)` / `B("y")` of one `enum E` unify, whereas the
  members of a struct-union (`type U = A | B`) keep their distinct struct
  types and do NOT unify (Go E031). The self-host types BOTH as
  `t_struct(name)`, so names are mapped back to their family via the user
  `enum` decls (`mx_enum_of`) and the built-in enums (`mx_builtin_enum_of`
  — Option / Result / JsonValue / IoError) but NOT via union aliases, so
  union members correctly stay incompatible. Bare no-payload variants
  (`None`, `Empty`) resolve to `unknown` here and are handled by the
  unknown-skip (matching Go, which sees the compatible enum type); if any
  arm types to `unknown` the whole check is skipped (a bad arm is its own
  error — Go reports E001 there, not E031). This matches the native
  checker on the discriminating cases — two unrelated structs, struct-union
  members, and element-wise tuple/array mismatches all now fire E031, while
  same-enum payload variants and Option/Result arms stay clean. Gated by 20
  corpus cases (if/match scalar mismatches, else-if chains; numeric + bool
  + same-struct + Option + enum-variant + nested-if + tuple compatible arms;
  unrelated-struct / union-member / tuple-elem / array-elem mismatches;
  same-enum-different-payload OK; an undefined arm → E001), all
  cross-checked against the Go checker, plus an 18-shape false-positive
  audit. Checker-only; checker.fern isn't in the fixpoint bundle.
- **Slice 69 (done): enum variant payload bindings get their payload type
  (fixes an E038/E003 false positive).** A payload-bearing enum variant
  `V(T)` is lowered by the parser to a struct `V` carrying a single marker
  field `__ev: T`, so the pattern `V(n)` binds the *payload value* of type
  `T`. The checker, however, bound the payload name to the *wrapper struct*
  (`t_struct("V")`) at every match-arm site — so passing the payload to a
  typed function (`f(n)` with `f(n: i32)`) false-positived **E038**
  ("argument type"), and an assignment to it would have mis-fired **E003**
  (the `stmts_assign_diags` site already worked around this by binding the
  payload to `unknown`, suppressing the check entirely). New helper
  `variant_binding_type(s, name)` reads the real payload type off the
  `__ev` marker field when present, falling back to `t_struct(name)` for a
  struct-union member (`type U = A | B`, which is a genuine struct with no
  marker — its binding stays the whole struct, matching Go). Applied at all
  three payload-binding sites (`check_stmt` type inference,
  `stmts_call_diags`, `stmts_assign_diags`), replacing the imprecise
  `unknown` workaround with the precise type. Gated by four new corpus
  cases (i32 + string payload passed to a matching function → clean; a real
  payload-type mismatch still fires E038; a struct-union member's field
  access stays clean), all cross-checked against Go. Checker-only;
  checker.fern isn't in the fixpoint bundle.
- **Slice 68 (done): E002 — return-type checking inside lambda bodies.**
  The top-level `ret_diags` pass recurses through if / while / for / match /
  defer sub-bodies but deliberately stops at a lambda boundary — a nested
  function has its own return contract — so a `var f = function(): i32 {
  return "x"; }` went unflagged even though the Go checker reports E002.
  The new `lret_stmts` / `lret_expr` pair walks every function (and the
  top-level statements), scope-threaded, finds each lambda, and runs the
  same `ret_diags` against the lambda's body using the lambda's OWN declared
  return type. A recursive local (`var f = function…`) pre-binds its own
  name to its function type so a self-call inside the body resolves rather
  than inferring `unknown` (which would silently skip the check). Because it
  reuses `ret_diags`, bare `return;` inside a non-void lambda is reported as
  E012 for free, and nested lambdas are covered by the body recursion. Runs
  for void enclosing functions too (a lambda can be declared anywhere).
  Mutual recursion with the top-level `ret_diags` is safe — `ret_diags`
  never descends into lambdas, so no diagnostic is emitted twice. Gated by
  nine new differential-corpus cases (mismatch, ok, void-enclosing, nested
  if, bare return → E012, closure-argument mismatch, nested lambda,
  no-return-type, recursive-local capture mismatch), all cross-checked
  against Go. Checker-only; checker.fern isn't in the fixpoint bundle.
- **Slice 68a (done): lambda-E002 must not check IIFE-desugared match/if
  expressions.** Follow-up to Slice 68. A `match` / `if` used in *value*
  position is desugared by the parser into an immediately-invoked lambda —
  `(function(): RT { … })()` — whose `RT` is a **coarse heuristic tag**
  (`if_expr_rt` picks it from the first arm's literal shape, defaulting to
  `"i32"`), not a user-declared return type. The Slice-68 pass therefore
  false-positived E002 on a valid string-valued match/if-expression whose
  first arm wasn't a string literal (e.g. an arm that's an identifier or a
  call, so `RT` mis-tagged as `"i32"` while the arms are strings). The fix:
  `lret_expr`'s `ExprCall` arm now special-cases a directly-invoked lambda
  (the only way an IIFE arises here) — it skips the IIFE's own return check
  but still recurses into the body via `lret_stmts` so genuinely-nested
  lambdas are still covered. (A rare hand-written IIFE with a real mismatch
  is no longer flagged — an acceptable, conservative trade matching the
  gate's zero-false-positive stance.) Gated by two new corpus cases
  (`match-expr-string-arms-ok`, `if-expr-string-arms-ok`), both clean under
  Go + self-host.
- **Slice 67 (done): recursive local functions no longer false-flag E001.**
  A local function `function f(...) { ... }` desugars to `var f =
  function(...) { ... }`, and the codegen hoist lifts a recursive one to
  top level — but the checker walked the lambda body without binding `f`,
  so a recursive self-call `f(...)` was reported as an undefined function
  (E001), falsely rejecting a valid recursive local (the Go checker accepts
  it via its IsLocal handling). Both the type-inference pass (`check_stmt`)
  and the call-resolution pass (`stmts_call_diags`) now pre-bind a
  function-valued local to its function type before checking the init —
  letrec scoping — so the self-call resolves and the body is checked
  properly. (A plain `var f = closure` self-reference is accepted too,
  matching the self-host's codegen which hoists both; the Go checker is
  stricter there, a documented minor leniency.) Gated by two new
  differential-corpus cases: a simple recursive local and a capturing one,
  both clean under Go + self-host. Checker-only; checker.fern isn't in the
  fixpoint bundle.
- **Slice 66 (done): E049 — captured-reference reassignment.** Assigning
  to a reference-typed (pointer) variable that a closure captures from an
  enclosing scope is read-only (rebinding it inside the closure can't take
  effect outside and could close a reference cycle) → E049. `e049_diags`
  finds lambdas (in var inits / returns / expr-and-assign values, through
  control blocks) and flags a `StmtAssign` whose target is a reference-
  typed enclosing var not shadowed by a lambda param/local. Reference vs
  scalar is classified by the declared type-name (scalars i32/i64/bool/
  f64/f32 are exempt; an unannotated capture is conservatively skipped).
  Checker-only; the self-host's own sources contain no captured-reference
  reassignments (they compile clean under the Go checker, which already
  enforces E049), so the fixpoint is untouched (x86-64 + arm64). Seven
  differential-corpus cases (string/array/struct/ref-param captures fire;
  scalar capture, read-only use, and a lambda-local assign stay clean),
  cross-checked against the Go checker.
- **Slice 65 (done): E050/E051 — owned-parameter move checking.** The
  parser gained an `own` modifier on `ParamDecl` (`function f(own xs: …)`);
  `own_diags` runs an affine flow analysis over each function body. Using an
  owned param after it is consumed (returned / passed as a call argument /
  matched / bound) is **E050** (use-after-move), with borrow-aware
  classification (a projection `x.f`, index `x[i]`, method receiver, or
  call callee borrows rather than consumes), flow-sensitive branch joins
  (a diverging arm's consumes don't reach the join), and a loop check (a
  consume of a still-live param inside a loop body). Passing a non-owned
  value to an `own` parameter is **E051** (the call-site guard;
  `ow_is_owned_expr` accepts fresh constructions / another owned param).
  Match-/if-EXPRESSION IIFEs are looked through so a match-expression
  scrutinee consume is attributed to the enclosing scope. Checker-only and
  gated on the presence of any `own` parameter, so ordinary programs and
  the self-host's own fixpoint are untouched (verified x86-64 + arm64).
  Captured params are emitted by codegen as borrows (no transfer lowering
  yet); this establishes the affine invariant. Ten new differential-corpus
  cases cross-checked against the Go checker.
- **Slice 56 (done): E024 — tuple destructure of a non-tuple / wrong
  arity.** The parser already lowers `var (a, b) = E` to a `StmtVar` whose
  `name` is `"a,b"`, so this is checker-only after all (not a missing
  feature). In the scope-aware var walk, a comma-named `StmtVar` whose
  init types to a non-tuple is E024 (`tuple destructure needs a tuple
  expression, got <type>`); a tuple whose element count ≠ the name count
  is the other E024 (`tuple has N elements, but M names given`). An
  unresolved (`unknown`) init is skipped. `var (a, b) = n` for `n: i32` →
  `1:35: error[E024]`, matching Go's code, position, and message; a real
  `var (a, b) = (1, 2)` stays clean. Zero false positives across all
  fifteen modules (the bundle only mentions destructuring in comments,
  never destructures a non-tuple). Checker-only (fixpoint-safe). Gated by
  new corpus (`tuple-destructure-non-tuple`, `tuple-destructure-ok`) and
  `check-position-tuple-destructure`.
- **Slice 57 (done): E033 — invalid `as` cast (bool ↔ non-bool scalar).**
  `x as T` desugars (parser) to the unary op `as_<T>` (operand = `x`).
  The Go checker's E033 allows numeric↔numeric casts plus the
  string↔i32 / struct/array/string→i32 data-pointer hops, and rejects
  everything else (verified empirically: `bool as i32`, `i32 as bool`,
  `bool as string`, `string as bool`, … → E033; `i32 as f64`,
  `string as i32`, `i32 as string`, `bool as bool` → OK). The self-host
  models a **sound, zero-false-positive subset**: a cast between a `bool`
  and any other concrete scalar (either direction) — the unambiguous
  bug class. Numeric/string conversions (which the self-host's own
  source uses, e.g. `as i32` / `as u32` / `as u8`) stay accepted, and
  the bundle never casts a `bool`, so there are no false positives
  (verified: the codes differential + the bundle compile stay green).
  The cast unary node now carries the **`as` keyword's** position (the
  parser switched `e_unary` → `e_unary_at`, capturing `peek_line`/`col`
  before advancing), matching the Go checker's `CastExpr.P`, so E033
  prints `1:56: error[E033]` for `b as i32`. parser.fern is in the
  fixpoint bundle, but the new position is codegen-ignored, so the
  byte-identical fixpoint holds (x86 verified). Gated by new corpus
  (`cast-bool-to-i32`, `cast-i32-to-bool`, `cast-bool-to-string`,
  `cast-string-to-bool`, plus four `cast-*-ok` cases) and a new
  `check-position-cast` CLI case.
- **Slice 58 (done): E033 — full scalar matrix + cast result typing.**
  Two follow-ups to slice 57. (1) `check_expr` now types an `as_<T>`
  cast as its **target type** (`type_from_name(T)`) instead of leaking
  `unknown`, matching the Go checker (which returns `n.Target` even on an
  invalid cast); unrecognised targets (wider ints `u32` / `i64` / …) stay
  `unknown` and are conservatively accepted. (2) A new `as_cast_allowed`
  predicate generalises the slice-57 bool-only check to the **complete
  sound matrix** over the four scalar types the self-host models (i32 /
  f64 / bool / string): identity, numeric↔numeric, and the string↔i32
  data-pointer hop are accepted; any bool↔non-bool **or `f64↔string`** is
  E033. Both sides are guarded by `is_primitive_type`, so struct / array
  pointer hops and wider-int targets are still left to the Go checker —
  verified against the full 16-pair scalar matrix (`f64 as string` /
  `string as f64` → E033; `f64 as i32` / `i32 as f64` → OK), zero false
  positives. Checker-only (checker.fern isn't in the fixpoint bundle).
  Gated by new corpus (`cast-f64-to-string`, `cast-string-to-f64`,
  `cast-f64-to-i32-ok`, `cast-i32-to-f64-ok`). *Limitation recorded:* the
  non-scalar E033 classes Go also catches (struct/array → bool, etc.)
  need the self-host to model the full pointer-hop matrix; the scalar
  matrix is the provably-sound subset.
- **Slice 59 (done): E048 — assignment to an immutable struct field.**
  Fern fields are immutable after construction (the immutable-data rule
  that keeps RC cycle-free — `docs/IMMUTABILITY-MIGRATION-PLAN.md`), so
  any `obj.field = v` is E048; rebuild with a struct-update literal
  (`T { ...old, field: v }`). The self-host parser **desugars** a field
  assignment to `__set_field(obj, "field", v)` (an expression statement),
  so this is detected in `call_diags` as an `ExprCall` whose callee is the
  parser-internal builtin `__set_field` — which no hand-written source
  ever names, and the self-host + stdlib were migrated off field mutation
  to functional updates, so there are **zero false positives** (verified:
  no field-assignment statement exists in any self-host or stdlib module).
  Reported at the field-access **dot**, matching the Go checker's
  `FieldAccess` position: the parser now carries the dot position on the
  field-name string arg (`e_string` → `e_string_at`), which the checker
  reads — `p.x = 5` → `2:48: error[E048]`. parser.fern is in the fixpoint
  bundle but only the (unused) field-assign desugar branch changed, so the
  byte-identical fixpoint holds (x86 verified). Compound field assignment
  (`p.x += 5`) desugars through the same path and is also caught. Gated by
  new corpus (`field-assign`, `field-compound-assign`, `nested-field-
  assign`, plus `index-assign-ok` / `local-reassign-ok` / `struct-update-
  ok` negatives) and a new `check-position-field-assign` CLI case.
- **Slice 60 (done): E001 — undefined name (value position).** The
  flagship checker code and the key blocker to retiring the Go checker as
  the strict gate. `call_diags` gained an `ExprIdent` arm: a bare
  identifier in **value** position that resolves to nothing —
  `is_resolvable_value` checks a local / param / loop / match binding, a
  free function or 0-arg const, an enum/union variant (declared or the
  builtin Option / Result / JsonValue / IoError variants), or a struct
  name — is E001, at the identifier. **Call callees are skipped** (a
  bare-ident callee no longer recurses into the value check), so this is
  the value-read subset; undefined-callee E001 (which needs a builtin-
  *function* allowlist) is a follow-up. Matching the Go checker also
  required not binding a **foreign** variant's match payload (so its read
  is genuinely undefined — Go emits both E014 and E001), and binding the
  constructs the assign walk previously left unbound: **loop variables**
  (incl. `for (k, v)` via `bind_names`), **tuple-destructure** names
  (`var (a, b)` is the single name "a,b" — split and bound for subsequent
  reads), and **lambda params** (the body sees them). A sharp bug found en
  route: rebinding a normal `var` after `check_stmt` shadowed its precise
  type (lookup is newest-first) and silently broke every type-dependent
  diagnostic (E033/E043/E046/E035/E004-method) — fixed by only splitting
  comma names. **Zero false positives**, verified two ways: the
  differential corpus (positive `value-undefined`; negatives for every
  binding vector — param, loop var, match payload, function-as-value,
  enum + builtin variant) AND a new bundle-wide guard
  (`check-selfhost-no-e001`) that runs the self-host `-check` over the real
  lexer / parser / checker / flatten / interp / printer / ssa modules and
  asserts no spurious E001. Checker-only (checker.fern isn't in the
  fixpoint bundle). *Follow-ups:* undefined-callee E001 (builtin-function
  allowlist), undefined-assignment-target E001, and target-position parity.
- **Slice 61 (done): E001 — undefined assignment target.** The
  allowlist-free companion to slice 60. In `stmts_assign_diags`, an
  assignment `x = v` whose target isn't in scope is E001 (an assignment
  LHS can only be a local / param / loop / match binding — field & index
  assignments desugar to `__set_field` / `__set_index` calls — so there's
  no function / variant / builtin to confuse it with, hence no allowlist).
  The same binding fixes the read walk needed were mirrored here: loop
  variables, match payloads, and tuple-destructure names are bound before
  walking the relevant body. Match payloads are bound **membership-only**
  (`t_unknown`), not as the variant struct — a scalar-payload variant
  (`Has(i32)`) binds the inner value, and typing it as the struct would
  mis-fire E003 on an assignment to it. Reported at the `=` token (the
  position the parser already captures); target-position parity is the same
  follow-up noted for the other E003-family codes. Zero false positives,
  verified by the differential corpus (positive `assign-undefined-target`;
  negatives for assign-to param / loop var / match payload / destructure)
  and the bundle-wide `check-selfhost-no-e001` guard (now exercising the
  assign walk too). Checker-only (fixpoint-safe). *Remaining E001
  follow-up:* undefined-callee E001 (needs a builtin-function allowlist).
- **Slice 62 (done): E001 — undefined call callee (completes E001).** The
  last E001 form: a bare-identifier call `foo(...)` whose callee resolves
  to nothing. `is_resolvable_callee` accepts a local (closure value), a
  free function, a variant constructor (declared or builtin), a runtime
  intrinsic (any `__`-prefixed name — these are emitter-internal and
  numerous, so a prefix test avoids enumerating them), or a builtin
  function (`is_builtin_function`: the user-facing I/O / fs / clock /
  random / map / strbuf / net / bit-reinterpret set — a **superset** of
  the Go checker's `FuncSigs` builtins and the emitter's dispatched names;
  over-inclusion only suppresses E001, so it's safe). `is_resolvable_value`
  gained the same builtin / `__` checks, since a builtin may also be
  referenced as a bare value (`var w = write;`). Completeness is
  CI-enforced: the bundle-wide `check-selfhost-no-e001` guard now runs the
  self-host `-check` over **all 13 major modules** (incl. asm / asm_arm64 /
  wasm / fern, which call the full breadth of builtins) and asserts no
  spurious E001 — any missing builtin fails the build. Zero false
  positives. Corpus: `callee-undefined` (positive) + `callee-user-fn` /
  `callee-builtin` / `callee-variant-ctor` / `callee-closure` /
  `value-builtin-as-value` negatives. Checker-only (fixpoint-safe). **With
  reads, assignment targets, and callees all covered, E001 is complete bar
  source-position parity** (it reports at the identifier; the `=`-token vs
  target-position nuance for the assignment form is the only remaining
  follow-up).
- **Slice 63 (done): E001 assignment-target position parity.** Closes the
  one source-position gap E001 introduced: the value / callee forms already
  reported at the identifier (matching Go), but the assignment-target form
  reported at the `=` token. Go's `errIdent` reports at the **target
  identifier** (`y = 5` → `1:24`, the `y`, not the `=` at `1:26`). Since the
  `=` position is still needed for E003-assign, `StmtAssign` gained
  `target_line` / `target_col` (appended last per the asm positional-field
  rule), captured from the target `ExprIdent` at the parse site and
  propagated through the constfold / flatten / ssa-rename rebuilds; synthetic
  assigns (the ssa for-increment, defer / dfa desugars) carry 0. The E001
  emission now uses the target position. parser.fern + flatten.fern are in
  the fixpoint bundle, but the fields are codegen-ignored, so the
  byte-identical fixpoint holds (x86 verified). Gated by a new
  `check-position-undefined-assign` CLI case. **With this, every E001 form —
  read, callee, assignment target — reports at the exact `line:col` the Go
  checker does**, so E001 is fully complete (codes + positions).
- **Slice 64 (done): E054 — `@export` world-export constraints.** An
  `@export(...)` function bound to a WIT world export cannot be generic (a
  world export is lifted with a single concrete canonical ABI) and cannot
  be a method (the export surface is top-level functions). The self-host
  already records the binding on `Module.exports` (P6) and each `FuncDecl`
  carries `type_param_count` (added for E040) + `receiver_name`, so this is
  a checker-only rule: `collect_decl_diags` looks each export's function up
  by name and flags `type_param_count > 0` / a non-empty receiver. Both Go
  sub-messages map to E054 (the differential gate compares codes).
  Checker-only, fixpoint-safe (x86 + arm64 verified). Gated by new corpus
  cases `export-generic`, `export-method`, `export-plain-ok`.
- **Slice 5: pattern matching** (E015/E025/E027/E036), incl. remaining
  match diagnostics.
- **Slice (done): `if let` / `let … else` diagnostics** (E022). The
  self-host parser already desugars both forms to a `StmtMatch` (so every
  backend compiles them with no new node); this slice tags that match with
  a new `StmtMatch.origin` field (`"if_let"` / `"let_else"`, `""` for a
  hand-written match) so the checker can tell a desugared binding-match
  apart from a real one. The checker then emits E022 — *source must be an
  enum value* (a primitive / struct source, where a hand-written match
  would draw E035, now suppressed for `origin != ""`) and, for `let … else`,
  *else branch must diverge* — matching the Go checker's dedicated
  `LetElse` / `IfLet` handling instead of the generic match-arm codes.
  `block_exits` (E052) special-cases a `let_else` match: its only
  fall-through path is the success arm (the else must diverge), so the
  desugar that folds the rest of the block into that arm doesn't draw a
  spurious missing-return. Bad-variant (E014) and payload-arity (E015)
  already aligned through the desugared match, so they need no change.
  Codegen ignores `origin`, so the fixpoint stays byte-identical (x86
  verified). Gated by corpus cases `iflet-source-nonenum`,
  `letelse-source-nonenum`, `letelse-source-struct`,
  `letelse-else-nondiverge`, `iflet-enum-ok`, `letelse-enum-ok`,
  `iflet-bad-variant`, `iflet-bad-arity`. **E023** (*unknown enum*) is the
  sibling rule for an enum-typed source whose decl is missing; it's
  effectively unreachable in the self-host (an enum-typed scrutinee always
  resolves to a registered union sig), so it carries no corpus case yet.
- **Slice 6: traits** (E021 conformance/coherence/object-safety/derive).
- **Slice 7: `?` / slices / tuples / maps / literal-fits** (E042, E037,
  E024/E046, E045, E047).
- **Slice 8: owned-parameter move checking** (E050/E051).
- **Slice 9: source positions** — thread token line/col into the AST and
  upgrade every `Diag` to a span, reaching `line:col` parity.
- **Slice 10: wire as the gate** — `fern.fern` prints
  `line:col: error[E0XX]: msg` from the self-host checker; the Go checker
  leaves the differential gate.

## Native-only codes blocked on feature ports

Five Go-checker codes are **not** standalone checker rules — each is tied
to a language feature the minimal self-host frontend doesn't model yet.
Because the differential driver runs lexer → parser → checker (no
codegen), a code is reachable only if the self-host **parser** produces
the construct and the **type system** can represent it. These need the
underlying feature ported first; they are tracked here so a future slice
picks them up with the right prerequisite, not as a lone checker tweak:

- **E023** (*unknown enum*) — the sibling of E022, implemented
  defensively in `stmts_call_diags` but effectively **unreachable**: an
  enum-typed scrutinee always resolves to a registered union sig, so the
  "enum decl missing" branch can't fire. No corpus case possible.
- **E032** (`use` binding-type inference failure) — the self-host parser
  has no `use` support; porting it means replicating the whole desugar
  (rest-of-block → a synthesised callback closure appended as the source
  call's last arg). E032 is also never isolable — in every real failure
  case it co-fires with E004 (the desugar makes a malformed-arity call),
  E001 (unknown callee) or E038 (callback arg type) — so a differential
  case can't pin it alone. (Probing this surfaced + fixed a Go-checker
  nil-pointer panic formatting the un-inferred callback type for E038 —
  see `ast.FuncType.String` and `TestUseWithoutAnnotationDoesNotPanicFormatting`.)
- **E044** (closure captures a void / generic-param-typed var) — fires
  only on an unrepresentable capture type. The self-host `Ty` system has
  no generic-parameter type (generics are out of scope); a captured
  generic reads as `unknown`, indistinguishable from an unresolved type.
  Needs generics modelling.
- **E057** (`Cell[T]` element must be scalar/string) — **done for both
  forms** (#4363 item 2). *Value form:* `cell_new(v)` is a recognised
  self-host builtin; the checker types it as a nominal `Cell` handle and
  flags E057 in `call_diags` when the argument's type isn't a scalar /
  string (`is_primitive_type` is the exact analog of the Go
  `isCellElemType`, modulo the generic-`ParamType` case the self-host
  doesn't model). The diagnostic points at the argument, matching the Go
  checker's `n.Args[0].Pos()`. *Annotation form* (`Cell[T]` in a param /
  return / body-`var` / field position): the Go blocker is fixed — native
  `resolveType` now threads the annotation's use-site position through and
  anchors E057 there instead of the synthesised `Cell` decl at 0:0 (which
  `diag.Format` rendered with no `error[E0XX]` prefix, hiding the code
  from the differential). The self-host port (`e057_annot_diags` +
  `e057_var_diags` in `checker.fern`) decomposes each annotation spelling
  via `parser.parse_type_ref` and flags any `Cell[X]` whose element isn't
  i32/i64/u32/u64/f64/boolean/string — recursing into generic-argument and
  tuple-element positions like native `resolveType`, so
  `Option[Cell[P]]` / `(i32, Cell[P])` / `Cell[P][]` all fire. Scoped like
  E064 to non-generic, non-method functions and non-generic structs, so a
  generic's `Cell[T]` (natively an admitted `ParamType` element) is never
  flagged and the bundle fixpoint stays clean. (Found while porting: the
  E064 generic-arg widening false-positived on `str` inside a generic
  argument — `Option[str]` — because a bare `str` is parse-erased to
  `string` but a nested one keeps its spelling; `str` is now allowlisted
  in `e064_unknown_bare`.) Gated by corpus cases `cellnew-*` (value form),
  `cell-annot-*` (annotation form incl. nested/negative controls), and
  `generic-arg-str-ok`.
- **E064** (a type annotation names no declared type) — `check_module`
  resolves each annotation against the same struct + union/enum name sets
  the rest of the checker uses (`type_from_name_with_names_and_unions`)
  and flags a result that `is_unknown`. Conservatively scoped via
  `e064_unknown_bare` to a **bare nominal identifier** (`[A-Za-z0-9_]`, so
  array / generic-instantiation / function (`fn`) / tuple / `dyn` / dotted
  cross-module spellings are skipped) in a **non-generic, non-method**
  function (`type_params` *and* `type_param_count` zero — the latter
  catches unbounded generics, whose params aren't recorded) or a
  **non-generic, non-enum-variant** struct — contexts with no type
  parameter in scope to be mistaken for an undefined type. This keeps the
  bundle fixpoint-clean while matching Go's E064 on the ported shape; the
  array-element / generic-argument / body-`var` positions Go also covers
  are a later widening. Gated by corpus cases `unknown-param-type`,
  `unknown-field-type`.
- **E067** (`@must_consume` value may leave scope unconsumed) — **done.**
  The self-host parser now stamps the `@must_consume` attribute onto the
  following struct/enum decl (`StructDecl.must_consume` /
  `EnumDecl.must_consume`, propagated through the flatten / monomorphise
  rewrites) instead of dropping it, and `checker.fern` ports the native
  walk (internal/checker/mustconsume.go) function-for-function as the
  `mc_*` family: `must_consume_diags` (per-function entry; non-`own`
  params + body walk), `mc_walk_body` (finds marked bindings at every
  block depth, scope-threaded so unannotated inits resolve via
  `check_expr`), `mc_seq` (all-paths at-least-once; loop bodies opaque;
  return/break/continue end paths), `mc_check_binding` (leak report at
  the binding + straight-line overwrite scan), and `mc_expr_consumes`
  (call-arg transfer, marked-store consume, laundering into
  array/tuple/unmarked struct/enum at the store site, lambda capture).
  Two self-host-specific mappings: an enum-variant constructor call
  (`Reply(t)`) plays the native `EnumLit` store rule (the callee ident
  resolves through `mx_enum_of` / `mx_builtin_enum_of`), and an
  immediately-invoked lambda — the parser's IIFE desugar of a
  value-position `match`/`if` — is treated as an inline block (`mc_seq`
  over its body), not a closure capture, so `var r = match (p) { … }`
  consumes `p` like the native `MatchExpr` tag rule (with the residual
  strictness that the IIFE body must consume on ALL paths, where the
  native expression walk accepts a consuming use on any). Gated only
  when the module declares a marked type (`mc_any_marked`), keeping the
  walk free for the self-host's own sources. Covered by the `mc-*`
  corpus cases (plain/one-arm/early-return/enum/loop/param leaks,
  laundering into array/struct/tuple, closure capture, overwrite, and
  the clean transfer / match / match-expr / marked-envelope / own-param
  shapes).
- **E053** (`fip` function may not allocate) — needs Perceus in the
  self-host before allocation sites can be attributed to a `fip` function.

## Differential testing

`internal/e2eselfhost/self_host_checker_codes_test.go` compiles
`checker_codes_run.fern` with the Go-built bundle compiler, runs it over
a corpus, and asserts the printed code set equals what Go's
`checker.Check` (formatted through `diag.Format`) reports for the same
source — **unfiltered**. The historical `selfHostImplementedCodes`
intersection is deleted (2026-07-12): the port now covers every code the
Go checker emits, so the three differentials compare raw sets and new
rules only need corpus cases.

## 2026-07-12 — the last five codes land; the filter is deleted (freeze precondition 3)

The "unreachable / needs-generics / needs-Perceus" assessments above were
too pessimistic; each of the remaining codes had a portable conservative
slice:

- **E023** — reachable after all: an *unknown-BASE generic* annotation
  (`s: Statuus[i32]`) keeps `ast.EnumType` through the native
  resolveType, so a match / if-let / let-else on it draws E023. The
  self-host mirror: `type_from_ref_su` resolves that shape to a name-only
  union (declared struct/union/`Map`/`Cell` bases keep their old
  resolution), and the match walk reports E023 when `lookup_union` misses
  a non-reserved name. E064 additionally checks generic BASES (native
  `knownTypeName` parity), with declared traits/resources allowlisted.
- **E044** — the two native shapes (captured `void` call result, captured
  erased generic param) are detectable syntactically: `e044_diags` tracks
  void-call-initialised vars plus bare-unknown-annotated params/vars in
  generic functions, and flags a lambda mentioning one (modulo shadowing
  lambda params).
- **E053** — `fip` is now parsed (`FuncDecl.fip`, same
  directly-before-`function` contextual rule as native) and
  `e053_diags` ports checkFipFunctions: array/tuple/struct literals,
  payload variant construction, string concat (the f-string `+`-desugar
  included), non-`fip` calls, non-whitelisted methods (`len` and
  own-rooted `.with` allowed), indirect calls, and non-`own` index/field
  writes.
- **E065** — the parse-time `str`→`string` erasure now records the raw
  spelling on `FuncDecl.ret_str` / `StmtVar.is_str`, so `e065_diags` can
  mirror the native chase: local owned `string` = backing storage, local
  `str` binding = view of its init, params/literals safe.
- **E032** — the `use` desugar marks its synthesised callback
  (`ExprLambda.use_infer`) when the binding is un-annotated;
  `e032_stmts` ports inferUseParam's non-identifier-source /
  no-signature / last-param-not-a-function arms. The
  callback-takes-no-arguments arity arm is NOT decidable (a
  function-type param coarses to the arity-less `"fn"` tag) and the
  generic-unification arm needs unification the self-host doesn't model —
  both stay native-only and out of the corpus.

Corpus cases: `e023-*`, `e044-*`, `e053-*`, `e065-*`, `e032-*` in
TestSelfHostCheckerCodesX86_64. E039 turned out to be a dead catalogue
entry (the Go checker never emits it — no `errfCode` site), so it needed
no port for unfiltered parity. Its explanation file has since been
deleted: it documented a bare `len(x)` builtin that no longer exists
(`len` is a method), so `fern explain E039` described a construct the
language does not have.

## 2026-08-17 — a bare `const` reference gets a type

A top-level `const` reaches the parser as a zero-parameter `FuncDecl` carrying
`is_const`, so `collect_func_sigs` files it in the SIG table while an
identifier is resolved against the VALUE scope. The two never met: every
program that READ a const was rejected by `-check` as un-inferable under #4346,
including `function main(): i32 { return N; }` over `const N: i32 = 41;`.

`FuncSig` now carries `is_const`, and `check_expr`'s ident case falls back to
the sig's return type for one. The flag is what makes it safe — a bare
reference to a plain zero-argument FUNCTION is a function value, not its return
type, so resolving on arity alone would mistype it.

Nothing here was a lowering problem: the rejected programs compiled and ran
correctly, which is why the fixpoint never saw it — the fixpoint compiles the
compiler, it does not `-check` it. The gate is
`TestSelfHostConstRefCheckX86_64`, an accept/reject differential against
native rather than a message comparison, so a case native rejects for its own
reasons cannot pass by being rejected here for the wrong one.

Two shapes in the same neighbourhood remain un-inferable and are NOT part of
this: a method call on a `string` local (`s.len()`) and a function-typed local
(`var f: (i32) => i32 = twice;`). Both fail with no const involved.

## The one front-end code: P002

An out-of-range float literal is a FRONT-END rejection on both engines
(native: strconv's `ErrRange` in `parsePrimary` → P002), and the self-host
parser has no error list to report it through. So `parse_primary` plants an
`ExprUnknown` carrying `util.f64_range_kind(text)` at the point the token would
have become a value; `mx_expr` turns that marker into a positioned P002, and
`asmcore.parse_unknown_error` names the same code on the compile path instead of
its generic P001. Every consumer already rejected a parser-side unknown, so the
single check in the parser is what makes the interp, the checker and the
backends agree.

The gate is `TestSelfHostFrontEndNumericLiteralCodes`, which drives
checker_codes_run under the native interpreter and compares against the Go
FRONT end. The E-code differential cannot cover this class: its oracle is the Go
CHECKER, which never runs for a program the Go parser rejects, so a front-end
divergence reads there as agreement. That is how #6842 stayed hidden while
`1e309` compiled to +Inf.

## 2026-08-17 — the build gate becomes an exclusion list (#6961)

Until this change the self-host `-target` path enforced six codes: the five
immutability cycle rules plus P002. Every other coded rule was reachable only
through `-check`, so `bin/fern-selfhost -check` reported `E003` on a source that
`bin/fern-selfhost -target` then compiled to a working binary — and a checker
rule ported for parity did not gate a build the day it landed.

`is_build_gate_code` (checker.fern) is now the inverse: a well-formed `E###` /
`P###` code rejects the build unless it is listed in
`is_partial_checker_gap_code`. The pseudo-code `"type"` — `ill_typed_hint`'s
#4346 marker for an expression this checker cannot represent — is not a
well-formed code and so never gates, which is what keeps the partial port from
refusing valid programs it merely cannot model.

### The exclusion list, and how it was measured

A code is excluded iff the self-host checker emits it for a program the NATIVE
compiler accepts. Measured over every such program in four corpora:

| corpus | programs native accepts |
|---|---|
| `conformance/cases` | 415 of 481 |
| `examples/` + `internal/stdlib` | ~1,400 |
| the 512 fernsmith differential seeds (`GenMain`, seeds 0–511) | 512 of 512 |
| `examples/self_host/*.fern` (the compiler's own sources) | all |

Twenty codes false-positive and are excluded:

```
E001 E004 E009 E013 E018 E019 E021 E024 E031 E034
E036 E038 E040 E041 E042 E043 E044 E051 E052 E064
```

Three of them are why the sweep has to span all four corpora rather than the
conformance corpus alone: **E034** and **E019** are clean across conformance and
first appear in `examples/`, and **E042** first appears in the differential
seeds. **E064** ("unknown type") fires on the compiler's own sources, so gating
it would break the fixpoint — it is the entry that makes the self-host corpus
non-optional. **E044** ("captured variable has unsupported type") fires on all
512 seeds, since any lambda capturing a value whose type the partial checker
cannot resolve draws it.

Each entry is a bug, not a permanent carve-out. E036 is the `derive_default`
misreading recorded in `self_host_shadowed_builtin_variant_ir_test.go`; E043 was
mostly #7011 (`type_eq` ignoring integer width), which the section below closed —
eight of its nine false-positive files went with it, and the one that remains is
a generic-parameter collision. Deleting a line from
`is_partial_checker_gap_code` is how one of these gets finished — re-run the
sweep above before doing it.

### Reproducing the sweep

For each program the native compiler accepts, compare what the self-host
checker says:

```
./bin/fern -check "$f" >/dev/null 2>&1 || continue     # native must accept
./bin/fern-selfhost -check "$f" internal/stdlib 2>&1 | grep -oE 'error\[[EP][0-9]+\]'
```

Any code that appears is a false positive and belongs in the exclusion list.
The end-to-end direction — that a gating code actually stops `-target` — is
`TestSelfHostBuildGate{,MatchesCheck}X86_64` in `internal/e2eselfhost`.

## 2026-08-18 — integer width enters the type (#7011)

`type_eq`'s integer arm compared only `is_char`, so every width and signedness
was interchangeable: `var x: i32 = c;` with `c: i64` type-checked, and no
i64/u32/u64 differential row could be written for anything else because each one
failed on this instead of on what it meant to test.

Native's rule, measured rather than assumed:

| shape | native |
|---|---|
| `var x: i32 = c;` (`c: i64`) — narrowing | E003 |
| `var x: i64 = f();` (`f(): i32`) — widening | E003 |
| `c - n`, `c < n` (`c: i64`, `n: i32`) — arithmetic / ordering | accepted |
| `c == n` (`c: i64`, `n: i32`) — equality | E041 |
| `var x: i64 = 5;` — unsuffixed literal | accepted |
| `var x: i32 = 3000000000;` — literal out of range | E047 only |

So there is no implicit conversion in either direction, integer widths unify in
numeric *operator* contexts but not in equality, and an unsuffixed literal is
read at its destination's type.

### Why tightening `type_eq` alone does not work

`type_eq(x, t_i32())` was the idiom for "is this an integer?" at 36 sites, so
tightening the one comparison made the operator rules width-strict too. Measured
on the compiler's own sources, that is **1,847 diagnostics** where native is
silent — 668 E009, 590 E041, 321 E033, 110 E003 — i.e. the fixpoint stops.

The laxity was standing in for three things the checker did not have. Each is
now explicit:

1. **`is_integer(t)`** — "an integer of any width", replacing all 36
   `type_eq(_, t_i32())` sites. Behaviour-preserving on its own: with the old
   `type_eq` those calls already meant exactly this.
2. **`settles_to` reaches integer destinations** — an unsuffixed literal is read
   at the destination's width, so `var n: i64 = 5;` is not an i32-into-i64
   assignment. The predicate already existed for `f64` (#6654); it needed the
   integer arm and five more call sites (method / closure / free-function
   arguments, `var` initialisers, `return`, array elements, `append` / `with`).
3. **`int_result`** — an integer binop yields its operands' width rather than
   always i32, so `a + a` on i64 stays i64. Unary minus likewise.

Equality gets the literal rule explicitly in both the structural and diagnostic
paths: two *differently typed* integers are E041 like native, but an unsuffixed
literal on either side is read at the other operand's width.

### The literal classifiers have to agree

`check_expr` read a literal's suffix but not its magnitude, so `1234567890123`
typed i32 while `parser.if_expr_rt` and `parser.mono_infer` — which the parser
already calls "sibling literal classifiers" — called it i64. `check_expr` is the
third and now applies the same `literal_is_i64` rule (made `pub` for it).

Two boundaries that rule gets wrong on its own, both pinned by differential rows:

- **`-2147483648`** is i32-min, but the magnitude rule judges the operand's
  *positive* text, where `2147483648` is one past i32-max. The negation is
  read on the signed value.
- **A wide literal at a destination** still settles: native reads it at the
  destination's type and lets E047 object if it does not fit, rather than
  reporting an assignment mismatch as well.

### Measured outcome

Same four-corpus sweep as #6961. On `conformance/cases`, false-positive files go
from **58 to 54**: eight fixed (`i64_arith`, `i64_bitops`, `i64_max_to_string`,
`i64_min_to_string`, `int_bit_accessors`, `int_byte_swap`, `int_checked_div`,
`to_string_round_trip`) and none newly broken. The compiler's own sources are
back to their single pre-existing E064.

E043's false positives drop from nine files to one — width was the cause of the
other eight. The one that remains (`type_param_name_collision`) is a
generic-parameter problem unrelated to width, so **E043 stays in
`is_partial_checker_gap_code`**; deleting that line needs the collision case
fixed first.
