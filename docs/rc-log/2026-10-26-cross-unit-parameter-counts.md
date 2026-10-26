# Cross-unit parameter counts

Issue #13583 records two failures on main `7b457eae4405762bcbe66a558a6060366276b8fe`.
Native emit-all stopped in batch `[64:128]` with exit 4.
The Wasm verifier rejected a call to `semsource__produce_module` with six arguments against a declaration with three parameters.
Scalar record parameters expanded into separate arguments, but external declarations retained their source parameter count.

The shared verifier gate now uses the physical ABI mode count for known producer signatures.
Signatures without producer metadata retain their previous count.
The name index and the call checks remain intact.
The Wasm driver also retains producer metadata and selects origins beside each shard's bodies and generated helpers.
It previously supplied an empty physical collection.

The focused gate regression failed before the fix on x86-64, ARM64/QEMU10, and Wasm.
All three targets now pass with sanitizer and zero final live bytes.
Too-few and too-many arguments still fail.
Missing-metadata controls preserve the previous checks.
A separate two-module Wasm regression proves the six-argument failure before metadata reaches the shard emitter.

The native emit-all fixpoint passes across 200 units with the original batch size of 64.
Both generations emit identical assembly.
The Wasm whole compiler links 156 units from 112 modules and passes core module validation.
That linked compiler compiles a two-module program with the expected result of 14.

The adjacent shard suite passes 22 test nodes without skips.
Both unchanged complexity gates pass within that suite.
The verifier observer suite passes 29 test nodes without skips.
It checks malformed-IR refusal and code equality for 24 controls across three targets.

The clean CLI reports `3bebeb4b6fa5`.
Its SHA256 is `59e020168d59e830c7eecc6b38abb8ff1c2731d12e0b391a5f1ac0fd11aad8ee`.
The native image script passes all 149 TAP cases, its smoke cases, negative compiler probes, and formatter regression.
All 61 native worker leak censuses report zero final live bytes.
The Wasm image script also passes all 149 TAP cases and its smoke cases with zero final live bytes.

The unchanged default allocation gate passes its 10% ceiling with 34,729,080 balanced allocation events.
Its separate 1% advisory still reports a 9.82% increase against the same baseline.
No baseline or threshold changed.

Go 1.27.3, Wasmtime 46.0.1, and wasm-tools 1.253.0 provide the pinned tools.
The selected ARM64 cases use GNU cross GCC 14.2 and QEMU 10.0.13.
These local results do not establish full repository CI success.
