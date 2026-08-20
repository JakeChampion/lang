# A `trim` result's BOX is this frame's; only its bytes are not

`var t: string = base.trim()` leaked its box on every backend. 400 rounds of the
churn harness, a pair of compilers from the same commit:

| shape | x86-64 | arm64 | wasm |
| --- | --- | --- | --- |
| `base.trim()` | 9600 → **0** | 9600 → **0** | 48000 → **0** |
| `base.trim().trim()` | 19200 → **0** | 19200 → **0** | 96000 → **0** |

## Cause, and the sentence that hid it

`str_local_binding_is_fresh`'s header lists the DELIBERATELY EXCLUDED forms:

> a string LITERAL (static, not heap), a bare ident / field / index / slice
> (aliases a live string), `.trim()` (zero-copy VIEW into the receiver buffer),
> `.replace()` / `.to_string()` (receiver-identity fast-paths)

`trim` sits next to the receiver-identity cases, and the parenthetical reads like
one — but it conflates the BOX with the DATA. `asmcore.rt_src_str_trim` returns
`s[start:end]`, always a slice and never the receiver, so the box is new on every
call and nobody else names it. Only the bytes belong to the receiver.

The measurement settles it without reading any of that: **24 bytes per round on
the register backends is exactly one view box**, and a result that WERE the
receiver would add no box at all.

`.replace()` in the same list is genuinely different — it returns the receiver
unchanged when the needle is absent — so it is left alone here. It measures
54400 / 48000 and wants its own answer.

## Fix

One flag, two releases. `LocalInfo.str_view_local` is set at the binding site,
where the receiver's type is known, and `str_free_helper` reads it at the three
release sites (exit sweep, loop-rebind store, and the tuple-element helper-name
consumer). On the register backends the box carries the immortal rc the slice op
stamps, so `__fern_str_view_free` returns the 24 bytes and leaves the data alone;
on wasm the slice COPIES, so the same helper takes `__fern_str_free`'s path and
frees box and data together.

The credit itself is an ordinary `STR:` name via `collect_trim_local_names` —
"is this box mine" is the same question every other entry in that set answers,
and only the RELEASE differs. That is the split the machinery already supports,
and it is why the flag lives on the slot rather than the credit carrying a new
prefix.

`trim_str_init` reads declared types for the receiver test, the third consumer of
that pair after `join_strarr_init` and `tostr_scalar_init`.

## Witnessed

- **The type gate**, by construction and by test: a user-declared
  `(h: Holder) trim()` returning `h.name` is refused, and it is the case that
  separates a sound compiler from a name-matching one — the heap cases move
  either way.
- **The release**, by measurement: both heap cases fail with 98 on the parent, on
  the x86-64 and wasm legs.
- **Liveness**, including the shape that worried me most — a trim OF a trim,
  where the second view's data pointer lies inside the first view's source. Both
  boxes are released and the bytes survive: the receiver and both results are
  read after decoy allocations, and `base.len() == t.len() + 3` pins the bytes as
  intact rather than merely readable.

## Method note

This one was found by probing rather than by reading the leak list: a sweep of
the string builtins one at a time (`to_ascii_upper`, `repeat`, `reverse`,
`bytes`, `trim`, `replace`) against the current compiler, all with the same
harness. Four measured 0 and two did not. That is a cheaper way to find the next
increment than re-deriving it from the classifier source, and it produced a
number that contradicted a comment.
