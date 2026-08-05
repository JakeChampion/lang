# SSA closure dispatch — design

Tracking: #4112 (SSA-level register allocation, child of the binary-size
epic #4109). This is the design for closure / function-value dispatch on
the SSA native path, the last whole-program gap after `Option`/`match`
(closures currently fail `EmitProgram` with `unknown opcode 19` —
`CallIndirect` in real asm).

## The problem

Today the SSA layer represents a function value **two different ways**,
and `OpCallIndirect` can't tell them apart:

- `ir.OpConstFunc` (a bare top-level function used as a value) lifts to
  `OpConstInt(index)` — a **raw function-table index**.
- `ir.OpMakeClosure` lifts to `OpMakeClosure` — a **heap `{fn, env}`
  cell pointer** (the representation this track already emits: `fn` at
  `+0`, `env_ptr` at `+8`).

The earlier `OpCallIndirect` model (added when only `OpConstFunc` was in
scope) treats `Args[0]` as a raw index. But real closure programs pass a
**cell pointer** as `Args[0]`:

```
function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function main(): i32 { return apply(function(y) { y*2+1 }, 20); }
```

lifts to (abridged):

```
func apply(f, x):        v3 = call_indirect f, x ; ret v3
func main():             v1 = make_closure       ; v3 = call "apply", v1, 20 ; ret v3
func __closure_lambda_1(y, env):  ret (y<<1)+1     // env is the LAST param
```

`apply`'s `call_indirect f, x` receives `f` = a `{fn, env}` cell pointer,
not an index — so raw-index dispatch is wrong. The two representations
must be **unified**.

## Chosen representation — mirror the native backend

`internal/codegen/x86_64` already solved this: **every function value is
a `{fn, env}` pair**, and `OpCallIndirect` uniformly dereferences it.

