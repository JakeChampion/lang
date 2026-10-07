# 2026-10-07 — a schema says once whether it nests a function value

`semrecords.Record.nests_func`, `semrecords.Enum.nests_func`,
`semsource.fields_nest_func`, `ssasem.field_placement_error`. Refs #8171.

## Before

`ssasem.analyze` refuses a function value nested in a record or variant field
(in an array, tuple or map that holds one), which no release walk reaches.
`field_placement_error` checked this by walking every field of every record and
enum in the function's schema table, every time a function was analysed. Each
function's table carries every schema it names, transitively. On a compile of
`checker.fern` that came to 2,281 analyses, each asking `holds_func` of 367
fields and `semtypes.is_env` of 47 records. `is_env` fed a binding that nothing
read. Together with the loop that was about 48 M Ir, 0.33% of the compile.

## Change

A schema's fields do not change after it is made, so the verdict belongs to the
schema. `semsource` sets `nests_func` where each of the four kinds of schema is
built: a record, a closure environment, a declared enum and a builtin enum. It
uses `ssasem.nests_func`, now public. `field_placement_error` reads one boolean
per schema.

The old per-field test, `holds_func(t) && !is_func(t)`, is `nests_func(t)`:
`nests_func` matches only an array, a tuple or a map, never a bare function
type. An array or tuple of function values is admitted, as before. Only a
function type used as a map key nests one, and the checker does not let a
source program express that, so the refusal is unchanged and still unreachable
from source.

The e2ecompiler programs that build a schema by hand state `nests_func: false`
next to the `views: false` they already state.

## Measured

`checker.fern` to an x86-64 binary under callgrind. Each side's stage 3 is
built by its own stage 2, with no `-g`. Both stage 3s rebuild themselves byte
for byte.

| | main (cdf55f22c) | this change |
|---|--:|--:|
| total Ir | 14,538,423,008 | 14,490,785,876 (−0.33%) |

Byte-identical against a compiler built from main: `checker.fern` for
x86-64, arm64 and wasm32-wasi, and `fern.fern` for x86-64.

## Next

Still in this area: `util.verify_on` reads `FERN_IR_VERIFY` from the
environment 10,022 times per compile (32.5 M Ir), twice per `ssasem.analyze`
and once each in `ssadeps.analyze`, `ssarc.validate` and
`irverifygate.verify_or_refuse`. It is a property of the compiler process,
read where no state carries it.
