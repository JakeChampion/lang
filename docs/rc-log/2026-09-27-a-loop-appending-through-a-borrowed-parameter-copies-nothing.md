# 2026-09-27 — a loop appending through a borrowed parameter copies nothing (#9526)

```fern
function append_raw(out: u8[], s: string): u8[] {
    let bs: u8[] = out;
    let i: i32 = 0;
    while (i < s.len()) { bs = bs.append(s[i]); i = i + 1; }
    return bs;
}
```

`coreutils/echo.fern`'s accumulator helper. The loop carries `bs` in a phi,
and the plan gave that phi a unit by retaining `out` on the entry edge. The
caller's box then stood at two counts, so the first push copied it whole:
one copy per call, O(n²) bytes over a caller that threads its output through
the helper.

## The change

#10351's append-chain links become a function-wide set (`ssarc.links_of`).
A link is an append onto a parameter whose retain was deferred, an append
onto a link, or a phi whose every input is the parameter or a link of it. It
carries its planned unit encoded at run time: still the parameter's box
means the unit is owed, and any other box is the frame's own at one count.
Candidates are removed until every supply naming one moves it into an
append's receiver, a link phi, or the return. A drop, a hand-off, a retain,
or a parameter read anywhere else removes it. What changes in the lowering:

- the entry edge no longer retains the parameter into a link phi
  (`owed_on_entry`);
- the return takes the owed retain when the value is still the parameter's
  box (`take_owed`).

The caller-side half is `ssaunits.grow_rows`. A parameter grown through a
phi or an earlier append now gets a grow row, so a caller that keeps its own
binding brackets the call and the callee copies. Without it, `loop_kept`'s
kept array read back what the callee pushed.

## Measured

Callgrind, x86-64-linux, `-O`. `n` calls append a 4-byte string each:

| n | native | self-host, main | self-host, this change | copies main → here |
|---|---|---|---|---|
| 1000 | 968,182 | 5,315,876 | 163,621 | 989 → 0 |
| 2000 | 2,937,275 | 20,629,923 | 323,994 | 1,988 → 0 |
| 4000 | 9,875,505 | 81,258,059 | 644,668 | 3,987 → 0 |

It is linear now, where main quadrupled per doubling. Native still copies
once per call (its borrow inference does not reach this shape) and stays
superlinear.

`coreutils/echo` with that many numeric operands, same instrument, output
md5 identical across all three builds:

| operands | native | self-host, main | self-host, this change |
|---|---|---|---|
| 200 | 129,213 | 320,789 | 133,049 |
| 800 | 761,983 | 3,626,869 | 513,077 |
| 1600 | 2,252,562 | 14,132,220 | 1,050,104 |

## Pinned

In `TestSelfHostSemanticSourceRC`:

- `loop_fill(100)` copies nothing: 4000, where the AST lowering reads 4092.
- `loop_kept` is the caller that keeps its array. It fails on output with
  the grow-row tracing removed.
- `loop_early` returns the untouched parameter on one path.
- `loop_drop` drops the accumulator, so it stays an ordinary owned value.

## Still open

The rule for a root is conservative. A parameter the frame reads anywhere
other than a deferred append or a phi input is not a root, even when the
read comes before the loop. `with` still has no non-consuming helper to
defer onto.
