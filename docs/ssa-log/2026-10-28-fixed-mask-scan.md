# Validate fixed-register masks once

`ssafixed.rows` checked every mask once for each register before it built the prefix rows.
The range checks now precede the register loop and inspect each mask once.
The register range, accepted masks, prefix rows and caller constraints retain their behavior.
The function retains its exact guard diagnostics.

One stage-1 producer builds both stage-2 compilers.
Its hash is `e744816112dd5727b225a093ea87c39e37df54ce3d0a145a4e2675784217f26b`.
The baseline source matches `77c46d58bd7c051c7111cb5940e99393d8b57469`.
Only `compiler/ssafixed.fern` differs in the candidate source.
The comparison reuses the complete baseline build and profile from the prior liveness experiment.
It retains that group's fixed checker and standard library.

The baseline compiler hash is `7482a908f7a1546423c23dcc1fd39d84f73307e4de3b322cd80ed534ba4e94a3`.
The candidate hash is `db56aacff59545b59b65a686750eb2590d1ff514a685eaaebed7d6eaa11789df`.
The checker hash is `11a3b2c1bbd016a7f255ce7a7bc1d40511dd37ec672a9741783217381cf6a99f`.
Both complete profiles exit successfully.
Each release binary matches the loaded code and data of its symbol binary, except for ELF section fields.

| Metric | Baseline stage-2 | Candidate stage-2 |
|---|---:|---:|
| Instructions | 23,404,957,928 | 23,374,386,834 |
| Calls to `__fern_arr_box` | 17,759,927 | 17,759,927 |

The fixed compile uses 0.130618% fewer instructions with the same box count.
Complete checker output remains byte-identical on x86-64, ARM64 and Wasm.
This named subject does not establish a native latency improvement or a combined gain with earlier groups.

A differential checks 2,976 valid cases on each target under sanitizers.
It compares complete prefix matrices, register ranges, empty status and interval decisions.
The cases span register counts 1 through 31, empty masks, growth boundaries and multiple mask patterns.
Native and ARM64 runs each balance 644,716 allocations.
The Wasm run balances 644,721 allocations.
All valid runs report zero live bytes.

Separate plain builds check ten refusal modes before and after on each target.
All 60 refusal runs retain the exact stdout, full diagnostic text and target-specific exit status.
Native and ARM64 refusals exit with status 3.
Wasm component refusals exit with status 1.
The corpus covers invalid ranges, negative masks, late invalid masks, high bits and caller constraints.

The archives retain two initial harness failures.
The first assumed sanitized refusals lacked the allocation report that the baseline emits at deliberate process exit.
The second assumed Wasm component exit status matched native status.
The final refusal checks compare full plain-build output, while every valid differential run retains sanitizers and leak checks.

The real compiler suites pass 103 nodes in 168.675 seconds, with zero failures and zero skips.
Source checks pass in 11.705 seconds.
Feature and unchanged complexity checks pass four nodes in 3.929 seconds.
The current formatter checks all 2,110 inputs in 2.563 seconds.

The linked cold-call driver passes before and after on all three targets.
Its file size remains 153,968 bytes on x86-64 and 225,904 bytes on ARM64.
Its Wasm file size decreases from 137,377 to 137,299 bytes.
These size checks use the common producer and do not establish a new bootstrap fixpoint.

The whole conformance comparison matches all 4,242 fixture-target rows from 1,414 frozen fixtures.
The matrix retains identical artifact hashes for 3,449 emitted programs and the same 793 refusals.
The comparison checks emitted bytes and acceptance, without a claim of program execution for every corpus row.
All frozen input hashes remain unchanged after both runs.

The [raw evidence](../benchmarks/fixed-mask-scan-2026-10-28/README.md) preserves the source snapshots, complete profiles, symbol reports, input fixtures and validation records.
Full issue #4412 acceptance and applicable current-head CI remain incomplete.
