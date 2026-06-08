# Self-Host Implementation — Code-Quality Audit

Audit of the self-hosted Fern compiler under `examples/self_host/` (~62k lines of
Fern across 47 files), compared where useful against the Go reference in
`internal/`. The goal is a worklist we can resolve **one item at a time**: every
finding has a stable ID (`SH-NNN`), a severity, the affected `file:line`, and a
concrete remediation. Check items off as they land.

> Scope note: the self-host tree is a deliberately constrained bootstrap subset
> (every file imports only `core/no_prelude`, siblings, and `std/io`). That
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

- [ ] **SH-002 — Control flow rides magic error strings.** `interp.fern:996-1009,
  1264-1286` encodes return/break/continue as `VErr("__noreturn__" /
  "__break__" / "__continue__")`; `vm.fern:1033-1036` uses jump targets
  `-1001`/`-1002`. A user `VErr` whose message collides, or a typo in a literal,
  silently breaks control flow. Severity **High**. _Fix:_ a dedicated
  `StepSignal` union (`SigReturn(Value)|SigBreak|SigContinue|SigError`) distinct
  from `Value`; distinct `OpBreak`/`OpContinue` ops in the VM.

- [ ] **SH-003 — `watbin` encodes a trap on unknown opcode.** `watbin.fern:556-645,
  683-696` — `arith_opcode`/`mem_load_opcode`/`mem_store_opcode` `return 0` on no
  match; `0x00` is `unreachable`, so an unhandled instruction silently emits a
  broken module. Severity **High**. _Fix:_ return `-1` / a `(found, byte)` result
  and raise an explicit "unknown opcode" error.

- [ ] **SH-004 — `parse_f64` does not round-trip to nearest double.**
  `watbin.fern:381-413` accumulates `v*10+digit` then scales by `0.1`-fractions,
  feeding exact IEEE-754 bit emission (`f64_bits`). Any literal not exactly
  representable that way emits wrong bits. Severity **High**. _Fix:_ a
  correctly-rounding decimal parser, or thread the front-end's already-parsed
  bits through.

- [ ] **SH-005 — `x86_gas` silently drops unsupported mnemonics.**
  `x86_gas.fern:619, 715` `return a; // skip` produces a corrupt executable with
  no diagnostic. Severity **High**. _Fix:_ record on an `X86Asm.unknown` list and
  fail the driver (the arm64 path already does this — `fern.fern:69` checks
  `p.unknown`).

- [ ] **SH-006 — `arm64_gas_reg` defaults unknown registers to x0.**
  `arm64_native.fern:1219-1231` returns `0` (=x0) for any unrecognised register
  token → wrong-register miscompile. Companion: `arm64_gas_atoi:1174-1205`
  silently skips non-digit bytes. Severity **High**. _Fix:_ sentinel `-1` +
  record on `p.unknown`; assert `p.unknown` empty at end of assembly.

- [ ] **SH-007 — `ssa_wasm` `index_of_str` returns 0 (not −1) on miss.**
  `ssa_wasm.fern:79-86` — a `funcaddr` not in the table silently emits slot 0 →
  calls the wrong function. Severity **High** (latent). _Fix:_ return −1 and
  assert/bail, or rename to `table_slot_of` and add a real guard.

- [ ] **SH-008 — `wasm` `StrTable.offset_of` returns scratch base on miss.**
  `wasm.fern:50-71` returns `24` for an un-interned string → silent wrong offset.
  Severity **Med**. _Fix:_ hard error on missing string; back the table with a map.

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
  _In progress (staged rollout):_ `util.fern` now exists (imports only
  `core/no_prelude`) and is seeded with the canonical `i32_to_string`;
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
  - String membership — **6 copies / 4 names**: `has_str` (`asmcore:86`,
    `vm:651`), `name_in` (`checker:2270`), `name_in_list` (`parser:4389`),
    `contains_name` (`fern:128`, `asm_load_run`, `asm_arm64_load_run`).
  - `base_type_name`/`type_base`/`strip_generic_args` — **4 copies / 3 names**
    (`asmcore:14`, `checker:2660`, `checker:3990`, `wasm:2166`).
  - `is_all_digits` — **done** (`asmcore` pub + `ssa` + `wasm`, all already util).
  - `index_of_str`/`index_of_byte`, `contains` (substring),
    `str_join_range`/`str_join_chunks`, `pred_slot`, `block_index`, `last_slash`,
    `join_path`, `module_name`, `resolve_path`, `dir_of`, `is_local`.
  - Named ASCII constants (`DOT=46`, `LBRACKET=91`, `RBRACKET=93`, `COMMA=44`,
    `ZERO=48`, `NINE=57`, `SLASH=47`, `DQUOTE=34`, `BACKSLASH=92`) to kill the
    magic-number comparisons that recur in **every** file.
  > Note: `core/int.int_to_string`, `parse_int_radix`, and the `std/i32` digit
  > predicates already exist in the stdlib. Prefer importing those if the
  > bootstrap subset can take them; otherwise mirror them once in `util.fern`.

### T2 — Stringly-typed type system
- [ ] **SH-021 — Carry a structured type AST from the parser** instead of flat
  strings re-parsed downstream. Root cause of: `parser.fern:2812-2905` (type
  re-decode by substring surgery, incl. the unsound "type names never contain
  `__`" assumption at `:2851`), `asmcore:1273-1358` (`ty_from_name`/`split_tuple_ret`,
  keyed on the exact 2-byte `", "` at `:1278`), `checker.fern`'s **6**
  `type_from_name*` resolvers (`:684,1508,1524,1548,1615`) and `wasm.fern:1283`.
  _Fix:_ a small `TypeRef { base, args[], array_depth }` produced once;
  pattern-match instead of byte-scanning. Large but eliminates a whole class of
  fragility findings.

### T3 — No generic AST visitor / fold (→ ~40 hand-written walkers)
- [ ] **SH-022 — Add `walk_expr`/`walk_stmt` (or a fold) once.** Every analysis
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
- [~] **SH-048 — `interp.fern:237-680` / `vm.fern:660-998`** `eval_expr` (~440 lines)
  & `compile_expr` (~340) — extract `eval_binary`/`eval_unary` (mirror the VM's
  `apply_binary`) and `compile_args` (4 near-identical arg loops). _Partial:_
  the ~108-line `ExprBinary` body is now `eval_binary(op, lv, rv)` (pure in its
  operands), shrinking `eval_expr`'s arm to a 4-line head + delegation. Still
  open: `eval_unary`, and the VM-side `compile_args` factoring.
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
  (`wasm_run`, `asm_run`, `asm_arm64_run`, `interp_run`, `checker_run`, `vm_run`,
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
