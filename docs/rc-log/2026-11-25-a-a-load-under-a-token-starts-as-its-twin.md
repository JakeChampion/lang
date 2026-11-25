# A load under a token starts as its twin

2026-11-25. Each target. The change is in `rccertify`.

## The gap

`ssa_lift.lift_impl` carries `stack`, an `i32[]`, round its loop. Each arm
of the loop gives `stack` to a helper and reads it back from the record that
the helper returns:

```
let lv = lift_prod_mem(kt, tag, k, op, nvals, stack, []);
stack = lv.stack;
cur_insts = splice(cur_insts, lv.insts);
```

The lowering reads `lv.stack` at the join of the reuse token of `lv`. It
retains the field where the token is null, and it releases the shell of
`lv` where the token is the box. Thus the field holds one unit on each path.

The walk without an assumption reads the field as a borrow: 0 units. With
the retain on one edge, the join meets 0 and 1 as maybe.

The walks under each value of a token prove such a field. But each walk
assumes one token, and each arm of the loop has its own token. Under one assumption, the other
arms still bring maybe round the loop. So the loop phi `stack` and the chain
of phis that joins the arms stayed unproved.

The lead of the entry before this one was the twin rule of `phi_input`. That
rule is not the cause.

## The change

- A load in a block that the token's block dominates can have a different
  origin under each value of the token. The load starts as a twin of the
  token, with the count of its origin under each value (`token_loads`).
  Where a value does not reach the load, that side is absent.

- `token_loads` uses the same pruned walks as the walks under each value
  (`pruned_walk`). It looks only at the tokens that decide an unproved
  value. Where two tokens give one load two twins, the load keeps its
  origin.

- The walk without an assumption then runs again with these twins. The
  tests of each token decide the side of its twins, and the token's block
  reads each twin as one count. A finding of the first walk stays a
  finding.

`loop-takes-a-field-under-each-arms-token` of
`TestSelfHostRcCertifyCases` holds the shape: two arms, each with its own
token. Without the change it has 1 unproved value. Its variant without the
retain on one arm stays unproved.

## Measured

`scripts/certify-census`, before and after, re-banked in
`.github/certify-baseline.txt`:

| row | before | after |
|---|---:|---:|
| `x86_64/fern_main.unproved` | 937 | 726 |
| `wasm32/checker.unproved` | 123 | 91 |
| `x86_64/checker.refuted` | 5,457 | 5,457 |
| `x86_64/checker.weakened` | 231 | 231 |
| `x86_64/checker.missed` | 220 | 220 |

`lift_impl` goes from 52 unproved values to 10.

A trap: the second walk alone moved 19 mutants from refuted to weakened.
Take a mutant without the retain on the null edge. The first walk gave it a
definite 0, and the twin of 1 and 0 gave only maybe. The first walk's findings now
stay in the report, and the mutant rows do not move.

A compile of `fern_main` with `FERN_RC_CERTIFY_MEMO=0` takes 1m44s, and
1m39s before. With the memo, a compile pays this only for a function that it
has not certified before.

## Next lead

Of the 10 values left in `lift_impl`, the first is `v7533`. It is a reuse
token of the box `v4196`, which the loop carries, and `__fern_alloc_reuse`
takes it after the join of `b923`. Examine how the loop phi of `v4196`
meets the token.
