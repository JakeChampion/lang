# Scalar return donor types

The FLAC encoder profile exposed an unnecessary record box for each Rice partition.
The four-field cost record qualifies for the existing scalar return ABI.
Its caller uses the fields to construct a different frame-plan record.

The inferred-donor walk preserved all reference origins of retained constructor fields.
It did not check the destination type.
An unrelated record cannot donate its box to that constructor.
The walk now collects retained construction types and preserves matching origins.
Explicit update donors and whole escaping values retain their existing rules.

A 10,000-call control changes from 20,000 allocations to 10,000.
The remaining allocations construct the retained destination records.
Its checksum remains 200040000, and both native runs report no live bytes.
The candidate also passes the existing scalar-return, alias, same-type donor, function-value, finalizer, and register-pressure controls on native and WASI.
New controls cover unrelated records, equal record layouts with distinct types, and tuple fields.

The four FLAC encoder suites and the existing audio suite pass native sanitizers and WASI.
All five compile as Android ARM64 shared libraries.
Every one of the 81 independently qualified FLAC bitstreams remains byte-identical.
The metadata and complexity checks pass without changed limits.

[The fixed-predictor comparison](../benchmarks/flac-fixed-2026-11-08/README.md) retains every trial, checksum, source hash, and compiler hash.
It includes libFLAC 1.5.0 at matched predictor and partition settings.
It separates encoded frame bytes from metadata overhead.
The allocation reduction does not close the CPU gap.
LPC analysis and native Android device qualification remain open.

Issue: #14760. Parent: #14516.
