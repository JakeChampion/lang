# Security policy

## Reporting a vulnerability

Report a vulnerability in private through GitHub's private vulnerability
reporting on this repository (the "Report a vulnerability" button under the
Security tab). Do not open a public issue for a vulnerability. When the
button is absent, the owner has not enabled private reporting yet. Then send
the report to the owner through a private GitHub channel and say that it is
a security report.

A report needs the Fern version or commit, the target (`fern -manifest`
prints both for a built program), the program or input that shows the
problem, and what you observed. A report that reaches the owner gets an
acknowledgement within seven days.

## What is in scope

- The compiler, its runtime, and the standard library, for the targets
  `docs/BACKEND-PARITY.md` lists as supported.
- The package tools (`fern -fetch`, `fern -resolve`, `fern -vendor`) and
  the hash-addressed package store.
- The release artifacts `release.yml` publishes.

A program that reaches raw memory or system calls through `unsafe` or the
raw builtins is responsible for its own obligations
(`docs/security/UNSAFE-INVENTORY.md`). A hardware mitigation that the host's
processor or kernel does not have is reported as absent by
`std/mitigations`, never claimed.

## What the compiler guarantees

`docs/security/GUARANTEES.md` lists each guarantee with the test that
proves it and the hosts the test ran on. A claim that no test proves is not
on that list. `docs/security/ADVERSARIAL-SUITE.md` indexes the attack-shaped
tests, and `docs/security/BASELINE.md` tracks the security program.

## Releases

`docs/security/RELEASE-CHECKLIST.md` names the gates a release must pass
and the evidence each one leaves. A release artifact carries a SHA-256
sidecar. The macOS artifacts are signed and notarized.
