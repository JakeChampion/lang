# A concat is a push of many

2026-11-25. Each target. The change is in `rccertify`.

## The gap

`watbin.async_functype` had 24 unproved values, the most of any function. It
pushes onto its borrowed parameter `b` (`v0`) in a loop, and each pass calls
`util.append_all`, which is inlined as `__fern_arr_concat`. The lowering
tests the array against `v0` before the concat:

- Where the array is `v0`, it calls the concat unmarked and tests nothing
  after it (`ssarc.concat_borrowed`). The result is `v0` or a new box.
- Where it is not, it calls the concat marked, and it releases the old box
  where the result is new (`ssarc.concat_owned`).

The certifier gave an unmarked call no origin, so the result had no slot. A
phi input from it handed 0 units. The marked result held a plain count after
its test. The join met 0 with the twin of `v0` that the pushes gave, and the
maybe went round the loop into each push after it.

A value with no slot is
also a value that the certifier does not check.

## The change

- The result of an unmarked call that returns its operand or a new box
  (`effect_kept_or_same`) now has a slot (`call_origin`). It starts as a
  twin of the operand's box: it holds no unit where it is that box, and its
  fresh unit where it is new (`kept_or_same_twin`). This is the rule of a
  push (`push_twin`). Where no key names the box, the result is unproved.

- Where the operand of a marked call is not the box of a key, the result is
  not that box either. The result keeps that through the test that decides
  whether it is its operand (`took_operand`).

`concat-onto-its-parameter` of `TestSelfHostRcCertifyCases` holds the shape.
Without the change it has 1 unproved value. Its variant without the release
gives the finding "leaked where it dies of v5".

A trap: a first version also made the marked result a twin of the
parameter where its operand was that box. The census went from 706 to 716,
and `async_functype` did not move, because the unmarked result still had no
slot.

## Measured

The census of `fern_main` on x86-64, on the same tree, with the certifier of
main and with the change:

| function | before | after |
|---|---:|---:|
| `watbin__async_functype` | 24 | 0 |
| `ecdsa____cat3` | 3 | 0 |
| `hpack____put_string` | 4 | 3 |
| `qpack__Encoder.__put_str` | 4 | 3 |
| all functions | 706 | 677 |

No function goes up, and no function has a finding. The certifier tracks 90
more values (277,512 to 277,602): the results of unmarked concats.

The row is re-banked in `.github/certify-baseline.txt`. `wasm32/checker.unproved`
stays at 84. The mutant census of `checker.fern` is the same with both
certifiers: 5,378 refuted, 227 weakened, 228 missed.

## Next lead

`watbin__enc_instr` now has the most unproved values: 20. The first,
`v2150 = arr_at rc v2138 v2149`, reads element 1 of the array `v2138` before
`__fern_rc_is_unique v2138`. One arm of that test retains the element
(`v2155`), and `v2138` is released after it. This is the field take of
shape A (`2026-11-25-b-a-load-under-a-token-starts-as-its-twin.md`), on an
element of an array under a uniqueness test. Examine whether `token_loads`
sees that test.
