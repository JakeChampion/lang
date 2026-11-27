# Use the new function type IDs to build closure environments

Part of #11534 and #8171.

The compiler finds closure environments in rounds. One environment can introduce a record that contains a function of another type. Each round must visit the new type. First discovery sets the order of environment rows and generic instantiation requests.

The schema traversal now interns each reachable function type into the existing table. An index in this table is a type ID for this environment calculation. Each round saves the length of the table before traversal. The code that generates environment rows reads the entries from that ID to the new end. Previously, one array held the encountered function types and another held the new types for the round.

The table uses type spelling to find a bucket and structural equality to select a type. For example, a record and a union can share a spelling. The traversal through dynamic traits and generic union arguments retains its existing rules. The general checker type table and parser node IDs remain open work.

## Stage-2 measurements

Stage 0 came from release `stage0-20261125-84426d4`. It built stage 1 separately on main at `f22ec3150cd7` and on the branch. Each stage 1 built its own stage 2.

Callgrind counted x86-64 instructions on the same inputs with IR verification and ownership certification enabled.

| Fixed input | Main instructions | Branch instructions | Change |
| --- | ---: | ---: | ---: |
| Full checker compile | 95,444,835,028 | 95,442,723,365 | -2,111,663 |
| 1 callback field | 52,430,570 | 52,427,436 | -3,134 |
| 32 callback fields | 54,989,394 | 54,982,181 | -7,213 |
| 128 callback fields | 65,465,570 | 65,448,229 | -17,341 |
| Generic closure dispatch | 456,289,952 | 456,255,019 | -34,933 |
| 1 distinct callback type | 55,033,206 | 55,030,002 | -3,204 |
| 8 distinct callback types | 67,161,530 | 67,147,869 | -13,661 |
| 24 distinct callback types | 96,449,786 | 96,412,925 | -36,861 |
| No callback (return zero) | 17,804,959 | 17,804,869 | -90 |

The repeated-field fixture puts one captured callback into each field of a record and calls the final field. The distinct-type fixture gives each callback a different record result. It calls every field and checks their sum with two argument counts. The generic fixture is `generic_closure_dispatch_instances` from the conformance suite.

The full checker compile saved 0.0022% of the instructions and emitted the same binary. It allocated 74,654,386 objects on main and 74,653,390 on the branch. The 128-field fixture allocated 159,401 and 159,372 objects. Both compilers freed all allocations. The stage-2 executable grew from 22,007,632 to 22,009,232 bytes.

## Behavior

Both stage-2 compilers reproduced themselves. The conformance reports matched across 6,276 fixture and target pairs. They contained 4,467 identical binaries and 1,809 matching refusals. All 529 JSON diagnostic comparisons matched. The six fixed checker binaries, the HTTP component and the two freestanding assembly outputs also matched.

All 16 focused programs produced matching output on x86-64 Linux, ARM64 Linux and WASI. Additional tests used nested generic environments, once-only callbacks and two modules with different records named `Item`. The five cases passed all 35 ordinary, leak-check and sanitizer comparisons. A direct test supplied ten function types and checked the five distinct IDs in discovery order. It covered empty spelling, equal spelling with different structure, and equal structure with different spelling.

Both compiler builds matched all 2,372 per-function mutant records. The feature and complexity ratchets passed. On either checker source the main compiler copied 58,521,008 shared-array bytes in 1,022,784 copies. The branch copied 58,518,488 bytes in 1,022,778 copies. The branch pins both measurements.

Main commit `ad5e7ece3539` updated the certifier baseline. Its compiler sources match the measured base. Both builds matched all five certifier pins. The branch also passed the strict check for the two array-copy pins.
