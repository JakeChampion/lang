# 2026-10-12 — one context per heap

Refs #11914, route 1: checked share-nothing. Fixes #12169.

## The shape

Counts stay non-atomic, so the guarantee is that no two contexts run Fern code
against one heap. `docs/RC-DESIGN.md` §2.1 now lists each boundary where a
second context exists and what holds it: separate processes for fork and
subprocess, a fixed flag-setting stub for signals, one thread for tasks, and
scalar-only signatures for wasm async exports.

The one boundary nothing held was a `-shared` export, which a host can call
from any thread. Each export entry now takes `__fern_busy` with an atomic
exchange and aborts if another thread holds it, and releases it on return.
A `__c_call` releases it for the length of the call, so a C callback that
enters an export on the same thread runs, as a JNI callback does. A host may
still call from different threads one at a time. Ordinary executables carry
none of this: the `shared` switch gates it.

Its overlap test showed every native exit used the thread-exit syscall, so a
fatal report inside a host thread ended that thread and the host exited 0.
Exits and aborts now use `exit_group` (#12169).

`TestX86_64SignalPreemption` and `TestArm64SignalPreemption` hold the signal
row under real preemption. A census build builds and drops 64 formatted
strings per round for 20,000 rounds while the test sends it SIGUSR1 from
outside, and the run must match a quiet one.

## Measured

| | x86-64 | arm64 (qemu) |
|---|---|---|
| signals sent during the run | 496 | 2,759 |
| allocations, quiet and stormy runs alike | 7,040,003 | 7,040,003 |
