# The string alias, and a comment that had been wrong for a while

#7282's string limb — the fourth container, and the one the container change
left explicitly unwired. It is not a repeat of that change: strings reach the
store through a different emitter, and the reason the retain was missing turned
out to be a stale claim in the code rather than a missing clause.

## What it was

| shape | interp / native | before | after |
| --- | --- | --- | --- |
| fresh-ret call (`w("ab")`) | `21` | `200/0` **3200** | `200/200` **0** |
| `<i32>.to_string()` | `77` | `200/0` **3200** | `200/200` **0** |
| `xs.join(",")` | `55` | `700/500` **3200** | `700/700` **0** |
| `.replace(old,new)` | `38` | `400/200` **3200** | `400/400` **0** |
| conditional alias | `21` | `200/0` **3200** | `200/200` **0** |
| `.trim()` → `str` view | `55` | `300/200` **2400** | `300/250` **1200** |
| alias of a PARAMETER | `21` | `200/0` **3200** | unchanged — must not move |
| alias chain `v = t; u = v` | `21` | `200/0` **3200** | unchanged — refused |
| reassigned alias | `21` | `400/0` **6400** | unchanged — refused |
| accumulator alias | `21` | `600/0` **9600** | unchanged — refused by class |
| for-in element source | `21` | `300/100` **3200** | unchanged — origin not credited |
| destructure-binder source | `57` | `300/0` **7200** | unchanged — origin not credited |

Every exit code is the two oracles agreeing (`bin/fern -interp` and native
x86-64), never read off the self-host run.

## The retain was computed and then thrown away

`lower_stmt_var` sets `alias_inc` for the bind, and the value dies before it can
be emitted: a reclaimable string slot routes to `emit_str_reclaim_store`, which
took no `alias_inc` at all. Its header comment said why —

> The string sibling of emit_arr_store's do_dec branch, but with NO alias_inc —
> a reclaimable string is proven non-aliased, so it is never inc'd.

— which was true only because the escape gate denied a credit to any aliased
string. The invariant was self-fulfilling: aliasing suppressed the credit, so no
aliased string ever reached the store, so the store never needed the retain.

The other half of the same comment had gone **stale**, and it is the half that
mattered:

> its box is header-less on the asm backends … never the rc-word `__fern_rc_dec`

A string box is rc-headered on every backend. `__fern_str_box` allocates 24
bytes, writes `movq $1, (%rax)` and returns the pointer PAST it, so the count
sits at `-8`; `__fern_str_free` reads that word, `jg` decrements without freeing
above 1 and only frees at 1, and `js` diverts a negative count to the underflow
counter. `__fern_rc_inc` increments the same word behind the same guards. The
duplication model applies to strings unchanged — the comment was describing a
layout the code no longer had.

## Ten families, one gate

`"STR:"` is granted by ten producer families (fresh producers, literal-init,
`.to_string()`, `.join()`, `.trim()`, `.replace()`, fresh-ret calls, owned
container reads, `?`-bound payloads, accumulators). Wiring one says nothing
about the others, so each takes `alias_bind_sites_of` and `credit_alias_sites`
at its own site. Two are deliberately excluded, and both for a property of the
class rather than convenience:

- **accumulators** are reassigned by definition, and each rebind frees the box
  it supersedes — an alias bound before a rebind would sweep freed memory. Every
  wired family carries `index_of_str(reassigned, …) < 0`, so no route grants one.
- **`SFRCAND:`** releases behind an identity guard aimed at a chain root's slot,
  which only a binding site can resolve. An alias has no root walk to carry it.

## Why the parameter row is the one that matters

38 of the 72 `var x: string = <ident>;` sites in `examples/self_host` and
`internal/stdlib` are **parameter**-origin — the majority. A parameter owns
nothing, so a retain on one is an inc nothing gives back, and an unbalanced
retain allocates nothing and frees nothing: it is invisible to the census on its
own and shows up only as some *caller's* box never reaching zero.

The gate is structural rather than enumerated. The retain asks
`slot_is_reclaimable_str`, whose first line is `if (i < s.n_params) return
false;`, and the credit is only ever copied from a source that already held one.
Retain and credit are therefore the same set by construction, which is also what
makes wiring the families incrementally sound: an unwired family's source is
never credited, so its aliases never retain, and the shape simply keeps its
previous behaviour.

## Three tests that used aliasing as scaffolding

The change broke three passing tests, and only one of them was about aliasing.
`aliased-not-reclaimed` was the old invariant itself and is now
`aliased-reclaimed-once`. The other two — `ident-operands-result-only` and
`accum-nonfresh-reassign-not-reclaimed` — merely *used* `var ka = a` as a
technique to make a value un-reclaimable so a whole-program count could isolate
something else. That technique is now void.

Both were rebuilt on parameters, which `slot_is_reclaimable_str` refuses at its
first line — a property of the class, not of an escape scan, so it cannot drift
back the way the alias trick did. Both then needed the count SCOPED to the
function under test, because `main`'s own literal temps were being counted
against the contract; `str_reclaim_ir` already had a `scope` field for exactly
this and it was reused rather than reinvented.

## What the instruments could and could not see

`conformance/cases` contains **zero** instances of the shape, so emit-hash reaches
it only through the stdlib that fixtures link. Of the 7 stdlib sites, only
`std/array` and `std/format` are imported by any fixture, and both of those are
parameter-origin; every remaining stdlib site has either the alias or the source
reassigned. **Emit-hash therefore reaches zero creditable sites** and is a pure
scope instrument here — it proves nothing else moved and carries no correctness
signal about the class.

That leaves the 24 local-origin sites in `examples/self_host` to the self-compile
fixpoint, which is self-referential and blind to a stable miscompile, and a leak
changes no emitted bytes in any case. `internal/e2eselfhost` is not merely
primary for this change; for the credited case it is close to the only coverage,
which is why the suite enumerates binding origins explicitly instead of sampling
shapes.

## Left open

- The **`str` view** class (`.trim()`) improves from 2400 to 1200 live bytes and
  does not close. A view box carries a negative rc, so both `__fern_rc_inc` and
  `__fern_str_free` decline it and only the source's `__fern_str_view_free`
  reclaims — safe, but still leaking. Pinned as measured.
- The **alias chain** (`var v = t; var u = v;`) stays refused: `v` escapes as a
  bare ident, so it is not an eligible alias site and `t` keeps no credit either.
  Conservative — it leaks rather than over-releasing.
- **for-in elements** and **tuple-destructure binders** are origins no `"STR:"`
  family credits, so there is nothing to share. The destructure binder is the
  origin the #7253 axis gained when it was applied to this shape.
