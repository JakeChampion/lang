# A builtin with a known contract is not an unknown callee (2026-08-31)

`slice_unchecked(s, a, b)` leaked twice over on every call. Both halves
are the same mistake: the per-helper registries exist now, and two
places still read "builtin" as "nothing is known".

Found by following #7875's second trace frame to the driver's largest
leak — `__fern_alloc_rc1 <- __str_slice`, 1172 of 6169 blocks — and
agreeing with #7867's independent static classification of the same
helper.

## What the runtime actually does

`emitStrSliceRuntime`, in its own words: *"returns a fresh string"*.
Three outcomes, none of them a view into the source — the shared empty
sentinel, an inline-packed string (≤7 bytes, no allocation), or a fresh
rc1 heap copy. `rcResultOwned` states the consequence: *"the caller may
always release the result"*, for all three, because an inline or empty
string short-circuits on the inline bit.

## Half one: the result was never reclaimed

`freshOwnedRcTempType` has a `*ast.SliceExpr` arm for a string slice and
had no arm for the builtin spelling of the identical thing —
`exprNoParamEscape` says they are the same, and the lowering routes both
onto `__str_slice`. So the argument-position temp was stranded.

This is *not* the hazard `ownedCallResultType` excludes builtins for.
That exclusion is concrete and correct: `arr.push(x)` and `m.set(k, v)`
return the RECEIVER's buffer in place at rc==1, so a dec would free a
live container. `slice_unchecked` allocates. The arm sits beside the two
named builtin exceptions already there (`isCellStringGet`,
`isMapStringGetOr`).

## Half two: the source lost its drop

The escape walk taints every string-typed IDENT passed as a call
argument — a user callee may store it into a container it returns, and
freeing it caller-side would dangle. Its only exemption reads
`paramCountedRetain`, which is keyed by **user declaration**, so a
builtin has no entry and never qualifies.

It was protecting against nothing. `pureReadReceiverBuiltin` already
records the fact — *"copies bytes OUT of its string receiver into a
fresh buffer"* — and three other sites in `rc_analysis.go` already rely
on it. Only argument 0 can be the receiver: the `__method_` forms have
`argStart == 1`, so their receiver never reaches the loop.

Guarded `!ast.UseTwoWordStrings(b.ptrW)`, so this half is native
single-word only.

## Measured, both halves, both ABIs

`eat2(s: str)` taking `slice_unchecked(line, 4, 4+N)`, four rounds:

| | N=4 | N=8 | N=16 |
| --- | --- | --- | --- |
| x86-64 before | 1 / 0 | 5 / 0 | 5 / 0 |
| x86-64 after | 1 / 1 | 5 / 5 | 5 / 5 |
| arm64 before | 5 / 1 | 5 / 1 | 5 / 1 |
| arm64 after | 5 / 5 | 5 / 5 | 5 / 5 |

The step at exactly 8 bytes on x86-64 is the SSO threshold: at ≤7 the
slice allocates nothing, so the single leaked block is the SOURCE, which
isolates half two on its own. **The two-word ABIs have no inline
packing**, so arm64 allocates at every length and shows only half one —
which is why the two corpus cases use a 4-byte and a 16-byte slice
respectively. Between them each half is pinned on the ABI that isolates
it, verified by removing each fix in turn.

## The self-host number, which is smaller than the attribution suggested

| | frees | live_bytes |
| --- | --- | --- |
| before | 5218 | 530656 |
| after | **5500** | **516064** |

282 more blocks freed, 14,592 bytes — **2.75%** of the driver's live
set, and 4.6% of its leaked blocks.

**Not the 19% the attribution implied**, and the gap is worth stating.
`__str_slice` allocated 1172 of the leaked blocks, but only the ones in
the argument-temp shape are reclaimed here; a slice bound to a local,
stored into a container, or returned is a different shape with a
different owner. 282 of 1172 is 24% of that bucket.

The general lesson, which is the same one #7871 taught from the other
direction: **an alloc-site bucket names a producer, not a defect.** One
producer's blocks can leak for several unrelated reasons, and fixing one
shape does not retire the bucket.

Banked, in the good direction:

- census `http_cookies` 22 → 15, `path_join` 1 → 0, and
  `sso_slice_len_8_heap` 1 → **0** — a fixture named for this exact
  boundary;
- arm64 rc corpus `stdlib_query_parse_roundtrip` 256 → 128.

## What is left, and it is a design question

`StrType`'s own documentation says something the runtime does not do:

> a `str` is a non-owning view of some string's bytes: it must never be
> freed by its holder. Runtime shape: identical to StringType (the
> #4294 immortal rc=-1 view box IS the runtime `str`)

An immortal rc=-1 box would need no reclaim and no source protection.
`__str_slice` returns a mortal rc1 copy instead, so the copy must be
freed by someone — which is what this change arranges. The doc also
says *"in this first slice no producer yields `str` yet"*, which is
stale: `slice_unchecked` is declared `: str` in `checker.go`.

So the fix here is right for the runtime as it stands, and whether the
runtime should instead yield the immortal view box the type describes —
zero-copy, and then the source really would need protecting — is a
separate call. Recorded on #7876 rather than decided here.
