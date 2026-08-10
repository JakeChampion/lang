# Self-Host Implementation — Code-Quality Audit

> **Tracked in GitHub:** the open items below are mirrored as a checklist in
> [#2849](https://github.com/JakeChampion/lang/issues/2849); the confirmed
> SH-057 miscompile has its own issue [#2850](https://github.com/JakeChampion/lang/issues/2850).
> This doc stays the detailed reference (file:line, repro, fix sketch).

Audit of the self-hosted Fern compiler under `examples/self_host/` (~62k lines of
Fern across 47 files), compared where useful against the Go reference in
`internal/`. The goal is a worklist we can resolve **one item at a time**: every
finding has a stable ID (`SH-NNN`), a severity, the affected `file:line`, and a
concrete remediation. Check items off as they land.

> Scope note: the self-host tree is a deliberately constrained bootstrap subset
> (every file imports only siblings and `std/io`). That
> constraint is real, but it does **not** explain most of the duplication below
> — receiver methods, struct-update spread, and sibling imports are all already
> used in this tree, so a shared sibling `util.fern` / `value.fern` /
> `modload.fern` is fully within the subset. Items tagged _(needs generics)_ or
> _(needs backend fix)_ are the genuinely blocked ones.

---

## 1. Executive summary

The implementation is **functionally impressive and broadly correct** — it has a
full lexer, parser, monomorphiser, checker, tree-walker, bytecode VM, an SSA
optimiser, four native/text backends, a WASM path, ELF/Mach-O writers, a WIT
codec, and a literate engine, all with extensive in-file test coverage. The
comments are unusually good and frequently record real bug history.

The quality problems are almost entirely **structural / maintainability**, not
correctness — with a small number of genuine latent bugs (§2). The three
dominant themes:

1. **Pervasive copy-paste of utility code** — 156 redundant function definitions
   across 82 names; one 3-line helper (`i32_to_string`) exists in **9 files**,
   a string-membership test in **6 copies under 4 names**. No shared util module
   exists even though the stdlib already provides most of these.
2. **A stringly-typed type system** carried as raw strings and re-parsed with
   magic ASCII byte constants (`91`=`[`, `44`=`,`, `93`=`]`, `46`=`.`) in dozens
   of places, spawning families of near-duplicate `type_from_name*` /
   `ty_from_name` resolvers.
3. **Giant parallel-maintained functions / files** — two ~4,000-line
   `emit_runtime` twins, ~1,000-line `emit_method_call` and ~880-line
   `try_emit_builtin` copies per backend, and no generic AST visitor so ~25–40
   tree-walkers are hand-rewritten across the tree.

### Scorecard (subjective, per area)

| Area | Files | Grade | One-line take |
|---|---|---|---|
| Machine-code encoders | `x86_encode`, `arm64_native` (pt.1), `elf` | **A** | Small, pinned-to-`llvm-mc`, single-responsibility. The model to emulate. |
| Optimiser / IR | `ssa` | B− | Strong algorithms + comments; undermined by string-tagged IR and 2 giant fns. |
| Literate / printer / disasm / constfold | — | B | Clean and tested; only duplication + one backend workaround. |
| Frontend | `lexer`, `parser` | B− | Capable; no AST visitor, sentinel error nodes, types-as-strings. |
| Checker / shared frontend | `checker`, `asmcore` | C+ | Real `Type`/`Diag` model, but 15 duplicated walkers, 6 scope ctors, 6 type resolvers. |
| Tree-walker / VM | `interp`, `vm` | C+ | Ambitious + tested; control flow & errors smuggled through magic strings (§2). |
| Text emitters | `asm`, `asm_arm64` | C | ~8k lines each; 4 large parallel-maintained surfaces beyond the documented one. |
| WASM path | `wasm`, `watbin` | C | 40-field god-struct, 21-arg fn, O(n²) string build, silent wrong-byte fallthroughs. |
| Driver / glue | `*_run`, `fern`, `bundle_*` | C− | 31 `main()`s; import-resolver triplicated verbatim; ~10 near-clone stdin shims. |

---

## 2. Correctness bugs / latent hazards (do these first)

These are not just smells — they can produce wrong output today.

- [x] **SH-001 — VM converts any user string starting with `"__"` into an error.**
  `vm.fern:1788` — `OpPushStr` sniffed the operand and, if it started with `"__"`,
  pushed a `VErr` instead of a `VString`. Any legitimate literal like
  `"__proto__"` / `"__init__"` became a runtime error. Severity **High**.
  _Done (narrow fix):_ the `OpPushStr` handler now matches only the three exact
  compiler sentinels (`__undef:`, `__assign-undef:`, `__exprunknown__`) instead of
  the blanket `__` prefix, so ordinary user literals stay `VString`. The fuller
  `OpError { msg }` opcode remains the ideal end state (see SH-040) but carries a
  3-match-site blast radius (`disasm.fern` enumerates all 60 `Op` variants).

- [x] **SH-002 — Control flow rides magic error strings.** _Done:_
  `StepResult` grew a dedicated `sig: i32` channel (0 = none, 1 = stop —
  a `return`'s value or a runtime error in `ret`, 2 = break, 3 =
  continue) built via `step_none`/`step_stop`; every construction and
  consumer (loop break/continue handling, `eval_block` short-circuit,
  function-body enders, the top-stmts driver) keys off `sig`, and the
  `VErr("__noreturn__"/"__break__"/"__continue__")` sentinels + their
  `is_*` matchers are deleted — no Value can be mistaken for a control
  signal, and a typo'd literal can no longer silently break control
  flow. (The `vm.fern` `-1001`/`-1002` half of this entry is obsolete:
  that VM was retired after the audit was written; the file no longer
  exists.)

- [x] **SH-003 — `watbin` silently drops unknown instructions.** _Done:_ the
  opcode tables' `return 0` sentinel made an unhandled instruction fall
  through BOTH encoder paths (folded `enc_instr` and the flat token loop)
  and emit NOTHING — the module encoded with the operation missing, so it
  failed validation with a baffling type mismatch or ran with wrong values
  (the `sat_trunc_opcode` comment records a real instance). Both
  fallthroughs now `eprint` the op and `exit(1)` when a NAMED op reaches
  them (empty/unnamed nodes keep the lenient skip). Verified benign-token
  clean across the wasm-binary, CLI (`-target wasm -emit core-module`), leb128,
  ret-struct-field, streq-helper, and arm64-builds suites.

- [x] **SH-004 — `parse_f64` does not round-trip to nearest double.** _Done:_
  all three assembler parsers (`watbin.parse_f64`, `x86_gas_parse_f64`,
  `arm64_parse_f64`) replaced with a correctly-rounding decimal parser —
  the classic exact decimal-shift algorithm (digit buffer + movable point,
  grade-school ÷2/×2 to binary-normalize, bit extraction, round-to-nearest
  ties-to-even, with subnormal/±inf/±0 handling). The reference copy +
  commentary live in `watbin.fern` (`pf64_*` / `parse_f64_bits`); the other
  two mirror it verbatim (the assemblers are deliberately import-free).
  Pinned bit-exact against `strconv.ParseFloat` by
  `TestSelfHostParseF64{Watbin,X86Gas,Arm64}` on a corpus of the compiler's
  libm constant spellings, the hard subnormal/overflow boundary literals
  (`2.4703282292062327e-324` et al.), and 17-digit round-trip spellings of
  seeded random doubles. This closes the in-process-vs-GNU-as float-bit
  parity gap (the `.Lfc_*` tables assembled ULPs off in-process before).

- [x] **SH-005 — `x86_gas` silently drops unsupported mnemonics.** _Done:_
  `X86Asm` grew an `unknown: string[]` list; the three silent-skip sites in
  `x86_gas_emit` (unknown single-operand mnemonic, the final two-operand
  fallthrough, and `cmpb`'s non-`$imm` operand form) record instead of
  dropping, and every ELF-writing driver (the capstone + x86_gas test
  mains) fails on a non-empty list before writing the executable —
  mirroring the arm64 path's `p.unknown` check in `fern.fern`. Pinned by
  the x86_gas unit driver (unknown one-operand / two-operand / cmpb-form
  each recorded; clean programs record nothing).

- [x] **SH-006 — `arm64_gas_reg` defaults unknown registers to x0.** _Done:_
  the decode is strict — x0..x30 / w0..w30, d0..s31, sp/lr/xzr/wzr, and
  `-1` for anything else (including digit-suffix garbage like `x1a`,
  previously 1 via the lenient atoi, and out-of-range `x31`/`x99`).
  Because the encoders would fold `-1` into garbage bits, a `-1` alone is
  not centrally catchable (`& 31` masks alias it to xzr/sp), so
  `arm64_gas_program`'s line loop pre-scans every instruction's operands
  for REGISTER-SHAPED tokens (x/w/d/s + digit lead, top-level or inside a
  `[...]` memory operand) that fail the decode and records them on
  `p.unknown` — the same gate that already refuses unknown mnemonics, so
  the driver rejects the output before a corrupt encoding can run. Pinned
  by the gas self-test (strict-decode units + program-level recording +
  clean-program control); the whole-compiler arm64-builds suite proves no
  benign token trips the shape heuristic.

- [x] **SH-007 — `ssa_wasm` `index_of_str` returned 0 (not −1) on miss.** _Done:_
  consolidated all three util-host copies (`ssa`, `ssa_wasm`, `wasm`) onto one
  canonical `util.index_of_str` that returns −1 on miss, so a missing `funcaddr`
  now emits table slot −1 (an out-of-bounds `call_indirect` that traps loudly)
  instead of silently calling slot 0. (`watbin`'s copy stays local — it's a
  deliberately self-contained module; it already returned −1.)

- [x] **SH-008 — `wasm` `StrTable.offset_of` returns scratch base on miss.**
  _Done:_ both the AST backend's `StrTable.offset_of` and the IR path's
  `offset_of_value` (`wasm_ir.fern` — the same bug, independently) now
  `eprint` the missing literal and `exit(1)` instead of silently
  returning 24 (the iovec scratch base). A miss is a compiler bug (the
  literal escaped the collection pre-pass), so it should halt the
  compile, not point the emitted code at scratch memory. (The map-backed
  table remains a possible perf follow-up; correctness no longer
  depends on it.)

- [x] **SH-009 — Dead duplicate `movl` branch.** `x86_gas.fern:703-708` was
  unreachable (the `movl` at `:667` returns first) and additionally used the
  *wrong* register decoder (`x86_gas_reg`, 64-bit) feeding `x86_mov_r32_imm32`.
  _Done:_ deleted; confirmed the live `x86_gas_movl` (`:128`) handles the `$imm`
  form correctly via `x86_gas_reg32`. Severity **Med**.

- [x] **SH-010 — `digits_to_i32` had drifted across copies.** `interp`'s
  `str_to_i32`, `constfold`'s and others were sign-naive while `vm`/`asmcore`
  were sign-aware. _Done:_ consolidated onto one canonical **sign-aware**
  `util.digits_to_i32` (sign-aware is a strict superset on digit-only input, so
  every caller is safe and the latent negative-string bug is fixed). All 6 copies
  (`asmcore` pub, `constfold`, `ssa`, `vm`, `wasm`, `interp`'s `str_to_i32`) plus
  the `asm`/`asm_arm64` cross-module callers now use it.

---

## 3. Cross-cutting themes (highest leverage)

These each touch many files; fixing the root removes dozens of individual
findings. Ranked by leverage.

### T1 — No shared utility module (→ 156 redundant defs)
- [~] **SH-020 — Create `examples/self_host/util.fern`** (sibling, import-friendly)
  and move the copy-pasted leaf helpers into it, then import everywhere.
  _In progress (staged rollout):_ `util.fern` now exists (imports nothing but
  siblings) and is seeded with the canonical `i32_to_string`;
  all 9 `i32_to_string` copies are now retired (`disasm`, `vm`, `constfold`, `printer` (dead), `ssa_x86`/`ssa_arm64`/`ssa_wasm`, `wasm`, `asmcore` — the last also dropped the `pub` cross-module copy used by `asm.fern`/`asm_arm64.fern`).
  The `i32_to_string` strand is **done**. Remaining for SH-020: fold in the OTHER duplicated helpers and the
  rest of the helpers below, one file per PR — each conversion must add
  `util.fern` to every Go test that stages that module (no shared staging list;
  `disasm` had a footprint of 1, most others are 4–8, `wasm`/`asmcore` are 58/74).
  Prefer files NOT imported by `fern.fern` (e.g. `disasm`, `vm`) so the whole
  affected suite is x86-runnable locally; the `fern.fern`-imported files
  (`constfold`/`printer`/`ssa_*`/`wasm`/`asmcore`) drag in arm64/macOS-darwin
  test suites whose staging edits can only be confirmed in CI.
  Helpers still to fold in:
  - `i32_to_string` — **9 copies**: `asmcore:35`, `constfold:49`, `disasm:36`,
    `printer:29`, `ssa(int_str):3633`, `ssa_arm64:17`, `ssa_wasm:35`,
    `ssa_x86:23`, `vm:358`, `wasm:2581`.
  - `digits_to_i32` — **done** (SH-010): one sign-aware `util.digits_to_i32`; all
    5 copies + `interp.str_to_i32` + the `asm`/`asm_arm64` cross-module callers converted.
  - String membership — **done**: one `util.has_str` replaces the 6 copies /
    4 names (`has_str`/`name_in`/`name_in_list`/`contains_name`) across `asmcore`,
    `vm`, `checker`, `parser`, `fern`, and the two `*_load_run` drivers.
  - `base_type_name`/`type_base`/`strip_generic_args` — **done**: one
    `util.base_type_name` (asmcore pub + wasm + checker's two names; asm/asm_arm64
    cross-module callers updated).
  - `is_all_digits` — **done** (`asmcore` pub + `ssa` + `wasm`, all already util).
  - `index_of_str` — **done** (SH-007 fixed). `contains` (substring) — **done**
    (`asmcore` pub + `disasm` + asm/asm_arm64 callers; `watbin`/`literate` keep
    their own copies — deliberately self-contained modules). `index_of_byte`
    `str_join_range`/`str_join_chunks`, `pred_slot`, `block_index`, `last_slash`,
    `join_path`, `module_name`, `resolve_path`, `dir_of`, `is_local`.
  - Named ASCII constants (`DOT=46`, `LBRACKET=91`, `RBRACKET=93`, `COMMA=44`,
    `ZERO=48`, `NINE=57`, `SLASH=47`, `DQUOTE=34`, `BACKSLASH=92`) to kill the
    magic-number comparisons that recur in **every** file.
  > Note: `core/int.int_to_string`, `parse_int_radix`, and the `std/i32` digit
  > predicates already exist in the stdlib. Prefer importing those if the
  > bootstrap subset can take them; otherwise mirror them once in `util.fern`.

### T2 — Stringly-typed type system
- [~] **SH-021 — Carry a structured type AST from the parser** instead of flat
  strings re-parsed downstream. Root cause of: `parser.fern:2812-2905` (type
  re-decode by substring surgery, incl. the unsound "type names never contain
  `__`" assumption at `:2851`), `asmcore:1273-1358` (`ty_from_name`/`split_tuple_ret`,
  keyed on the exact 2-byte `", "` at `:1278`), `checker.fern`'s **6**
  `type_from_name*` resolvers (`:684,1508,1524,1548,1615`) and `wasm.fern:1283`.
  _Fix:_ a small `TypeRef { base, args[], array_depth }` produced once;
  pattern-match instead of byte-scanning. Large but eliminates a whole class of
  fragility findings.
  _Foundation slice landed:_ `parser.fern` now defines
  `TypeRef { base, args[], array_depth, is_tuple }` plus the canonical
  `parse_type_ref` / `render_type_ref` pair (the single place the
  `[]` / `(…)` / `Name[…]` / `", "` grammar is scanned), with a round-trip golden
  (`typeref_run.fern` + `TestSelfHostTypeRef`: `render(parse s) == s` over the
  full grammar corpus + structure spot-checks).
  _Slice 2 landed:_ asmcore `ty_from_name` now decodes via
  `ty_from_ref(parse_type_ref(name))` — a structured pattern-match — retiring its
  hand-rolled byte scan (and the `generic_value_ty` / `generic_key_is_i32` helper
  scans it drove). Byte-identical, locked by a 51-case golden
  (`ty_from_ref_run.fern` + `TestSelfHostTyFromRef`, `ty_tag(ty_from_name s)` over
  every decode branch) plus the bootstrap / per-module fixpoints.
  _Slice 3 landed:_ asmcore `split_tuple_ret` / `tuple_ret_tag_at` now decode a
  tuple spelling via `parse_type_ref` (element idx = `args[idx]`), retiring their
  top-level-comma scans. Byte-identical, locked by `tuple_tags_run.fern` +
  `TestSelfHostTupleTags` (a golden of both decoders over element / OK-type /
  index / out-of-range / non-tuple / 3+-element paths) + the fixpoints.
  _Slice 4 landed:_ the checker's richest resolver,
  `type_from_name_with_structs_unions`, now decodes via `parse_type_ref` +
  pattern-match (new `type_from_ref_su`), retiring its array-suffix / tuple /
  `Map[` first-comma scans (and the now-dead `split_top_comma` / `split_top_commas`
  / `trim_spaces` helpers). Byte-identical, locked by `type_resolve_run.fern` +
  `TestSelfHostTypeResolve` (a golden — via a new `type_debug` renderer — over
  scalar / struct / union / array / tuple / Map / generic / unknown branches,
  reasons included) + the bootstrap.
  _Slice 5 landed:_ the three simpler resolvers (`_with_structs` /
  `_with_struct_names` / `_with_names_and_unions`) now peel their `Elem[]` array
  suffix via `parse_type_ref`'s `array_depth`, retiring the magic-byte `[`(91)/
  `]`(93) scan; byte-identical (before/after diff over a 30-entry × 3-resolver
  corpus), locked by `type_resolve_simple_run.fern` + `TestSelfHostTypeResolveSimple`.
  The scalar-only base `type_from_name` has no byte-scan, so it is left as-is.
  _wasm extern-sum slice landed:_ the wasm backend's flat-sum extern checks
  `extern_sum_param_supported` / `extern_sum_param_is_option` now decode
  `Option[…]` / `Result[…, …]` via `parse_type_ref` instead of the magic-byte
  `Option[` / `Result[` prefix + top-level-comma depth scan. Byte-identical
  (pure boolean fns, so identical output ⇒ unchanged wasm codegen), verified
  old-vs-new over a 25-input corpus and pinned by `wasm_extern_sum_run.fern` +
  `TestSelfHostWasmExternSum`.
  _wasm payload-extractor slice landed:_ `parse_option_payload` /
  `parse_result_err_payload` now pull the Some/Ok payload `T` and the Err type `E`
  out of an `Option[T]` / `Result[T, E]` via `parse_type_ref` (`base` / `args` /
  `array_depth`) instead of the `Option[` / `Result[` prefix + top-level-comma
  depth scan. Byte-identical on every valid input; the migration additionally
  corrects the old scan's garbage-on-array edge case (`Option[i32][]` /
  `Result[…][]` — an array value, for which the prefix + trailing-`]` test wrongly
  fired — now correctly return "" via the `array_depth == 0` guard). The three x86
  self-compile fixpoints confirm no such array type reaches these during
  bootstrap, so the correction leaves the self-compile byte-identical. Pinned by
  `wasm_option_payload_run.fern` + `TestSelfHostWasmOptionPayload`.
  _irlower tuple-element slice landed:_ `tuple_type_elem_tag` (extract element `n`
  of a `(t0, t1, …)` tuple spelling) now decodes via `parse_type_ref` (`is_tuple` /
  `args` / `array_depth`) instead of its own depth-tracking top-level-comma scan;
  byte-identical over a corpus exercising nested-generic / nested-tuple / array
  elements (the inner commas the scan must not split on), single-element tuples,
  out-of-range and negative indices, non-tuples, and a tuple-array `(a, b)[]`.
  Pinned by `tuple_elem_tag_run.fern` + `TestSelfHostTupleElemTag`.
  _checker generic-arity slice landed:_ `count_type_args` (top-level type-arg
  count of a `Name[A, B, …]` annotation, feeding the E019 struct-arity check) now
  decodes via `parse_type_ref` (`args` / `is_tuple` / `array_depth`) instead of a
  first-`[` + trailing-`]` window with a depth-tracking top-level-comma count. On
  every non-array annotation the count matches the former scan exactly (incl.
  depth-correct nesting: `Pair[Map[a, b], c]` → 2); arrays/tuples resolve to -1
  (not a generic head — the former scan returned a garbage count on a trailing
  `[]`, but that value only ever fed E019 on a struct's OWN generic head, never an
  array, so the arity diagnostics are unchanged, confirmed by the fixpoints).
  Pinned by `count_type_args_run.fern` + `TestSelfHostCountTypeArgs`.
  _wasm tuple-element slice landed:_ `nth_tuple_type_elem` (idx-th element of a
  `(A, B, …)` tuple spelling, feeding the extern flat-tuple-param check + tuple
  struct-element recovery) now decodes via `parse_type_ref` (`is_tuple` / `args` /
  `array_depth`) instead of its own bracket/paren depth scan. On every non-array
  spelling the element matches the former scan exactly (incl. nested generic /
  tuple elements); a tuple-array `(i32, i32)[]` resolves to "" (array_depth > 0 is
  a value of array type, not a tuple — the former scan keyed only off a leading
  `(` and mis-read the trailing `[]`, wrongly reporting it as a flat extern tuple
  param). This is a codegen path, so the three x86 fixpoints strictly gate the
  correction. Pinned by `nth_tuple_elem_run.fern` + `TestSelfHostNthTupleElem`.
  _flatten tuple-mangle slice landed:_ `rewrite_type_name`'s tuple branch (mangle
  each element of a `(A, B, …)` cross-module type, preserving nesting) now decodes
  the tuple via `parse_type_ref` (`is_tuple` / `args` / `array_depth`) instead of a
  hand-rolled depth-tracking comma split + per-element space trim; each element is
  rendered back to its canonical spelling and recursed through `rewrite_type_name`.
  This is the mangle path — byte-identity-critical — so the three x86 fixpoints
  (which self-compile real cross-module tuples like std/test's `(string,
  TestRunner)`) strictly gate it. Covered by new tuple assertions in flatten.fern's
  own `main()` self-test (own-decl + imported-qualified + nested-generic + nested-
  tuple elements, and the tuple-array fallthrough) under `TestSelfHostFlattenX86_64`.
  _Remaining:_ every genuine canonical-type-spelling comma-depth decoder in the
  self-host compiler is now migrated onto `parse_type_ref` (asmcore, the checker's
  resolvers + `count_type_args`, wasm's extern-sum / payload / tuple decoders,
  irlower's `tuple_type_elem_tag`, flatten's tuple mangle). What's left is either
  lower-value or delicate: the unambiguous `[]`-suffix element-strips (`ft[0:len-2]`
  / `ty_spelling_is_array`; a trailing `[]` is a structurally unambiguous array
  marker, so these carry no nested-comma mis-read risk and are not worth routing
  through a heavier parse); the internal `,`-joined tag encodings (irlower's
  `csv_nth` / `LowerState.tuple_elem_tag`, which decode a spaceless CSV of tags,
  not a canonical type spelling); and the parser's `bind_unify` monomorphisation
  unifier, whose final case matches a generic pattern against a `__`-mangled clone
  name (not a type spelling `parse_type_ref` can decode), so it stays string-based.
  The endgame remains having the parser store `TypeRef` directly so the string
  becomes render output. Unblocks #4394 lever 1 (symbol interning ripples into this
  type system).

### T3 — No generic AST visitor / fold (→ ~40 hand-written walkers)
- [~] **SH-022 — Add `walk_expr`/`walk_stmt` (or a fold) once.** _In progress
  (`astwalk.fern` started; see appendix for the corrected analysis):_ the
  free-variable collectors (`collect_idents_expr`/`_stmt`/`collect_bound_stmt`) are
  byte-identical across `asmcore`/`ssa`/`vm` and now live once in `astwalk.fern`;
  all three are converted (asmcore carried the `///MODULE astwalk` bundle cascade +
  a 73-list staging sweep). The `expr` collector
  also converges with `wasm`'s (wasm's accumulator-dedup is **redundant** — every
  consumer dedups again). wasm's **stmt** collector genuinely diverges, but the
  real reason is its `StmtAssign` arm collecting the assign **target** name
  (`a.target`) which astwalk/asmcore/ssa/vm do NOT — see appendix; wasm is deferred. Every analysis
  re-enumerates all Expr/Stmt variants by hand: `parser.fern` ~10 walkers
  (`expr_mentions:1574`, `mono_*`, `ms_*`, `rw_call_*`, …); `checker.fern` ~15
  scope-threading passes (`ret_diags`, `lret_*`, `mx_*`, `slit_diags`,
  `call_diags`, …) that also rebuild `build_func_scope` 8× per function
  (`:4527-4660`); `wasm.fern` ~25 `collect_*`/`module_uses_*` (`:299-531`);
  `asmcore` `collect_idents_*`; `ssa`/`ssa_wasm` re-open-code the 3-level
  funcs→blocks→insts loop. _Fix:_ one traversal taking a per-node callback;
  removes well over 1,000 lines and the "added a field, forgot a walker" hazard.

