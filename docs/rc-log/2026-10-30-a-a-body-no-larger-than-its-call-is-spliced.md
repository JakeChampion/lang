# 2026-10-30 — a body no larger than its call is spliced

`seminline` spliced a small function only when it was a leaf, an updater, an
unpacker, `@inline`, or called once. A body of a few instructions that reads a
cell or returns an enum is none of these. Thus each call to
`io_buffered.BufWriter.error`, which is 8 instructions, stayed a call. It
also released its `Option[IoError]` result through a call.

## The change

- `seminline.grown_tiny` takes a function of at most 8 instructions with no
  loop, no recursion and no lent view. It takes the function only after no
  callee of its own is still to be spliced into it.
- These splices run as rounds of their own after `splice_rounds` converges.
  This keeps their copies out of the caller budget that a larger splice
  needs.
- `splice_rounds` now returns the bodies that the grown rules add (forced,
  unpacker and tiny) in `sp.once`. Before, only the planned set reached
  `spliced_away`. A grown body that every caller took was thus still lowered
  and reported as `produced`.

## Measurement

x86-64, `-O`, instructions from callgrind. The `seq.txt` input is
`seq 1 1000000`. The prose input is the 42 MB `fmt` bench file.

| Workload | Before | After |
|---|--:|--:|
| `shuf -i 1-1000000` | 555,221,212 | 482,111,240 |
| `nl` of `seq.txt` | 252,830,402 | 241,818,149 |
| `fmt` of prose | 6,098,641,362 | 6,056,313,150 |
| `fmt -w 40` of prose | 6,392,946,536 | 6,322,418,269 |
| `cat -n` of `seq.txt` | 212,078,926 | 212,074,268 |
| `uniq` of `seq.txt` | 193,865,951 | 193,859,372 |
| `fold -w 40` of prose | 511,355,611 | 512,171,711 |

`fold` is 0.16% slower. The loop exit in `break_before` has one more register
move. The splices into the option parse of `main` change the allocation
there. The output of each workload is byte for byte the same.

In `shuf`, `gnu.settle` now costs 43 instructions for each line, not 55, and
`BufWriter.error` is gone from the profile.

## Tests

`TestSelfHostTinySplice` has a free function and a method with three callers
each. Each of the two reads a cell and returns an enum. No caller calls either
one, and the free function loses its body. The program runs on each target
under the leak check and the sanitizer.

`TestSelfHostInlineScalarArrayLeaf` checks that a string-array append is not a
leaf. Its `add_text` was 3 instructions, so the tiny rule took it. It is now
larger than 8 instructions, so the test still checks the leaf rule.

## Next lead

A method is never in the once set, because `semsource.outside_unnamed`
refuses each name that contains a `.`. A method that only direct calls reach
is the same as a private function. With methods in that set,
`BufWriter.settle` is spliced into `gnu.settle`, and `shuf -i` loses a further
15M instructions.

But the HTTP framing probe then makes 2 allocations for each parse, not 0.
`__framed_body` grows from 44 to 436 instructions as its own callees are
spliced. That is more than the 400 that `saves_box` allows. Its caller had
room for it under the 1500 budget only while the methods stayed calls. The
limit for a splice that saves a box applies to the grown size, not to the size
that made the body a once body. Fix that first, then let the methods in.
