# The rc plan is verified once, where it is lowered

`ssaunits.plan` ended by running `ssaunits.verify` on the plan it had just
built, and `ssarc.lower` runs the same `verify` on the plan it is handed, as
part of `validate`. Every caller that makes a plan (`semlower.produced_plan`
and the census's `stage_of`) hands it straight to `ssarc.lower` with the
same function and modes, so each function was verified twice. A verify
re-runs `ssasem.analyze`, the liveness flow and a full replay: in a stage-2
profile of compiling `lexer.fern`, each of the two passes cost about 115 M
instructions over 76 functions.

`plan` no longer verifies. `lower` stays the gate, since it is where a plan
is trusted, and the rc tests already pin that it refuses a plan that does
not replay (`missing successful unit plan`, `missing or duplicate entry
step`). A plan that failed verification now fails at lowering, which
reports it the same way.

| | main | this change |
|---|---|---|
| `lexer.fern` compile, stage-2 (self-host-built) compiler, Ir | 1,766,063,154 | 1,643,903,318 (−6.9%) |

The stage-2 compilers' output for `lexer.fern` is byte-identical.