### T4 — Struct-copy boilerplate (use the spread the parser already supports)
- [ ] **SH-023 — Replace full struct-literal rebuilds with `{ ...x, field: y }`.**
  The 11-field `FuncDecl{…}` literal is spelled out in ~15 places in `parser.fern`
  just to change one field (`:459,2318,2357,…`); the spread form is *already used*
  at `parser.fern:4557,5088`. Same for `Module{…}` (~10×), the 5 `new_scope*`
  ctors in `checker.fern:309-347`, and the `EmitState`/`Scope` updates. Apply the
  spread consistently; removes the "added a field, forgot a copy site" bug class.

### T5 — Backend duplication beyond what asmcore/CLAUDE.md claims
- [ ] **SH-024 — Introduce an `Emitter` interface to dedupe `asm.fern` ↔
  `asm_arm64.fern`.** The CLAUDE.md claim that "only `emit_*` instruction-selection
  is parallel" is materially understated. Four large target-**independent**
  surfaces are still hand-maintained twice: `emit_stmt` (`asm:3321` ≈
  `asm_arm64:3069`), `emit_function` (`asm:3762` ≈ `asm_arm64:3450`),
  `try_emit_builtin` (`asm:168`, 61 branches ≈ `asm_arm64:184`, 71 branches — the
  10-branch gap is itself drift), and the two ~4,000-line `emit_runtime` twins
  (`asm:3867` / `asm_arm64:3553`) gated on the **same 33 `has_need` keys**. _Fix:_
  a thin target interface (`push`/`pop`/`load_local`/`branch_if_zero`/`call`/
  `syscall`) driven from shared code; each backend implements only the leaves.
