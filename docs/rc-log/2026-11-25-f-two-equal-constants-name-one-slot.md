# Two equal constants name one slot

2026-11-25. Each target. The change is in `rccertify`.

## The gap

`watbin.enc_instr` had 20 unproved values, the most of any function. Its
first was an element that the code takes from an array:

```
v2149 = const_int 1
v2150 = arr_at rc v2138 v2149
v2151 = call __fern_rc_is_unique v2138
brif v2151 b655 b656
b655:
  v2152 = const_int 1
  v2153 = const_int 0
  v2154 = arr_st 1 v2138 v2152 v2153   // the slot is cleared
b656:
  v2155 = call __fern_rc_inc v2150
```

Where the array is unique, the store of null clears the slot, so the
element takes the unit of the slot. Where it is shared, the element is
retained. Thus it holds one unit on each path.

A take needs the store to match the load (`matches_store`). For an array
slot, the match compared the two indices by value id. The lowering wrote the
index 1 twice, as `v2149` and as `v2152`. So the store took no unit, and the
join met 0 and 1 as maybe.

## The change

`matches_store` compares the indices with `index_key`, as the other matches
of a load and a store do. Two constants with the same value are one key.

`element-taken-through-a-second-constant` of `TestSelfHostRcCertifyCases`
holds the shape. Without the change it has 1 unproved value. Its variant
that stores into slot 2 stays unproved.

## Measured

The census on the same tree, with the certifier of main and with the change:

| row | before | after |
|---|---:|---:|
| `x86_64/fern_main.unproved` | 685 | 602 |
| `wasm32/checker.unproved` | 84 | 67 |

40 functions go down, and no function goes up. The largest are
`watbin__enc_instr` (20 to 0), `seminline__inline_leaves` (7 to 0),
`x509__parse` (10 to 4) and `semsource__call` (4 to 0). No function has a
finding. Both rows are re-banked in `.github/certify-baseline.txt`. The
mutant census of `checker.fern` is the same with both certifiers: 5,392
refuted, 227 weakened, 228 missed.

## Next lead

`ssaunits__carried_pairs` now has the most unproved values: 18. The first is
the loop phi `v2458 = phi v1860 v3299` at `b312`. Examine what its back edge
`v3299` brings into the loop.
