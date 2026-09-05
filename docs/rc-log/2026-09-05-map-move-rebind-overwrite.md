# 2026-09-05 — a MOVED map alias still hands the slot a count

`var (m2, ok) = sm.without(k); sm = m2;` stranded the whole table once per
rebind. The map overwrite site chose its same-pointer release with

```go
aliasInced := (needsRcIncOnAlias(n.Value, b) && !b.rc.moveSites[n]) || …
```

which asks **whether an inc was emitted**. That is the wrong question. A move
skips the transfer inc *and* skips the source local's exit sweep — the count is
not created, it is **handed over**. The slot receives one either way, on top of
the one it already held, so the release is owed either way.

`__map_cow_inplace` returns the receiver's own handle on its in-place branch, so
`m2` and the `sm` about to be overwritten are the same pointer and the
same-pointer arm is the only place that release can go. With the arm suppressed
the assignment emitted `if (old != new) { drop }` and no else at all.

## Measured

A twelve-line program, `FERN_RC_TRACE=1 FERN_LEAKCHECK=1`, x86-64:

| shape | allocs | frees | live at exit |
|---|---|---|---|
| `var st = sm.without(k); sm = st.0` | 3 | 3 | 0 |
| `var (m2, ok) = sm.without(k); sm = m2` | 3 | **1** | **224 B** |

Three allocations, one freed: the kv buffer and the key string both survive.
The trace named the missing op precisely — the clean shape carries a `d` on the
string between the delete and the exit sweep, the leaking one does not — and the
IR dump then showed the two assignments emitting the same `OpNe`/`OpIf` and only
one of them an `OpElse`.

Corpus, 500 rounds:

| case | x86-64 | arm64 | wasm32 |
|---|---|---|---|
| `map_delete_destructure_churn_free` | 112000 → **0** | 128000 → **0** | 96000 → **0** |

Every other pin unchanged, and all three `RcCorrectnessCorpus` legs stay green —
exit codes and `__rc_underflow_count()` both — so the bytes went away without
an over-release taking their place.

## #8434 was two bugs, not one

The same probe that settled this one **refused** the other shape:
`sm = sm.without(k).0` produced no row at the map overwrite site at all. Its IR
emits a flat `__fern_rc_dec` at the assignment and `__drop_struct_flat_Map` at
scope exit — `freeEligible[sm]` is false, so the local never reaches the Map arm
and the columns are never walked. Different mechanism, different fix; it keeps
its pin (`map_delete_projected_self_assign_churn_free`, 128000 / 144000 /
104000) and stays on #8434.

Both recorded candidate mechanisms for this bug were wrong, and a probe on
`computeFreeEligible` is what killed them: `elig[sm]` is **true** in the clean
and the leaking shape alike, so the divergence was never in the eligibility
verdict. Reading the analysis would not have found it; dumping what the two
shapes emit did.
