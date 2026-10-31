# Three owned references in tuple returns

The current HTTP/3 profile shows repeated tuple allocations in `__h3_event`, `__h3_datagram` and `__h3_pass`.
Each function returns three managed references.
The emitted assembly allocates a three-field tuple at each return.

The compiler now returns the first typed reference as the primary result.
The two pair words carry the other references through separate ownership channels.
The physical ABI metadata marks both pair words as unconditional owned units.
The transformation changes no backend or capture format.

Only three-field tuples whose fields are references use this extension.
Each field must fit one word and contain no view or direct finalizer.
Records with reference fields retain their prior ABI.
The earlier scalar-tail and boolean-tail tuple shapes retain their prior ABI.

## Correctness checks

The prior focused suites pass 432 results against the current library, with no skips.
The new return and metadata tests pass 22 results.
The additional tuple-forward tests pass four results.
These tests cover independent fields, aliases, heap strings, inline strings, empty strings and nested finalizers.
They also cover a shared input tuple and a branch that changes field order.

The runtime tests execute on x86-64, ARM64 under QEMU and Wasm under Wasmtime.
The verifier accepts independent ownership transfers of all three fields.
It rejects a leaked primary field, second field or third field.
It rejects a scalar lane as an ownership transfer for the third field.
The negative cases report exactly one diagnostic and zero unproved results.

The old compiler fails the unchanged zero-tuple-allocation probe on x86-64 with exit 92.
The candidate passes the probe on all three targets.
The probe allocates its input arrays before the measured interval.
Its allocation census is nonzero and balanced.

The main feature controls pass 14 runtime results.
The source census passes 26 results.
Stage 3 and stage 4 are byte-identical.
All four index-serial controls pass.
All 1,244 compiler and library input hashes match after qualification.

## Allocation measurements

Both stage-2 compilers use one stage-1 compiler and the same library.
The fixed HTTP/3 diagnostic uses one server source and one client image.
It sends 2,000 warmup requests and then measures 1,000 requests.

| Fixed diagnostic | Before | After |
|---|---:|---:|
| Native allocations per request | 50.854 | 47.863 |
| Callgrind allocations per request | 50.854 | 47.863 |

The complete profiles include startup and warmup.
Their array-box calls change as follows.
These counts do not use the 1,000-request measurement denominator.

| Function | Before | After |
|---|---:|---:|
| `__h3_event` | 3,005 | 0 |
| `__h3_datagram` | 6,010 | 3,003 |
| `__h3_pass` | 3,013 | 3 |

The longer allocation gates give these results.

| Workload | Candidate | Limit | Result |
|---|---:|---:|---|
| Native hello | 0 | 0 | pass |
| Ring hello | 0 | 0 | pass |
| ARM64 hello | 0 | 0 | pass |
| Wasm hello | 0 | 0 | pass |
| HTTP/2 | 20 | 21 | pass |
| HTTP/1.1 TLS | 10 | 11 | pass |
| HTTP/3 | 47.93 | 48 | pass |
| Multiplexed HTTP/3 | 29.02 | 29 | fail |
| Two-fetch | 61 | 58 | fail |

The zero rows permit fixed and Date-cache overhead.
All nine programs compile and execute.
No allocation limit changed.
The short multiplexed diagnostic reports 28.990 natively and 28.993 under Callgrind, with 64 requests in flight.
Those diagnostic results do not override the failed 100,000-request gate.

## Fixed checker comparison

The comparison retains main `26d01d8` from published parent `d0a268e`.
Main later advanced to `8a758fb` with a lexer change.
That change needs separate integration checks.

Only `sempair.fern` and `physicalbody.fern` differ between the compiler inputs.
The other 1,242 compiler and library files are byte-identical.
The fixed checker subject, its imports and its library remain unchanged.
Both profiles enable strict IR checks and disable the RC certificate memo.

| Compiler | Callgrind instructions |
|---|---:|
| Published parent | 38,063,087,226 |
| Three-reference tuple returns | 37,651,605,393 |

The decrease is 411,481,833 instructions, or 1.08105218 percent.
The raw summary, totals and collected count agree.
These profiles retain addresses without symbolic function attribution.
The ABI change alters emitted output on all three targets.
The runtime tests and the bootstrap fixpoint provide the correctness gates.
No native latency or throughput result is claimed.

```text
common stage1 sha256: 84bc2d65e9ef15339877ca39b8acdf5c727cdb5e4a472fd0df71d055dd17104f
fixed checker subject sha256: 5ace3eadd42ce301ec112e6d448ce4420c30e25a08895ba320be41f37aa8fe68
before compiler sha256: 6da8d3a9341e9aca42b88d1ab3fae27ba37fcaa1577d1cb3668edece7b06293d
after compiler sha256: 5ff5eb7acb83a4d8dc33aa178d245aa0fbc4489ddd65babd2a414038527c2aac
before raw checker profile sha256: 8e595660cb063ab33bcec427edf9473a976f7e5cdeff31e6004a01c0b2268904
after raw checker profile sha256: ec90356824628066b45e82c14ec0af27c5931180a108ff75dff9c359186b73a9
before raw HTTP3 profile sha256: b4616fa9c135ba69f709faea036684829e6ac6e251bfa93676a377ad3a065adb
after raw HTTP3 profile sha256: 42e17d3977518511e03e1364563350c707a7b8a690caef455cbb2399d9939254
bootstrap fixpoint sha256: 88ebadf8efaf4b572d6952afb90ea6ec138cb3ce93d3d872885a12875768e7bd
```

The first prototype omitted the second pair setter's call contract.
The compiler rejected its runtime probes before execution.
That source, image and rejection logs remain intact.
The corrected source installs the owned setter contract.
The formatted source produces a byte-identical stage-2 compiler.

The two allocation failures still block full acceptance.
Current-head CI remains separate from these local results.
Issue 4412 remains incomplete.
