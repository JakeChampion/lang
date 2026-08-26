# Whole-program reachability: what actually gets dropped

Audit behind #4114 (child of the binary-size epic #4109), measured
2026-08-26 on a 4-core x86-64 host.

## The question

A self-host driver links every backend it imports — x86-64, arm64 and wasm
emitters, ~2.3 MB of parallel source. #4114 asked whether the merged bundle
keeps the emitters a given driver never reaches.

## Answer: module-granular pruning already works

Two probes, identical except for two extra imports:

```fern
import "./asm_ir";          // both probes
import "./asm_arm64_ir";    // second probe only
import "./wasm_ir";         // second probe only

function main(): i32 { … asm_ir.emit_module_ir(mod) … }
```

| probe | AST funcs loaded | survive tree-shake | binary |
| --- | --- | --- | --- |
| x86 emitter only | 4123 | 2558 | 28 853 772 |
| + arm64 + wasm emitters | 4540 | 2558 | 28 853 772 |

Same live set, same byte count. The 417 functions the two extra backends
add are dropped in full. The IR-level pass afterwards finds almost nothing
left: `LiveFunctionsWithAliases` culls 4 of 2807 functions, carrying 0 ops.

The two binaries are the same size but not byte-identical: enum tags and
type ids are numbered over every loaded module, so the surviving code
carries different immediates. Numbering is per-compilation and consistent
within one, so this is layout, not retained code.

## What was NOT pruned: dynamic-dispatch roots

The vtable behind a `dyn Trait` coercion, an `as? T` downcast, and a
`core/mem.Drop` finalizer all name impl methods no call site mentions, so
tree-shake rooted them separately. Those roots were computed once, up
front, from whole-program `checker.Info` — not from the program being
shaken. A coercion inside a function nothing calls therefore rooted its
impl method, and everything that method called, into the artifact:

```fern
function dead_coercion(): i32 {     // nothing calls this
    var g: dyn Greet = Loud { n: 1 };
    return g.hello();
}
function main(): i32 { return 7; }  // the whole program
```

6 976 bytes of unreachable code, in a program whose `main` returns a
constant. The over-root also reached the IR-level pass, which was seeded
with the same list — so the one pass positioned to cull it precisely was
told to keep it.

Two consequences, both fixed in #4114:

- **Retained code.** Gone once the roots follow the walk: the probe above
  drops to the 4 281-byte empty-program baseline, as does the same shape
  behind a downcast or a Drop impl.
- **Spurious E066.** `platforms.Enforce` documents its contract as "every
  surviving function is part of the artifact" and reports capability use
  over all of them. Two programs differing only in whether a *dead*
  function coerces to `dyn` diverged: the one with the coercion was
  rejected for a `print` nothing can reach.

The fix roots each vtable from the site that builds it, discovered by the
same reachability walk (`dynVtableRoots` / `downcastRoots` in
`internal/treeshake`), and drops the AST roots from the IR-level seed —
where `ip.Vtables`, built by lowering from the tree-shaken program, is
already the precise root set.

## The residue: Drop

`DropImplMethods` stays whole-program. A coercion and a downcast are
expressions the walk can reach; a `Drop` finalizer has no site at all —
its only caller is `__drop_struct_<C>` glue IR lowering synthesises after
tree-shake. Gating it means deciding whether a live function can hold a
`C`, which is type reachability this pass does not compute, and guessing
wrong is a dangling `__method_<C>_drop` at link time.

It costs no bytes — the IR-level pass sees the real call edge, or none,
and culls precisely. It still costs `Enforce`: a target can reject a
program for a capability only an unconstructed type's finalizer uses.
Closing that needs the type-reachability analysis, tracked separately.

## The self-host driver itself does not shrink

`fern.fern` is byte-identical before and after. It dispatches to every
backend it imports, so nothing in it was unreachable, and the self-host
sources use no `dyn` at all — there was no over-root to remove. The fix
is worth having for what it prevents: a driver that stops driving one
backend, or grows a `dyn`-dispatched emitter table, would otherwise keep
the dead half with nothing to notice but the advisory size gate.

## Reproducing

- `internal/e2e/treeshake_backend_dce_test.go` — an unreached backend
  module, and one reachable only through a dead `dyn` dispatch, leave no
  marker in the emitted asm.
- `internal/treeshake/treeshake_test.go` — the coercion / downcast roots
  follow reachability in both directions.
- `internal/platforms/enforce_test.go` — the E066 contract.
- Driver sizes are tracked per-commit by `scripts/ci-check-driver-sizes`
  against `FERN_DRIVER_SIZE_REPORT`.
