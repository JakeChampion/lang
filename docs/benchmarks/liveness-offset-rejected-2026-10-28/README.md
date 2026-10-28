# Rejected liveness offset prototype

The [measurement entry](../../ssa-log/2026-10-28-liveness-offset-rejected.md) describes this private prototype and its rejection.
The compiler snapshots differ only in the two liveness offset tables.
The production source retains the original append operations.

The controlled stage-2 comparison records 0.037165% fewer instructions and 27,540 additional boxes.
The raw caller counts identify 28,000 additional allocation-helper calls and 460 fewer push allocations.
Both complete profiles exit successfully.
Complete checker output matches byte for byte on all three targets.

The differential compares every Flow field across 256 cases per target under sanitizers.
It checks both solver entry points, three keep masks and input alias preservation.
The helper graphs include array-growth boundaries, dense and sparse IDs, reversed blocks, phis, dependencies and absent entries.
All three runs show nonzero balanced allocations and zero live bytes.
These helper checks do not establish a full typed program corpus pass.

The archives preserve both compiler snapshots, the fixed library, full profiles, output bytes, commands, symbol reports and differential source.
The prepared Go validation driver did not run because the prototype remains rejected.
The manifest records archive and decompressed hashes.
Each archive passes a decompression identity check.
The workspace also preserves the compiler binaries and rejected patch.
