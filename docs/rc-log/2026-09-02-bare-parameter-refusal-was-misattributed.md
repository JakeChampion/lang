# The bare-parameter refusal's measurement does not reproduce (#7914)

`2026-09-02-returned-alias-transfer-inc-credit.md` landed the returned-alias
credit and refused it for a BORROWED parameter, on a measurement:

> crediting a bare returned parameter loses three of the five frees in
> `url.query_parse("a=1")`, 256 B on the smallest form that shows it

That entry is closed to edits, so this is the correction of record. **The
measurement does not reproduce, and the refusal it justifies rests on
nothing.** The comment on `returnedAliasIsRetained` now says so; the refusal
itself is unchanged, for a separate reason given below.

## The disproof

Two compilers built from **`0cb7aa1`**, the commit that landed the refusal,
differing only in `returnsOwnBox`'s Ident arm (`fresh[x.Name] || retained`
against `... && !refused[x.Name]`), confirmed distinct binaries. Three
spellings of the same call:

| probe | refused (shipped) | credited |
| --- | --- | --- |
| `var m = query_parse("a=1"); m.len() - m.len()` | 5 allocs / 5 frees / **0 B** | 5 / 5 / **0 B** |
| `return query_parse("a=1").len() - 1` | 5 / 2 / **256 B** | 5 / 2 / **256 B** |
| `var m = query_parse("a=1"); return 0` | 5 / 5 / 0 B | 5 / 5 / 0 B |

Identical in every cell, and the same table comes back from current main. The
"three of five frees, 256 B" signature is the **middle row** — the unnamed
call-result temp — and it is present with the refusal in place. It was
misattributed to the credit.

A second claim in the same entry is also disproved. It reasoned that the taint
path's flat `__fern_rc_dec` was compensating for an unbalanced return-transfer
inc on the `m = __query_pair(m, ...)` self-reassign, which predicts main leaks
once the input carries more than one pair. It does not: `a=1`, `a=1&b=2` and
`a=1&b=2&c=3&d=4` read 5/5, 8/8 and 14/14 with live 0. The refused build's
accounting is correct and scales.

## What removing the refusal is worth, and why it is still not taken

| | refused | credited |
| --- | --- | --- |
| self-host driver retained | 416,560 B | **405,536 B** (−11,024) |
| driver frees | 54,995 | **55,214** (+219) |
| `pair_form_payload_borrowing_call`, both backends | 144 B | **128 B** |
| rc corpus correctness, x86-64 + arm64 | green | green |
| conformance leak census | green | green |

Nothing leaks more anywhere the gates reach, and one corpus case leaks less on
both backends — the leak gate fails only by asking for that to be banked.

It is still not taken. **Five probe shapes read identically under both
compilers**: a bare identity function on `string[]` and on `string[][]`, a
user function returning a struct array, and a `Map[string, i32]` and a
`Map[string, string[]]`, each bound to a local and as an unnamed temp. Nothing
smaller than the whole self-host driver distinguishes the two, so the change is
not vacuous — it is worth 11 KB — but there is no test that would pin it, and
this is a credit whose own history includes a leak. Shipping it would rest on a
green gate run alone, which is the basis the entry above rejected the
reassigned-only boundary for. Same call, same reason.

## Traps

- **A bucket is not a defect, again.** I first wrote the middle row up as "a
  call result consumed as an unnamed temp is not released, where the same value
  bound to a local is". Three probes at increasing fidelity read clean in both
  spellings, so the shape is not the trigger. The leak is something
  `query_parse` carries that a minimal Map-returning function does not, and it
  is unidentified.
- **Check the probe spelling before trusting a leak table.** Probe A and probe
  B above differ only in whether the result is bound, and they differ by 256 B
  under *every* compiler tested. A comparison that varies the compiler and the
  spelling at once reads as a compiler difference.

## Next

Drive it from the frees rather than the shape: take the credited driver's 219
extra frees, attribute them with the two-frame tracer (`FERN_RC_TRACE=1
FERN_LEAKCHECK=1 -g`, pair by pointer, resolve both frames against `nm -n`),
and let the sites name a shape a probe can hold. That instrument is what turned
#7867's static histogram into a real diagnosis.
