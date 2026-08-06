# Erased-wide `T[]` generics silently miscompile on the self-host wasm path

**Status:** open bug, characterised. No fix in this note.
**Severity:** silent wrong values — not a bail, not a validator rejection.

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
