# The last five name-keyed credits, and the one that was leaking in silence

Five families in `irlower.fern` still resolved a reclaim fact by a bare
variable NAME after #7358 deleted `reclaim_slot_name`. They are now keyed on
the binding SITE (`name@line:col`) or, where no `var` binds the slot, on the
slot number. One of the five was leaking on a shape two tokens away from a
shape that was clean.

## The witnessed one: a block-scoped `dyn Trait[]` leaks every element

```fern
function go(k: i32): i32 {
    var t: i32 = 0;
    if (k >= 0) {
        var xs: dyn Show[] = [41, "hello"];   // <- inside a block
        t = xs[0].show() + xs[1].show();
    }
    return t;
}
```

| | x86-64 | wasm |
|---|---|---|
| the block-scoped form, before | **98** | **98** |
| the identical literal at FUNCTION scope, before | 0 | 0 |
| both, after | 0 | 0 |

One `if` apart, and 98 means the heap grew across a second 3000-round churn.
`98` here is every element box: the credit was minted (`collect_dyn_arr_names`
recurses into nested blocks, so the entry exists) and then never resolved,
because the sweep looked it up by `st.locals[i].slot_name` and `retire_locals`
has already renamed that slot to `"!retired!xs"` at the block's exit. The
sweep runs after. The name it asks with is not the name the row was filed
under.

This is the #7594 shape — the discriminator being whether the lookup reads
`slot_name` — on a second family. The site key survives the rename, so both
forms now free.

The emitted-asm form of the same fact, which is the cheaper instrument: the
function-scope program emits 4 `__fern_str_free` calls and the block-scoped
one emits 3. One missing release, visible without running anything.

## The other four, and what is honest to claim about them

None of the four produced a discriminating probe. Recording that plainly,
because #7253's own record is largely a list of null results that were wrong,
and the way to not add to it is to say which column a change sits in.

**`ENUMRE:`** — the sharpest hazard on paper: a hand-rolled first-match VALUE
lookup, no shadow-blanking rule, and the value it returns is the enum whose
variant layout `emit_enum_variant_drops` then walks. Two same-named locals of
different array-payload enums would deep-drop one box through the other's
layout. I could not build that program: `enum_only_wildcard_used_rec` is a
name-GLOBAL gate, and its `StmtAssign` arm refuses any assignment whose value
is not a fresh ctor of the *credited* enum — so the nested shadow's own
`b = S(...)` withdraws the outer credit before the collision can happen. Every
other nesting the gate reaches (`StmtMatch` on another scrutinee, a plain
block) falls to its `_ => { return false; }`. What remains is the defer-replay
path, where `slot_of` routes through `defer_alias_slot` precisely because the
declaring block has been retired — leak-direction, not measured here.

**`SCENRB:`** — the probe is instructive and its null result is not evidence.
Top-level admitted scalar-enum `e`, a sibling-block `var e` of another scalar
enum reassigned inside the block:

```
collided     98        renamed control  98        top-level alone  0
```

Identical on both sides, so nothing discriminates — but the reason is that the
block-scoped scalar enum has **no credit of its own** (the collector runs over
the function's top level only), so it leaks either way and the leak is larger
than the stray dec is loud. That is the "latent" row of #7253's severity table
arriving together with a denial: the leak masks the fault. It also means this
one is a trap armed by its own future fix — close the block-scoped gap and the
stray dec lands on a box that now has an owner.

**`ENVCAP:`** and **`SNAP:`** — collision-only hazards, both unmeasured. A user
local sharing a hoisted capture's spelling inherited the capture's BORROW
exclusion and lost its own dec; `make_clo_func` stamps the synthesized reads at
`0:0`, so under the site key no source local can collide with one. `SNAP:` is
answered for param slots only and a param has no binding site, so it takes the
slot NUMBER instead — `lower_func` lays the frame out receiver-first then
parameters, which is the same order `snapshot_param_slots_of` walks.

## The gate earned its keep: a second consumer, in another function

The first version of the `"SNAP:"` conversion moved both halves I had found —
the producer in `lower_func` and `slot_is_snapshot_param` — and type-checked
clean. `scripts/selfhost-emit-hashes` then moved **6 rows**, two fixtures on
all three targets:

```
own_param_array_move/main.fern       x86-64 / arm64 / wasm
own_param_branch_transfer/main.fern  x86-64 / arm64 / wasm
```

Both are `own`-param over-release regressions. The asm diff said what had
happened in three lines: one local slot fewer (`$8` → `$7`) and a
`movq -8(%rbp), %rax; movq %rax, -24(%rbp)` pair gone — an entry SNAPSHOT store
that was no longer being emitted.

`seed_snapshot_slots` is a **third** site, in a different function again, and it
consumes the same rows by `slot_of(snaps[sni])`. With the rows holding slot
numbers, `slot_of("0")` returned -1 and no snapshot local was created — so every
reassign of a snapshot param would have freed the CALLER's original box, which
is the exact defect those two fixtures exist to pin.

Nothing else would have said so. It type-checks (`string[]` either way), the
targeted probes do not use `own` params, and the two fixtures assert values and
lengths that a freed-then-immediately-reused block still satisfies for short
call chains — which is why both fixtures carry a four-call comment explaining
that two calls passed while the original bug was live.

This is the #7292 rule with a third instance: **enumerate consumers, not callers
of the thing you are editing.** The hidden local is now named by the slot too
(`snapshot_slot_name`), so the two halves cannot spell it differently.

## The mechanism that is now shared

`slot_of_site` is `slot_of`'s twin and the shape of the whole fix in six
lines. `slot_of` answers with the innermost binding of a SPELLING, which is
what a shadow redirects; a site key belongs to exactly one binding. The two
scan the same direction for the same reason — a loop body re-entering its own
declaration rebinds to a fresh slot and the latest is live.

Two deletions came with it, both dead-by-construction once the key changed:
`collect_dyn_struct_in_stmt`'s shadow-blanking rule (unreachable since `DYN:`
went site-keyed — site keys do not collide, so nothing to blank) and the same
rule in `collect_dyn_arr_in_stmt`.
