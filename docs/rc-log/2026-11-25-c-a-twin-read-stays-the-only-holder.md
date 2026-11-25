# A twin read stays the only holder of its slot

2026-11-25. Each target. The change is in `semsink`. Refs #14440.

## The shape

```fern
@noinline
function retire(s: Ss, i: i32): Ss {
  let st = s.list[i];
  if (st.closed) {
    return s;
  }
  let cur = Ss { ...s, retired: s.retired + 1 };
  if ((st.id & 1) == 1) {
    let t = (st.id & 3) as i32;
    let init = initial(cur, t);
    ...
  }
  cur = Ss { ...cur, list: cur.list.with(i, cur.list[i].close_it()) };
  return cur;
}
```

This is `streams.Streams.__retire_done` with its helper
`__initial_streams` spliced. The helper has an `if`, so its splice adds a
join. `TestSelfHostRereadKeepsTheTwinRead` holds the program.

## The cause

The entry block reads `s.list` (`v2`) and `s.list[i]` (`v3`). The record
update reads `s.list` again as `cur.list` (`v8`), in the next block. `v8`
is live until the last `with`.

The early `sink` read `s.list[i].id` again in the branch: a new
`record_get 0 v0` and `array_get` in place of the use of `v3`. That is a
third read of the `list` slot of `s`, while `v8` is live into the branch. The
plan then did not take `v8` out of `s`. It took the other fields of `s`.
The last `with` copied the list (`arr_slice` and `arr_inc_elems`) and made a
new `St` box on each call.

`child_kept_across` did not refuse the read. It looks at the children of the
first read of `v`. `projection_tree` makes two reads of a slot twins only
in one block. `v2` and `v8` are in different blocks.

## The change

`semsink.twin_kept_across` refuses a new read in a block when a
twin read from another block is live into it. It looks at `v` and at
each ancestor of `v`. An array parent does not count, because an element
read is not a field slot. `read_above` uses it next to `child_kept_across`.

## Measured

x86-64, `__heap_alloc_count` over 1,000 calls:

| Program | main `139d55ed` | after |
| --- | --: | --: |
| `TestSelfHostRereadKeepsTheTwinRead` | 2,007 | 7 |

HTTP/3 probes, allocations a request:

| Build | serial | multiplexed |
| --- | --: | --: |
| main | 49.69 | 28.67 |
| after | 49.69 | 28.68 |
| main, each method spliced | 50.56 | 29.65 |
| after, each method spliced | 47.69 | 27.65 |

"Each method spliced" is a local patch to `seminline.splice_plan`, not
committed. It admits each method that `other` admits for an `unnamed`
method. A bisection over its 508 names named only
`streams.Streams.__initial_streams`: 52.56 a request when it was spliced
alone.

## The traps

- The graph name of a method in this pass is `streams__Stream.__sent`, not
  the symbol `streams__Stream____sent` or the source name
  `streams.Stream.__sent`. A forced splice that names it in the wrong form
  changes nothing and looks like a null result. Check the emitted assembly
  for the call.
- The bisection is not monotone. Half of the 508 names gave 51.56, which is
  more than all of them (50.56). Other splices hide some of the cost.
- A splice of `streams.Stream.__sent` alone costs nothing on main now.
  #17027 removed that cost.

## Next lead

With this change, a splice of each method saves 2.00 a request on the
serial probe. It saves 1.02 on the multiplexed probe. The next step is to admit
more methods in `splice_plan` and measure the compiler and the HTTP/3 probes.
