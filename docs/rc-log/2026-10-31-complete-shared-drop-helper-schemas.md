# Complete the schema for shared drop helpers

The shared drop-helper cache used the type context of its first caller.
That context can omit concrete dynamic types that later callers require.
An inlining change can change which caller supplies the first context.
The cached helper then releases the outer box but omits its children.

The FractalSource test exposed this fault in two error routes.
The overflow and non-numeric coordinates each leaked an allocated error-message string.
The unchanged full test leaked two blocks, or 160 bytes, on x86-64.
The same test balanced under the comparison compiler.
A reduced test also leaked under that compiler, so reduction alone did not establish the regression.

`semlower.module_helpers` now collects closure and dynamic rows across the module before it creates shared drop helpers.
It uses the complete record, enum, and finalizer schemas from `semsource.Built`.
The per-function scan still supplies kernel and map helpers.
The fix does not change the mode inference or the lifetime guard in `seminline`.

The unchanged `TestSelfHostFractalSource` passes on x86-64, ARM64, and Wasm.
Each target passes its sanitizer and balanced-census assertions.
The stage-2 and stable compilers each pass 179 lifetime checks.
Stages 3 and 4 produce byte-identical compilers.
The stable compiler also passes 61 additional inliner checks.

The fixed checker comparison uses stage-2 compilers from one stage-1 compiler.
The instruction count changed from 39,967,273,663 to 38,592,319,121, a reduction of 3.4402%.
The comparison disables the certification memo and uses fixed source and library inputs.
The change affects lowering, so the emitted checker assembly differs.
This result is an instruction count, not a native latency measurement.

The PR remains a draft because serve allocation checks still fail.
This change does not complete issue #4412.

The preserved measurement record identifies these exact inputs and outputs:

- Stage 1: `1a9e3d8e2d6fd9f2572a7d6f561a21ddd0085e5baf479b7f541e429c66021a87`.
- Baseline stage 2: `162bf7996f11309410c129c834859e030e0a8f6b2be3d1e29438b5e578dc8880`.
- Candidate stage 2: `09fb82ebb2b132809424e6f9abdeea654971a14d80ff2e67f4d4190fe46c3483`.
- Stable stages 3 and 4: `174bba828fba1df3fdc607d19be5788a1af3208ac9d1046a71482120659a9858`.
- Fixed checker source: `5ace3eadd42ce301ec112e6d448ce4420c30e25a08895ba320be41f37aa8fe68`.
- Raw baseline profile: `28a3a6e2551adafb24c5cba160121e60a67776445cc7f6f051875ef15d4932f8`.
- Raw candidate profile: `23c7a6f1c8bb95260ca2120a0702dcb919a18f8ab3044c986240a11be86ebe78`.
