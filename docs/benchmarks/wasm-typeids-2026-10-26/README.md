# Unused Wasm type-ID globals

Issue #13494 records the cause and acceptance criteria.
PR #13495 omits private type-ID globals from modules that never read them.
The compiler scans complete unit bodies and checks runtime IO error dependencies.
If either needs an ID, the compiler preserves the complete legacy section and every numeric ID.
This gate does not prune individual IDs or change their order.

## Fixed inputs

The baseline source is `0e2176c40c6e61e522cfa3ef204e4a4f0555933b`.
The candidate source is `934244ba8480e3dcef934d32e029aad3852c441f`.
Both include the upstream index arithmetic change from PR #13461.
Both raw compilers use the pinned stage0 bootstrap and pass its smoke checks.
The collector supplies the same candidate benchmark and standard-library files to both compilers.

The clean candidate CLI reports `934244ba8480`.
Its SHA256 is `22211db4872c3279bba6d498c037e3d725ba8e08c5626134585b4ad0c46f0b57`.
The measurement uses raw compilers, whose separate hashes appear in `emission.json`.
That record also hashes all compiler sources, standard-library sources, benchmark inputs, the collector, and wasm-tools.
All hashes remain unchanged after the campaign.

## Observations

The campaign compares 39 benchmarks across three targets, with one compile per compiler and input pair.
All 78 native pairs are byte-identical.
All 39 Wasm pairs have identical text after removal of type-ID definitions.
The 24 modules without ID reads omit 52 to 60 globals each.
The other 15 Wasm modules retain byte-identical output, including their type-ID sections.
Every baseline and candidate Wasm module passes wasm-tools validation.

| Benchmark | WAT lines before | WAT lines after | Core Wasm bytes before | Core Wasm bytes after |
| --- | ---: | ---: | ---: | ---: |
| int_loop | 123 | 71 | 1957 | 673 |
| string_count_byte | 511 | 452 | 6587 | 4939 |
| string_slice | 500 | 448 | 6336 | 5058 |

WAT counts cover indented text lines, as the existing self-host size lane specifies.
Binary lengths cover unoptimized core modules from wasm-tools parse.
These are size observations, not retired-instruction counts, CPU time, or throughput parity.
They do not establish compatibility, memory bounds, or production safety.

## Validation

Before the fix, real integer and string-view absence checks fail.
The dynamic-record control passes before and after the fix.
The final focused Go suite passes 52 test nodes with no skips.
It covers actual execution, five runtime modes, 114 helper dependency roots, IO error ownership, and both unchanged complexity gates.
The clean candidate also passes all 136 image TAP cases and the complete Wasm smoke script with balanced leak counts.

The native image script fails after the upstream index change.
The in-process assembler rejects `imull $3, %esi` before the pixel suite executes.
Issue #13554 and active PRs #13496 and #13499 track this separate failure.
The native script is not a pass.
The fixed-input comparison proves that this Wasm change does not alter native assembly.

`gate-before.log` records 21 findings under the unchanged strict comparator.
The baseline update changes only the 24 Wasm rows that this fix moves.
It includes improvements below the one-percent reporting threshold.
`gate-after.log` records a pass against the complete 117-row report.
Native rows, unrelated Wasm rows, tolerances, allocation baselines, and other ledgers stay unchanged in this PR.
Current full CI is not green.

## Reproduction

Build the baseline and candidate raw compilers in separate worktrees with the pinned toolchain.
Run this collector with absolute paths:

```sh
python3 docs/benchmarks/wasm-typeids-2026-10-26/measure.py \
  --repo /workspace/lang-image-wasm-typeids \
  --baseline-repo /workspace/lang-image-wasm-typeids-before \
  --baseline /workspace/lang-image-wasm-typeids-before/bin/fern-selfhost \
  --candidate /workspace/lang-image-wasm-typeids/bin/fern-selfhost \
  --wasm-tools "$(command -v wasm-tools)" \
  --output /workspace/type-id-emission.json
```

The collector rejects changes to native output or Wasm text outside type-ID definitions.
It rejects changes to any measured input or tool during the campaign.
It validates every Wasm module and records the complete sample set.
