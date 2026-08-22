# Self-Host Implementation — Code-Quality Audit

> **Tracked in GitHub:** the open items below are mirrored as a checklist in
> [#2849](https://github.com/JakeChampion/lang/issues/2849); the confirmed
> SH-057 miscompile has its own issue [#2850](https://github.com/JakeChampion/lang/issues/2850).
> This doc stays the detailed reference (file:line, repro, fix sketch).

Audit of the self-hosted Fern compiler under `examples/self_host/` (172,450 lines
of Fern across 91 files), compared where useful against the Go reference in
`internal/`. The goal is a worklist we can resolve **one item at a time**: every
finding has a stable ID (`SH-NNN`), a severity, the affected `file:line`, and a
concrete remediation. Check items off as they land.

> Scope note: the self-host tree is a deliberately constrained bootstrap subset
> (every file imports only siblings and `std/io`). That constraint is real, but
> it does **not** explain most of the duplication below — receiver methods,
> struct-update spread, sibling imports, and generics (`astwalk.fern:19` takes
> `[T]`) are all already used in this tree, so a shared sibling `util.fern` /
> `modload.fern` is fully within the subset. A handful of modules
> (`watbin`, `x86_native`, `arm64_native`, `elf`, `lexer`) import nothing **by
> design**; their duplicate helpers are deliberate and are called out where they
> appear.

---

## 1. Executive summary

The implementation is **functionally impressive and broadly correct** — it has a
full lexer, parser, monomorphiser, checker, tree-walker, an IR + SSA optimiser,
native x86-64/arm64 and WASM backends, ELF/Mach-O writers, a WIT codec, and a
literate engine, all with extensive in-file test coverage. The comments are
unusually good and frequently record real bug history.

The quality problems are almost entirely **structural / maintainability**, not
correctness — with a small number of genuine latent bugs (§2). The dominant
themes:

1. **Giant functions.** `ssa.fern:524 build_expr` is **790 lines**;
   `asmcore.fern:2704 infer_expr_type` is **497**; `parser.fern:6033
   parse_type_name` is **341**. `wasm_ir.fern`'s `emit_function_ir` led this
   list at 1,257 and is now **84** (SH-058).
2. **Left-fold string accumulation.** `out = out + …`, the O(n²) shape whose
   cost the tree's own comments document. `wasm_ir.fern` led at **701 sites**
   and is now down to **1** (SH-027); **278 in `ssa_arm64` and 259 in
   `ssa_x86`** remain, though most of those are a fixed prologue rather than a
   per-op path — see SH-027 for the breakdown.
3. **Parallel-maintained twins and god-structs.** `asm_ir.fern` ↔
   `asm_arm64_ir.fern` keep two `emit_runtime` surfaces whose `has_need` key
   sets have **drifted apart by 7 keys**; `irlower.fern:409 LowerState` carries
   **31 fields, 20 of them arrays**; `wasm_ir.fern:8027` takes 13 positional
   params carrying 8 parallel arrays.
4. **A stringly-typed type system** carried as raw strings and re-parsed with
   magic ASCII byte constants (`91`=`[`, `44`=`,`, `93`=`]`, `46`=`.`). Largely
   migrated onto `parse_type_ref` (SH-021); the endgame — the parser storing
   `TypeRef` directly — is still open.

Leaf-utility duplication, the headline of the original audit, is **mostly
resolved**: see SH-020 for what is left and what is deliberately kept.

### Scorecard (subjective, per area)

| Area | Files | Grade | One-line take |
|---|---|---|---|
| Machine-code encoders / containers | `x86_native` (pt.1), `arm64_native` (pt.1), `elf` | **A** | Small, pinned-to-`llvm-mc`, single-responsibility. The model to emulate. Import-free by design. |
| Optimiser / SSA | `ssa` | B− | Strong algorithms + comments; undermined by an overloaded `imm` field and 2 giant fns. |
| Literate / printer / constfold | `literate`, `printer`, `constfold` | B | Clean and tested; only duplication + hand-rolled option types. |
| Frontend | `lexer`, `parser` | B− | Capable; no AST visitor in the rewrite passes, positionless `StmtUnknown`, types-as-strings. |
| Checker / shared frontend | `checker`, `asmcore` | C+ | Real `Type`/`Diag` model, but 15 duplicated walkers and `build_func_scope` rebuilt 9× per function. |
| Tree-walker | `interp` | C+ | Ambitious + tested; five parallel arrays as an environment, scoping by length-trim. |
| IR lowering | `irlower` | C | 59k lines; a 31-field `LowerState` and a 1,704-line `lower_call_method`. |
| Native IR emitters | `asm_ir`, `asm_arm64_ir` | C | Two `emit_runtime` surfaces hand-maintained in parallel, key sets already drifted. |
| WASM path | `wasm_ir`, `watbin` | C | 13-param unbundled entry point; `emit_function_ir` (1,257 lines) and the 701 left-folds are resolved. |
| Driver / glue | `*_run`, `fern` | C− | 56 `main()`s / 45 `*_run.fern`; import resolver duplicated and already drifted. |

---

## 2. Correctness bugs / latent hazards (do these first)

- [x] **SH-001 — the bytecode VM turned any user string starting with `"__"`
  into a runtime error.** Closed by matching only the exact compiler sentinels;
  the bytecode VM is no longer part of the tree.

- [x] **SH-002 — Control flow rides magic error strings.** _Done:_ `StepResult`
  (`interp.fern:2596`) grew a dedicated `sig: i32` channel (0 = none, 1 = stop —
  a `return`'s value or a runtime error in `ret`, 2 = break, 3 = continue) built
  via `step_none`/`step_stop`; every construction and consumer (loop
  break/continue handling, `eval_block` short-circuit, function-body enders, the
  top-stmts driver) keys off `sig`, and the
  `VErr("__noreturn__"/"__break__"/"__continue__")` sentinels + their `is_*`
  matchers are deleted — no Value can be mistaken for a control signal, and a
  typo'd literal can no longer silently break control flow.

- [x] **SH-003 — `watbin` silently drops unknown instructions.** _Done:_ the
  opcode tables' `return 0` sentinel made an unhandled instruction fall
  through BOTH encoder paths (folded `enc_instr` and the flat token loop)
  and emit NOTHING — the module encoded with the operation missing, so it
  failed validation with a baffling type mismatch or ran with wrong values
  (the `sat_trunc_opcode` comment at `watbin.fern:919` records a real instance).
  Both fallthroughs now `eprint` the op and `exit(1)` when a NAMED op reaches
  them (empty/unnamed nodes keep the lenient skip). Verified benign-token
  clean across the wasm-binary, CLI (`-target wasm32-wasi -emit core-module`), leb128,
  ret-struct-field, streq-helper, and arm64-builds suites.

- [x] **SH-004 — `parse_f64` does not round-trip to nearest double.** _Done:_
  every decimal→f64 reader in the self-host is the same correctly-rounding
  kernel — the classic exact decimal-shift algorithm (digit buffer + movable
  point, grade-school ÷2/×2 to binary-normalize, bit extraction,
  round-to-nearest ties-to-even, with subnormal/±inf/±0 handling). It exists in
  FOUR copies: `util.fern:382 parse_f64_bits`, which the IR emitters and the
  interpreter's literal reader import, and the three the import-free assembler
  modules keep standalone (`watbin.fern:458`, `x86_native.fern:2141`,
  `arm64_native.fern:2917`). The reference commentary lives in `util.fern`.
  Pinned bit-exact against `strconv.ParseFloat` by
  `TestSelfHostParseF64{Watbin,X86Gas,Arm64}` and
  `TestSelfHostInterpFloatLiteralBits` on a shared corpus of the compiler's
  libm constant spellings, the hard subnormal/overflow boundary literals
  (`2.4703282292062327e-324` et al.), exact ULP ties written out in full, and
  17-digit round-trip spellings of seeded random doubles;
  `TestSelfHostParseF64MirrorsAgree` holds the four copies to identical code,
  since three of them only run behind a Linux x86-64 toolchain.

- [x] **SH-005 — the GAS-text x86 front-end silently dropped unsupported
  mnemonics.** _Done:_ `X86Asm` (`x86_native.fern:1397`) grew an
  `unknown: string[]` list (`:1433`); the three silent-skip sites in
  `x86_gas_emit` (`:2624`) record instead of dropping, and every ELF-writing
  driver fails on a non-empty list before writing the executable — mirroring the
  arm64 path's `p.unknown` check in `fern.fern`.

- [x] **SH-006 — `arm64_gas_reg` defaulted unknown registers to x0.** _Done:_
  the decode (`arm64_native.fern:1630`) is strict — x0..x30 / w0..w30, d0..s31,
  sp/lr/xzr/wzr, and `-1` for anything else (including digit-suffix garbage like
  `x1a` and out-of-range `x31`/`x99`). Because the encoders would fold `-1` into
  garbage bits, a `-1` alone is not centrally catchable (`& 31` masks it to
  xzr/sp), so `arm64_gas_program` (`:3251`) pre-scans every instruction's
  operands for REGISTER-SHAPED tokens that fail the decode and records them on
  `p.unknown` — the same gate that already refuses unknown mnemonics.

- [x] **SH-007 — `index_of_str` returned 0 (not −1) on miss.** _Done:_ the SSA
  and wasm hosts share one canonical `util.index_of_str` (`util.fern:330`)
  returning −1, so a missing `funcaddr` emits table slot −1 (an out-of-bounds
  `call_indirect` that traps loudly) instead of silently calling slot 0.

- [x] **SH-008 — the wasm string table returned the scratch base on miss.**
  _Done:_ `wasm_ir.fern:444 offset_of_value` now `eprint`s the missing literal
  and `exit(1)` instead of returning 24 (the iovec scratch base). A miss is a
  compiler bug (the literal escaped the collection pre-pass), so it halts the
  compile rather than pointing emitted code at scratch memory.

- [x] **SH-009 — Dead duplicate `movl` branch** in the GAS-text x86 front-end,
  unreachable and additionally using the 64-bit register decoder to feed
  `x86_mov_r32_imm32`. _Done:_ deleted; the live `x86_gas_movl`
  (`x86_native.fern:1996`) handles the `$imm` form via `x86_gas_reg32`.

- [x] **SH-010 — `digits_to_i32` had drifted across copies** (sign-naive in some,
  sign-aware in others). _Done:_ consolidated onto one canonical **sign-aware**
  `util.digits_to_i32`; sign-aware is a strict superset on digit-only input, so
  every caller is safe and the latent negative-string bug is fixed.

---

## 3. Cross-cutting themes (highest leverage)

These each touch many files; fixing the root removes dozens of individual
findings. Ranked by leverage.

### T1 — Shared utility module
- [~] **SH-020 — `examples/self_host/util.fern`** exists, holds **31
  definitions**, and is imported by **61 of the 91 modules**. The `i32_to_string`
  strand is finished: one canonical copy at `util.fern:21`.
  Current duplication census across the tree: **5,126 function definitions**
  (4,863 free functions + 263 receiver methods); **89 names are defined in 2+
  files**, giving **171 redundant copies**, of which **55 are `main`** — so
  roughly **116 real redundant copies** remain.

  **Most of that residue is not leaf-utility duplication and is not a dedupe
  target.** It breaks down as:
  - **Per-ISA `emit_*`** in `ssa_x86` / `ssa_arm64` / `ssa_wasm` (`emit_inst`,
    `emit_term`, `emit_func`, `emit_program`, `emit_phi_moves`, `emit_binary`) —
    same names, genuinely different code. See SH-025 for the part of this that
    *is* liftable.
  - **Per-driver `main`** — one per `*_run.fern`. See SH-056.
  - **Import-free modules.** `watbin.fern`, `x86_native.fern`,
    `arm64_native.fern`, and `elf.fern` are single self-contained modules by
    design (each header says so), and `lexer.fern` likewise imports nothing.
    Their duplicate helpers must stay.
  - The **`parse_f64_bits` + `pf64_div2` / `pf64_mul2` / `pf64_sub` /
    `pf64_all_zero` family** — 5 functions in 4 copies each (`util.fern:382`,
    `watbin.fern:458`, `x86_native.fern:2141`, `arm64_native.fern:2917`) — is
    hand-synced **on purpose** and pinned by `TestSelfHostParseF64MirrorsAgree`.
    **Not a dedupe target.**

  Genuinely liftable leftovers: `split_commas` (`irlower`, `printer`). The
  `block_index` / `pred_slot` strand is closed by SH-025 (`pub` on `ssa.fern`'s
  pair, the three backend copies deleted). The `join_path` strand is closed by SH-055
  (`util.module_path_join`; `mvs.join_path` is a different function that stays).

  **The `trim` strand is closed, and it was a BUG, not a dedupe.** The three
  copies were not identical: `fern_toml`'s stripped only space and tab where
  `mvs`'s and `literate`'s also stripped `\r`. `fern_toml` splits its input on
  `'\n'` alone, so on a CRLF file every line kept a trailing `\r` — and
  `parse_lock` compares a line for exact equality against `"[[package]]"`, so
  that header never matched, `have` never went true, and **a CRLF `fern.lock`
  parsed to zero packages**: the loader saw an empty lock rather than an error.
  Native does not have this bug — `internal/mvs/lock.go:66` makes the identical
  comparison but trims with `strings.TrimSpace`, which strips `\r` — so this
  was a self-host-only divergence from the reference it mirrors. One
  CRLF-aware `util.trim` now serves `fern_toml` and `mvs`, pinned by
  `toml_crlf_run.fern` + `TestSelfHostTomlCRLF`, which parses the same
  documents LF and CRLF and requires the halves to agree.
  Only the lock half ever misbehaved, which is why it went unseen: the manifest
  half reads values through `quoted_value`, which scans to the closing quote and
  steps over a trailing `\r`.

  **`literate`'s copies cannot be lifted at all**: the Go gates hand
  `literate.fern` to the compiler as one standalone source with no module
  staging (`literate.fern:54-56` states the import-free design), so its `trim`
  and `contains` stay. `ferndoc`'s `split_commas` is likewise excluded — it
  annotates its slices `own(...)`, a different ownership contract from the
  `irlower`/`printer` pair.

  **A second naming hazard, worse than the `index_of_str` one below.** `regn`
  (`ssa_x86.fern:79`, `ssa_arm64.fern:62`) is **byte-identical text with
  divergent behaviour**: it dispatches to `regname` / `regname64` / `slot`,
  which are per-ISA leaves (`%r10d` vs `w12`, `-N(%rbp)` vs `[sp, #N]`). A
  byte-diff reports it as safe to lift and it is not. **Not a dedupe target.**

  **Naming hazard — a naive `util.`-qualification sweep binds the wrong
  function.** `index_of_str` names **two different functions**: array index-of
  (`util.fern:330`, `watbin.fern:330`, `(xs: string[], s: string)`) and
  **substring** index-of (`fern_toml.fern:86`, `(s: string, sub: string)`).
  Similarly `contains` is substring search (`util.fern:342`, `literate.fern:74`)
  while array membership is the differently-named `contains_str`
  (`ssa.fern:1773`). Check the parameter shape, not the name.

  Still worth doing: named ASCII constants (`DOT=46`, `LBRACKET=91`,
  `RBRACKET=93`, `COMMA=44`, `ZERO=48`, `NINE=57`, `SLASH=47`, `DQUOTE=34`,
  `BACKSLASH=92`) to kill the magic-number comparisons that recur in every file.

  > Note: `core/int.int_to_string`, `parse_int_radix`, and the `std/i32` digit
  > predicates already exist in the stdlib. Prefer importing those if the
  > bootstrap subset can take them; otherwise mirror them once in `util.fern`.

### T2 — Stringly-typed type system
- [~] **SH-021 — Carry a structured type AST from the parser** instead of flat
  strings re-parsed downstream. Root cause of `asmcore.fern:2603 ty_from_name` /
  `:2619 split_tuple_ret`, the checker's **5** `type_from_name*` resolvers
  (`checker.fern:1878, 3162, 3218, 3276, 3587`), and the wasm/irlower type
  decoders. _Fix:_ a small `TypeRef { base, args[], array_depth }` produced once;
  pattern-match instead of byte-scanning.
  _Foundation slice landed:_ `parser.fern` defines
  `TypeRef { base, args[], array_depth, is_tuple }` plus the canonical
  `parse_type_ref` (`:7896`) / `render_type_ref` (`:7935`) pair — the single
  place the `[]` / `(…)` / `Name[…]` / `", "` grammar is scanned — with a
  round-trip golden (`typeref_run.fern` + `TestSelfHostTypeRef`).
  _Slices landed since:_ asmcore's `ty_from_name` (via `ty_from_ref`) and
  `split_tuple_ret` / `tuple_ret_tag_at`; the checker's
  `type_from_name_with_structs_unions` (via `type_from_ref_su`) and the three
  simpler resolvers' array-suffix peel; `count_type_args`; the wasm backend's
  `extern_sum_param_supported` / `_is_option`, `parse_option_payload` /
  `parse_result_err_payload`, and `nth_tuple_type_elem`; irlower's
  `tuple_type_elem_tag`; flatten's `rewrite_type_name` tuple branch. Each is
  byte-identical on valid input and pinned by its own golden driver
  (`ty_from_ref_run`, `tuple_tags_run`, `type_resolve_run`,
  `type_resolve_simple_run`, `count_type_args_run`, `wasm_extern_sum_run`,
  `wasm_option_payload_run`, `nth_tuple_elem_run`, `tuple_elem_tag_run`) plus
  the bootstrap / per-module fixpoints. Three of the migrations additionally
  correct a garbage-on-array edge case the old prefix+trailing-`]` scans hit
  (`Option[i32][]`, `Result[…][]`, `(i32, i32)[]`).
  _Remaining:_ every genuine canonical-type-spelling comma-depth decoder is now
  migrated. What is left is either lower-value or delicate: the unambiguous
  `[]`-suffix element-strips (`ft[0:len-2]` / `ty_spelling_is_array`); the
  internal `,`-joined tag encodings (irlower's `csv_nth` /
  `LowerState.tuple_elem_tag`, which decode a spaceless CSV of tags, not a
  canonical type spelling); and the parser's `bind_unify` monomorphisation
  unifier, whose final case matches a generic pattern against a `__`-mangled
  clone name, so it stays string-based. The endgame remains having the parser
  store `TypeRef` directly so the string becomes render output. Unblocks #4394
  lever 1.

### T3 — No generic AST visitor / fold in the remaining passes
- [~] **SH-022 — Add `walk_expr`/`walk_stmt` (or a fold) once.** _In progress:_
  `astwalk.fern` carries the walk itself once — `fold_expr_nodes` /
  `fold_stmt_nodes`, generic in the accumulator (`astwalk.fern:19` takes `[T]`)
  and parameterised by a statement visitor, an expression visitor and a descent
  predicate, with `fold_expr` / `fold_stmt`, the pruned pair, and
  `map_expr` / `map_stmt` / `map_stmts` as wrappers (#6993). Every collector in
  the module is a visitor over it: `collect_idents_expr`/`_stmt`,
  `collect_bound_stmt`, `collect_calls_stmt`, `collect_qualrefs_expr`/`_stmt`.
  `flatten`'s and `asmcore`'s private binder walks are converted onto it —
  **asmcore's `collect_idents_*` is gone**, leaving only the delegating comments
  at `asmcore.fern:37,39` and the call at `:47`.
  Still hand-enumerating every Expr/Stmt variant:
  - ~~`wasm_ir.fern` — 28 `collect_*` / `module_uses_*` walkers~~ — **this row
    was wrong about them, and they are done.** They are not AST walkers: they
    scan the flat `LowerResult[] -> ops[]` IR list by `kind_tag`, so
    `astwalk`'s expression fold does not apply to a single one of them. What
    they shared was a nested `cache`/`ops` loop, open-coded 17 times, and they
    now go through one `any_op(cache, pred)` spine with a predicate each. The
    other 17 already delegated to `module_emits_op_cached`. Six `string[]`
    collectors (`str_values`, `const_agg_values`, `field_reclaim_types`,
    `struct_drop_types`, `fn_value_table`, `indirect_fn_sigs`) keep their loops:
    they accumulate rather than short-circuit, so a boolean spine does not fit.
  - **`parser.fern` — 12** rewrite passes (`expr_mentions`, `mono_infer`,
    `mono_expr`, `mono_call_expected`, `mono_stmt`, `mono_stmts`, `ms_expr`,
    `ms_stmt`, `ms_stmts`, `ms_func`, `rw_call_expr`, `rw_call_stmts`) — all
    genuine hand-enumerated walks, and **none of them can use `astwalk`**.
    `astwalk` imports `parser`, so parser importing `astwalk` is a cycle; the
    fix this row prescribes does not compile there. Converting them needs the
    AST types lifted into a base module both can import, or a second traversal
    spine living inside `parser.fern` — a decision, not a mechanical change.
    (Same shape as SH-025's proposed `ssabackend.fern`, which also cycles.)
  - **`checker.fern` — 51** functions matching on 4+ Expr/Stmt variants,
    ~4,957 lines, not the 15 this row claimed. `checker` DOES import `astwalk`,
    so this half is convertible. Most of the 51 are not: `check_expr` (17
    variants) and `check_stmt` (14) dispatch differently per node, which is the
    checker's job rather than boilerplate. The convertible ones are the
    collectors that look for one fact and otherwise just recurse — the
    `mc_mentions_*` pair is done, `vref_*`, `ow_count_ident` and
    `e049_expr_lambdas` are the same shape.

    **A collector may read fields that are not expressions.** `mc_mentions_*`
    tests `StmtAssign.target`, a bare string on the statement, which an
    expression-only fold never sees; it needs `fold_expr_nodes` /
    `fold_stmt_nodes` with a statement visitor as well. Check what each
    collector reads before assuming `fold_expr` covers it.

    **Check the boundary in both directions.** `mc_mentions_*` needs the fold
    to DESCEND into lambda bodies; `e049_expr_lambdas` hands each body to
    `e049_check_assigns` and needs it PRUNED there (`fold_expr_pruned` with a
    descent predicate), or every nested lambda is reported once per enclosing
    one. Same spine, opposite requirement at the same node.

    **The remaining collectors are DIAGNOSTIC collectors, and nothing gates
    what a wrong answer there looks like.** Every check over self-host checker
    output compares a sorted, de-duplicated SET — so reordering diagnostics or
    reporting one twice is green everywhere (`docs/TEST-GATES.md`, "What
    nothing gates"). The boolean and counting collectors converted so far
    (`mc_mentions_*`, `ow_count_ident`) are immune to this by construction;
    `vref_expr`, `slit_diags`, `e044_expr`, `e032_expr` and the rest are not.
    Verify order and multiplicity by hand for each, or add an order-sensitive
    check first — do not batch them.

    Not every remaining one is convertible at all. `vref_stmts` threads a
    `cur: Scope` that CHANGES as it walks (each `StmtVar` extends it), which is
    what this row means by "scope-threading passes" — a fold whose accumulator
    is just the diagnostic list cannot carry that. And `slit_diags` is
    **POST-order**: it recurses into a struct literal's `field_values` before
    emitting that literal's own E043/E005, where every `astwalk` fold visits a
    node BEFORE its children. Converting it either reverses nested-vs-outer
    diagnostic order or needs a post-order fold added to `astwalk` — a design
    decision, not a mechanical change. Its order is pinned in the sequence gate
    either way.

    Probing `slit_diags` for that gate turned up a **live divergence nothing
    else can see**: for `Out { bad2: In { bad1: 1 } }` the self-host reports
    four diagnostics (the inner literal's unknown and missing fields as well as
    the outer's) where the Go checker reports two, having stopped descending
    once the outer field name was unknown. Both sides give the code SET
    {E043, E005}, so the codes and hint-text differentials are blind to it.
    Which side is right is open — the inner literal does have an unknown field
    and does miss one, and `slit_diags` reaches both from the literal's own
    `type_name` without needing an expected type, so the extra reports may be
    the better answer. Pinned as-is so it cannot drift further.
  - `ssa` / `ssa_wasm` re-open-code the 3-level funcs→blocks→insts loop.

  _Fix:_ one traversal taking a per-node callback; removes well over 1,000 lines
  and the "added a field, forgot a walker" hazard.

### T4 — Struct-copy boilerplate (use the spread the parser already supports)
- [x] **SH-023 — Replace full struct-literal rebuilds with `{ ...x, field: y }`.**
  _Done:_ 40 rebuild literals across `parser`, `irlower`, `flatten`, `checker`
  now spread — 22 `Module` (11 fields) and 18 `FuncDecl` (20 fields) — eliding
  **516 field restatements**. `checker.fern`'s scope constructors were already
  down to 2. What the row was really about is the bug class: adding a field to
  one of these structs meant finding every rebuild and threading it through, and
  a missed one silently dropped that field rather than failing to compile.

  **Two things make this row less mechanical than it reads, both learned the
  hard way. If you automate a similar rewrite, encode them.**

  1. **Not every literal is a rebuild.** One that builds a value from computed
     parts names every field legitimately; a spread there is wrong, not tidy.
     29 `FuncDecl` and 3 `Module` literals are of that kind and stay.
  2. **A same-named field does NOT mean a same-typed base.** `StmtVar` and
     `ExprLambda` each carry `name` / `body` / `ret_type`, so a
     `FuncDecl { name: v.name, body: v.body, … }` at a lambda-lifting site
     looks exactly like a rebuild of `v` — and `FuncDecl { ...v }` is then a
     type error (E003, "struct-update base must be FuncDecl, got StmtVar").
     **The separating signal is the passthrough RATIO**: a real rebuild passes
     17 of 20 fields through, a name collision only 3. Requiring a majority of
     the declared fields splits them exactly. A "2+ passthroughs" rule does not,
     and mis-rewrites 7 sites.
  3. **A literal-finder keyed on `Name {` also matches `): Name {`** — a return
     type followed by the FUNCTION's opening brace. Require the whole body to
     parse as `field: value` pairs covering the declared field set, or the
     rewrite eats function bodies. 22 such matches occur for `Module` in
     `parser.fern` alone.

  The checker caught (2) rather than any test, which is the argument for running
  `-check` before the expensive byte-identity gate rather than after.

### T5 — Backend duplication beyond what asmcore/CLAUDE.md claims
- [ ] **SH-024 — Introduce an `Emitter` interface to dedupe `asm_ir.fern` ↔
  `asm_arm64_ir.fern`.** The two IR emitters keep three hand-maintained twin
  surfaces:

  | function | `asm_ir.fern` | `asm_arm64_ir.fern` |
  |---|---|---|
  | `emit_ir_runtime` | `:1031` | `:6323` |
  | `emit_ir_runtime_fern_fn` | `:6370` | `:2138` |
  | `emit_function_via_ir` | `:4303` | `:464` |

  They are gated on `has_need` keys that have **already drifted**: 94 distinct
  literal keys in `asm_ir.fern` (178 call sites) against 91 in
  `asm_arm64_ir.fern` (131 call sites). Five keys exist only on x86 —
  `alloc_u8`, `maps`, `strbuf`, `subprocess`, `tcp_pollable` — and two only on
  arm64 — `arr_i32_min_max`, `float_transcendentals`. **The drift is itself the
  finding**: the runtime surface is supposed to be target-independent, so a key
  present on one backend and absent on the other is either a missing arm64
  runtime or dead x86 code. _Fix:_ a thin target interface
  (`push`/`pop`/`load_local`/`branch_if_zero`/`call`/`syscall`) driven from
  shared code, with the need-key table as shared data; each backend implements
  only the leaves.
- [~] **SH-025 — lift what the three SSA backends hand-mirror.**
  _Helper half done:_ `block_index` and `pred_slot` were byte-identical across
  all four SSA modules; `ssa.fern`'s two are now `pub` and the three backend
  copies are gone (−48 lines, no new import — the backends already import
  `./ssa` for `SFunc`/`SBlock`, and `ssa.fern` imports none of them).

  **This entry used to ask for a new `ssabackend.fern` (the SSA analogue of
  `asmcore`). That module cannot exist**: it would have to import `ssa` for
  `SFunc`/`SBlock`, and `ssa` would have to import it back for its own six
  `block_index` call sites — a cycle. `asmcore` is not a precedent, because it
  declares the types it shares rather than borrowing them. Lift to the module
  that already declares the types.

  _Still open:_ the three backends hand-mirror `emit_inst` / `emit_term` /
  `emit_func` / `emit_program` / `emit_phi_moves` dispatch ladders. Align the
  gratuitously-divergent `emit_term` signatures first — `ssa_x86` takes a `name`
  param, `ssa_arm64` reads `f.name`, `ssa_wasm` takes neither — then lift a
  parameterised `emit_func`/`emit_program` driver. Note the trap recorded under
  T1: `regn` is byte-identical TEXT across `ssa_x86` and `ssa_arm64` with
  per-ISA behaviour, so it is not liftable and a byte-diff will say otherwise.

  _Noted, not changed:_ `pred_slot` returns 0 rather than −1 on a miss — the
  shape SH-007 closed for `index_of_str`. It is consistent across all four
  copies (so not drift), and whether a miss is reachable depends on the
  phi-predecessor invariant. Changing it is a behaviour change wanting its own
  analysis and test.

### T6 — Errors & signals smuggled through value types / sentinels
- [ ] **SH-026 — Stop overloading value types for errors/signals.** Surviving
  sentinels: `v_err("__noreceiver__")` (`interp.fern:1464`, `:2433`) with its
  checker twin `t_unknown("__noreceiver__")` (`checker.fern:1972`), and
  `v_err("__noclosure__")` (`interp.fern:1465`). `lookup_*` returns `name:""` to
  mean "not found" (`checker.fern:1219 lookup_sig`, `:1508 lookup_struct`,
  `:1630 lookup_method`, `:1731 lookup_union`), forcing `.name.len() > 0` checks
  at every call site. **22 bespoke `*Result` structs** each re-implement
  `(value, ok)` or `(node, next_pos)` — and two of them are both named
  `CheckResult` in different modules. _Fix:_ a kinded error type;
  `Option[T]`/`Result[T,E]` now that generics work (T8); meanwhile give the
  sentinel-carrying values a `kind` field and namespace the internal sentinels.

### T7 — O(n²) string accumulation in emitters
- [ ] **SH-027 — Use a `strbuf`/chunk-join accumulator in the string emitters.**
  `wasm_ir.fern` is **done**: `emit_ir_op` (#7320) and the other 19 accumulating
  functions (420 sites) now collect into a `string[]` and
  `util.str_join_chunks` once. One fold remains there, in `fn_type_decl`, which
  returns `out + suffix` rather than `out` — the accumulator is read, so the
  shape does not apply.

  The SSA backends' `emit_program` is **done** too (204 / 200 / 15 sites).
  Remaining `out = out +` sites:

  | file | sites | where |
  |---|---|---|
  | `ssa_arm64.fern` | 74 | the per-instruction chain — leave it, see below |
  | `ssa_x86.fern` | 59 | same |
  | `printer.fern` | 214 | does not take this rewrite, see below |
  | `ferndoc.fern` | 30 | unexamined |
  | `irlower.fern` | 25 | unexamined |
  | `ssa_wasm.fern` | 3 | the per-instruction chain |
  | `asm_ir.fern` | 2 | — |
  | `asm_arm64_ir.fern` | 1 | — |
  | `wasm_ir.fern` | 1 | `fn_type_decl`, above |

  (`printer.fern` was missing from this table entirely; the `*_run.fern` test
  drivers also fold, and are not worth converting.)

  **Where the SSA cost was, and was not.** `emit_program` folds the whole
  program body into `out` and *then* appends the ~140-line runtime on top of
  it — **198 of `ssa_arm64`'s 204 folds, and 194 of `ssa_x86`'s 200, come after
  the body is already in the accumulator**. The appended strings are fixed; the
  string they are appended TO is the entire program, so embedding the runtime
  cost ~200 copies of a program-sized value. That is the real find here and it
  is what got converted.

  **The per-instruction chain is the opposite and should be left alone.** It
  looks worse — the accumulator is threaded as a parameter through `emit_inst`,
  `emit_term`, `load_op`, `store_res`, `emit_const`, `emit_binary`, `store_reg`
  and `emit_phi_moves` — but `emit_func` seeds `emit_inst(…, "")` **fresh for
  every instruction** (3 `""` seeds against 55 `out` threads in `ssa_arm64`),
  so the accumulator never spans more than one instruction's output. Converting
  it would mean changing that whole chain's signatures for a fold bounded by a
  few hundred bytes.

  A count of fold sites does not order this work; where the accumulator's
  *contents* come from does. Both halves of that were recorded here backwards
  until measured.

  **A default `selfhost-emit-hashes` run does not gate the SSA backends** — the
  SSA pipeline is opt-in behind `-ssa` (`fern.fern:1873`), so the sweep never
  reaches `ssa_arm64` / `ssa_x86` / `ssa_wasm`. Verified: changing one emitted
  string in `ssa_arm64.fern` leaves that sweep byte-identical. Use
  **`selfhost-emit-hashes --ssa`**, which exists for this and moves every arm64
  row under the same probe. Note that `-ssa` alone would not have been enough:
  `try_ssa` falls through to the IR path for any program outside the SSA subset,
  silently, so most rows would have been IR bytes — the `--ssa` mode emits each
  case both ways and marks those `IR-FALLBACK` so they cannot count as
  coverage.

  **`printer.fern` does not take the same rewrite.** Almost every one of its
  accumulators ends `return out + ")"` / `return (out + pad + "}", g)` rather
  than `return out`, so the partial string is read and the write-only
  precondition fails. They are convertible — append the suffix as a final chunk
  — but one at a time, by hand, and most are small per-node strings rather than
  a whole-file accumulator. Only `escape_fstring_lit` and `indent_str` match the
  mechanical shape.

  _Method that worked on `wasm_ir.fern`:_ convert only where the accumulator is
  provably **write-only** — every mention of `out` in the function is its
  declaration, an `out = out + …` fold, or `return out;`. That is the
  precondition: no arm inspects the partial string, so the concatenation can be
  defer to the join. Then assert every appended expression survives **verbatim**
  and the site count is unchanged, and gate on emitted bytes.

### T8 — Hand-rolled options/containers that generics would replace
- [ ] **SH-028 — Replace the hand-rolled option/result families with generics.**
  Generics work in this tree (`astwalk.fern:19` is `fold_expr[T]`), so this is no
  longer blocked — it is unconverted code. Still present: `OptInt`
  (`constfold.fern:93`), `OptBool` (`:121`), `OptString` (`:131`); the 22
  `*Result` structs (SH-026); the four `append_*` concat helpers in
  `flatten.fern` (`append_funcs:820`, `append_structs:825`, `append_aliases:830`,
  `append_enum_decls:1088`), which one generic `append_all[T]` replaces; and the
  placeholder `tag: i32` fields on the nullary variants. Convert family by
  family, each with the test at the layer it touches.

---

## 4. Per-file high-severity backlog

Items not already folded into a theme above. (Med/Low findings live in the
appendix §6.)

### Frontend
- [x] **SH-040 — `parse_primary` re-implemented struct-literal body parsing**
  that `parse_struct_lit_body` already provides. _Done:_ the bare-ident path now
  delegates to the shared helper (mirroring the qualified `pkg.Type {…}` and
  generic `Name[T] {…}` paths), removing ~55 duplicated lines and the drift risk
  between the two copies of the spread / field / trailing-comma loop.
- [ ] **SH-041 — Parse errors still collapse into positionless sentinel nodes.**
  Half fixed: `ExprUnknown` (`parser.fern:143`) carries `line`/`col`. But
  **`StmtUnknown` (`parser.fern:293`) has no position at all** — it is still
  `{ kind: string }` — and the position-carrying expression constructor is
  barely used: **1 call site of `e_unknown_at` (`:2202`, defined `:628`) against
  42 calls to the positionless `e_unknown` (`:627`)** and 25 calls to
  `s_unknown` (`:1075`). _Fix:_ give `StmtUnknown` `line`/`col`, then convert the
  `e_unknown` / `s_unknown` call sites to the positioned constructors and
  accumulate real diagnostics.
- [ ] **SH-042 — `parser.fern:6033-6373`** `parse_type_name` is a **341-line**
  giant whose recovery exists "to avoid OOM spin". No `parse_type_atom` /
  `parse_type_suffixes` split exists. Split it and return structured types
  (feeds T2).
- [x] **SH-043 — `lexer.fern`** the C-escape decoder (`\n\t\r\0\"\\\xNN`) was
  copy-pasted between `scan_string` and `scan_fstring`. _Done:_ extracted
  `apply_escape(l, esc) -> EscResult` (`lexer.fern:524`) so both scanners share
  one decode ladder while keeping their own literal-kind error messages. A future
  `\u` escape is a one-site change.

### Checker / asmcore
- [ ] **SH-044 — `asmcore.fern:2704-3200`** `infer_expr_type` is a **497-line**
  function with hundreds of hardcoded builtin-name string compares —
  table-drive `builtin_return_type(name, args)` + per-receiver method resolvers.
- [x] **SH-045 — `check_module` rebuilt one function's scope 10-13 times.**
  _Done:_ the count was worse than this entry recorded — 9 rebuilds across the
  body diagnostic passes, a 10th inside `check_func_body` (which the same loop
  calls), and up to 3 more in the `e053`/`e065`/`e032` loop. `build_func_scope`
  is a pure function of `(fd, st, structs, unions, methods)` and none of those
  change across the loop, so every rebuild re-resolved the same parameter types
  — `O(P × (parse_type_ref + |structs| + |unions|))` plus `O(P²)` array copying
  per call, since each `bind`/`with_*` allocates a fresh 14-field `Scope`.
  Now one build per function, shared by every pass; `check_func_body` takes the
  built `Scope` instead of the five tables; the five top-level passes share one
  bare module scope. Two things deliberately stay unshared: `.with_impls` is
  derived separately (only the call walk and `check_func_body` may see a
  populated impl table — `Scope.impls` records that E021 no-ops on an empty
  one), and the P002 default-value check keeps its own bare `new_scope_full`,
  since a parameter default binds no parameters. Pure CSE: no pass mutates the
  scope it is handed. The `Scope` doc comment conceding the nine rebuilds is
  deleted.
- [x] **SH-046 — builtin function / enum-variant membership as hand-kept `||`
  chains.** _Done:_ `is_builtin_variant` (`checker.fern:1048`) derives from the
  single variant→enum table in `mx_builtin_enum_of`, and `is_builtin_function`
  moved to `parser.fern:7430` as a one-liner over the single
  `builtin_function_names()` table. Loose end: `is_reserved_enum_name`
  (`checker.fern:1055`) is still a 4-name `||` chain
  (`Option`/`Result`/`IoError`/`JsonValue`) that should read from the same table.

### Interp / SSA
- [ ] **SH-047 — `interp.fern:444`** `Env { names, values, funcs, aliases,
  variants }` is **five parallel arrays** rebuilt per binding (O(n²)), and block
  scoping is done by length-trim: four sites capture
  `var base: i32 = env.names.len()` (`:2671`, `:2698`, `:2841`, `:2891`) and
  hand back to `trim_env(e, base)` (`:2926`), which rebuilds the arrays element
  by element. The invariant that outer-scope updates live below `base` is
  off-by-one-fragile and enforced only by comment. _Fix:_ a scope-frame `Env`
  with an explicit frame stack, so trimming is popping a frame rather than
  recomputing a prefix.
- [x] **SH-048 — `eval_expr` / `compile_expr` giants.** _Done:_ `eval_binary` and
  `eval_unary` extracted from interp's `eval_expr`; the VM's six near-identical
  arg-emit loops folded into one `compile_args` before that backend was retired.
- [ ] **SH-049 — `ssa.fern:51 SInst` overloads its `imm` field.** The records are
  **integer-tagged**, not string-tagged: `SInst { kind_tag: i32, result: i32,
  args: i32[], imm: i32, str: string }` (`:51`) and
  `STerm { kind_tag: i32, cond, target, t, f, value }` (`:54`). The live defect
  is that **one `imm: i32` carries four unrelated meanings**, discriminated only
  by `kind_tag`:

  | meaning | example sites |
  |---|---|
  | constant value | `ssa.fern:346` (`kind_tag: 1`), `:537` |
  | param index | documented at `ssa.fern:47` |
  | operand width, 32 or 64 | `ssa.fern:505` (`imm: 64`), `:572` (`imm: 32`) |
  | allocation count | `ssa.fern:500` (`imm: 1`), `:565` (`imm: 2`) |

  A `kind_tag`/`imm` mismatch is invisible to the checker and shows up as a
  wrong-width load or a wrong-size allocation. _Fix:_ a tagged union with named
  `width` / `ret_kind` / `count` fields and a checker-enforced exhaustive
  `emit_inst`.
  **Also stale:** the doc-comment block at `ssa.fern:41-50` still describes the
  kinds as string-tagged (`"const_int"`, `"binary"`, …) and documents the `imm`
  overload as if it were a feature. Rewrite it against the integer `kind_tag`
  encoding in the same change.
- [ ] **SH-050 — `ssa.fern:524-1313`** `build_expr` is **790 lines**, and
  `regalloc_linear` (`ssa.fern:3286-3440`) is 155 lines threading **16 separate
  `i32[]` locals** (`lo`, `hi`, `calls`, `def_pos`, `blk_ids`, `blk_starts`,
  `reg_of`, `order`, `act_val`, `act_hi`, `act_reg`, `free`, `nv`, `nh`, `nr`,
  `nf`) as ad-hoc parallel arrays. Extract per-builtin lowerings from
  `build_expr`; give the allocator an `Interval` / `ActiveSet` struct so the
  parallel arrays become fields.
- [ ] **SH-051 — `ssa.fern:1768`** `env_put` mutates in place and is correct only
  on unaliased scratch, versus the near-identical copying `env_set_at` (`:1750`)
  — make the aliasing contract type-level (`ScratchVec`) or rename it
  `env_put_unsafe_owned`.

### Emitters / WASM
- [ ] **SH-052 — `asm_arm64_ir.fern:6738-6859`** `darwinize` is a **122-line**
  line-oriented post-hoc string rewrite of already-emitted asm (peephole-on-text)
  — drive the dialect from the `darwin` flag at emit time instead (it already
  does for syscalls). Its O(n²) sub-complaint is **fixed**: the function opens
  with `strbuf_reset()` and returns `strbuf_take()`, and its header records why
  (one ordinary fixture emits 118,035 lines / 2.7 MB, and the old `out = out +`
  fold exhausted the arena on 61 of 339 arm64-darwin fixtures). What remains is
  the rewrite-after-the-fact shape, plus the sticky `pend_sys` state machine it
  needs to correlate a syscall-number line with the `svc` that consumes it — a
  correlation the emitter has for free.
- [ ] **SH-053 — `irlower.fern:409 LowerState` is a 31-field god-struct, 20 of
  them arrays.** `ops`, `locals`, `loop_blk`, `closure_locals`,
  `closure_opt_rets`, `structs`, `reclaimable_names`, `aliased_names`,
  `borrowed_names`, `xblock_pending`, `try_cleanup`, `defer_slots`,
  `grow_exempt`, `append_inplace`, `grow_sole`, `own_params`, `optret_pending`,
  `moved_names`, `moved_elided`, `move_sites` — a dozen of which are `string[]`
  sets hand-modelling ownership facts about named locals. `asmcore.fern:73-132
  EmitState` is the same shape one size down (16 fields, 10 arrays).
  _Fix:_ group the ownership sets behind a `LocalFacts` keyed by name (the
  `locals: LocalInfo[]` array is already the right shape to hang them off), so
  adding an ownership fact is one field on one struct rather than a new parallel
  array threaded through every lowering function. (`wasm_ir.fern` declares zero
  structs, so its state is instead spread across positional params — SH-054.)
- [ ] **SH-054 — `wasm_ir.fern:8027`** `emit_ir_module_units_mode` takes **13
  positional params carrying 8 parallel arrays** — `nss`, `unit_texts`,
  `all_strs`, `str_counts`, `all_caggs`, `cagg_counts`, `all_fns`, `fn_counts` —
  plus `mod`, `needs`, `fn_sigs`, `rc_bodies`, `mode`; the 12-param
  `emit_ir_module_units` at `:8007` is its twin. The count-and-values pairs
  (`all_strs`/`str_counts`, `all_caggs`/`cagg_counts`, `all_fns`/`fn_counts`) are
  three hand-rolled ragged arrays. This is down from a 21-param predecessor, so
  it is moving the right way; it is still unbundled. _Fix:_ a `ModuleUnits`
  struct holding the per-unit slices, and fold the two entry points into one with
  a defaulted `mode`.

### Drivers / glue
- [x] **SH-055 — the import-resolution suite was duplicated across two
  drivers.** _Done:_ `last_slash` / `dir_of` / `module_name` / `is_local` /
  `join_path` / `should_load` / `resolve_path` lived twice, in `fern.fern` and
  `asm_load_run.fern`, byte-identical for the first five (comments included).
  They had drifted where it counted: only fern's `should_load` took a `deps`
  param and consulted the manifest dependency list, so a manifest-declared
  import resolved under `fern` and silently did not under the asm loader.
  Lifted to `util.fern` — which imports nothing, so neither driver's compile
  closure grows and both already imported it. The shared `should_load` is
  fern's three-argument superset; `asm_load_run` passes an empty `deps` because
  it reads no manifest at all (stdlib root on argv, no `fern_toml` dependency),
  so that is the driver's true dependency set rather than a stub. `join_path`
  became `util.module_path_join`: `mvs.join_path` is a different function
  (normalises `.`/`..`, appends no extension) and `fern.fern` calls both.
  126 duplicated lines deleted.
  _Not folded onto `modloader`_, despite this entry's original phrasing:
  `modloader.dirname` keeps the trailing slash `dir_of` drops,
  `modloader.resolve_module` is a richer manifest/vendor/workspace/lock
  resolver with a different dedupe policy, and importing it into
  `asm_load_run` would drag `modloader` + `fern_toml` (~1085 lines) into a
  driver needing neither — the closure widening `visibility.fern:43-46`
  records having broken six minimal drivers before.
  _Still open:_ `fern`'s `ImportQueue` / `queue_imports` breadth-first worklist
  (which tags each queued import with the directory of the module that wrote
  it, #6756) has no counterpart in `asm_load_run`'s flat `add_imports`. Folding
  those together is a behaviour change to the asm loader, so it is its own
  change.
- [ ] **SH-056 — Retire the redundant `*_run.fern` shims.** There are **56
  `main()`s across 45 `*_run.fern` files**, and no shared `run_stdin` helper
  exists. Two large groups are **keepers, not targets**:
  - **Golden-test drivers pinned by a Go test** — `typeref_run`,
    `ty_from_ref_run`, `tuple_tags_run`, `type_resolve_run`,
    `type_resolve_simple_run`, `wasm_extern_sum_run`, `wasm_option_payload_run`,
    `tuple_elem_tag_run`, `count_type_args_run`, `nth_tuple_elem_run`. Most of
    the growth in this count is these; each one is the pinning half of an SH-021
    slice. **Keep them.**
  - **A `main()` inside a library module** — `parser.fern`, `checker.fern`,
    `lexer.fern`, `printer.fern`, `interp.fern` (and `constfold`, `flatten`,
    `literate`, `pipeline`) — is the tree's **in-file test idiom**, not a driver
    shim. **Do not count these as redundant.**

  What is actually collapsible is the single-mode stdin clone group (`wasm_run`,
  `asm_run`, `interp_run`, `checker_run`, `ssa_run`, …), which `fern.fern`
  already supersedes as the unified driver: give them one shared
  `run_stdin(emit_fn)` and make each a one-line wrapper, keeping only those a Go
  test pins.
- [x] **SH-058 — `wasm_ir.fern` `emit_function_ir`**, the largest function named
  anywhere in this audit. **1254 → 84 lines (−93%)**: four slices each lifting
  one op family to a plain `Op -> string` helper behind a predicate — string ops
  via the shared `ir.is_str_op_kind` (#7280), `dyn_dispatch` (#7284), the host
  family and the map family (#7297) — then the op loop itself moved out to
  `emit_ir_op(o, cx)` behind a `WasmOpCtx` (#7313).

  **The row's proposed shape was wrong in two ways**, both found by measuring
  rather than reading. `WasmOpCtx` carries **6 fields plus `funcs`**, not the
  row's proposed 9: `{ns, str_vals, cagg_vals, structs, fn_table, funcs,
  base: i32}`, where
  `base = r.n_locals` reconstructs all five scratch temps (`arrtmp`=base,
  `eltmp`=+1, `f64tmp`=+2, `i64tmp`=+3, `bctmp`=+4) — none of the five is
  reassigned anywhere in the function, so that collapse is sound. `funcs` stays
  because the loop still forwards it to `emit_dyn_dispatch_wat`, even though no
  arm reads it directly. `fd`, `r` and `vsigs` are gone: every apparent use was
  inside a comment (`fd 0`/`fd 3` is the WASI file descriptor, `r.read_chunk` is
  a Reader method).

  The op-family extractions came first for a reason: 98 of 124 arms (519 lines)
  touch no ambient state at all, so introducing the context struct first would
  have produced a 1,100-line `emit_ir_op` — the same function one indent
  shallower — with a byte-purity diff that is maximally hard to bisect. Each
  family lifted out shrank what the struct had to carry.

  **Three traps, each of which cost something.** Encode them if you automate a
  further slice:

  - **Compute each arm's ambient references; never take a family by name.**
    `temp_dir` reads `ns` while sitting mid-WASI, and `map_new` reads
    `ns` + `fn_table`. A line-span move of "the WASI block" fails to compile.
  - **Verify every hoisted tag is UNIQUE in the ladder.** `map_set` has two
    arms — an `i64_imm != 0` 8-byte-value form checked *before* the plain one —
    so routing tag 125 through one predicate arm emits a single width for both.
    The checker cannot see it and the fixpoint reproduces it happily.
  - **Assert each extracted arm ends with exactly ONE return, and compare
    append counts before and after.** Several map arms append more than once
    (`map_hash_seed` in four pieces); rewriting each append as a `return` makes
    all but the first dead code. That **type-checks clean** — unreachable code
    after a return is legal — and surfaces only as
    `expected i32 but nothing on stack` from `wasm-tools validate`, at a byte
    offset unrelated to the dropped op. It reached CI before it was caught.

---

## 5. Suggested sequencing

1. **Correctness first** — SH-001…SH-010 are all closed; keep that bar.
2. **SH-054** — SH-058 is done (`emit_function_ir` 1254 → 84), and SH-027 is
   done everywhere it pays: `wasm_ir.fern` and the SSA backends' `emit_program`.
   What is left of SH-027 is `printer.fern` (a different shape) and the SSA
   per-instruction chain (deliberately not worth it) — see the row.
3. **T3 visitor** (SH-022) — `parser`'s 12 rewrite passes and `checker`'s 15
   scope-threading passes are what is left; `wasm_ir`'s cluster was never an AST
   walk and is done. Then the giant function splits (SH-044/SH-050) it unlocks.
5. **T2 structured types** (SH-021 endgame — parser stores `TypeRef`).
6. **T5 backend interfaces** (SH-024/SH-025) — largest effort, do last with CI as
   backstop. Resolve the 7-key `has_need` drift before lifting anything.
7. **SH-028** — no longer blocked; schedule alongside SH-026.

Keep the engineering bar from `CLAUDE.md`: every change re-runs the relevant
suite (x86-64 + WASM locally; CI for arm64/qemu), and each fix ships with the
test at the layer it touches. `internal/e2eselfhost` is primary on self-host
lowering changes; the fixpoint is self-referential and blind to a stable
miscompile.

---

## 6. Appendix — full per-file findings

The complete Med/Low findings per file (with line citations) are recorded in the
audit working notes. The high-severity and cross-cutting items are tracked above;
pull the remaining Med/Low items into this section as they're scheduled, so this
file stays the single worklist. Files audited in full: `lexer`, `parser`,
`checker`, `asmcore`, `interp`, `constfold`, `flatten`, `ssa`, `ssa_x86`,
`ssa_arm64`, `ssa_wasm`, `asm_ir`, `asm_arm64_ir`, `irlower`, `x86_native`,
`arm64_native`, `wasm_ir`, `watbin`, `elf`, `printer`, `literate`, `wit_decode`,
`wit_compose`, plus the `*_run` / `pipeline` / `fern` driver group.

---

## Appendix — SH-022 design proposal (generic AST traversal)

### The problem, concretely
There is no shared AST traversal in the passes that have not been converted, so
each hand-enumerates the Expr/Stmt variants: `wasm_ir` 28 collectors,
`checker` 15 scope-threading passes, `parser` 12 rewrite passes. Adding a field
or variant to the AST means touching all of them; a missed arm is a silent bug.

### What converges
- **`collect_idents_expr` converges everywhere.** The apparent difference — the
  wasm collector dedups into the accumulator (`if (!contains_str(acc, …))`) — is
  **redundant**: every consumer dedups *again* when building the capture list, so
  a `refs` list with duplicates yields an identical capture set and first-seen
  order. One non-deduping collector serves all callers with zero behaviour
  change.
- **`collect_idents_stmt` does NOT fully converge.** The wasm collector's
  `StmtAssign` arm collects the assign **target** name, which astwalk's does not.
  For a lambda that *writes* an outer var without reading it (`{ x = 5; }`), wasm
  captures `x` and the others do not. That divergence is the confirmed bug
  SH-057 below, not a merge hazard to route around — the wasm behaviour is the
  correct one.

**Status:** `astwalk.fern` holds the canonical (non-deduping, full-coverage)
`collect_idents_expr` (`:466`) / `collect_idents_stmt` (`:517`) /
`collect_bound_stmt` (`:661`) / `collect_calls_stmt` (`:589`) /
`collect_qualrefs_*` (`:715`, `:721`), plus the `map_expr` (`:304`) /
`map_stmt` (`:379`) rebuilding pair. `asmcore`, `ssa`, and `flatten` are
converted. `wasm_ir`'s 28 walkers are the remaining cluster.

### What the bootstrap language supports
Generics are usable here: `astwalk.fern:19` is
`pub function fold_expr[T](e: parser.Expr, acc: T, visit: (parser.Expr, T) => T): T`.
Closures/lambdas with captures lower on every backend and function-typed
parameters work, so a higher-order traversal taking a per-node callback is
feasible. The functional/immutable style means a **fold** (`acc -> acc`) fits
better than a mutating visitor.

### Staged rollout for the remaining cluster (one PR each)
1. Convert `wasm_ir`'s `collect_*` family onto `fold_expr` / `fold_stmt`, keeping
   the deduping policy explicit in the caller's `f` and pinning the set
   semantics with a test.
2. Convert `wasm_ir`'s `module_uses_*` predicates — these are folds to `boolean`
   and are the easiest half.
3. Convert `checker`'s scope-threading passes onto `fold_stmt_nodes` (the
   accumulator carries the `Scope`).
4. Convert `parser`'s `mono_*` / `ms_*` / `rw_call_*` rewrite passes onto
   `map_expr` / `map_stmt` — larger, do last.

### Risks / test strategy
- Behaviour drift on the diverged collectors — isolate each policy change with
  its own pinning test.
- Bundle cascades (incl. dynamic-marker bundles) — grep both `///MODULE astwalk`
  and `"///MODULE " +` builders.
- Verify locally on x86 (cli/fixpoint/stage2/cross-validation) + wasm via
  wasmtime; arm64/macOS via CI, as throughout SH-020.

---

## SH-057 — self-host miscompiles a lambda that *writes* a captured outer var (confirmed bug)

**Repro:** `function main(): i32 { var x = 1; var f = function (): i32 { x = 42; return 7; }; var r = f(); return r + x; }`

| engine | result | |
|---|---|---|
| Go reference (`fern -interp`) | **49** ✓ correct | `x` mutated to 42 → by-reference scalar capture; `7 + 42` |
| self-host interp | **8** (bug) | captured **by value** → the write doesn't propagate → `7 + 1` |

**Root cause:** the free-variable collector's `collect_idents_stmt` `StmtAssign`
arm (`astwalk.fern:517`) collects only `a.value`, **not** the assign target
`a.target`. So a lambda that assigns to an outer var *without reading it* never
lists that var as a free reference → `lambda_captures` doesn't capture it. The
wasm collector is the only one that collects `a.target`, so the wasm path is the
lone correct backend here.

**Why CI misses it:** the cross-validation suite's closure cases capture vars they
*read*; none assign a captured var write-only.

**Confirmed semantics (from the authoritative Go reference).** The language has a
principled, deliberate split — not an undecided one:
- **Scalar captures (`i32`/`bool`/`f64`) are mutable, by-reference.** A closure may
  read *and* write a captured scalar and the writes persist/propagate — closures-as-
  counters are a supported feature. Verified: `var x = 0; var inc = function (): i32
  { x = x + 1; return x; }; inc(); inc(); return x;` → **2** under the Go reference,
  and `fern -check` accepts it (exit 0). The repro above is **49** (by-ref) in the
  reference.
- **Reference captures (`string`/array/struct) are read-only — E049.** Writing one
  back could close an RC reference cycle (Perceus), so it's a compile error. This is
  why E049 (`checker.go:7067`, `checker.fern`) is intentionally
  *reference-typed only*.

So **E049 must stay reference-only — do _not_ extend it to scalars** (that would
reject the intentionally-supported mutable-scalar-capture feature).

**The actual bug:** the self-host doesn't implement mutable scalar captures
correctly. The reference says the repro is 49 (and the counter is 2); the self-host
interp gives **8** — captured by value, so the write is lost. The self-host stores
captures by value, so this is an unimplemented-semantics gap, not a missing checker
rule.

**Fix (deferred — substantial, multi-PR; scope before starting):** make self-host
scalar captures by-reference/mutable to match the reference. That means: (1) the
collector must capture write-assigned scalars (collect `a.target` in
`astwalk.collect_idents_stmt`), and (2) the capture *codegen* must make scalar
captures writable-and-shared rather than by-value snapshots — across interp's env
and the native/wasm closure boxes — with a cross-engine test that the repro → 49
and the counter → 2 on x86/arm64/wasm. The wasm collector already collects
`a.target`, so its capture analysis is ahead of the others here.
