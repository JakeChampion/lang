# A runtime divisor shares its quotient

`ssaquotient.shared_quotients` gave `x / c` and `x % c` one division only for
a constant `c`, and only inside one block. It now pairs any divisor that is
the same SSA value, and pairs across blocks: the divisions and remainders of
one dividend by one divisor are grouped in preorder of the dominator tree
under a leader that dominates the rest. A group with both kinds computes
`q = x / y` at its leader; a remainder becomes `x - q * y` and a division a
copy of `q`. Constants 0, +-1 and powers of two stay a shift and a mask.

Nothing needs excluding at run time: the language defines `x / 0` as 0,
`x % 0` as x, `MIN / -1` as MIN and `MIN % -1` as 0, and `x - q * y` gives
each of those under wrapping arithmetic.

The trap: `ssadeps.dominates` numbers `pre` and `post` from one clock, so a
preorder number runs to 2n, not n; an array indexed by it is 2n long.

shuf's draw (GNU's randint) takes `excess % n` and `num % n` in its loop and
divides both by n in the block that returns: four hardware divides per draw
became two. `shuf -i 1-1000000`, x86-64, 60 runs: 114.5 ms to 106.5 ms.
Pinned by `TestSelfHostConstDivisorShapesX86_64` (`dmxyuw`, `dmxyb`) and
`conformance/cases/shared_quotient_runtime`.
