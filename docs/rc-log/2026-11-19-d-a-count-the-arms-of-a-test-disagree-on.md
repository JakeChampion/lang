# A count the arms of a test disagree on

2026-11-19. Each target. The change is in `rccertify`.

## The gap

A push onto a field of a borrowed parameter, such as
`imp.libs.append(x)`, reads the field and tests whether the parameter has
another holder. Where it has, the lowering retains the field before the push
and releases it after the push, under a second test of the same word. The
join between the two tests met 1 and 0, so the field was unproved. The push
result was unproved too, because a push gave a twin only of a parameter.
Over `fern_main`, 151 of about 1,160 unproved values had this form.

## The change

- The two arms of a test can reach a join apart. There, a count that the
  arms disagree on is a twin of the word that the test reads (`edge_sides`,
  `side_twin`). A later test of that word against zero decides the side.
- A push onto any box that is not a token or such a word gives a twin of that
  box (`box_key`).
- A store of null can clear the slot that the box was read from. Then the unit
  of the slot goes to the value that is the box on that path (`named_box`).
- A value defined again reads each twin keyed on it as one count
  (`forget_key`).

The case `retained-on-one-arm-released-on-another` has no unproved value now.
Its new variant releases on the other arm and gives two findings. Two new
cases check a push onto a field: the balanced form, and a slot left uncleared,
which stays unproved.

## Measured after

The census rows, re-banked in `.github/certify-baseline.txt`:

| row | before | after |
|---|---:|---:|
| `x86_64/fern_main.unproved` | 1,163 | 1,016 |
| `wasm32/checker.unproved` | 158 | 127 |
| `x86_64/checker.refuted` | 5,190 | 5,210 |
| `x86_64/checker.weakened` | 233 | 235 |
| `x86_64/checker.missed` | 248 | 226 |

The 2,749 programs under `conformance/`, `tests/`, `examples/` and the
utilities give 0 findings on x86-64, arm64 and wasm32. Each program exits as
it did before. The unproved values over those programs:

| target | before | after |
|---|---:|---:|
| x86-64 | 33,368 | 32,861 |
| arm64 | 30,481 | 29,974 |
| wasm32 | 13,788 | 13,312 |
