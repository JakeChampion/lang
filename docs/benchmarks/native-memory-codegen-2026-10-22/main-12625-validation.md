# Native memory code generation on main 12625f42b

Base: 12625f42bccd4e1b9cdbfe302d612849bdb72306.
Source: 89eaede22c77142b65d9fa7aa2ae2f947d112e5f.
The Go test runner uses the current pin, Go 1.27.3.

The focused suite passes in 13.173 seconds.
It covers constant raw-word offsets, allocator tail calls, large donor release, ARM64 hardening, the feature census and lane-call refusals.
The allocator fixture also requires an ordinary vector tail call to retain its vector argument registers.
Both native writers and GAS pass, including sanitizer modes and ARM64 hardening.

The broader suite passes in 188.601 seconds.
It covers tail recursion, cleanup, allocator entries, WASI loop reuse, both Linux profilers, constants, call ABI, registries and complexity.

Bootstrap stages 2 and 3 match at 14,147,424 bytes, SHA-256
2fddeeb3c3b056b7041e3b5dda552d4cda7e6470eb95d8d24735771551d1697c.
Stage 1 differs.
All eleven strict size checks pass with complete coverage and unchanged limits.
The driver build takes 112.762 seconds.

The main-12625 directory contains complete logs, commands, stage hashes and the source fingerprint.
The documentation commit leaves the compiler and standard library source fingerprint unchanged.
All earlier measurements retain their recorded source and compiler scope.
