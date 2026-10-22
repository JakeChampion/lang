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

The earlier measurements retain their recorded source scope.
Broader reuse checks, a fresh bootstrap, and strict size checks are in progress.
