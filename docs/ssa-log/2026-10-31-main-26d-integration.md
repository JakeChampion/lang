# Main integration at 26d01d8

This update integrates main `26d01d8578ce72ef1797fe64ebdd6bb97855d823` into the branch at `0a170766a9982d27762be3095bb6cea2b25d4634`.
It preserves the parser, checker, server and comparison traits from main.
Before integration, the branch copies of the five changed compiler and library files matched the common merge base.
Those files require no conflict resolution.
The integration preserves all three tuple-ABI modules from the branch.

## Test scope

The stage-2 compiler uses the same fixed stage-1 compiler as the prior tuple-ABI comparison.
All 1,244 compiler and library input hashes match after the build and qualification.
The focused suites pass 432 results against the integrated library, with no skips.
The source census passes 26 results.
Stage 3 and stage 4 are byte-identical.
All four index-serial controls pass.

The main feature controls pass 16 additional results.
These cover block-tail expressions, Option/Result comparison traits, the 40-peer connection cap and connection cost.
This total includes two Go parser and checker results.
The block-tail fixture reports a balanced zero-allocation census.
The comparison-trait fixture reports a balanced nonzero census.
The ARM64 results use QEMU, and the Wasm results use Wasmtime.

Some earlier CLI probes used the older library in the isolated Go harness.
Their 432 pass results did not establish complete qualification against the intended library.
This qualification binds every CLI probe to the integrated library.
The correction does not alter the prior compiler build hashes or fixed checker profiles.
The prior protocol tests already used their recorded library paths.

## Protocol allocation gates

| Workload | Integrated candidate | Limit | Result |
|---|---:|---:|---|
| Native hello | 0 | 0 | pass |
| Ring hello | 0 | 0 | pass |
| ARM64 hello | 0 | 0 | pass |
| Wasm hello | 0 | 0 | pass |
| HTTP/2 | 20 | 21 | pass |
| HTTP/1.1 TLS | 10 | 11 | pass |
| HTTP/3 | 50.93 | 48 | fail |
| Multiplexed HTTP/3 | 29.09 | 29 | fail |
| Two-fetch | 61 | 58 | fail |

The table follows each gate's units and numeric rules.
The zero rows permit fixed and Date-cache overhead.
Native hello records 32 allocations over 100,000 requests.
All nine protocol programs compile and execute.
No allocation limit changed.

## Fixed checker comparison

The fixed checker uses 38,058,504,388 instructions before integration and 38,063,087,226 after integration.
The increase is 4,582,838 instructions, or 0.01204156 percent.
This result does not show a compiler-speed gain.

This comparison includes the main features and does not isolate a new optimization.
The subject, its imports and its library remain fixed.
Both profiles disable the RC certificate memo and enable strict IR checks.
The emitted assembly is byte-identical on x86-64, ARM64 and Wasm.
The raw profiles retain addresses without symbolic function attribution.
No native latency or throughput result is claimed.

```text
common stage1 sha256: 84bc2d65e9ef15339877ca39b8acdf5c727cdb5e4a472fd0df71d055dd17104f
before compiler sha256: 20a02fcf789a6bfc15da35524047744701fba0f8eb111750c6452fd24dfd37a3
after compiler sha256: 6da8d3a9341e9aca42b88d1ab3fae27ba37fcaa1577d1cb3668edece7b06293d
fixed subject sha256: 5ace3eadd42ce301ec112e6d448ce4420c30e25a08895ba320be41f37aa8fe68
bootstrap fixpoint sha256: c4b28b4cd81b760b1732b583fbbde31838042d91a310c0cb10810ddc5942771f
before raw profile sha256: 5d42cd2cb7d6bc6f495a99b78fcf3de2bbe2712015a5a05154346cd822685124
after raw profile sha256: 8e595660cb063ab33bcec427edf9473a976f7e5cdeff31e6004a01c0b2268904
```

The three protocol failures still block full acceptance.
Current-head CI remains separate from these local results.
Issue 4412 remains incomplete.