- [ ] **SH-025 — Create `ssabackend.fern`** (the SSA analogue of `asmcore`). The 3
  SSA backends share ~180–220 LOC of **byte-identical** helpers (`i32_to_string`,
  `str_join_*`, `block_index`, `pred_slot` — all 3) plus the native-pair
  const/label/reg helpers, and 3 hand-mirrored `emit_inst` dispatch ladders.
  Align the gratuitously-divergent `emit_term` signatures (`ssa_x86:431` takes
  `name`, `ssa_arm64:429` reads `f.name`) first, then lift a parameterised
  `emit_func`/`emit_program` driver.

### T6 — Errors & signals smuggled through value types / sentinels
- [ ] **SH-026 — Stop overloading value types for errors/signals.** Beyond SH-001/
  SH-002: `VErr` is a catch-all sentinel (`"__noreceiver__"`, `"__noclosure__"`,
  `"__uninit__"`, `"__pending__"`); compile errors ride `VString`
  (`vm.fern:696,993,1080`); `lookup_*` return `name:""` to mean "not found"
  (`checker.fern:3756` etc., forcing `.name.len()>0` checks everywhere); ~30
  bespoke `*Result` structs each re-implement `(value, ok)` / `(node, next_pos)`.
  _Fix:_ a kinded error type; `Option[T]`/`Result[T,E]` once generics land (T8);
  meanwhile give `VErr` a `kind` field and namespace internal sentinels.

