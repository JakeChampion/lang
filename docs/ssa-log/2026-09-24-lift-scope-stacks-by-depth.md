# The lift's scope stacks keep a depth

`ssa_lift.lift_impl` tracks open `if` / `block` / `loop` scopes in nine
parallel stacks. A scope exit popped all nine with `drop_last`, which
rebuilds the array without its last element one append at a time. Lowered
code nests scopes deeply (a `match` opens a block per arm), so in a stage-2
profile of compiling `lexer.fern` each pop cost about 1,680 instructions and
the scope exits about 43 M in all.

The stacks now keep an explicit depth, `sc_n`, the way the per-scope slot
rows beside them already did: an entry overwrites the slot at that depth
(`put_at`) and an exit lowers it. `drop_last_s` had no other caller and is
gone.

| | main | this change |
|---|---|---|
| `lexer.fern` compile, stage-2 (self-host-built) compiler, Ir | 1,929,964,517 | 1,878,468,725 (−2.7%) |

The two stage-2 compilers' output for `lexer.fern` is byte-identical.
