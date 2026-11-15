# A constant field costs less than a mask

2026-11-15. x86-64 and arm64. Follows
`2026-11-15-g-a-field-that-an-arm-tested-keeps-its-word.md`.

## The shape

`ins` of rbtree returns `Node(Red, l, k, v, r)` when the key is already in
the tree. The key and the value are new, and the arm tested the colour of
the node for `Red`.

`kept_bits` kept the tested colour, so the constructor loaded the word and
masked it to the colour byte. The colour is a constant, so it costs less
alone: `Red` is zero, and the word is the key and the value with no load.

## The change

`packed_word` gets two answers from `kept_bits`: one without the tested
fields and one with them. `chosen_bits` uses the tested fields only in two
cases:

- The constructor keeps all of the word.
- The constructor masks a word in any case.

On the unique side of each test of the node in `ins`, the code now does not
read the word of the node.

## Tests

- `TestSelfHostSSASharedArmLoadsTheKeptWord` checks this property. It also
  checks that a side that copies a shared node stores the word in the copy.
  The test looked for a load of the value at its offset before the packing,
  and it failed on main.
- `TestSelfHostNarrowFieldsShareAWord` adds `bump` and `late`. `bump` keeps
  the tested colour and writes a new key. `late` writes a colour after the
  inner match, where the test does not hold.
- `TestSelfHostCrossBlockReuse` failed on main for another reason. The two
  `i32` fields of `Two` now share one word, so no arm fits `M`. The variants
  now hold an `i64`, which keeps a word of its own.

## Measured

The path runs only for a key that is already in the tree. Cachegrind counts
12 fewer instructions for rbtree 400,000 on x86-64.