### T7 — O(n²) string accumulation in emitters
- [ ] **SH-027 — Use a `strbuf`/chunk-join accumulator in the string emitters.**
  `out = out + line` left-folds: `wasm.fern:7180-7351`, `ssa_x86:581-718` &
  `ssa_arm64:577-711` & `ssa_wasm:580-642` (each embeds its ~140-line runtime via
  the very left-fold its own comments warn against), `asm_arm64.fern:8211`
  (`darwinize`, self-admitted O(n²)), `wasm.fern` lambda-defs (`:3166`). _Fix:_
  collect pieces in a `string[]` and `str_join_chunks` once; move inline runtime
  asm into data constants (`runtime_x86(): string`).

### T8 — Missing generics force hand-rolled options/containers
- [ ] **SH-028 — _(needs generics)_** Hand-rolled `OptInt`/`OptBool`/`OptString`
  (`constfold:84-115`), the ~30 `*Result` structs, the four `append_*` concat
  helpers (`flatten:462-536`), and the placeholder `tag: i32` fields on ~25
  nullary variants (`asmcore:721-759`, `checker:45`, `parser:137`, `vm`
  opcodes). Track as blocked on parser generics + nullary struct variants; until
  then centralise each family in one place rather than per-file.

---

## 4. Per-file high-severity backlog

