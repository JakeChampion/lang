# Enumerate unit-plan successors without temporary arrays

The current checker profile records 525,401 array allocations in `ssaunits.successors`.
Its ten callers only iterate the result or inspect its length.
The helper now returns the distinct successor count.
The callers read each target through `ssa.successor_id`.

A jump has one target, and a conditional branch has at most two distinct targets.
Equal conditional targets still produce one edge.
Terminal blocks still produce no edges.
The verifier still copies its state for the first of two distinct edges and consumes the original state on the last edge.
Every ownership assertion and rejection remains in place.

The comparison uses two stage-2 compilers from one stage-1 producer.
The source baseline is `c63e488b4ed21b7161417382b8e4f7f6f05a5da5`.
The frozen checker and standard library come from `23c5bc2e4a5993ffe092d04b5ad1dcf771af0269`.
The producer hash is `ce1074b697485ad511740e5c499ff771ceccf270c5afaf521fcc85e74e847b05`.

| Metric | Baseline stage-2 | Candidate stage-2 |
|---|---:|---:|
| Instructions | 25,020,841,026 | 24,995,670,399 |
| Calls to `__fern_arr_box` | 17,924,317 | 17,398,916 |

The fixed compile uses 0.100599% fewer instructions and 525,401 fewer boxes.
The complete emitted output remains byte-identical on x86-64, ARM64 and Wasm.
The binaries with symbols have the same loaded code and data as their release binaries.
This result does not establish a native latency improvement.

This profile group precedes the later numeric-label repair and main merge.
Its named snapshots remain separate from the earlier producer groups.
The [raw evidence](../benchmarks/unit-successor-count-2026-10-28/README.md) records the sources, commands, profiles and binary identities.

The ownership suites report 89 successful controls, zero failures and zero skips on the combined branch source.
The direct confirmation records the Go process exit status as zero.
The first run printed PASS, but its command wrapper returned one, so that status does not qualify the change.
The permanent controls cover a shared conditional destination and rejection of an extra copy of that edge.
The current combined compiler also produces byte-identical stage-2 and stage-3 binaries.

Source, feature, complexity, vet, test-name and full formatter checks pass.
The formatter covers 2,096 inputs.
Full current-head CI and issue #4412 remain incomplete.
