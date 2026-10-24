# Map iterator validation

Source: `2669c6a5d740583e24d17ffbf9abd865d5b54476`.
Base: `55e8bdc33ffe5c27c6418ac83cc50f0e11b56a82`.

`Map.iter()` returns an owned `iter.ArrayIter[(K, V)]` snapshot.
It implements the Iterator protocol and supports its adapters.
Copies advance independently. Map updates do not change an earlier snapshot.
The public MapIter cursor and its runtime and compiler paths are gone.
Direct Map loops retain their separate column walk.

## Checks

- The locked stage0 builds three compiler stages. Stages 2 and 3 are identical.
- All 11 driver sizes pass the unchanged strict baseline gate.
- Targeted Map, iterator, checker, type decoder, and SSA registry tests pass in 162.482 seconds.
- Eight fixtures pass 80 checks with a fresh Go oracle and the stage 3 compiler.
- These checks include both interpreters and x86-64, ARM64, and core WebAssembly programs.
- The compiled fixture runs use strict IR, the sanitizer, and allocation censuses.
- Selected Go front end tests and the CLI fallback hint case pass.
- Format checks cover 1,176 tracked Fern files. The selected vet checks pass.
- New comments pass the STE structural check with no errors or warnings.

The fixtures include records, strings, arrays, wide numbers, empty maps, snapshot returns, and generic adapters.
Import-aware checker tests cover 15 cases in normal and lambda bodies.
They check the removed API, argument errors, missing receiver column types, and source method declarations.

## Inputs

The validation record gives the source, compiler, oracle, and patch digests.
The source digest covers every tracked compiler and standard library file in sorted path order.
Each path and its contents have a zero byte after them.
The compressed file inventory records each input separately.
The source patch applies to the recorded base and includes the new fixtures.

The bootstrap began before the source commit.
Its recorded Git head is the base commit, but its complete source digest equals the committed source.
The original bootstrap record remains unchanged.

The driver report records exact compiler arguments, executable sizes, and hashes.
The build helper removes all FERN environment variables, as the test harness requires.
The size baseline remains unchanged.

## Direct loop profile

The profile visits 16 integer pairs in 10,000 loops.
Every target reports 60,000 allocations for direct Map loops and 420,000 for public iterator snapshots.
Allocation counts equal free counts in all three runs. The sanitizer reports no error.
The measurements support the separate direct Map loop path.
The public iterator snapshot follows the same value contract as the other collection iterators.

`loop-profile.json` records the executable producer and exact compile arguments.
The compressed input preserves the profile source.