Items not already folded into a theme above. (Med/Low findings live in the
appendix §6.)

### Frontend
- [x] **SH-040 — `parser.fern:1000-1070`** re-implemented struct-literal body
  parsing that `parse_struct_lit_body` (`:621`) already provides. _Done:_ the
  `parse_primary` bare-ident path now delegates to the shared helper (mirroring
  the qualified `pkg.Type {…}` path at `:840` and the generic `Name[T] {…}` path
  at `:750`), removing ~55 duplicated lines and the drift risk between the two
  copies of the spread / field / trailing-comma loop.
- [ ] **SH-041 — `parser.fern:1136-1169`** parse errors collapse into untyped
  `ExprUnknown{kind:string}`/`StmtUnknown` with no position — accumulate real
  diagnostics; at least carry source position.
- [ ] **SH-042 — `parser.fern:2079-2243`** `parse_type_name` is a ~165-line giant
  whose recovery exists "to avoid OOM spin" — split into `parse_type_atom` +
  `parse_type_suffixes` and return structured types (feeds T2).
- [x] **SH-043 — `lexer.fern:397-485`** the C-escape decoder (`\n\t\r\0\"\\\xNN`)
  was copy-pasted between `scan_string` and `scan_fstring`. _Done:_ extracted
  `apply_escape(l, esc) -> EscResult` (decoded fragment + `hexerr`/`unknown`
  flags) so both scanners share one decode ladder while keeping their own
  literal-kind error messages. The escape grammar now lives in one place (no
  drift risk; a future `\u` escape is a one-site change).

