# 2026-10-02 — the record and enum lookups refuse other types without a scan

`semrecords.find`, `semrecords.find_enum`; `find_in` deleted. Refs #8171.
No emitted byte changes: the `selfhost-emit-hashes` sweep is 1,965 rows
per compiler with 0 differing against a compiler built from main at
e2ed8f3a, and the `checker.fern` binaries are byte-identical.

## What the profile named

`ssasem.holds_view` was 150 M inclusive on the 31.09 G stage-2 compile
of `checker.fern` for 31k calls. It asks `semrecords.find` whether its
type has a record and `find_enum` whether it has an enum, in that
order. `find` answered a struct type through the name index, and any
other type through `find_in`, a scan of the whole table comparing every
record's type with `semtypes.equal` (116 M for 20k calls, every one of
them for a union type). `find_enum` did the same for any type that is
not a union, scanning the enums.

## What changed

A record's type is a struct and an enum's is a union: `semsource`'s
schema walk builds each entry off that declaration and nothing else
constructs one. So `find` answers any type that is not a struct with
"none" at once, `find_enum` any type that is not a union, and `find_in`
goes.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at e2ed8f3a and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 31.09 G | 30.96 G (−0.42%) |
| stage 2, `holds_view` inclusive Ir | 150 M | 15 M |
| stage 2, `semrecords.find` inclusive Ir | 255 M | 135 M |
| stage 2, `semtypes.equal` inclusive Ir | 367 M | 260 M |

## A trap

`x86_gas_trim(s: string)` is called 1.47 M times on this compile, mostly
with a view of the line, and returns `slice + ""`: one copy. Changing the
parameter to `str` to save a second copy at the call measured 54 M
SLOWER (31.14 G): a view handed to a `string` parameter does not copy,
so there was no second copy to save, while every `string` handed to the
new `str` parameters (`x86_gas_top_comma`, trim's `string` callers)
allocated and freed a view box, +37 M in `__fern_alloc` and +8 M in
`__fern_str_view_free`. A `str` parameter is cheaper only where every
caller already holds a view.

## Witnessed

`TestSelfHostSemanticSource*`, `TestSelfHostSemanticProduction`,
`TestSelfHostSemanticAllocationCounts`, `TestSelfHostFrameViews*`,
`TestSelfHostLentViewHandback*`, `TestSelfHostBoundProjection*`,
`TestSelfHostArrStructBoundElem*`, the lint ratchet, `make fmt-check`,
and the emit-hash sweep.

## Next

`semtypes.equal` is still 260 M: `find_union` (748k calls) compares the
union's name and then `equal` compares it again; `find_struct` the same
through the chain. The x86 assembler's first round parses operand text
per op: `x86_gas_mov_rm` is 1,390 Ir per `movq` (198k), of it
`x86_gas_parse_mem` 830 per memory operand (110k) and `x86_gas_reg_w`
170 per register name (285k), `x86_gas_alu_family` 1,290 per op (122k),
`x86_jcc_label_at` 760 per branch (215k). `ssa_lift.lift_impl` is 1.07 G
self: 23% of it is the main loop's back-edge parallel move, some thirty
register rotations and twenty frame-slot copies per op lifted (the
allocator gave each loop-carried value a register one step from its
phi's), and 9% the `es` scan from `lo_c` to `hi_c` over slots at every
block exit (3.6 M iterations), which could walk the `nb` written slots
in slot order instead.
