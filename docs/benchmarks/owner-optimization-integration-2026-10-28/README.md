# Owner optimization integration evidence

The [measurement entry](../../ssa-log/2026-10-28-owner-optimization-integration.md) states the source commits, producer, subject, results and limits.
This directory preserves the raw record for that controlled integration comparison.
The five optimizations come from JakeChampion's main commits.

`manifest.json` records each archive hash and decompressed hash.
Each archive passes a decompression identity check against the original bytes.
The source archives preserve both compiler snapshots and the fixed standard library.
The output archives preserve complete checker assembly for all three targets and both compilers.

The profile archives retain Callgrind data, full logs, caller counts and symbol reports.
The ELF records compare loaded code and data between each release binary and its symbol binary.
The build records identify both stage-2 compilers and their common stage-1 producer.
The producer records preserve the native cache identity and public compiler command.
The driver and resolver scripts retain their original workspace paths.

The raw runner records both complete profile runs with exit zero.
The fixed compile uses 7.483885% fewer instructions and 46,181 fewer boxes.
All three target outputs match byte for byte.
The result concerns the frozen `9e8edb87` and `e6f4bf55` sources.
It does not measure the later `41879a90` source or native latency.

The earlier integration record contains the targeted compiler checks and sanitized IR contract.
Full Go formatter coverage and full retained-machine qualification remain incomplete.
Applicable current-head CI and issue #4412 also remain incomplete.