### Checker / asmcore
- [ ] **SH-044 — `asmcore.fern:1374-1686`** `infer_expr_type` is a ~310-line monster
  with hundreds of hardcoded builtin-name string compares — table-drive
  `builtin_return_type(name,args)` + per-receiver method resolvers.
- [ ] **SH-045 — `checker.fern:4527-4660`** `check_module` rebuilds `build_func_scope`
  8× per function and runs the whole pass list twice (funcs + top_stmts) — build
  the scope once; extract `run_body_passes(stmts, scope)`.
- [~] **SH-046 — `checker.fern:454-486` + `:400-413`** builtin function/enum-variant
  membership as giant hand-kept `||` chains in 3 places — single source-of-truth
  table. _Partial:_ `is_builtin_variant` (`:400`) now derives from the single
  variant→enum table in `mx_builtin_enum_of`, collapsing the duplicated 17-name
  variant list. Still open: `is_builtin_function` (`:454`, the ~30-name builtin
  list) and folding `is_reserved_enum_name`'s 4 enum names into the same table.

### Interp / VM / SSA
- [ ] **SH-047 — `interp.fern:125-176` & `vm.fern:1681-1990`** environment/stack are
  immutable parallel arrays rebuilt per op (O(n²)); block scoping via length-trim
  is off-by-one-fragile — extract scope-frame `Env`/`LocalsTable` + `stack_drop`/
  `stack_take_top` helpers (used ~6× by hand today).
- [x] **SH-048 — `interp.fern:237-680` / `vm.fern:660-998`** `eval_expr` (~440 lines)
  & `compile_expr` (~340) — extract `eval_binary`/`eval_unary` (mirror the VM's
  `apply_binary`) and `compile_args` (4 near-identical arg loops). _Done:_
  `eval_binary` and now `eval_unary` extracted from interp's `eval_expr`; the
  VM's six near-identical arg-emit loops folded into one `compile_args`.
- [ ] **SH-049 — `ssa.fern:69` SInst/STerm are string-tagged flat records** with an
  `imm` field overloaded as value / param-index / **width 32-64** / alloc-count /
  call-return-kind (`:2983`) — make it a tagged union (checker-enforced exhaustive
  `emit_inst`) with named `width`/`ret_kind` fields.
- [ ] **SH-050 — `ssa.fern:428-1310`** `build_expr` (~730 lines) and
  `regalloc_linear` (`:3244`, ~155 lines over ~10 parallel i32 arrays) — extract
  per-builtin lowerings and interval-construction/liveness/scan helpers.
- [ ] **SH-051 — `ssa.fern:1606`** `env_put` mutates in place and is correct only on
  unaliased scratch (vs near-identical `env_set_at:1588`) — make the aliasing
  contract type-level (`ScratchVec`) or rename `env_put_unsafe_owned`.

