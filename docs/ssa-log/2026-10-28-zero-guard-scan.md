# Scan loop depth with zero-slot guards

`prune_zero_slot_guards` first built a boolean array that marked every operation inside a loop.
The guard scan then read that array once.
The pass now maintains the same loop depth during its forward scan.
It removes the marker array and its separate operation scan.

The depth rules are unchanged for blocks, conditionals and nested loops.
Each successful replacement skips one direct call, which cannot change scope depth.
The pass still refuses guards inside loops and guards on parameters or written slots.

The subject is the same frozen `checker.fern` compile with `-O -target x86-64-linux -emit asm`.
One stage-1 compiler produced both stage-2 compilers.
The baseline compiler reproduces itself, so its stage-2 bytes match the common producer.
The retained baseline profile measures those same bytes.

| Metric | Baseline stage-2 | Candidate stage-2 |
|---|---:|---:|
| Instructions | 25,031,956,216 | 25,020,122,061 |
| Calls to `__fern_arr_box` | 17,926,218 | 17,924,193 |

The instruction count decreases by 0.047276%, and the box count decreases by 2,025.
The complete checker output is byte-identical on x86-64, ARM64 and Wasm.
The private differential has 1,350 cases per target before and after the change.
It compares all operation fields, retained input aliases and repeated application under the sanitizer.
All six runs have the same output and zero live bytes.

The permanent driver also covers nested conditional regions and adjacent guards around a loop.
The [evidence](../benchmarks/zero-guard-scan-2026-10-28/README.md) includes raw profiles, manifests, commands and validation records.
This result does not establish a native latency gain.
This slice does not complete issue #4412.
