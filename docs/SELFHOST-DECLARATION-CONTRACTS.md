# Exact self-hosted declaration contracts

This is a production type-boundary prerequisite for the Fern-written typed-IR
migration, not completion of AST ownership retirement. It integrates the
declaration adapter and semantic type spelling from the preserved typed-match
work. Related: #8968, #8930, #8920 and bootstrap convergence #4451.

## Boundaries and deletion

Source declaration sites share structural TypeRef recovery. Grouping, generic
argument commas, named parameters, callable parameters, returned callables and
array attachment survive in the signature metadata. This replaces four
independent token-peek/selector helpers.

Checked local bindings use the shared semantic Type adapter. Annotation reuses
the binding type already resolved during scope advancement; initializer
expressions still see the preceding scope. The lowerer attaches callable
metadata when creating each local slot, including branch and loop locals. The
top-level string-keyed signature seed table and pre-pass are deleted.

Existing capture lowering carries complete declaration contracts. Its lifted
symbol table includes newly generated functions before subsequent captures
are adapted. The declaration bridge does not replace lexical capture resolution
or add borrowing, escape, uniqueness or ownership heuristics.

Callable result values use the existing closure-object ABI regardless of arity.
The old return boundary boxed zero-argument named functions but returned raw
pointers for functions with parameters. Direct calls could conceal that mismatch;
passing or capturing the result dispatched a raw pointer as a closure object.
Already-bound callable values remain unchanged, including shadowing parameters.

## Validation

New regression boundaries:

- 24 preserved declaration adapter cases and 14 production adapter cases.
- Seven signature shapes at seven declaration sites, with exact metadata rows.
- Eleven actual CLI programs on x86-64 Linux, ARM64 Linux and Wasm: 33 runtime
  cases, each with an independently pinned interpreter result. Includes mixed
  widths, array results, nested functions, branch/loop locals, capture, argument
  passing, mixed return forms and parameter shadowing.

The initial adapter regression failed 2/14 cases. Parser and checked-binding
repairs exposed three cross-target capture failures; fixing the return ABI
passes all 33 expanded cases. Broader closure tests then exposed a missing
lifted signature-table update; its correction passes the 20 closure-calls-closure
cases. Existing diagnostics and assertions are retained. The ARM64 callable
capture test now uses its already-configured x86 driver runner instead of
skipping non-x86 hosts. QEMU execution is correctness evidence, not timing data.

The final broad targeted selection passed in 107.828 seconds. Its two
stdlib-runner tests still skipped behind native-x86-only guards; follow-up
validation removes those guards and runs the configured driver runner. All
declaration, callable, capture, type-resolution, float-width and closure-return
tests in that selection ran. Full source lint passed in 15.608 seconds and
lint-all passed. Full integration CI remains required before merge.

## Controlled compiler size

Parent: a70822c051bbf720f48f91449109d1e9388535de. Both sources compiled to ARM64
Linux using the same Go-bootstrap compiler, SHA-256
52aab6c4c723f731cdf3dc6835cfee72991ddbece554973023b4a9e9199ab6e7,
then linked with the same container GCC using `-static -nostdlib`.

| Component | Parent bytes | Change bytes | Delta |
| --- | ---: | ---: | ---: |
| ELF file | 17,031,272 | 17,030,656 | -616 |
| .text | 15,605,636 | 15,603,976 | -1,660 |
| .rodata | 550,834 | 550,850 | +16 |
| .eh_frame | 251,336 | 251,620 | +284 |
| .bss | 8,320 | 8,320 | 0 |

The remaining file delta is symbol table +432, string table +317 and alignment
-5 bytes. Code growth buys exact semantic spelling, the shared declaration
adapter, structural source recovery and complete capture contracts. Removing
the independent parsers and seed pre-pass offsets it. No baseline changed.
These are stage-1 compiler size measurements, not a runtime speedup claim or
a stage-2 code-generation comparison.

## Native compile-cost samples

Same two stage-1 compilers, built by the fixed bootstrap for ARM64 Darwin,
compiled the identical parent checker source to ARM64 Linux assembly. Absolute
input path and stdlib root were identical. A one-round pipeline check succeeded,
then three alternating parent/change rounds ran with `/usr/bin/time -lp` after
the local test/build processes completed.

| Metric | Parent samples | Change samples |
| --- | --- | --- |
| Elapsed seconds | 2.09, 2.07, 2.01 | 2.09, 1.98, 1.98 |
| User seconds | 1.97, 1.98, 1.95 | 1.97, 1.92, 1.92 |
| Peak RSS bytes | 209731584, 209731584, 209764352 | 209747968, 209715200, 209747968 |

This small sample does not establish a speedup. It shows no clear time or peak
RSS penalty on this input. RSS is not an allocation count. These native samples
measure the cost of the changed compiler frontend/lowering algorithms, not
self-compiled stage-2 code quality or coreutils runtime performance.
