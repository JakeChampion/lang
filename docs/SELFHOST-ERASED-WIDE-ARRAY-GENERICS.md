# Erased-wide `T[]` generics silently miscompile on the self-host wasm path

**Status:** BOTH steps done. Step 1 made the shape refuse instead of
miscompiling; step 2 makes it compile correctly.
**Severity when found:** silent wrong values — not a bail, not a validator
rejection.

## Reproducer

```fern
import "std/array";
function main(): i32 {
    var xs: f64[] = [1.5, 2.5, 4.5];
    var ys: f64[] = array.reverse(xs);
    return (ys[0] * 10.0) as i32;      // want 45 (4.5); self-host wasm gives 15
}
```

## Measured

| path | `reverse` | `rotate_left` | `drop` |
|---|---|---|---|
| native `-interp` (oracle) | 45 | 45 | 45 |
| native compiled `-target x86-64` | 45 | 45 | 45 |
| **self-host `-target wasm`** | **15** | **0** | **0** |
| self-host `-target x86-64` | 45 | — | — |

So: **self-host wasm only.** Every other path is correct, including the
self-host's own x86-64 backend.

## Why wasm and nothing else

These are erased `T[]` generics — `reverse[T](xs: T[]): T[]` builds its result
with `out.append(xs[i])` over an erased element type. On the register backends
every slot is 8 bytes, so an erased element stride is harmless: the wrong
*nominal* width still reads and writes the whole slot. On wasm32 an i32 is 4
bytes and an f64 is 8, so the erased stride is genuinely wrong — the copy reads
and writes at 4-byte steps through an 8-byte-element array, and the result is
silently garbage rather than a trap.

This is the same erased-wide problem the monomorphisation promotion in
`parse_func` (clause (c), `has_bare_scalar_param` + `feeds_wide_container`,
guarded by `all_tp_count == 1`) exists to solve — but that clause triggers on a
**bare scalar param**. `reverse[T](xs: T[]): T[]` has no scalar param; its
parameter is already the container. So it is never promoted, never cloned per
concrete instantiation, and stays erased all the way to emit.

## The part that matters most

**The erased-wide deferral gate did not catch it.** `wasm_ir_deferrals_ok` /
`module_erased_wide` exist precisely to keep an un-lowerable erased-wide module
off the wasm IR path — and with the AST emitters gone, an unhandled construct is
supposed to be a diagnostic naming the bail site, not silent output.

Here the module sailed through: `FERN_STRICT_IR=1` reports nothing, the compiler
exits 0, and the program returns wrong numbers. A hole in a safety gate is worse
than a plain bug, because the gate's entire job is to make this class loud.

## Why the fixture corpus misses it

All 335 fixtures pass on both self-host legs. The wasm leg would have to
instantiate a `T[]`-param stdlib generic at a **wide** element type to see it,
and nothing in the corpus does. Note also that the byte-identity argument
recorded in `CLAUDE.md` for the #5464 promotion — "the stdlib generics that
match (`array.intersperse` / `async.gather`) are DCE'd uncalled in the
bootstrap" — is about which generics get *promoted*; it says nothing about the
much larger set that are never promoted at all.

## Fixing it

Two directions, and the choice is a real one:

1. **Widen the promotion** so a `T[]`-param generic with a wide instantiation is
   monomorphised like the bare-scalar-param shapes are. Fixes the programs. Risk
   is the bootstrap fixpoint: widening the trigger changes which generics get
   cloned in the self-compile, and the existing byte-identity safety argument
   rests on the current trigger set.
2. **Close the gate first** so the shape is refused rather than miscompiled.
   Strictly smaller, strictly safer, and it converts a silent wrong answer into
   a diagnostic — which is the project's stated posture (IR-or-error). Worth
   doing even if (1) follows immediately after.

Recommend (2) then (1): a loud failure is a correct failure, and it makes (1)'s
test surface obvious.

## Step 1 landed

The gate now refuses the shape. `fn_param_sigs_of` gained flag `'7'` for an
erased typevar ARRAY param (`is_erased_typevar_array`), and
`erased_wide_expr`'s wasm branch flags a call passing a wide-element array
(`expr_is_f64arr` / `expr_is_i64arr`) to such a param. `reverse` / `rotate_left`
/ `drop` at f64 now produce the standard IR-ineligibility diagnostic instead of
wrong numbers.

The gate keys on the ELEMENT width, not on the generic, so narrow
instantiations are untouched: `array.reverse` at `i32[]` and `string[]` still
lower and run. Both directions are pinned by
`TestSelfHostErasedWideArrayGate{,Narrow}Wasm`, which drive `fern.fern` — the
real CLI — because these programs `import "std/array"` and a loader-less driver
would silently ignore the import and report a verdict about a broken program.

Both 335-fixture legs stayed green, which also answers the coverage question
above: no fixture depended on the miscompiled path.

## Step 2 landed

`parse_func` gained clause **(c-arr)**: the array-param sibling of clause (c),
promoting a single-typevar generic with a bare `T[]` param whose return feeds a
wide container (`has_bare_array_param`). Monomorphisation then clones it per
concrete element type, so the copy gets a real stride. `reverse` / `rotate_left`
/ `drop` at f64 now return the right values on wasm instead of being refused.

The exclusion this reverses was justified in-tree as "the wide value rides an
i32 array pointer, never trips the erased-wide gate, and promoting them would
over-monomorphise the stdlib". The first half was simply wrong — nothing wide is
passed by value, so the gate never fired, but the copy was silently wrong
anyway. The second half is a real cost, and is why the promotion stays guarded
to `all_tp_count == 1`.

**The gate from step 1 is still load-bearing, and that is deliberate.** A
two-typevar `map[T, U](xs: T[], f)` at a wide element type is NOT promoted, so
it is still erased — and still refused rather than miscompiled.
`TestSelfHostErasedWideArrayGateWasm` now pins exactly that case, so the gate
cannot rot back into silence once the headline shapes work. Fixing part of a
class is a reason to keep the guard for the rest of it, not to drop it.
