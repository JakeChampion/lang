# Main integration at 8a758fb

This update integrates main `8a758fb9d4256127b3e3799a64071875441a52e2` into the branch at `ccecb57e9168383d50ab4055e180af5c28c61d31`.
Main adds a diagnostic for a backslash inside an `f-string` interpolation.
Main also preserves escaped character literals inside an interpolation.
The update includes both lexer implementations, both conformance cases and the conformance census.
The tuple ABI remains unchanged.

## Test scope

Only `compiler/lexer.fern` changes among the 1,244 compiler and library inputs from the prior candidate.
The stage-2 build and the final qualification preserve all recorded input hashes.
The compiler and library paths in each CLI adapter select the current snapshot.

The focused suites pass 432 results, with no skips.
The three-reference return, ownership and forward controls pass 26 results.
The prior main feature controls pass 14 results.
The new lexer conformance controls pass nine results across x86-64, ARM64 and Wasm.
The Go lexer package passes 74 results.

The source census passes 26 results.
Stage 3 and stage 4 are byte-identical.
All four index-serial controls pass.

## Protocol allocation gates

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

The zero rows follow the fixed and Date-cache overhead rules of their gates.
Serial HTTP/3 records 4,792,611 allocations over 100,000 requests in six seconds.
Multiplexed HTTP/3 records 2,901,773 allocations over 100,000 requests, with a peak of 64 concurrent requests.
Two-fetch records 610,038 allocations over 10,000 requests.
All nine programs compile and execute.
No allocation limit changed.

## Fixed checker comparison

Both stage-2 compilers use one fixed stage-1 compiler.
The checker subject, its imports and its library remain fixed.
Both profiles disable the RC certificate memo and enable strict IR checks.

The instruction count changes from 37,651,605,393 to 37,651,605,393.
The change is +0 instructions, or +0.00000000 percent.
This comparison includes the main lexer change and does not isolate a new optimization.
The emitted output is byte-identical on x86-64, ARM64 and Wasm.
The raw profiles retain addresses without symbolic function attribution.
No native latency or throughput result is claimed.

```text
common stage1 sha256: 84bc2d65e9ef15339877ca39b8acdf5c727cdb5e4a472fd0df71d055dd17104f
before compiler sha256: 5ff5eb7acb83a4d8dc33aa178d245aa0fbc4489ddd65babd2a414038527c2aac
after compiler sha256: 6bb756c1369ff74ca750cd5a803c208cc46898e63586467ed35428da32d6a734
fixed subject sha256: 5ace3eadd42ce301ec112e6d448ce4420c30e25a08895ba320be41f37aa8fe68
bootstrap fixpoint sha256: 2a8c33be35ec2fdd69a7ec2a11f79340a00334fcaad96fa446f0d2c61fea2716
before raw profile sha256: ec90356824628066b45e82c14ec0af27c5931180a108ff75dff9c359186b73a9
after raw profile sha256: 044cb2c99379e4119e8675a4ef696937c66d15732f33f28f6641c60c04566ee7
```

## Runtime profile scope

The candidate emits byte-identical assembly for the fixed serial HTTP/3 server, multiplexed server and client, and two-fetch server.
All library inputs match the prior profiles.
The serial and multiplexed servers share identical source and assembly.
The identity records preserve the exact subject and assembly hashes.
The prior runtime profiles remain evidence for this identical code.
This identity check does not repeat those runtime profiles.

The prior two-fetch A/B reports 61.04 allocations per request for both compilers under Callgrind.
Its fixed proxy returns the same bytes, and each variant makes exactly 500 upstream requests.
The two variants emit byte-identical assembly.
The tuple change gives no two-fetch gain in this control.

The small multiplexed diagnostic reports 28.990 allocations per request natively and 28.993 under Callgrind.
Both use 2,000 warmup requests, 1,000 measured requests and a peak of 64 concurrent requests.
Those results do not replace the failed 100,000-request gate.
The raw profile call counts include startup and warmup.
They cannot establish allocations per request without the separate counter interval.

The two allocation failures still block full acceptance.
Current-head CI remains separate from these local results.
Issue 4412 remains incomplete.
