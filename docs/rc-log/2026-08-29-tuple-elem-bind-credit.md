# An rc-tuple element bound to a local cost the tuple its whole credit

#7766, found while confirming #7466's latent alias-side gap. The source side is
not latent.

`var e: T = t.1` on an rc-tuple refused `"TUPRC:"` and `"TUPRCS:"` together, so
the local got no release at all: the tuple box and its element buffer both
leaked.

## Measured

x86-64, `FERN_LEAKCHECK=1`, `__rc_underflow()` 0 on every row, `bin/fern -interp`
and the native x86-64 backend agreeing on every exit code.

| shape | rounds | native | before | after |
| --- | --- | --- | --- | --- |
| `var e: i32[] = t.1` | 100 | 200/200/0 | **200/0 live 8000** | 200/200/0 |
| same | 400 | 800/800/0 | **800/0 live 32000** | 800/800/0 |
| `var e: string = t.1` on `(i32, string)` | 100 | 100/100/0 | **300/0 live 7200** | 300/300/0 |
| bound, read, then dead | 100 | 200/200/0 | **200/0** | 200/200/0 |
| three-element tuple, one bound | 100 | 200/200/0 | **400/0 live 12000** | 400/400/0 |
| loop-resident, bound per iteration | 100 | 600/600/0 | **600/0 live 24000** | 600/600/0 |
| bound in an if-arm | 100 | — | **200/0** | 200/200/0 |

80 B/round and unbounded. `FERN_SANITIZE=1` reported it directly rather than by
arithmetic: `fern-sanitizer: leak 8000 bytes in 200 blocks`.

## Why the refusal exists, and what it does not cover

`rctuple_esc_expr`'s own header states it:

> `return t.1` / `var u = t.1` hands the element's reference to a NEW owner, so
> releasing it here over-releases it (witnessed as exit 99 on the escaping form).

That is right for `return t.1`: the element outlives the frame and the
whole-tuple deep free would release it under the caller. The reasoning does not
reach a bind the frame KEEPS. At the exit sweep `e` is dead, so the deep free is
releasing memory nothing reads again — and refusing costs the tuple its BOX as
well as its element, which is why the census reads `frees=0` rather than a
partial.

The two cases were treated alike. `return t.1` measures 200/100 (box freed,
element correctly stranded); the local bind measured 200/0.

## The narrowing

`tuple_elem_bind_sites_of` collects the `var e = t.<i>` binds whose target is
neither reassigned nor escaping (the ordinary `body_unsafe_for` on the TARGET),
and `rctuple_esc_stmt_alias`'s StmtVar arm forgives a site in that set — the same
shape the `alias_ok` forgiveness already had, one relation over.

Threaded as its own `elem_ok` parameter rather than folded into `alias_ok`: the
two mean different things, and the same list is handed to
`body_unsafe_for_alias_ret_counted`, where an element bind forgiven as an alias
bind would be forgiving the wrong relation.

Wired at the rc-tuple credit gate only. The three other callers of the scan pass
`[]` and are byte-unchanged.

## No new walker, and no census movement

The closure reuses `bare_alias_bind_sites_of` with a `want_elem` flag and an
`ExprFieldAccess` arm. That arm is NOT a wildcard, and the feature census counts
`_ =>` arms only, so the ratchet does not move — which mattered, because it sat
at 2899 against a ceiling of 2900.

## Gate

`TestSelfHostTupleElemBind{X86_64,WasmIR,IRArm64}` — twelve rows, each with a
`FERN_SANITIZE=1` leg, at two round counts on the repro because the
discriminator against a bounded leak is whether `live_bytes` moves.

Three refusal rows carry the safety, and each is a different way for the frame
not to keep the element: RETURNED, STORED into a container that outlives the
bind, and REASSIGNED. Two more pin what was already clean and must stay so — a
borrow (`t.1.len()`) and a scalar element bind.

This widens a deep free, which is the shape that double-frees, so the sanitizer
leg is not decoration: the census cannot see an over-release into a freelist, and
this log has recorded that four times in this family now.

## Next lead

#7466's alias-side sibling is still latent and measured clean, and its prescribed
port would deny a credit that currently balances — see the comment there. The
tuple ALIAS CHAIN (#7750) is still refused and still unexplained; this change
does not touch it, and `tuple_alias_chain_refused` still pins it.
