# Current capture checks

The source commit is `d87fbed08cbc344940eb05190b200966c8493c7d`.
The native compiler and library bytes match checkpoint `76f95b0edb8b0de0881b2f7f10f9ca8c63a78b8f`.
The later commit changes the Go interpreter and adds tests for issue #13128.
Earlier evidence in the parent directory retains its original source scope.

The compiler records the last assignment before a closure capture.
A read-only capture after the last write no longer forces the value into a shared cell.
Later assignments, loop backedges, deferred writes and writer closures still use a shared cell.
Range loops keep the feature census within its unchanged limit.

The self interpreter also shares a cell between an earlier reader and a later writer.
This fixes issue #13127.
The Go interpreter resolves a local function before a builtin with the same name.
This fixes issue #13128.

All 702 broad compiler regression cases and 36 focused cases pass without failures or skips.
The focused cases include the unchanged feature census and complexity limits.
The full Go interpreter suite passes all applicable cases.
Its three skips cover synthetic AST nodes that the source parser does not produce.
All seven local function tests and 32 related compiler cases pass without failures or skips.

The matrix runs 33 commands against three exact inputs.
Both checkers and both interpreters agree.
All four target builds pass.
All nine executions on x86-64, ARM64 and WASI balance allocations with zero live bytes.
Darwin is a compile check.

The reader and writer input asserts the expected value, 77, then returns zero.
The separate public baseline probe retains the original return value.

Four pairs alternate execution order for each map size on the same host.
Both compilers use identical benchmark source and library files.

| Map keys | Baseline allocations | Candidate allocations | Baseline median | Candidate median |
| ---: | ---: | ---: | ---: | ---: |
| 25000 | 50016 | 15 | 0.271281 s | 0.002200 s |
| 50000 | 100017 | 16 | 2.055616 s | 0.004142 s |
| 100000 | 200018 | 17 | 11.143075 s | 0.008044 s |

Four compiler pairs compile the same complete compiler source.
The baseline median is 16.534762 seconds. The candidate median is 16.264348 seconds.
Every candidate output matches the final stage 3 image byte for byte.
These times describe this host and workload.
The allocation counts are exact.

The published stage0 pin produces byte-identical stages 2 and 3.
All 11 strict driver size gates pass without baseline changes.
Go vet, Go format and all 1201 canonical Fern format checks pass.
The manifest records 497 source file hashes and 29 evidence artifacts.
The source patch passes a reverse-application check.
The archive contains all 66 matrix output logs and three input files.
