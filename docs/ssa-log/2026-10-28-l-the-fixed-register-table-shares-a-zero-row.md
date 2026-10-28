# 2026-10-28 — the fixed-register table shares a zero row

`ssafixed.rows`, under `with_clobbers` and `table`. Refs #8171.

## The shape

The fixed-register table keeps one row for each register. A row holds,
for each position, the count of masks before it that name the register.
`rows` built each row with one append for each position, for every
register in the range. Most masks are zero, so most rows were all zero.
The self-compile spent about 740 M Ir in `rows`.

## What changed

`rows` first makes the union of all the masks. A register that no mask
names gets one shared zero row, made with `__alloc_i32`. Only a register
in the union gets its own row of counts. The rows are read only, so the
shared row is safe.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 293eaa55e. The base is main at b0241b2f3,
with #13782.

| subject | main at b0241b2f3 | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 110.209 G | 109.806 G (−0.37%) |

The whole-compiler binary is byte-identical to the base compiler's.
