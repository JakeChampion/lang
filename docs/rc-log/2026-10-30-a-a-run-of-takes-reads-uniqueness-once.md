# 2026-10-30 — a run of takes reads its donor's uniqueness once

A take moves a field out of a box. The lowering tests whether the box is
unique. On the unique arm it clears the slot, and on the other arm it retains
the field. Each take made its own test, so a record update that the lowering
scalar-replaced paid one test for each field. A 31-field `Streams` paid 31.

A classified donor already shares one test across its takes. But a donor is
classified only when it dies in the block. In `Streams.__retire_done` and
`Session.__write`, the donor stays live past the takes. For example, an
element read from its list is used after the takes.

## The change

- `ssaunits.tested` finds a **run**: two or more takes of the same shape
  from one donor in one block. Between them, each use of the donor is a
  read that changes no count. The donor is not dropped, and it is not made
  a reuse token, before the last take.
- A run is a `Keeps` entry with `lives` set. Its start reads the donor's
  uniqueness into the token slot (`keep_token`). Each take of the run
  (`ssarc.tested_take`) clears its slot or retains under that one read.
- `classified_at` and `token_kept` skip a run. A run releases nothing at its
  end, and it holds no token.

The run frees the token slot after its last take. The donor can die at that
take and become a reuse token, because the token is held after the take.
Thus the run needs the token slot free only up to the take before its last.

The certifier caught the `token_kept` case first. `checker__admission_env`
leaked the children of `env.sigs`, because the hold of the token at the
run's last take was skipped.

## Measurement

The HTTP/3 hello server, 20,000 requests with 64 in flight, x86-64, `-g`,
callgrind on the server:

| | instructions for each request | uniqueness tests in takes |
|---|--:|--:|
| main with the date line | 25,394 | 152 |
| a run reads uniqueness once | 24,323 | 56 |

`__retire_done` goes from 17.6 tests a request to 3.3, and `Session.__write`
from 9.9 to 2.2. A take in a run costs 6 instructions on the unique arm, not
16.

## Tests

`TestSelfHostTestedTake` has two shapes. `retire` takes the fields of a
record that outlives the run. `lock` ends a run where its donor dies and
becomes the next construction's token. That is the `admission_env` shape.
The program calls each with a unique and a shared argument.

The x86-64 leg pins 10 and 4 uniqueness tests (main: 12 and 9). It also runs
the program under the leak census. Without the `token_kept` change, the certifier refuses
`lock`.

## Next lead

The 56 tests that remain are mostly single takes, one for each block. In
`Streams.__signals`, `__stream_windows` and `__stream_data_blocked`, the
record is rebuilt with every field read back at its slot. But the slot values
come through phis from an earlier record, so no keep forms.
