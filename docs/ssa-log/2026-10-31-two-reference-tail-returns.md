# Two owned references in tuple returns

The published hello loop still allocated two tuples for each request.
The current native profile names `__flight_event` and `__serve_stop_step` as the two hot allocation sites.
The first returns two references and a boolean.
The second returns two references and two booleans.

The new tuple ABI carries the first typed reference as the primary result.
Pair word zero carries the second reference through the owned channel.
Scalar lane one carries a scalar, or two boolean bits.
The transformation uses the pair-word and scalar-lane operations on all three tested targets.

Only three-field tuples with two references and one scalar, or four-field tuples with two references and two booleans, use this shape.
Both reference parts must fit a word and contain no view or direct finalizer.
Records with reference fields retain their prior ABI.
Both references keep separate ownership metadata.
The scalar lane carries no ownership unit.

The physical ABI metadata marks both reference words as owned.
The verifier rejects a leaked primary reference and a leaked secondary reference.
It also rejects a scalar lane as an ownership transfer.
The runtime controls cover aliases, nested finalizers, strings and every boolean combination on x86-64, ARM64 and Wasm.

The focused suites pass 432 results, with no skips.

Some CLI probes used the older harness library.
The 432 results do not establish complete qualification against the tested compiler library.
The later main integration binds these probes to the intended library.
The protocol gates and fixed checker profiles used their recorded library paths.

Stage 3 and stage 4 are byte-identical.
All four index-serial controls pass.
The baseline compiler fails both new zero-tuple-allocation controls on x86-64, with exit 92.
The candidate passes those controls on all three targets.

## Protocol allocation gates

| Workload | Published baseline | Candidate | Limit | Candidate result |
|---|---:|---:|---:|---|
| Native hello | 2 | 0 | 0 | pass |
| Ring hello | 2 | 0 | 0 | pass |
| ARM64 hello | 2 | 0 | 0 | pass |
| Wasm hello | whole-number failure | 0 | 0 | pass |
| HTTP/2 | 21 | 20 | 21 | pass |
| HTTP/1.1 TLS | 12 | 10 | 11 | pass |
| HTTP/3 | 51.93 | 50.93 | 48 | fail |
| Multiplexed HTTP/3 | 29.11 | 29.09 | 29 | fail |
| Two-fetch | 67 | 61 | 58 | fail |

The table follows each gate's units and numeric rules.
The zero gates permit fixed and Date-cache overhead.
Native hello records 32 allocations over 100,000 requests, within that allowance.
Wasm records 17 over 10,000 requests, compared with 36,588 for the baseline.
No allocation limit changed.

Six protocol gates pass and three fail.
The remaining failures still block full acceptance.
Issue 4412 remains open.

## Fixed checker comparison

Both stage-2 compilers use one stage-1 compiler.
They share all 1,244 compiler and library inputs, except for the three ABI modules.
The subject and its import files remain fixed throughout both profiles.
Both runs enable strict IR checks and disable the RC certificate memo.

| Compiler | Callgrind instructions |
|---|---:|
| Published parent | 38,389,310,158 |
| Two references and a scalar tail | 38,058,504,388 |

The decrease is 330,805,770 instructions, or 0.86171324 percent.
This result describes compiler work on the fixed subject.
It does not establish a native latency result.
The raw summary, totals and collected count agree for each profile.
The profiles retain addresses without symbolic function attribution.

The ABI change alters emitted output on x86-64, ARM64 and Wasm.
The runtime controls and the bootstrap fixpoint provide the correctness gates.
The feature census passes 26 results on the candidate source.

The comparison uses the compiler source of parent `7ce4ecd553b76371d385c21c61633801943d089e`.
Main later advanced to `1aea7f8e4c97b95ea07006fb502e9e67c042f278`.
That revision changes the parser, checker and serve library.
These measurements retain the tested parent snapshot.

```text
common stage1 sha256: 84bc2d65e9ef15339877ca39b8acdf5c727cdb5e4a472fd0df71d055dd17104f
fixed subject sha256: 5ace3eadd42ce301ec112e6d448ce4420c30e25a08895ba320be41f37aa8fe68
before compiler sha256: b90d4b6a328b10505ae5fe50203d411825c91b35d590c31a58b913a2420439d9
before raw profile sha256: a2209f628a2546ca284a11b87116a1b3187f0fe4a54eaebdda791d87a6aff47d
after compiler sha256: 20a02fcf789a6bfc15da35524047744701fba0f8eb111750c6452fd24dfd37a3
after raw profile sha256: 5d42cd2cb7d6bc6f495a99b78fcf3de2bbe2712015a5a05154346cd822685124
bootstrap fixpoint sha256: 8ce3c3dbcd911147c55cdb0cabaf6f6e2e24ee3540c24706ff31470186756833
```
