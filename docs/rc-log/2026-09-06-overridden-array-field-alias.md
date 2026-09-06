# An aliased struct's overridden array field leaks its buffer, once per rebind

*2026-09-06* — #8644, narrowed. The issue was filed as "one alias makes `append`
quadratic". The append is not the mechanism, and the defect is not quadratic.

## Minimal shape

No append, no `.with`. An alias, and a spread that overrides an rc array field:

```fern
struct S { ops: i32[], n: i32 }
function run(x: i32): i32 {
    var s: S = S { ops: [1, 2, 3], n: 0 };
    var i: i32 = 0;
    while (i < 200) {
        var prev: S = s;
        s = S { ...s, ops: [4, 5, 6], n: s.n + 1 };
        i = i + 1;
    }
    return s.n;
}
```

| | allocs | frees | live_bytes |
|---|--:|--:|--:|
| self-host | 40,200 | 20,200 | 960,000 |
| native | 20,200 | 20,200 | 0 |

## What leaks, established by scaling rather than assertion

Widening the literal from three elements to ten holds the leaked COUNT at 20,000
and moves `live_bytes` from 960,000 to 2,074,400 — 48 bytes each to 104. That is
24 bytes of header plus 8 per element (element slots are 8-byte on the register
backends), so the leaked block is the **array buffer**, one per rebind, and not
the struct box.

It is therefore CONSTANT per rebind. #8644's headline Θ(N²) — live_bytes
6,012,800 / 23,763,200 / 94,489,600 at N of 100 / 200 / 400 — was this flat
defect multiplied by an array that happened to be growing under `append`.

## Three-way isolation

| shape | self-host | native |
|---|---|---|
| alias + spread, scalar override only (`n: s.n + 1`) | 20,200 / 20,200 / **0** | 200 / 200 / 0 |
| alias + spread, array override (literal) | 40,200 / 20,200 / **960,000** | 20,200 / 20,200 / 0 |
| no alias + spread + append | 20,900 / 20,900 / **0** | 900 / 900 / 0 |

It needs an alias AND an rc array field being overridden. Either alone is clean.
That puts it in #8628's family — an overridden rc field's old value going
unreleased — rather than making it a separate accumulator problem, which matters
before the two are fixed as unrelated bugs.

The scalar-override row is what makes this actionable: same alias, same spread,
same rebind, clean. The only axis that moves is whether the overridden field
carries rc state.

## Why the old buffer survives

The rebind emits `emit_field_reclaim_store`'s documented shape — rc-gated on the
OLD BOX, `__field_reclaim_S` when it is unique, a box-only `__fern_arr_dec` when
it is shared — and that function's comment states the premise the shared arm
rests on:

> a shared old box takes the box-only path: this slot gives up its counted
> reference (`__fern_rc_dec`) so **the surviving owner reaches rc 1 and does the
> deep work**

Here the surviving owner is `var prev: S = s;`, a LOOP-SCOPED binding. Hoisting
it to function scope and reassigning it, changing nothing else:

| alias scope | self-host | native |
|---|---|---|
| loop-scoped | 40,200 / 20,200 / **960,000** | 20,200 / 20,200 / 0 |
| function-scoped | 40,200 / 39,600 / **28,800** | 40,200 / 40,200 / 0 |

Same allocation count, one scope change, and 20,000 leaked blocks become 600.
So the hand-off is real and it is the loop-scoped owner that drops it: its own
release is box-only too, so the deep work happens for nobody.

The residual is a SEPARATE defect, and bounded. Doubling the loop bound doubles
allocations (40,200 -> 80,200) and frees (39,600 -> 79,600) while `live_bytes`
stays at exactly 28,800 — so the hoisted version's leak is constant per CALL
(six array buffers per `run`), not per iteration. The hand-off failure therefore
accounts for the whole of the iteration-scaling leak; what is left is a fixed
per-call cost of a different origin, unchased.

### The link to the compiler's own retention is NOT established

The probe's shape does not literally occur in the compiler. Walking each
function in `irlower.fern` with real brace tracking finds **zero** loop-scoped
`var X: LowerState = <ident>;` bindings. The 77 a first, sloppier pass reported
were function-scope state threading — `function f(s: LowerState) { var st = s; … }`
— mis-attributed because an earlier function's `while` left the loop stack
non-empty. Verifying two by eye is what caught it.

That does not rule the mechanism out of the compiler: aliasing can arise without
a named bare-ident binding — a call argument the callee retains, a field read, a
struct literal capturing the state. It does mean the shape has to be found in
the compiler by measurement before this defect is credited with any part of the
retention attributed to `LowerState.emit`, and it is not credited here.

What this does NOT settle is **where a fix belongs**: a loop-scoped struct
binding could deep-drop at scope exit, or the shared arm could stop handing off
deep work it cannot prove anyone will do. Both fit the evidence; neither is
established.

## Method note

Four structural inferences about this code were overturned in one session — the
`.with` fix site (a zero-byte diff), the `emit_self_overwrite_reuse` fix site (a
gate that exists to prevent exactly that release), a clause attribution that a
probe could not confirm, and the append lowering's identity. Each time, running
the compiler settled in one command what reading it had got confidently wrong.

The append lowering is the cleanest example. Three sites emit `ir.op_arr_push()`
and reading excluded all three, which cannot be true. Extending
`FERN_APPEND_REPORT` to expression-position appends (#8651) and adding one
temporary line per site named it in a single run: `lower_field_append_inplace`.
The exclusion had been wrong because the `__fern_arr_share_inc` / `_dec` bracket
belongs to its `.with` twin, not to it.

Prefer building the instrument. The two-emitter driver pair in this directory's
predecessor entry costs 30 seconds to rebuild and answers questions that
assembly-reading only appears to answer.
