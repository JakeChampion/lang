# A reuse token is a twin of its box

2026-11-19. Each target. The change is in `rccertify`.

## The gap

A struct update such as `p = P { ...p, a: x }` lowers to a reuse token. The
token is a phi of the box and null. The construction tests it two times.
The first test copies the box where the token is null. The second test
releases the old box, or only its old fields.

The token phi took the unit of the box on one edge. The box kept its unit on
the other edge. Thus the box was unproved at the join of the token.

The walk under each value of a token proved one such update. Two updates in
sequence stayed unproved, because each walk fixes one token. One update in a
loop stayed unproved too, because a loop takes each value of the token on
different rounds.
Over `fern_main`, 1,494 of about 2,790 unproved values had an
`__fern_alloc_reuse` among their inputs.

## The change

- The box of a token takes a twin keyed on the token. On the edge where the
  token is the box, the token takes the unit. On the edge where the token is
  null, the box keeps it (`token_fed`).
- A test of the token against null decides the side of each twin keyed on the
  token (`token_side_on_edge`). A block that defines the token again reads
  each such twin as one count (`forget_tokens`).
- Where the token is not null, the token and its box are one box. A phi input
  of one name can take the unit that the other name holds (`other_name`).
- `__fern_alloc_reuse` moves a unit only if one is held. On a twin, it acts on
  each side apart.

A twin keyed on a token is not a box of a parameter. The rules for a push onto
a parameter and for a test against a parameter keep to parameter keys.

Three new cases in `self_host_rccertify_test.go` check a box that a loop
carries and builds again through its token: the balanced form, a copy arm
that does not release the old box, and a reuse arm that releases it. The case
`reuse-through-a-carried-token-writes-the-slot` now has 1 unproved value, not
2.

## Measured after

The census rows, re-banked in `.github/certify-baseline.txt`:

| row | before | after |
|---|---:|---:|
| `x86_64/fern_main.unproved` | 2,787 | 1,169 |
| `wasm32/checker.unproved` | 312 | 158 |
| `x86_64/checker.refuted` | 5,173 | 5,178 |
| `x86_64/checker.weakened` | 235 | 235 |
| `x86_64/checker.missed` | 255 | 250 |

The certifier finds nothing over `fern_main`.

The 2,749 programs under `conformance/`, `tests/`, `examples/` and the
utilities give 0 findings on x86-64, arm64 and wasm32. Each program exits as
it did before. The unproved values over those programs:

| target | before | after |
|---|---:|---:|
| x86-64 | 45,901 | 33,464 |
| arm64 | 43,113 | 30,577 |
| wasm32 | 23,953 | 13,840 |

A cold compile of `fern_main` with the memo off takes 82 s before and after on
the 4-core container.
