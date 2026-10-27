# Native performance pins after safety changes

The native pins now record the complete verified b42 reports after merged compiler safety checks.
The update preserves all metric keys, benchmark inputs, strict settings, and tolerances.
Both complete reports pass the unchanged comparator, and controlled drift still fails.

The [qualification record](../benchmarks/native-perf-pins-2026-10-27/README.md) retains raw reports, artifact digests, source identities, and emitted differences.
The separate allocation baseline and all other baselines remain unchanged.
Actual main recovery and all M5 and epic acceptance gates remain open.
