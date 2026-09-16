# arm64ssa: the last helper the corpus asked for

**Date:** 2026-09-16

## Why

Measuring the two conditions for making the SSA backends the default turned up
one concrete gap. Over the 348-program corpus differential:

| Target | Agree | SSA refused | Diverge | Baseline rejected |
|---|---|---|---|---|
| x86-64-linux | 328 | 0 | 0 | 20 |
| arm64-linux | 326 | 2 | 0 | 20 |

Both arm64 refusals were the same diagnostic, `a runtime helper this backend
does not emit: fn_tcp_connect`. The backend had the rest of the socket family
— listen, accept, recv, send, close, pollable — and only this one was missing.

## Change

`emitTcpConnectHelper` writes `tcp_connect(host_be, port) -> i32`: an AF_INET
socket connected to the address, its fd, or the `-errno` of whichever syscall
failed. `host_be` arrives already in network byte order, so it goes into
`sin_addr` as it is; the port is byte-swapped with `rev16`.

It builds the sockaddr_in from the incoming arguments before `socket(2)` runs,
which is what lets it hold one callee-saved register where `tcp_listen` needs
two: only the fd has to survive a syscall. The stack pointer returns before
either branch to the exit, so the failure path leaves the frame as it found it.

## Result

The arm64 differential now reads 328 agree, 0 refused, 0 diverge — the same
coverage x86-64 already had, over the same corpus.

## Tests

- `TestArm64SSACliRoundtrip/tcp_connect_loopback_roundtrip`: one process plays
  both ends over loopback, since a connect completes in the kernel's backlog
  before accept runs. It sends and receives, closes all three descriptors, and
  requires a connect to the now-closed port to come back negative, which is the
  `-errno` path. The x86-64 leg has the same shape in
  `x86_64_ssa_sockets_test.go`; the port differs so the two can run at once.
- The corpus differential is the end-to-end check: the two programs that were
  refused now compile, run, and agree with the shipping backend.
