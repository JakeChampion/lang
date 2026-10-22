# 2026-10-22 — the intrinsic lookups skip plain names

`checker.intrinsic_result`, `checker.intrinsic_params` and the raw-floor
table. Refs #8171.

## The shape

`check_call_expr` asks `intrinsic_result` about the callee of every call
expression, 302 K times per self-compile. Most callees are functions
the program declares. Each of those ran the whole chain before it fell
through: `raw_floor_find` with its three prefix tests, then about 90
name compares. That was 92 M Ir of self time, and 250 M Ir inclusive.

## What changed

Every runtime intrinsic is named `__…`. Both lookups now answer "not an
intrinsic" at once for a name without that prefix.

`raw_floor_find` then built the raw-floor signatures as records, and
dropped them again, on 20 K of its calls, 15 K of them hits. The builds
cost 44 M Ir and the releases 71 M Ir. The table is now built once per
module, with the function table (`SigTable.raw`), and `raw_floor_at`
answers an index into it. A module environment is built about 450 times
per self-compile. Rows the lookup parses on a hit, as `builtin_sig`
does, were the other option, and they do not pay: `type_from_name`
costs 1.5 to 2.2 K Ir per type, so the parses cost about 82 M Ir.

Each lookup answers as before.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is one frozen copy of main at ddc5b11.

| subject | #12963 at d86c4a2 | `__` test | table built once |
|---|--:|--:|--:|
| `compiler/fern.fern`, the whole compiler | 73.699 G | 73.580 G | 73.459 G (−0.33%) |

Each whole-compiler binary is byte-identical to the base compiler's.

## What is left

`builtin_sig` still scans its 242 rows on each call.
The same table can carry those rows, parsed once.
