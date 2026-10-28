# Unit-plan successor comparison

Both stage-2 binaries use the same stage-1 producer and frozen checker subject.
The baseline source is `c63e488b4ed21b7161417382b8e4f7f6f05a5da5`.
The candidate changes only `compiler/ssaunits.fern` in that snapshot.
The later numeric-label repair and main merge remain outside this profile group.

The baseline executes 25,020,841,026 instructions and calls `__fern_arr_box` 17,924,317 times.
The candidate executes 24,995,670,399 instructions and calls that allocator 17,398,916 times.
The decrease is 0.100599% instructions and 525,401 boxes.
All three target outputs remain byte-identical.
The loaded code and data match between each release binary and its counterpart with symbols.

The raw source archive and source manifests identify the baseline.
The source patch identifies the candidate.
The build records retain the pinned bootstrap, common producer and both stage-2 identities.
The raw Callgrind files and resolved reports retain instruction and allocation attribution.
The archive manifest records raw byte counts and hashes.

The validation records distinguish the measured snapshot from the later combined branch source.
The [SSA log](../../ssa-log/2026-10-28-unit-successor-count.md) explains successor order and verifier state use.
Full current-head CI and the architecture roadmap remain incomplete.

The direct primary confirmation returns zero after 89 successful test nodes, with zero failures and zero skips.
The earlier log reports PASS, but its wrapper returns one and does not qualify the change.
Both records remain intact.
The combined-source self-reproduction records a separate byte-identical stage-2 and stage-3 hash.
That compiler includes the later numeric-label repair and main merge, which remain outside the profile comparison.