### Text emitters / WASM
- [ ] **SH-052 — `asm_arm64.fern:8207-8288`** `darwinize` is a line-oriented string
  rewrite of emitted asm (peephole-on-text) — drive the dialect from the `darwin`
  flag at emit time instead (it already does for syscalls).
- [ ] **SH-053 — `wasm.fern:81-164`** `Ctx` is a ~40-parallel-array god-struct hand-
  modelling a symbol table — replace with `Scope` keyed name→`VarInfo`.
- [ ] **SH-054 — `wasm.fern:6038`** `emit_func` takes **21 positional params** (call
  sites pass trailing `[],[],[],[]`) — bundle into `ModuleInfo`/capture structs.

### Drivers / glue
- [ ] **SH-055 — Extract `modload.fern`.** The ~120-line import-resolution suite
  (`last_slash`, `dir_of`, `module_name`, `is_local`, `join_path`, `should_load`,
  `resolve_path`, `add_imports`, `contains_name`) is triplicated **verbatim** in
  `asm_load_run.fern`, `asm_arm64_load_run.fern`, and `fern.fern:89-165` (the
  arm64 copy's own header admits it's identical).
- [ ] **SH-056 — Retire redundant `*_run.fern` shims.** There are **31 `main()`s**;
  `fern.fern` is the intended unified driver yet ~10 single-mode stdin clones
  (`wasm_run`, `asm_run`, `interp_run`, `checker_run`, `vm_run`,
  …) remain — collapse to one-line wrappers over a shared `run_stdin(emit_fn)`,
  keeping only those a Go test pins (document which).

---

## 5. Suggested sequencing

1. **Correctness first** — SH-001…SH-010 (small, isolated, some are real bugs).
2. **T1 `util.fern`** (SH-020) — mechanical, removes the largest dup surface, low risk.
3. **T4 spread / SH-023** and **SH-055/SH-056 glue** — mechanical, high line-count payoff.
4. **T3 visitor** (SH-022) then the giant-function splits (SH-044/45/48/50) which it unlocks.
5. **T2 structured types** (SH-021) — the big one; do after the visitor lands.
6. **T5 backend interfaces** (SH-024/025) — largest effort, do last with CI as backstop.
7. **T8 generics-blocked** items — revisit when the parser gains generics.

Keep the engineering bar from `CLAUDE.md`: every change re-runs the relevant
suite (x86-64 + WASM locally; CI for arm64/qemu), and each fix ships with the
test at the layer it touches.

---

## 6. Appendix — full per-file findings

The complete Med/Low findings per file (with line citations) are recorded in the
audit working notes. The high-severity and cross-cutting items are tracked above;
pull the remaining Med/Low items into this section as they're scheduled, so this
file stays the single worklist. Files audited in full: `lexer`, `parser`,
`checker`, `asmcore`, `interp`, `vm`, `constfold`, `flatten`, `ssa`, `ssa_x86`,
`ssa_arm64`, `ssa_wasm`, `asm`, `asm_arm64`, `x86_encode`, `arm64_native`,
`wasm`, `watbin`, `elf`, `x86_gas`, `disasm`, `printer`, `literate`,
`wit_decode`, `wit_compose`, plus the `*_run` / `pipeline` / `bundle_*` /
`fern` driver group.

---

## Appendix — SH-022 design proposal (generic AST traversal)

_Investigated during the SH-020 work; this is the plan to implement, not yet done._

