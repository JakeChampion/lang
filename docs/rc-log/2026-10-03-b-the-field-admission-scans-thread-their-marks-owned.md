# 2026-10-03 — the field-admission scans thread their marks owned

`fnsigs.strarrfld_scan`, `fnsigs.clofld_scan` and the walks that feed
them. Refs #8171. No emitted byte changes: the stage0-built compiler
before and after emits the fixed older tree (`compiler/fern.fern`
at 1ae9cad, its bindings spelled `let`) and that tree's `checker.fern`
byte for byte.

## What the profile named

The string-array and closure field admissions collect, over every body of
the module, the fields some read or store makes unsafe to reclaim. Each
mark went through `push_str_unique`, a linear scan of the module-wide
accumulator: 141 k pushes, 0.86 G of `index_of_str` on the whole-compiler
emit. Nothing reads these accumulators before the walk ends, and the
admission loop only asks whether a mark is present.

## What changed

The walks append each mark, the accumulator is an `own` parameter through
all seven of them so an append grows it in place, and the admission loop
asks a name index built once over each accumulator.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver
the pinned stage0 builds from each tree, emitting the fixed older tree to
x86-64 asm text, against main at 4a4e387c.

| | main | this change |
|---|--:|--:|
| total Ir | 143.56 G | 142.68 G (−0.61%) |
| `fnsigs.push_str_unique`, inclusive | 0.99 G | 0.20 G |
| `util.index_of_str`, inclusive | 1.75 G | 1.02 G |
| `fnsigs.strfld_reclaim_ok_types_of`, inclusive | 3.09 G | 2.22 G |

The compiler each of those drivers builds from its own tree (stage 2),
emitting the fixed tree's `checker.fern`: 21.63 G on main, 21.58 G here
(−0.23%). `scripts/selfhost-alloc-bench` moves by 2 k allocations of
57.7 M.

## Witnessed

Both emit identities; the 709 targeted `internal/e2eselfhost` tests of
the earlier entries; `make check-sources` and the lint ratchet.

## The trap

The first cut appended without making the accumulator `own`. The walks
took it borrowed and bound it to a local, so the first append in every
call copied the whole array; `push_str_unique` had hidden that by
returning the list untouched for the duplicates that are most pushes. The
stage-2 driver that build produced spent 24.70 G emitting `checker.fern`
against 21.78 G for main at ec5f8089, all of it in `__fern_rc_inc`,
`__fern_arr_inc_elems` and the scans themselves, while the stage0-built
driver, carrying stage0's runtime and code, showed nothing amiss. A change
to how the compiler's own Fern is written is measured on a compiler built
by itself.

## Next: the merge cascade moves every local

`ssa_lift.lift_impl`'s loop body is an else-if chain about twenty levels
deep, and every merge down the chain moves each local it carries a phi
for into a different register or slot: runs of 28 to 35 slot-to-slot
copies on the arm edges and the fall-through edges both, and 145
instructions of copies on one block that runs once per op. Two causes,
found with a trace of `regalloc_linear` on a small reproducer:

- `phi_mates` hints a phi at the first operand defined before it. At a
  merge, the operand from the paths that do not write the local is the
  loop header's phi, live through the whole body, so the hint can never be
  taken.
- When the hint is an operand that dies at the edge, two phis of one merge
  can still want one register: an arm's value and the inner merge's phi
  sat in it on disjoint paths. The first phi placed takes it.

Preferring an operand dead by the phi cut `lift_impl` 15.6% at stage 2
and the whole stage-2 emit of `checker.fern` 0.3%, but made a plain
sixteen-local else-if loop worse (13 slot-to-slot copies to 20), because
of the second cause. It is not in this change. The register path needs the
reservation the slot path already has (`mates_wanted`), with a priority
that tells a merge-to-merge edge from an arm's: "the mate is a phi" does
not, because an arm's value is itself a phi of the reference-count
branches inside it.
