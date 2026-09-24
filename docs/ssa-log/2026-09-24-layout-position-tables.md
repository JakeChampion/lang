# The layout pass and the dependency verifier look blocks up by table

`ssalayout` found a successor's position by scanning every block, and asked
for the same block's successors again in `pending_edges` and in `release`,
once per region per chosen block. `representative` also re-ran `outermost`,
which walks every candidate header, for each block position, although its
answer depends only on the header. `ssadeps.verify` scanned every block for
each phi operand's predecessor.

`ssalayout`'s `Loops` now carries the id-to-position table and each block's
successor positions, built once; `representatives` works out which headers
are outermost once per region. `ssadeps.verify` reads the position table it
already builds. `ssalayout`'s `successors` and `block_index` and `ssadeps`'s
`block_index` are gone.

| | main | this change |
|---|---|---|
| `lexer.fern` compile, stage-2 (self-host-built) compiler, Ir | 1,507,635,909 | 1,490,189,967 (−1.2%) |

The stage-2 compilers' output for `lexer.fern` is byte-identical, and so is
the assembly the native-built compilers emit for `checker.fern` on x86-64
and arm64.
