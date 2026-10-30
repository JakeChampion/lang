# 2026-10-30 — a body no larger than its call is spliced

`seminline` spliced a small function only when it was a leaf, an updater, an
unpacker, `@inline`, or called once. A body of a few instructions that reads a
cell or returns a string or an enum is none of these. Thus such a body stayed
a call at each of its call sites.

## The change

- `seminline.grown_tiny` takes a function of at most 4 instructions with no
  loop, no recursion and no lent view. It takes the function only after no
  callee of its own is still to be spliced into it.
- These splices run as rounds of their own after `splice_rounds` converges.
  This keeps their copies out of the caller budget that a larger splice
  needs.
- `splice_rounds` now returns the bodies that the grown rules add (forced,
  unpacker and tiny) in `sp.once`. Before, only the planned set reached
  `spliced_away`. A grown body that every caller took was thus still lowered
  and reported as `produced`.

## The limit

The self-compiled compiler (`fern.fern/stage2` in
`.github/selfhost-driver-sizes.txt`) measures the cost. On main it is
15,797,824 bytes.

| Limit | stage2 bytes | `shuf -i` instructions | `nl` instructions |
|--:|--:|--:|--:|
| none (main) | 15,797,824 | 555,221,220 | 252,830,410 |
| 4 | 15,844,272 (+0.3%) | 497,130,222 | 242,821,748 |
| 6 | 15,995,680 (+1.3%) | 497,130,276 | 242,822,440 |
| 8 | 16,189,056 (+2.5%) | 482,111,240 | 241,818,149 |

At a limit of 8, `io_buffered.BufWriter.error` is spliced into `gnu.settle`.
That gives `shuf -i` 3% more, but the compiler grows 2.2% more. A limit of 5
that does not count parameters and literals gives 2.3% growth and no more
speed. The limit is 4.

The first version ran these splices with the other rounds. Their copies grew
`uniq`'s `main` and `std/http`'s `parse_request_framed_from`. The larger once
splices into those callers then did not fit the 1500-instruction caller
budget. `uniq` was 4% slower, and the framing probe boxed 2 values for each
parse.

## Measurement

x86-64, `-O`, instructions from callgrind. The `seq.txt` input is
`seq 1 1000000`. The prose input is the 42 MB `fmt` bench file.

| Workload | Before | After |
|---|--:|--:|
| `shuf -i 1-1000000` | 555,221,220 | 497,130,222 |
| `nl` of `seq.txt` | 252,830,410 | 242,821,748 |
| `fmt` of prose | 6,098,641,370 | 6,069,841,330 |
| `fmt -w 40` of prose | 6,392,946,544 | 6,344,946,504 |
| `fold -w 40` of prose | 511,355,619 | 511,271,869 |
| `cat -n` of `seq.txt` | 212,078,934 | 212,075,733 |
| `uniq` of `seq.txt` | 193,865,975 | 193,862,574 |

No workload of the ten measured is slower. The output of each workload is
byte for byte the same.

## Tests

`TestSelfHostTinySplice` has a free function and a method with three callers
each. Each of the two returns a string from a cell. No caller calls either
one, and the free function loses its body. The program runs on each target
under the leak check and the sanitizer.

`TestSelfHostInlineScalarArrayLeaf` checks that a string-array append is not a
leaf. Its `add_text` was 3 instructions, so the tiny rule took it. It is now
larger than 4 instructions, so the test still checks the leaf rule.

## Next lead

A method is never in the once set, because `semsource.outside_unnamed`
refuses each name that contains a `.`. A method that only direct calls reach
is the same as a private function. With a tiny limit of 8, methods in that
set make `cat -n` 1.9% faster and `nl` 1.3% faster.

Two things block it. First, the HTTP framing probe makes 2 allocations for
each parse, not 0. `__framed_body` grows from 44 to 436 instructions as its
own callees are spliced, and `saves_box` allows 400. Without that cap the
probe makes 0 again.

Second, `uniq` is 4% slower. The methods spliced into its `main` leave no room in the 1500 budget for `run`, which holds the hot loop.
A once splice adds no code in total, so the caller budget is the limit to
look at.
