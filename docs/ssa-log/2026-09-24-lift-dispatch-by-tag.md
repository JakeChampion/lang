# The lift dispatches on kind tags, not kind names

`ssa_lift.lift_impl` chose each op's arm by comparing its kind name against
a chain of string literals, and every binary op also went through
`bin_sym`, another chain of 23 string compares. In a stage-2 profile of
compiling `lexer.fern` that was 377 K `__fern_str_eq` calls from
`lift_impl` and 309 K from `bin_sym`.

The arms now compare the op's integer tag against tags looked up once per
function with `ir.kind_id`, and `kind_class` gains bit 3 for the binaries,
worked out once per kind with the rest of the class. The kind name is still
passed to the helpers that build instructions from it.

| | main | this change |
|---|---|---|
| `lexer.fern` compile, stage-2 (self-host-built) compiler, Ir | 1,677,738,820 | 1,656,624,821 (−1.3%) |

The stage-2 compilers' output for `lexer.fern` is byte-identical, and so is
the assembly the native-built compilers emit for `checker.fern` on x86-64
and arm64.
