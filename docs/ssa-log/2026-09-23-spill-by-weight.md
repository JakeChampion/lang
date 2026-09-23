# The allocator spills by use weight and shares frame slots

`ssa.regalloc_linear` evicted, when both pools were full, the active
interval that ends latest: Poletto and Sarkar's rule, which is a stand-in
for "referenced least" and gets it wrong whenever a loop value outlives the
cold values around it. On `checker.fern` 653 functions use all five x86-64
callee-saved registers, and they hold all 35,078 frame reloads.

Two changes, measured 2026-09-23 on x86-64 unless noted:

1. **Eviction by weight.** Each value's weight counts its definition and
   every read at 8 per enclosing loop (up to three), a phi operand at the
   end of the predecessor it arrives from (`spill_weights`,
   `block_loop_weights`). A loop is the span of block positions from a back
   edge's target to its source, the same approximation
   `extend_loop_invariant_intervals` uses. The cheapest active interval of
   the needed class is evicted when it costs less than the incoming value;
   a register chain built by sharing is weighed as the sum of its values,
   since evicting it spills them all. Ties fall back to the latest end.
2. **Shared slots.** `assign_spill_slots` gave every spilled value its own
   slot. It now packs them greedily by interval start, reusing a slot whose
   last occupant ended strictly before; a parameter, stored on entry, is
   live from position 0.

| `checker.fern` | before | weights | weights + shared slots |
|---|---|---|---|
| x86-64 static | 374,182 | 374,630 | 373,903 |
| x86-64 frame reloads | 35,078 | 30,935 | 30,569 |
| x86-64 frame stores | 8,079 | 14,472 | 14,106 |
| x86-64 disp32 frame refs | 20,803 | 34,377 | 17,501 |
| arm64 static | 370,077 | | 368,100 |
| arm64 `ldr` from sp | 19,481 | | 13,624 |
| x86-64 binary | 1,551,112 | | 1,536,088 (−1.0%) |

Weights alone move reloads into stores, and the new spills spread over more
slots: the stage-2 compiler grew 2.8% in bytes because its frame references
crossed into 32-bit displacements. Shared slots undo that and more.

Stage 2 compiling `ssa.fern` to assembly, the weight pass's own cost
included: 3,600,917,487 → 3,543,673,535 Ir (−1.6%), output byte-identical.

| bench | Ir change |
|---|---|
| `sort_ints` | −1.8% |
| `pvec_with` | −1.6% |
| `sort_strings` | −1.0% |
| `tokenize` | −0.9% |
| `struct_drop` | −0.5% |
| `ordmap_insert` | −0.2% |
| `string_build` | +0.09% |
| 6 others | 0% to −0.1% |
