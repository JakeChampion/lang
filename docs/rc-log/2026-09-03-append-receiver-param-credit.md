# An append's receiver is a counted occurrence (#7867 class C)

The last native residue of #7867: a fresh array temp handed to a callee that
appends to its parameter and RETURNS the result without reassigning the
parameter —

```fern
@noinline
function acc_fn(xs: i32[], s: i32): i32[] { return xs.append(s); }
@noinline
function round(i: i32): i32 {
    var ys: i32[] = acc_fn([], i);
    var zs: i32[] = acc_fn([1, 2], i);
    return ys.len() + zs.len() - 4;
}
```

read `allocs=400 frees=0` over 100 rounds on both natives. Two refusals, one
value: stage-(b) declined the temps because `paramCountedRetain[acc_fn]` read
false for `xs`, and `rhsTainted` kept the conservative call-result taint on
`ys` / `zs` for the same reason, so nothing at all was released.

## The design that was proposed, and why it is not the fix

The brief was an interprocedural summary for "the parameter whose only
escaping occurrence is the receiver root of the returned append chain", a
guarded release of the temp at such positions (dec only when the result
differs from the temp), and a matching result credit. Verified against the
runtime before building it, and the guard is wrong for this shape.

`__fern_arr_push_grow` — the x86-64, arm64 and wasm helpers alike — does not
hand its receiver back uncounted. On the in-place path (rc == 1 with spare
capacity) it **sets the buffer's count to 2** and returns the same pointer;
on the copy path it leaves the receiver at its incoming count beside a fresh
rc 1 buffer. So a fresh temp at the receiver position is at rc 2 when the
callee hands it back and rc 1 when it does not — which is precisely the
"rc 2 on the escaping path, rc 1 on the non-escaping one" contract the
counted-retain tier's UNCONDITIONAL post-call dec already rests on. A
pointer-guarded release would decline on the identity path and strand the
buffer at rc 2 with one owner; the unguarded dec nets it to one either way.

So there is no new summary and no guard. `arrayParamCounted` credits the
receiver occurrence of `__method_Array_push`, and both halves follow from the
one map: `countedArgTemp` admits the temp release, and `rhsTainted`'s Call
arm credits the binding of the result. Co-extensive by construction.

`.with` is NOT its sibling. `__fern_arr_cow_inplace` returns the receiver at
rc 1 unbumped on its fast path — an uncounted identity — and the refusal test
pins it.

## The live-local argument

`acc_fn(g, i)` with `g` live afterwards keeps value semantics by the #4873
bracket, not by anything in the callee: `growBracketArgs` incs `g` across
the call, so the callee's return-position append sees rc 2, takes the copy
path, and the caller's buffer — which has spare capacity and would otherwise
grow in place — is untouched. The result is therefore fresh, and with the
position counted its binding is credited; `g` is not a temp and is not
released at the call. Pinned by `append_receiver_param_live_local_keeps_value`
(`g.len()` stays 3 after two calls, both results 4, underflow 0).

## Found on the way: a chain strands its intermediate

`x.append(a).append(b)` — any append whose receiver is itself an owned temp:
an inner append's result, a literal, a fresh call result. The inner grow's
in-place bump leaves the buffer at rc 2, so the outer sees a shared receiver
and copies; the inner result then sits at rc 2 (or rc 1, if the inner copied)
with nothing naming it. `x = x.append(k).append(k + 1)` leaked eight of its
nine buffers per round. `emitArrayPush` now releases such a receiver after
the outer grow through `emitOwnedSlotDrop` — the deep drop is right for
pointer elements because the `_ptr` / `_str` copy paths retain each element,
so the superseded buffer still owns its own; on the in-place path the drop is
uniqueness-gated and only decs. `return p.append(a).append(b)` is clean only
with both halves.

The outer copy itself is a pre-existing cost (a cliff crossing bought by the
inner's rc 2), unchanged here: the self-host has three chained appends.

## Measured

`FERN_LEAKCHECK=1`, 100 rounds, `__rc_underflow_count()` folded into the
exit, `-interp` the oracle (exit 0 everywhere, wasm included):

| probe | x86-64 before | x86-64 after | arm64 after |
| --- | --- | --- | --- |
| class C, `i32[]` | 400 / 0, live 12,800 | 400 / 400, 0 | 400 / 400, 0 |
| class C, `string[]` | 800 / 600, live 4,800 | 800 / 800, 0 | 1000 / 1000, 0 |
| live local `g`, two calls | 400 / 200, live 9,600 | 400 / 400, 0 | 400 / 400, 0 |
| `x = x.append(k).append(k + 1)` | 900 / 100, live 70,400 | 900 / 900, 0 | 900 / 900, 0 |
| literal / call-result / string-chain receivers | — | 2000 / 2000, 0 | 2400 / 2400, 0 |
| chain callee + a bare-return control | 350 / 0 | 350 / 200 | 350 / 200 |

The last row's residue is the deliberate bare-parameter refusal
(`if (…) { return xs; }`), pinned separately in the corpus.

## The driver does not move

Self-host driver (`fern.fern` built native with `FERN_LEAKCHECK=1`), before
and after, on two subjects:

| subject | allocs | frees | live bytes |
| --- | --- | --- | --- |
| the receiver probe above | 4,020,823 | 3,293,672 | 33,024,624 |
| `lexer.fern` | 934,415 | 773,362 | 7,925,856 |

Identical on both sides, to the allocation — the two driver binaries differ,
so the credit reached the compiler's code, but no site it reaches runs with a
fresh temp at the position. The self-host's `return acc.append(x)` sites are
not the class-C shape after all: every one carries a bare `return acc` on
another path (`push_str_unique`'s `if (index_of_str(xs, s) >= 0) { return
xs; }`, `ident_of`'s non-ident arms) or reassigns the accumulator (the
consumed-threaded form #7995 already covers). The bare-parameter return is
the refusal `2026-09-02-returned-alias-transfer-inc-credit.md` deliberately
keeps, and that is where the #7914 frontier now sits for this family.

The conformance census moved one row: `chained_array_methods` 250 → 50
unpaired allocations (the chain release), 8,627 → 8,427 in total.

## What remains of #7867

Nothing in the fresh-argument class this issue names. A fresh temp at a
position whose callee returns the parameter BARE on some path is still
refused, deliberately (`string_pushed_then_returned_bare_stays_refused`,
`2026-09-02-returned-alias-transfer-inc-credit.md`), and that is the residue
the corpus pins rather than a class of this issue.