### The problem, concretely
There is no shared AST traversal, so every pass hand-enumerates the Expr/Stmt
variants. Current hand-written Expr-variant arm counts: **wasm 180, checker 89,
asmcore 30, ssa 28** (plus the parser's own ~10 rewrite passes). Adding a field or
variant to the AST means touching all of them; a missed arm is a silent bug.

### What actually converges (corrected after closer analysis)
An earlier draft of this appendix claimed the walkers had "semantically diverged"
and couldn't be merged. That was over-cautious — the real picture:

- **`collect_idents_expr` converges across all four** (`asmcore`/`ssa`/`vm` are
  byte-identical modulo whitespace; `wasm` matches too). The apparent difference —
  `wasm` dedups into the accumulator (`if (!contains_str(acc, …))`) — is
  **redundant**: every consumer (`lambda_captures` and the ssa/vm equivalents)
  dedups *again* when building the capture list (`!has_str(caps, nm)`), so a `refs`
  list with duplicates yields an identical capture set + first-seen order. wasm's
  `_ => {}` wildcard over the literal leaves is equivalent to the others' explicit
  no-op arms. So one **non-deduping** collector serves all four with zero behaviour
  change.
- **`collect_idents_stmt` converges across `asmcore`/`ssa`/`vm`** (identical, all 13
  Stmt variants) but **wasm genuinely diverges**. The `StmtSwitch`/`StmtDefer`
  difference I first cited turns out to be a no-op (`module_with_builtins` runs
  `desugar_switches_module` + `lower_defers_module` before `wasm.emit_module`, so
  those nodes never reach any collector). The **real** divergence: wasm's
  `StmtAssign` arm collects the assign **target** name (`if (!contains_str(acc,
  a.target)) acc.append(a.target)`), which astwalk/asmcore/ssa/vm do **not**. So for
  a lambda that *writes* an outer var without reading it (`{ x = 5; }`), wasm
  captures `x` and the others wouldn't. Converging wasm is therefore **not** a safe
  no-op — wasm stays separate.

  **Open question (worth its own investigation):** this discrepancy is either a
  **latent capture bug in `asmcore`/`ssa`/`vm`** (they miss capturing a
  write-only-assigned outer var) or **redundant work in `wasm`** (if such captures
  aren't needed — e.g. captures are read-only). Resolve before any wasm merge.

**Status:** `astwalk.fern` holds the canonical (non-deduping, full-coverage)
`collect_idents_expr`/`_stmt`/`collect_bound_stmt`; **`asmcore`, `ssa`, and `vm` are
all converted** (asmcore's bundle cascade + 73-list sweep done). The only remaining
backend is **`wasm`**, whose *stmt* collector genuinely diverges (skips
`StmtSwitch`/`StmtDefer`) — a deliberate bugfix-vs-regression call, not a sweep.

### What the bootstrap language supports
Closures/lambdas with captures lower on every backend (`OpMakeClosure` etc.), and
function-typed parameters work — so a higher-order traversal taking a per-node
callback is feasible. The functional/immutable style means a **fold**
(`acc -> acc`) fits better than a mutating visitor.

### Proposed shape
Add `astwalk.fern` (imports `parser` only) with two primitives:

```
// Post-order fold: visit every sub-expression, threading an accumulator.
// `f` is called once per Expr node (leaves and composites) with the node
// and the current acc; astwalk handles all the structural recursion.
pub function fold_expr[A](e: parser.Expr, acc: A, f: fn(parser.Expr, A) -> A): A
pub function fold_stmt[A](s: parser.Stmt, acc: A, f: ...): A   // recurses into exprs + nested stmts
```

If parser generics (`[A]`) aren't usable here yet (verify first — see SH-021/
generics status), fall back to a monomorphic `string[]`-accumulator fold
(`fold_expr_strs`) which already covers the biggest cluster (the `collect_*`
ident/var-name walkers). The diverged dedup policy becomes the caller's `f`
(append vs. append-if-absent), so wasm keeps its set semantics explicitly.

### Staged rollout (one PR each, lowest-risk first)
1. `astwalk.fern` + `fold_expr_strs` + convert the **collect-ident family** in
   `ssa`/`vm` (identical copies; not in `///MODULE` bundles → low blast radius).
   Leave `wasm`'s deduping version until step 3 so its policy change is isolated
   and reviewable.
2. Convert `asmcore`'s `collect_idents_*` — note `asmcore` is hand-bundled by the
   asm-path self-hosting tests, so add `///MODULE astwalk` to those bundles
   (same cascade handled for `util` in SH-020; **also check dynamic-marker
   bundles** — `interp_driver`-style `"///MODULE " + name` — which the literal
   sweeps miss).
3. Convert `wasm`'s deduping collector, passing the append-if-absent `f`; pin the
   set-semantics with a test so the behaviour is explicit, not incidental.
4. Generalise the rewrite passes (constfold/flatten/monomorph) onto a
   `map_expr`/`map_stmt` (rebuilding fold) — larger, do last.

### Risks / test strategy
- Behaviour drift on the diverged collectors — mitigate by converting identical
  copies first and isolating each policy change (step 3) with its own pinning test.
- Bundle cascades (incl. dynamic-marker bundles) — grep both `///MODULE astwalk`
  and `"///MODULE " +` builders.
- Verify locally on x86 (cli/fixpoint/stage2/cross-validation) + wasm via
  wasmtime; arm64/macOS via CI, as throughout SH-020.

---

## SH-057 — self-host miscompiles a lambda that *writes* a captured outer var (confirmed bug)

_Surfaced while resolving the SH-022 wasm-collector question; pre-existing (not
caused by SH-022 — astwalk's collectors are byte-identical to the old copies)._

**Repro:** `function main(): i32 { var x = 1; var f = function (): i32 { x = 42; return 7; }; var r = f(); return r + x; }`

| engine | result | |
|---|---|---|
| Go reference (`fern -interp`) | **49** ✓ correct | `x` mutated to 42 → by-reference scalar capture; `7 + 42` |
| self-host interp | **8** (bug) | captured **by value** → the write doesn't propagate → `7 + 1` |
| self-host vm | **254**/VErr (bug) | write-only scalar never captured → `x = 42` → assign-to-undefined sentinel |

**Root cause:** the free-variable collector's `collect_idents_stmt` `StmtAssign` arm
(now in `astwalk.fern`, inherited identically from asmcore/ssa/vm) collects only
`a.value`, **not** the assign target `a.target`. So a lambda that assigns to an
outer var *without reading it* never lists that var as a free reference →
`lambda_captures` doesn't capture it. `wasm`'s collector is the only one that
collects `a.target`, so wasm is the lone correct backend here. (This is why SH-022
left wasm un-converged.)

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
  why E049 (`checker.go:7067`, `checker.fern:4475`) is intentionally
  *reference-typed only*.

So **E049 must stay reference-only — do _not_ extend it to scalars** (that would
reject the intentionally-supported mutable-scalar-capture feature).

**The actual bug:** the self-host doesn't implement mutable scalar captures
correctly. The reference says the repro is 49 (and the counter is 2); the self-host
gives interp **8** (captured by value → the write is lost) and vm **254** (the
write-only scalar is never captured → assign-to-undefined). CLAUDE.md notes the
self-host stores captures "by value" — so this is an unimplemented-semantics gap,
not a missing checker rule.

**Fix (deferred — substantial, multi-PR; scope before starting):** make self-host
scalar captures by-reference/mutable to match the reference. That means: (1) the
collector must capture write-assigned scalars (collect `a.target` in
`astwalk.collect_idents_stmt`), and (2) the capture *codegen* must make scalar
captures writable-and-shared rather than by-value snapshots — across interp's env,
vm's `OpMakeClosure`/capture slots, and the asm/wasm closure boxes — with a
cross-engine test that the repro → 49 and the counter → 2 on x86/arm64/wasm.
`wasm`'s collector already collects `a.target`, so its capture analysis is ahead of
the others here.