> Every function value on natives is now a `{fn_ptr, env_ptr}` pair —
> `OpConstFunc` emits static `.rodata` cells with `env=0`; `OpMakeClosure`
> allocates heap pairs whose env points at the captured slot block. Either
> way, the indirect call loads `fn_ptr` from `[pair+0]`, `env_ptr` from
> `[pair+8]`, pushes env as the `(argc+1)`-th argument, and `call`s
> `fn_ptr`. Top-level fns receive `env=0` in an extra register they don't
> read (System V's "unused args may hold any value").

Adopt the same shape at the SSA level:

- **Cell layout** — `{ fn@+0, env_ptr@+8, drop@+16, env_ptr@+24 }`, 32
  bytes. `fn` is a *backend-resolved function reference*: a table index
  for the model / wasm, a code address for native real-asm.
- **`env` is appended as the last argument** (matching the lifted lambda
  `lambda(user_args…, env)` and the native `(argc+1)`-th slot).

The second half is a **callable sub-pair**: `drop` is the target's
`__closure_drop_<name>` thunk, and `env_ptr` is duplicated so
`{drop@+16, env_ptr@+24}` is itself a well-formed cell. That is what lets
a *generic holder* free an element's captures without knowing which
closure it holds — the IR's `__drop_arr_closure` walks an array of
function values and dispatches each element at `element + 2*ptrW`
(`genArrClosureDropFn` in `internal/ir/rc_insert.go`). The SSA cell was
2 slots wide for its first two years, so that walk read past the cell
into the next heap block and called the **lambda** as the drop routine,
with the env in the wrong register: #6144, a SIGSEGV the moment the
lambda touched a capture.

`drop` is `0` when the module has no `__closure_drop_<name>` thunk for
the target (`RcFree` off, a `OpConstFunc` function value, or a thunk
dead-function elimination culled), and the drop walk guards on
`drop != 0`. **Function-value indices are therefore 1-based on the
index-resolving backends** — index `0` is the reserved null reference, so
the dispatch table opens with a null slot. A **zero-capture** closure has
no env block at all, so it carries `env_ptr = 0` *and* `drop = 0`:
dispatching its thunk on a null env would fault reading the env's rc
header. This matches the native backends' pair byte for byte.

### Per-op lowering

| SSA op | value produced | dispatch |
|---|---|---|
| `OpConstFunc` (bare fn) | `{fn(target), env=0, drop=0, 0}` cell | via `OpCallIndirect` |
| `OpMakeClosure` (captures) | `{fn(target), env(captures), drop(target), env}` cell | via `OpCallIndirect` |
| `OpMakeClosure` (no captures) | `{fn(target), 0, 0, 0}` cell | via `OpCallIndirect` |
| `OpCallIndirect(ptr, args…)` | — | `fn = [ptr+0]`, `env = [ptr+8]`; call `fn(args…, env)` |

The lift change is the crux: **`ir.OpConstFunc` must stop lifting to
`OpConstInt`** and instead produce a `{fn, env=0}` cell, so `Args[0]` of
every `OpCallIndirect` is uniformly a cell pointer.

## Per-backend implementation

### `ssa.Eval` (oracle) and the `x86_64ssa` model

The model has a name→func table (`EvalInTable` / `RunModuleTable`), so
`fn` in the cell is a **table index**:

- `OpConstFunc` → a `{fnIndex(target), env=alloc(0)}` heap cell (reuse the
  `OpMakeClosure` construction with zero captures; a static pool is an
  optimisation, not needed for correctness).
- `OpCallIndirect(ptr, args…)`: `fnIdx = load(ptr+0)`; `env = load(ptr+8)`;
  `callee = funcs[table[fnIdx]]`; recurse with `argvals = [args…, env]`.

This *replaces* the current raw-index `OpCallIndirect` semantics; the
hand-built raw-index tests are updated to build a cell first (that's the
representation real programs use).

### Native real-asm (`gas.go`)

`fn` in the cell is a **code address**:

- `OpMakeClosure` / `OpConstFunc`: store `lea reg, [rip + fnLabel(target)]`
  at `cell+0`, the env pointer at `cell+8`. (`OpConstFunc` can use a static
  `.rodata` cell; `OpMakeClosure` uses the `.bss` bump heap already in
  place.)
- `OpCallIndirect`: `mov r11, [ptr+0]` (fn addr); `mov <scratch>, [ptr+8]`
  (env); set up the SysV arg registers as `(args…, env)`; `call r11`
  (register-indirect — `FF /2`, already supported by the assembler). This
  needs **no dispatch table** — the address is in the cell.

The caller-saved / stack-arg machinery from the direct-call path
(`callLines`) is reused; the only additions are the two cell loads and
the env-as-last-arg, and the register-indirect `call r11` instead of
`call label`.

### wasm (`wasmssa`)

`fn` is a **wasm function-table index** and dispatch is `call_indirect`
against a recorded signature (the wasm-native form). `OpConstFunc` /
`OpMakeClosure` write the index into the cell; `OpCallIndirect` loads it
and `call_indirect`s with `(args…, env)`. (wasm already has the closure
machinery in `wasmbin`; this aligns `wasmssa` with it.)

## Signature / env convention

Every dispatch target takes `env` as its **last parameter** — confirmed
empirically for both shapes:

- Zero-capture lambda `function(y) { y*2+1 }` lifts to
  `__closure_lambda_1(y, env)` — `y` first, `env` (unread) last.
- Capturing lambda `x => x+n` lifts to `__closure_add_1(x, env)` where the
  body is `x + load(env+0)` — the user arg `x` is first, the captured `n`
  is read out of the env block (last param), so again `env` is last.

Bare top-level functions used as values (via `OpConstFunc`) do **not**
declare an env param. Passing an extra trailing `env=<empty>` is harmless
under System V ("unused args may hold any value") and is ignored by the
model (the callee simply never reads that slot). No SSA-level signature
rewriting is required — the extra trailing arg is inert for env-less
targets.

## Migration & tests

1. **Model slice** — `OpConstFunc` → cell; `OpCallIndirect` deref + env
   append, in `Eval` + the `x86_64ssa` model. Update the raw-index
   `call_indirect` tests to build a cell (`RunModuleTable == EvalInTable`).
   Add hand-built closure-dispatch tests (a closure over a capture called
   through `OpCallIndirect`; a bare-fn value called the same way).
2. **Real-asm slice** — `OpMakeClosure`/`OpConstFunc` store the code
   address; `OpCallIndirect` → `call r11`. Validate natively that a
   closure-calling `main` matches the interpreter (the `apply(fn, x)`
   program above, plus a capturing closure `adder(n)(x)`).
3. **wasm slice** — `call_indirect` form, validated by the wasm suites.
4. **Whole-program** — extend `program_run_test` with the closure
   programs, diffed against the interpreter.

## Dependency on RC / runtime helpers

Closure dispatch is necessary but **not sufficient** for every closure
program. A closure that is dropped emits an `OpCall "__fern_closure_drop"`
(which itself calls `__fern_rc_is_unique`), so a **capturing** closure that
goes out of scope needs the RC runtime helpers too:

```
func main():  … v5 = call_indirect v3, v4
              v6 = call "__fern_closure_drop", v3   // RC drop — needs runtime helpers
```

So the two remaining whole-program gaps overlap:

- The **simple** non-escaping, non-capturing case (`apply(function(y){…}, x)`
  — the closure is consumed and never dropped) runs on **closure dispatch
  alone**. This is the first, self-contained target.
- **Capturing / escaping** closures additionally require the RC/runtime-
  helper slice (`__fern_closure_drop`, `__fern_rc_is_unique`, …), shared
  with the struct/array programs.

Sequence closure dispatch first (unblocks the simple case and fixes the
representation), then the RC-helper slice (unblocks capturing closures and
composite types together).

## Risks / open questions

- **`OpConstFunc` result used outside `OpCallIndirect`.** Audit: does any
  lift path consume the (currently integer) function value other than as
  an `OpCallIndirect` callee (e.g. stored in a struct field, compared)?
  If so, those sites must also accept the cell-pointer representation.
  (Vtables use a separate `OpConstVtable` path, so trait dispatch is out
  of scope here.)
- **Env for zero-capture closures / const funcs.** A zero-size `env`
  block (`alloc(0)`) yields a valid but empty pointer; the target must not
  read it. Confirmed for the lifted lambdas (they only read captures they
  declared).
- **Static vs heap `OpConstFunc` cells.** Correctness only needs a cell;
  a per-call heap alloc leaks but computes correctly. A static `.rodata`
  cell (native) / de-duplicated pool is a follow-up optimisation.
- **Defunctionalisation overlap.** The IR's `defunctionalise` pass already
  rewrites monomorphic closure calls to `OpCallClosureDirect` (a direct
  call with env as an extra arg). Those never reach `OpCallIndirect`, so
  this design only needs to cover the genuinely polymorphic sites. If
  `OpCallClosureDirect` appears in the SSA lift, it lowers like a direct
  call with the env arg already in place (a small separate case).

## Sequencing

Model slice → real-asm slice → wasm slice → whole-program tests. Each is
independently reviewable and testable, mirroring how the rest of this
track has been built. The model slice is the one that changes shared
semantics (`OpConstFunc` + `OpCallIndirect`); the backend slices are
mechanical once the representation is fixed.
