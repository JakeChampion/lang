# Current main validation

Base: cebda6528fdc0fd368a42d5061c86490df04fee5, 2026-10-22.
Source: 54af47718, before this validation note.

The 80 donor ABI probes and the ARM64 hardening suite pass in 31.835 seconds.
The probes execute both native Linux targets, with ARM64 under QEMU.
The PAC mutation faults, and the signature-strip control returns with zero live bytes.
The producer is the pinned stage0 from bootstrap/stage0.lock.

Command:

```sh
go test ./internal/testing/e2ecompiler \
  -run '^(TestSelfHostAllocReuseLargeDonor|TestSelfHostHardenArm64)$' \
  -count=1 -timeout=12m -v
```

The broader reuse, WASI, certification, call ABI, sanitizer, heap-class,
inventory and complexity checks pass in 115.717 seconds.
CI lane coverage checks pass in 1.552 seconds.

Bootstrap stages 2 and 3 match at 13,305,712 bytes, SHA-256
1cc403e4384a86fe41f30d49f7505e99296f022eba3c08d15c4f457966fa8978.
Stage 1 differs. The bootstrap record contains all three stages and the source fingerprint.
All eleven strict size checks pass with complete coverage and unchanged limits.
The driver build takes 113.514 seconds.

The main-ceb/ directory contains the complete logs, hashes and size report.
The earlier measurements retain their recorded source scope.

CI found a format error in the archived off-probe.fern fixture.
The fixture and its reproduction script now use the required format.
A fresh comparison with the same historical producers and stdlib gives identical binaries on both native targets.
The original results remain unchanged. The off-format files contain the fresh comparison and its source hash.
