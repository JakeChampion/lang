# The arm64 leak divergence is deferred slice 5g, not a release bug

Correction to `2026-08-29-arm64-string-map-leak-divergence.md`, written the
same day. That entry recorded the corpus leak census and offered a lead:

> A clean doubling on a string-carrying shape points at the string
> representation rather than at the map or closure drop path — the two-word
> string ABI … is the obvious suspect, and **a two-word string whose second
> word is released on one backend and not the other** would produce exactly
> this signature.

The subsystem was right. The mechanism was wrong, and the entry named
`FERN_RC_TRACE` on `string_closure_capture_churn_free` as the next step. That
step is unnecessary: reading `allocs` and `frees` rather than only `live_bytes`
settles it.

## What the counts say

`leakcheck: allocs=N frees=M live_bytes=K`, both backends, same source:

| case | x86-64 | arm64 |
|---|---|---|
| `string_closure_capture_churn_free` | 400 / 200 / 3200 | 400 / 200 / **6400** |
| `with_reassign_local_alias_threaded` | 46 / 7 / 27888 | 46 / 7 / **55152** |
| `string_array_append_grow_struct_field` | 166 / 126 / 2800 | 166 / 126 / **2784** |
| `string_closure_capture_aliased` | 2 / 1 / 16 | **3** / 1 / 48 |
| `map_string_values_churn_free` | 800 / 800 / **0** | 1400 / 1200 / 3200 |
| `map_string_keys_churn_free` | 1200 / 1200 / **0** | 2800 / 2600 / 3200 |
| `struct_map_field_escapes` | 103 / 103 / **0** | 107 / 105 / 32 |
| `map_delete_tuple_churn_free` | 4500 / 0 / 288000 | 8000 / 2000 / 312000 |
| `cell_string_read_aliased` | 10 / **9** / 32 | 10 / **10** / 0 |

Three groups, and only one of them is a reclamation difference.

**A — identical allocs AND frees, different bytes.** The first three rows. The
same objects leak on both backends; arm64's blocks are simply bigger. Nothing
is released differently. `string_array_append_grow_struct_field` leaks *less*
on arm64 (2784 vs 2800), which is the same effect with the sign flipped and
which no "arm64 releases less" story survives.

`live_bytes` rounds each block to `(size+15)&-16`, so a 4-character string is
16 bytes under the single-word ABI and 32 under the two-word one. The 2× is
block sizing.

**B — arm64 allocates MORE, and some of the extra leaks.** The middle five. On
`map_string_values_churn_free` x86-64 is clean at 800/800 while arm64 runs
1400/1200: arm64 makes 600 extra allocations and frees 400 of them. This is
the real divergence, and it is neither new nor a bug.

**C — one case leaks on x86-64 only.** `cell_string_read_aliased`: same 10
allocations on both, arm64 frees ten and x86-64 nine. This one is not explained
by anything below, and is the residual lead.

## Group B is a documented deferral

`internal/ir/ir.go`, at the overwrite `__fern_str_dec`:

> arm64 (ptrW==8 + TwoWordOverride, two-word str_dec) is **DELIBERATELY
> EXCLUDED** for now: native-arm64 heap-string reclamation is the RC-perceus
> plan's deferred slice 5g … Enabling the overwrite str_dec there over-releases
> on real arm64 hardware (qemu user-mode masks it), so arm64 keeps its **prior
> safe-leak behaviour** … Re-enable by widening the wasm branch back to
> `ast.UseTwoWordStrings` once 5g lands.

The branch is gated on `b.ptrW == 4`, which is wasm32 alone.
`docs/SSO-NATIVE-FLIP-STATUS.md` records the same knock-on and says the cost of
the exclusion is measured in **#6554**. So the arm64 string-in-map leaks are a
known, deliberate, tracked safe-leak awaiting slice 5g — not something to
investigate.

Groups A and B share one root: arm64 sets `ast.TwoWordOverride` and x86-64 does
not, so at the same pointer width the two backends run different string ABIs.
That is the half of the earlier lead which was correct.

## The trap

**A difference in `live_bytes` is not a difference in behaviour.** Equal alloc
and free counts with unequal bytes means the two backends leaked the same
things at different sizes; only a difference in the counts is a difference in
what got reclaimed. The earlier entry read a 2× byte ratio as "arm64 releases
less" and proposed tracing an alloc site that both backends leak identically.
One extra number from a report already in hand would have settled it, and the
probe cost under a minute.

The pinned baselines in `internal/e2e/rc_leak_gate_test.go` are unaffected —
they pin bytes per case per backend, which is the right thing to pin whatever
the cause. What changes is what the difference between the two columns *means*.

## Next

`cell_string_read_aliased` is the only row left pointing the other way: same
allocations, one fewer free on x86-64, and slice 5g does not explain a leak
that arm64 does *not* have. It is 10 allocations total, so it reduces easily.
