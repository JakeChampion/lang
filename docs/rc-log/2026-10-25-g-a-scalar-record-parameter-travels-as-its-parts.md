# 2026-10-25 — a scalar record parameter travels as its parts

`sempair.scalar_params`, `params_kept_apart`, `split_calls` and
`branched_block`; `semscalarreturn.param_parts`, `split_param`,
`split_args` and `branch_parts`; `seminline.scalar_all`;
`semsource.split_contracts`; `irtables.fn_sig_index`. Found when #12723
typed the stdlib's time parameters (#13369).

## The shape

```fern
pub function wait_any(set: i32[], timeout: Option[Duration]): i32 { ... }
pub function (a: Duration) cmp(b: Duration): i32 { ... }
function wait_into(self: RealDriver, own events: i32[], timeout: Option[Duration]): i32[] { ... }
```

A record passed to a function that is not spliced was always a box, and
an Option of one was two: the Option's and its payload's. The compiler
returned a record of scalars in lanes (`semscalarreturn`) but passed one
only as a box. Once the stdlib's timeouts became `Option[Duration]` and its
deadlines `Instant`, every wait in the serve loop built two boxes. The plain
hello went from 0 allocations a request to 4.

## What changed

A parameter that is a record of one to four scalar fields is passed as its
fields. An `Option` of such a record, or of a scalar, is passed as a Some
flag followed by the payload's parts. The callee must be one every
reference names by a direct call, the same condition a paired return has,
and its body must only read the parameter apart or hand it on to another
such parameter. This covers free functions, methods, and trait impl
methods reached through a generic.

The rewrite is on the semantic form, so the three backends gain it without
any code of their own. A backend already takes its parameter count and
float slots from the lowered body, and the IR verifier now does the same.
At a call:

- a record or a Some built for the call hands on its arguments, and a None
  hands on a false flag and zeros;
- any other record is read field by field;
- any other Option is taken apart on a branch on its flag. This splits the
  call's block, and the call moves to the join.

It runs before the return pairing. A call result reaching a split
parameter is therefore read apart, and the callee that produced it can be
paired.

## Measured

Serve allocations a request, `internal/testing/e2ecompiler`, x86-64 unless
named. The before column is main at af9a25ef5, with the typed stdlib.

| Test | Pin | Before | After |
|---|---:|---:|---:|
| `TestSelfHostServeAllocsPerRequest` | 0 | 4 | 0 |
| `…Ring` | 0 | 6.5 | 0 |
| `…Arm64` | 0 | 4 | 0 |
| `…Wasm` | 0 | 5.6 | 0 |
| `…H2` | 21 | 24 | 20 |
| `…TLS` | 11 | 15 | 10 |
| `…H3` | 67 | 79.9 | 67.9 |
| `…H3Mux` | 36 | 38.0 | 35.7 |
| `…TwoFetch` | 91 | 121 | 97 |

`TestSelfHostScalarParams` pins a `Duration`, an `Option[Duration]` and an
`Option[i64]` across a module boundary, through a method, and through a
trait impl reached by a generic, on the three targets: 0 allocations over
100 rounds, where main allocates 800.

## What is left

The H3 and two-fetch rows are still over their pins. On the two-fetch
path, comparing the same compiler against the stdlib from before #13369
gives two causes:

- **`async.Wait` stores its timeout as an `Option[Duration]` field.** A
  record's Option field is a reference, so each park builds the Option and
  its payload. That is 8 of the 12 extra allocations a request in that
  comparison.
- **`Duration.sub` boxes its result under `Instant.since`.** It is not
  paired, because a caller elsewhere in the program keeps the result whole.

Both need a record field of scalar-record or Option type laid out in its
parent's box, or the pairing narrowed per call site. Neither is in this
change.
