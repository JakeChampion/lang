# A literal is never unique

2026-11-25. Each target. The change is in `rccertify`.

## The gap

This is shape B of `2026-11-25-a-the-stack-of-lift-impl-waits-on-token-fields.md`.
`ssa_lift.lift_impl` carries the current `STerm` (`v4196`) round its loop.
It starts as a literal, so the loop phi is free or 1. Each reuse of it is
this shape:

```
v7530 = call __fern_rc_is_unique v4196
brif v7530 b921 b922
b922:
  v7531 = call __fern_rc_dec v4196
v7533 = phi v4196 v7532        // the token: the box or 0
```

`token_fed` needs a plain count of the box on each arm. The box is free or 1
on the unique arm and free or 0 after the release. Thus the token met free
or 1 with 0, and the reuse took a maybe. These were the last 10 unproved
values of `lift_impl`.

## The change

- A uniqueness test skips an immortal box, so a literal is never unique. On
  the edge where `__fern_rc_is_unique` is true, a count met with free is
  that count (`unique_on_edge`).

- A box that is free or 0 hands a token nothing. On the zero arm,
  `token_fed` reads it as 0.

- The first rule applies only where the false edge releases the box with
  `__fern_rc_dec`. Without that condition, the rule alone raised the census
  from 781 to 785. In `semsink.reread` and `literate.expand`, the false edge
  keeps the box, so it stays free or 1. The plain count of the unique arm
  then met it as maybe, and the walks under each value of the token no
  longer proved it. The second rule alone moves no count.

This is the trap that the shape-A entry records: a rule on the unique edge
alone makes the two arms meet worse. Both rules together give each arm a
plain count.

`unique-test-of-a-literal-phi-feeds-a-reuse` of `TestSelfHostRcCertifyCases`
holds the shape. Without the change it has 1 unproved value. Its variant
without the release gives the finding "leaked where it dies of v5".

## Measured

The census of `fern_main` on x86-64, on the same tree, with the certifier of
main and with the change:

| function | before | after |
|---|---:|---:|
| `ssa_lift__lift_impl` | 10 | 0 |
| `ssarc__block_body` | 4 | 0 |
| `p256____point_mul` | 2 | 1 |
| `elf__elf_append_debug` | 1 | 0 |
| `fern__darwin_dwarf` | 1 | 0 |
| `literate__expand_file` | 1 | 0 |
| `semsource__renumber` | 1 | 0 |
| `ssaprepare__with_selects` | 1 | 0 |
| all functions | 781 | 761 |

No function goes up, and no function has a finding.

`scripts/certify-census` gives `x86_64/fern_main.unproved` 761, re-banked
in `.github/certify-baseline.txt`. `wasm32/checker.unproved` stays at 91.
The mutant census of `checker.fern` is the same with the certifier of main
and with the change: 5,464 refuted, 233 weakened, 219 missed. These rows
moved on main with its sources, so the change does not pin them.

The 2,871 programs under `conformance/`, `tests/`, `examples/` and
`coreutils/` give 0 findings on x86-64 with both certifiers. Each program
exits as it did before. Their unproved values go from 36,268 to
35,862.

## Next lead

`arm64_native__macho_sect` now has the most unproved values: 44. The inlined
pushes onto its borrowed parameter `buf` (`v0`) run in a loop. The loop phi
`v282 = phi v0 v294` meets the parameter with the result of a push, and a
test against `v0` (`v291 = eq v282 v0`) selects the push. This is
`push-joins-its-parameter` round a loop. Examine how the twin of `v0` goes
round the loop phi.
