# 2026-09-03 — `m.insert(k, v)` on an existing key strands the incoming key on x86-64 (#7911)

An overwrite keeps the key the column already holds and discards the one it
was handed. On the boxed string ABIs (wasm, arm64 two-word) the discarded key
cell was already released by `freeDiscardedSetKeyCell` (#6243). The
single-word x86-64 path — the column holds the string data pointer itself, so
nothing is boxed and the plain call fell through to the generic emission —
dropped that reference on the floor: one heap key per overwriting insert.

## Measured

`FERN_LEAKCHECK=1`, 64 inserts cycling through 4 keys past the SSO threshold:

| backend | before | after |
|---|---|---|
| x86-64 | 195 / 135, live 3840 | 195 / 195, live 0 |
| arm64 (qemu) | 323 / 323, live 0 | 323 / 323, live 0 |

3840 B is 60 blocks × 64 B — exactly the 60 overwrites.

## Why the release can be unconditional

The set already leaves the incoming key carrying exactly one owned reference
on x86-64: a fresh key (concat / slice / call result) is moved in at rc 1, an
aliased key (ident / field / index) is retained by `emitMapSetKeyRetain`, and
a literal short-circuits on its sentinel. That is the invariant the boxed
path's comment states for its own unconditional cell free, and it holds here
for the same reason, so the same insert-vs-overwrite test applies: `len`
grows by one on the insert branch only, is untouched by the grow and by the
CoW copy, and an unchanged count is the exact test. `__fern_str_dec` on the
stashed key then returns a fresh key's block, undoes an aliased key's retain
(2 → 1, the local's sweep frees it later), and is a no-op on a literal.

Witnessed rather than argued: the aliased-key probe inserts the same local
twice, a literal twice, and an index-read twice, then reads every key back,
under `FreeOn` with `__rc_underflow_count()` folded into the exit — 0 on all
three backends.

## Trap

The aliased-key probe leaks 3 blocks a round under `-sanitize` on BOTH
natives, before and after. That is the `Map_set` escape taint (#4399 — an
aliased key is tainted and never swept by its owner), not this shape; the
probe gates the over-release direction only.
