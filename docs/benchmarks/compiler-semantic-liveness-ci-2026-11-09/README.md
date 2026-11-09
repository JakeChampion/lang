# String count baseline after main updates

Status: [record] CI baseline evidence from 2026-11-09.

PR #14918 CI run 42670662870 reported three changed x86-64 benchmark rows.
The job is 129346709783, at head `d5878092fe`.
The strict primary gate failed on the two primary rows.
The later emitted-size report identified the third row.

| Row | Previous baseline | Measured baseline |
| --- | ---: | ---: |
| `x86_64/string_count_byte.text` | 1,271 | 1,321 |
| `x86_64/string_count_byte.ir` | 68,053,368 | 41,173,368 |
| `selfhost/x86-64-linux/string_count_byte.emit` | 1,271 | 1,321 |

The code size increases, and the guest instruction count decreases.
The baseline files retain all tolerances and strict gate behavior.
The [raw CI log](ci-perf-x86_64.log) retains the exact corrected rows.
These rows are not an isolated semantic-map performance gain.

## Baseline control

The baseline compiler source is `12be8f11864c00d71bc3ae9e0c3bc1ddabea75ef`.
The candidate compiler code source is `3a296b887ccb1c879743cce8a2a67351c548481f`.
Both compile the unchanged benchmark and standard library from `12be8f1186`.
Their producer is the same qualified `38f241c` binary.
The builds omit `-g`, disable profiles and leak instrumentation, and retain ownership certification.

Both compilers emit byte-identical assembly with 1,321 indented lines.
The assembly SHA-256 is `aee54ecd1cad235488954dcd50ea22e0848e8838578560c7b4441aafe718d55f`.
Both native executables also match byte for byte.
Their SHA-256 is `fb86b9a452ce20a05a89f281cb097e1a486af45ff259d4de65ff4ac885cbd44a`.
The [comparison records](native-comparison.json) retain these identities.

The baseline executable records 41,173,368 guest instructions under Callgrind 3.24.0, matching CI.
It returns the expected checksum status of 40.
The benchmark performs 6,000 iterations with 1,024 occurrences of each selected byte.
Its checksum is `12288000 % 97`, which is 40.
The [raw count log](guest.log) remains intact.

The identical output shows that this kernel change predates semantic map reuse.
This record makes no wall-time or external-rank claim.
The [commands](commands.json) retain the completed invocation details.
The [manifest](manifest.json) retains producer, compiler, input, and data hashes.
The performance-gate self-test passes all 22 cases.
