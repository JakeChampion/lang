# 2026-10-12 — op kinds named by constants

`ir.K_*`, `parser.is_reserved_struct_name`. Refs #8171, #8258. No emitted
byte changes.

## What changed

- **Op kinds the compiler names are constants.** The compiler wrote 283
  `kind_id("name")` calls to get an op kind's id, in op constructors and in
  per-op predicates such as `is_foldable_binary_kind`. Each call is a length
  switch and a chain of string compares, and the whole compiler's
  self-compile made about 2.7 M of them. `ir.fern` now declares a `K_<KIND>`
  constant for each of the 65 kinds named that way. A const read compiles to
  an immediate. `TestSelfHostIRKindConstants` pins each constant to
  `testdata/ir-kind-ids.txt`. `kind_id` stays for names that are data, such as
  an SSA binary's operator string.
- **`is_reserved_struct_name` answers a lowercase name at once.** It built its
  29-name list on every call and scanned it: 163 K calls, about 70 M Ir. Every
  reserved name starts with an uppercase ASCII letter, which the parity test
  now checks.

## Measured

The whole compiler under callgrind. Both compilers compiled the same frozen
copy of the tree, each on a warm memo of its own.

| | Ir |
|---|--:|
| before (5d4f79c, the branch with #12155's changes) | 72.857 G |
| both changes | 71.350 G (−2.07%) |

Most of the drop is not the lookups themselves. By module:

| | change |
|---|--:|
| `ssaunits` | −1,190 M |
| `ir` | −271 M |
| `__fern_str_eq` | −61 M |
| `util` | −59 M |
| `ssa_lift` | −33 M |

The `ssaunits` share is `needed_past_slot` (−407 M), `reaches_root`
(−605 M) and `reads` (−386 M). This change touches none of them. Built
before it, the three are out-of-line calls: `needed_past_slot` asks
`reaches_root` about every value anchored to the root, 17.2 M calls for
41 K questions, and `reaches_root` asks `reads`. Built after it, `seminline`
places them differently, and the same 17.2 M checks cost about 1.2 G less.
The same three functions moved by about +1.08 G in the first A/B of
2026-10-12-b, the one with the wrong base. So about 0.45 G (0.6%) of this
row is the constants and the reserved-name check. The rest is `seminline`'s
placement of a quadratic loop, and an unrelated change could move it back.

## Then: one pass in `reaches_root`

`reaches_root` scanned each value's chain twice: `reads` to find the root,
then `first_hop` for the projection before it. One pass now finds both. On
top of the row above it measured 71.350 G → 71.327 G (−0.03%), because in
this layout the anchored-loop call is spliced into `needed_past_slot` and
only 880 K calls stay out of line. In the other layout the second scan was
`reads`' 329 M. The RC plans for `checker.fern` and `fern.fern` are
unchanged.

## Open

`needed_past_slot` walks every value anchored to the root for each
question: about 410 per call. `reaches_root` returns false for all but 0.2% of
them, which is 40 K `used_after` calls against 17.2 M checks. A question
answered without that walk would make this loop cheap whatever
`seminline` does with it. It decides what an RC take may move, so the
rewrite needs its own proof that it gives the same answer.
