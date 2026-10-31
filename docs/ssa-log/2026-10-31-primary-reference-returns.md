# Typed primary references in the return ABI

Small return tuples can contain one owned reference and scalar fields.
Before this change, the ABI used scalar lanes only when every field was scalar.
A native hello server therefore allocated a tuple for a reference with two scalar results.

The return ABI now keeps the first reference of an eligible tuple in its original type.
The remaining fields use scalar lanes.
The maximum remains four fields.
Eligible primary references are arrays, strings and records without views or a direct finalizer.
Other reference positions and direct finalizers retain the boxed return path.
Records with reference fields retain their previous ABI.

This boundary preserves the count traffic that the scan-shape control requires.

The physical call contract exports the actual primary result type.
Scalar lane captures borrow the primary result through a helper with the same type.
The RC certifier distinguishes scalar lanes from owned pair words through the IR width.
It scans consecutive captures because a scalar capture can precede an owned pair word.
No reference becomes an untyped integer for this ABI.

The new tests cover array aliases, heap strings, inline strings, empty strings and mixed scalar fields.
They also cover nested finalizers, whole tuple use and a loop with zero tuple allocations.
Negative certifier cases reject a leaked primary reference.
A mixed capture case preserves the ownership of a secondary pair word.


## Compiler measurement

Both stage-2 compilers use one stage-1 compiler and 1,244 source files.
Only the four ABI modules differ.
The subject is the fixed `checker.fern` from the earlier comparison corpus.
The subject, its compiler imports and its library retain the same hashes throughout both profiles.
Both runs disable the RC certificate memo and enable strict IR checks.

| Compiler | Callgrind instructions |
|---|---:|
| Main merge without this tuple ABI change | 38,388,414,555 |
| Main merge with this tuple ABI change | 38,389,310,158 |

The increase is 895,603 instructions, or 0.00233300 percent.
This result does not show a compiler-speed gain.
The runtime allocation result below is the benefit of this slice.
No native latency result is claimed.
The emitted output differs on x86-64, ARM64 and Wasm because this change alters the ABI.

The raw profile summary, totals and collected count agree for both runs.
The profiles contain addresses, without symbolic function attribution.
The saved evidence retains the full profiles, emitted outputs, source snapshots and hash manifests.

```text
stage1 sha256: 84bc2d65e9ef15339877ca39b8acdf5c727cdb5e4a472fd0df71d055dd17104f
before stage2 sha256: b460e7d4159fc553a95a84dabc3c42bbd48ddd1a10d22860123167eebcdc115e
after stage2 sha256: b90d4b6a328b10505ae5fe50203d411825c91b35d590c31a58b913a2420439d9
subject sha256: 5ace3eadd42ce301ec112e6d448ce4420c30e25a08895ba320be41f37aa8fe68
before raw profile sha256: 4d76a3ac19fab87b47c0964ca77b8fefe73df21c6aa5b08136d434d89e86ddbb
after raw profile sha256: a2209f628a2546ca284a11b87116a1b3187f0fe4a54eaebdda791d87a6aff47d
```

## Correctness checks

The focused suites passed 410 results, with no skips.
These include the primary lifetime controls, 128 scalar-return results, 52 certifier cases and nine primary-reference results.
They also include 24 current-main ownership results and four decimal controls.
The scan-shape control retains one uniqueness test and two retains.

Stage 3 and stage 4 are byte-identical.
Their SHA-256 is `6f000cf61f0521356ab7544c45df811060d57451e0cb38e7e4781c428b8be75b`.
All four index-serial controls pass.
Rollback and a removed serial leave the lock unchanged.

A broader prototype also split records with reference fields.
It regressed the scan-shape control to two uniqueness tests and three retains.
The main baseline passed that control.
That prototype remains rejected, with its source, profile and failure logs intact.
Its 0.50018696 percent profile decrease is not a result for the tuple-only change.

## Runtime allocation comparison

The baseline and candidate use the same main revision and library.
The baseline controls fail their original limits.
Their measured counts provide the before values below.

| Workload | Before | After | Limit | After result |
|---|---:|---:|---:|---|
| Native hello | 3 | 2 | 0 | fail |
| HTTP/2 | 22 | 21 | 21 | pass |
| HTTP/1.1 TLS | 13 | 12 | 11 | fail |
| Two-fetch | 68 | 67 | 58 | fail |

Counts are allocations per request.
The final candidate passes one of the nine protocol allocation checks.
Eight still fail, with no skips and no compiler refusals.
The ARM64 and ring hello routes also measure two allocations per request against zero.
HTTP/3 measures 51.93 against 48, and multiplexed HTTP/3 measures 29.11 against 29.
Wasm measures 36,588 allocations over 10,000 requests and fails the whole-number check.

No allocation limit changed.
The draft PR and issue 4412 remain incomplete.

Main later advanced to `a1f6b73d830c2091d895744236f153869a9fbca5`.
That change updates only the feature-census test.
The candidate passes that upstream test without assertion changes.
The compiler and library inputs remain byte-identical to the profiled revision.
