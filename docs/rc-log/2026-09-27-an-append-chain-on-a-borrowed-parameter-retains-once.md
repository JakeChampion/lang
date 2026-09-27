# 2026-09-27 — an append chain on a borrowed parameter retains once (#9440)

```fern
function le32(b: i32[], v: i32): i32[] {
    b = b.append(v & 255);
    b = b.append((v >> 8) & 255);
    b = b.append((v >> 16) & 255);
    return b.append((v >> 24) & 255);
}
```

The first append takes `append_borrowed`: the non-consuming push, which grows
the caller's box in place, then a retain of the result for the unit the plan
gives it. That retain left the caller's box at two counts, so the second
append's `rc_is_unique` read it as shared and copied the whole buffer. One
copy per call, so a caller that threads an accumulator through such a helper
is quadratic in its output.

## The change

`ssarc.block_chains` marks a LINK: an append result whose only use in the
whole function is as the receiver of a later append in the same block, which
the plan moves it into, where the chain started at an append whose retain was
deferred onto a borrowed parameter. A link keeps the unit the plan gives it,
encoded at run time instead of taken:

- still the parameter's box: the unit is owed, and the next push is the
  non-consuming one again (`borrowed_push`);
- any other box: a fresh one of the frame's own at one count, and the next
  push is the consuming one.

The append that ends the chain takes the retain `append_borrowed` always took.
`Plan` is unchanged. The sole-use rule is what makes that sound: no path can
drop a link, or read it anywhere the encoding is not understood.

## Measured

Callgrind, x86-64-linux, `-O`, 2000 calls of `le32` onto one accumulator:

| build | copies (`__arr_push_shared_count`) | bytes copied | retired instructions |
|---|---|---|---|
| native | 0 | 0 | 557,518 |
| self-host, main | 1,988 | 63,918,400 | 20,571,695 |
| self-host, this change | 0 | 0 | 261,970 |

`FERN_SEM_IR=` (the AST lowering) still copies: `chain_fill(100)` reads 4092
there against 4000 here, and that fixture is the pin.

## Not covered

- A chain through a loop (`while … { b = b.append(x) }`) is a phi, not a
  sole use, so #9526's `append_raw` still copies once per call (1,988 copies
  at n = 2000, unchanged). Extending the encoding to phis whose inputs are
  all links of one root is the next step there.
- A `.with` chain on a borrowed parameter still retains up front and copies
  once per call: there is no non-consuming `with` helper to defer onto.
