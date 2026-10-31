# Preserve map support in shared drop helpers

Date: 2026-10-31. Issue: #4412. PR: #13897.

The shared helper context used the map flag from the first declaration.
That declaration can omit `core/map` while later declarations use maps.
The complete schemas then generated plain decrements for map fields.
Those decrements damaged map storage and its reference counts.

The context now preserves map support from every produced declaration.
Map fields use their map destructors when the module links map support.
The complete dynamic schemas still preserve concrete child cleanup.

The map-key reachability test also truncated generic symbols at `$`.
Its symbol expressions now accept complete generic names.
Its call expression also checks the complete target and its ABI suffix.
The original expression reported a missing `__fn___sem_drop_Option`.
The complete expression finds every target.
GNU assembly, static linkage, and execution pass for that same program.

## Validation

Both stage-2 compilers use stage-1 SHA-256:
`1a9e3d8e2d6fd9f2572a7d6f561a21ddd0085e5baf479b7f541e429c66021a87`.

The fixed subjects come from PR head `d3a2e501defe18920bee2e6de3113fa32fe252ed`.
The published compiler corrupts HTTP-static output and crashes in the simulation.
The corrected compiler passes both subjects with identical expected TAP output.
The sanitized stable compiler passes both subjects on x86-64 and ARM64.
All four executions report equal allocation and free counts with zero live bytes.

Both subjects also pass on WebAssembly with balanced censuses.
The simulation uses the WASI component path because it imports socket interfaces.

The lifetime suites pass 179 results on stage 2 and 179 on the stable compiler.
The stable inliner suite passes 61 results.
FractalSource passes on all three targets with both compiler generations.
The three targeted reachability tests pass.
Stage 3 and stage 4 are byte-identical:
`4786d8bebb2f91b4d555b8581223e60901979860626ad76f5afc6f09caf05128`.

The fixed-checker Callgrind comparison remains active at publication preparation.
Its source, library, stage-1 producer, and baseline profile remain fixed.
This correctness fix does not include the separate ownership experiment.
That experiment increased compiler instructions by 10.05 percent and remains unpublished.

Raw programs, profiles, JSON records, and logs remain in the saved workspace.
The current full CI run still has runtime failures.
The server allocation gates and full issue acceptance remain incomplete.
The PR remains a draft.
