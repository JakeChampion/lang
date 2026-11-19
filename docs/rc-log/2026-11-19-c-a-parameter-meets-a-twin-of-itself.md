# A parameter meets a twin of itself

2026-11-19. Each target. The change is in `rccertify`.

## The gap

A push onto a parameter gives a twin of that parameter. The twin holds no
unit if the push kept the box, and one unit if the push made a new box. A
join of the push and the parameter itself came to the phi as a twin and a
plain count, so the meet gave maybe. `fern.append_line` is an example: it
pushes onto its parameter on one arm only.

## The change

- A parameter is its own box. As a phi input, it is a twin of itself with
  one side (`phi_input`).
- A twin with one side is a plain count. It meets a twin of another key as
  that count (`meet_twin`).
- The token rule and a test against another parameter read a twin with one
  side as its count.

A new case, `push-joins-its-parameter`, checks the join. Its variant
without the retain before the return stays unproved, because a return reads a
twin with two sides as one count.

## Measured after

The census rows, re-banked in `.github/certify-baseline.txt`:

| row | before | after |
|---|---:|---:|
| `x86_64/fern_main.unproved` | 1,169 | 1,162 |
| `wasm32/checker.unproved` | 158 | 158 |
| `x86_64/checker.refuted` | 5,184 | 5,184 |
| `x86_64/checker.weakened` | 233 | 233 |
| `x86_64/checker.missed` | 250 | 250 |

The 2,749 programs under `conformance/`, `tests/`, `examples/` and the
utilities give 0 findings on x86-64, arm64 and wasm32. Each program exits as
it did before. The unproved values over those programs:

| target | before | after |
|---|---:|---:|
| x86-64 | 33,464 | 33,368 |
| arm64 | 30,577 | 30,481 |
| wasm32 | 13,840 | 13,788 |
