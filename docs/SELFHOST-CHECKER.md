# Self-host type-checker (Option/Result vs. concrete)

> **Status: superseded.** This doc describes the original narrow
> Option/Result guard. The self-host checker has since grown into a full
> parity port of the Go checker — see `SELFHOST-CHECKER-PORT.md` (the living
> log) and the open residue in
> [#4363](https://github.com/JakeChampion/lang/issues/4363) /
> [#4346](https://github.com/JakeChampion/lang/issues/4346). Kept as the
> historical origin of that arc.

## Why

The self-hosted compiler (`examples/self_host/asm_arm64.fern` and its x86
twin `asm.fern`) is a pure code generator: it parses Fern and emits
assembly with **no type-checking pass**. The production (Go) compiler
already rejects type errors — e.g. `fern -check` on

```fern
function main(): i32 { var xs: i32[] = [1,2,3]; return xs.max(); }
```

emits `error[E002]: return type mismatch: function returns i32 but
expression is Option[i32]`. But the self-host emitter silently lowers the
`Option[i32]` box and returns its **pointer** as the value, so the program
returns garbage instead of failing to compile.

This adds a real (if narrow) checking pass to the self-host compiler so the
same class of bug is caught at compile time rather than producing wrong
code at runtime.

## Scope (deliberately narrow, by necessity)

The self-host type system is a set of coarse string tags (`"i32"`,
`"string"`, `"array_i32"`, `"map:V"`, …), not a real type lattice, and the
AST carries **no source positions**. So this is *not* a port of the Go
checker. It models **Option/Result as first-class tags** and rejects
exactly the unambiguous, high-value error class:

> a wrapped value (`Option`/`Result`) used where a concrete scalar/array
> is expected, or vice versa.

Scalar-vs-scalar mismatches (e.g. `i32` where `string` is wanted) are left
to the Go checker — the coarse tags + lossy inference make those
false-positive-prone, and the Go checker owns them.

### Hard constraint

The pass runs on **every** program the self-host compiles, including
`asm_arm64.fern` compiling **itself** (the fixpoint test) plus
`parser.fern` / `lexer.fern` and every driver/bootstrap snippet. It MUST
produce zero false positives on that corpus or the fixpoint, driver, and
bootstrap tests break. The conservative `assignable` rule below is chosen
for exactly this reason, and the full self-host test suite is the
empirical guard.

## Design

### 1. First-class Option/Result tags

Tags `"option:<payload>"` and `"result:<payload>"`. Payload may be
`"unknown"` when inference can't determine it (that's fine — see
`assignable`).

**`ret_tag_of(name)`** (normalizes a *declared* type string → tag): add
`Option[...]` → `"option:" + ret_tag_of(inner)` and `Result[T, E]` →
`"result:" + ret_tag_of(T)`. Currently `"Option[i32]"` wrongly normalizes
to `"array"` (it ends in `]`), so this must run *before* the trailing-`]`
→ `"array"` fallback.

**`infer_expr_type(e, s)`** (infers an *expression's* tag): add wrapper
results for the constructs that produce them —
- `array_i32 . max()/.min()` (0 args) → `"option:i32"` (currently
  `"unknown"`; this is the motivating case)
- `Some(x)` → `"option:" + infer(x)`; `None` → `"option:unknown"`
- `Ok(x)` → `"result:" + infer(x)`; `Err(_)` → `"result:unknown"`
- map `.get(k)` → `"option:" + value_part(map)` (currently unhandled)
- reader `.read_chunk(n)` → `"option:string"`; `read_file(p)` →
  `"result:string"`

### 2. `assignable(want, got) -> bool` (conservative)

```
want == got                      -> true
want == "unknown" || got=="unknown" -> true
both want and got are wrappers   -> true   (payload inference too coarse)
exactly one is a wrapper:
    let other = the non-wrapper side
    is_concrete(other)           -> false  (REJECT: the real bug class)
    otherwise                    -> true   (coarse tag; don't guess)
neither is a wrapper             -> true   (Go checker owns scalar-vs-scalar)
```

`is_concrete` = one of `i32 | string | bool | f64 | array_i32 |
array_string | array`. Wrappers = tag starts with `option:` or `result:`.

### 3. The walk: `check_module(mod, s) -> string`

Returns accumulated diagnostics (`""` = clean). For each `FuncDecl`:
- `s2 = s.reset_locals()`; bind receiver (if method) and params via
  `bind_local_typed(name, ret_tag_of(type_name))` — mirrors
  `emit_function` so `infer_expr_type` can resolve idents like `xs`.
- Walk the body (recursively through `StmtIf`/`StmtWhile`/`StmtFor`/
  `StmtMatch` sub-bodies), threading `s2`:
  - `StmtReturn{value}`: check `assignable(fn_want, infer(value))`.
  - `StmtVar{name,type_name,init}`: if `type_name != ""`, check
    `assignable(ret_tag_of(type_name), infer(init))`; then bind the local
    (annotated tag if present, else inferred).
  - `StmtAssign{target,value}`: check `assignable(local_type_of(target),
    infer(value))`.
  - `StmtMatch` arms: for `PatVariant(Some|Ok, binding)`, bind `binding`
    to `match_payload_type(scrutinee, s2)` so arm-body returns resolve.

Diagnostics carry a position where the node they are reported at has one:
`dg_at(code, msg, line, col)` renders `line:col: error[E0XX]: …` exactly as the
Go checker does, and `dg` is the positionless fallback for a fact derived from
the decl tables rather than a node (7 sites against 167 positioned ones).
Messages include the function name and both tags, e.g.
`error[E002]: in fn 'main': returns i32 but expression has type Option[i32]`.
Tags are rendered back to friendly names for the message
(`option:i32` → `Option[i32]`).

### 4. Wiring

In `emit_module`, after the seeded state `s` is built and before the
codegen loop:

```
var errs: string = check_module(mod, s);
if (errs.len() > 0) { eprint(errs); exit(1); return ""; }
```

(`return ""` is unreachable but satisfies flow analysis.) `eprint`/`exit`
are existing builtins already used by the corpus, so they lower on both
backends.

## Parity

Implement in `asm_arm64.fern` first, get the full self-host arm64 suite
green, then mirror verbatim into `asm.fern` (x86) and confirm its suite.
The two emitters are kept in lockstep per the project's backend-parity
policy.

## Tests

- **Negative (new):** a Go e2e case asserting the self-host compiler
  *rejects* `function main(): i32 { var xs: i32[] = [1,2,3]; return
  xs.max(); }` — non-zero exit + the `E002` message on stderr. Mirror for
  x86.
- **Regression:** the existing `arr-i32-{min,max}*` cases (which unwrap via
  `match`) must stay green — they exercise the match-arm payload binding.
- **Corpus:** full `TestSelfHost{InterpDriver,CheckerDriver,Fixpoint}Arm64`
  + `TestSelfHostAsmArm64Bootstrap` (and x86 equivalents) must stay green —
  proves zero false positives on the self-host + stdlib corpus.

## Known limitations (documented, not bugs)

- Only the wrapper-vs-concrete class is caught; scalar-vs-scalar and
  arity/field errors remain the Go checker's job.
- Inference is coarse (`struct`/`tuple`/`map` lose their parameters), so
  many expressions tag `"unknown"` and are conservatively accepted.
