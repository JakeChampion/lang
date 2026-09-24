# The bare-row flags skip the analysis when nothing is borrowed

`ssarc.bare_flags` decides, per borrowed parameter, whether the callee keeps
a reference to it. It ran `ssasem.analyze` first, for every planned
function, and only then looked at the modes. A function with no borrowed
parameter answers `""` whatever the analysis says. In a stage-2 profile of
compiling `lexer.fern` the 66 analyses cost about 34 M instructions, and
most of those functions borrow nothing.

`bare_flags` now returns before the analysis when no mode is a borrow.

| | main | this change |
|---|---|---|
| `lexer.fern` compile, stage-2 (self-host-built) compiler, Ir | 1,766,147,293 | 1,745,836,976 (−1.2%) |

The stage-2 compilers' output for `lexer.fern` is byte-identical.
