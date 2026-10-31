# 2026-10-30 — a length set keeps its array sole

A loop that appends to an array it made writes in place. The plan proves the
array sole (`ssaunits.sole_boxes`), so each append tests only its capacity.
But the proof marks an array shared when a step passes it to a call and does
not move it. The call can keep a reference, and then the array has two
holders.

`__arr_set_len` borrows its array and keeps no reference. Thus a function
that made an array with `__alloc_u8` and then set its length to zero lost the
proof. Each append in its loop then tested the array's count.
`huffman.decode` has this shape. It tested the count at each of its two
appends for each input byte.

## The change

- `ssasem.holds_no_argument` names the builtins that read or write a
  borrowed argument in place and keep no reference to it: `__arr_set_len`,
  `__str_bytes`, `__ct_secret` and `__ct_public`.
- `sole_boxes` treats an argument of such a call as a read. The call does
  not make the array shared.

`semunique` puts no check before the loop, because the array is fresh on
entry. The proof alone removes the tests.

## Measurement

The HTTP/3 hello server, 20,000 requests with 64 in flight, x86-64, `-g`,
callgrind on the server:

| | instructions for each request | `huffman.decode` |
|---|--:|--:|
| main | 24,460 | 714 |
| a length set keeps its array sole | 24,306 | 595 |

## Tests

`TestSelfHostSoleLoopWithX86_64` adds `append-after-set-len`. It makes an
array with `__alloc_u8`, sets its length to zero, and appends to it in a
loop. The emitted function must have no uniqueness test. It runs under the
sanitizer.
