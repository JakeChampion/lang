# Forward-edge control-flow integrity

Status: implemented under `-harden` for [SEC-016](https://github.com/JakeChampion/lang/issues/13518).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The hardening flag as a whole is described in `docs/HARDENING.md`.

## The rule

An indirect call can only reach a function whose address the program takes.
Under `-harden` a function value does not hold the function's entry.
It holds the address of a stub that starts with the landing pad of the target architecture and then branches to the entry:

| Target | Stub | Check before the call |
|---|---|---|
| x86-64-linux | `endbr64` then `jmp __fn_X.r` | the four bytes at the target must be `f3 0f 1e fa` |
| arm64-linux | `bti c` then `b __fn_X.k` | the word at the target must be `0xd503245f` |

Every `call_indirect` the backend emits loads the target, reads its first word, and compares it with the pad.
A mismatch branches to `__fern_cfi_fail`, which prints `fern: indirect call to an address that is not a function` and exits with status 134.
The check runs before the call, so a forged pointer never executes.

The stubs are the allowed-target metadata.
They live in the text segment, which the kernel maps read-only and executable, so a write through a dangling pointer cannot add a target.
A function that no value refers to has no stub and no pad. Its entry is not a valid target, even when its signature matches.
Each compilation unit emits one stub per function it takes the address of. The label carries the unit's namespace, so sharded builds do not collide.

The pads are the ones the hardware mitigations use.
On arm64 the stub's `bti c` is the landing pad BTI requires, and the function entries no longer carry one. That narrows the BTI target set to the same allowed set.
Runtime entry points the kernel or a trampoline reaches, such as `__fern_sig_catcher`, carry `bti jc`, which BTI accepts and the software check does not.
On x86-64 the stub's `endbr64` is the entry IBT requires, and it executes as a NOP on a processor without IBT.
The software check runs before the hardware one, so a BTI fault test has to turn the compare off to reach the hardware.

## What it costs

A build without `-harden` is byte-identical to the build before this change.
Under `-harden` an indirect call gains one load, one compare and one branch, and each address-taken function gains a stub of two instructions.

## Verification

`TestSelfHostForwardEdgeCFI` runs on x86-64-linux natively and on arm64-linux under QEMU.

- The control builds with `-harden` and with `-harden -pie`, runs, and prints the same output as the plain build.
- The plain image carries no pad and no check.
- The `-harden` image carries one pad per address-taken function.
- Three forged builds patch the emitted assembly, link it with gcc and run it. The function value points at the real entry, which has no pad. The second points at a function with the same signature whose address is never taken, and the third at a data address. Each prints the cause and exits with status 134.

```sh
go test ./internal/testing/e2ecompiler -run 'TestSelfHostForwardEdgeCFI|TestSelfHostHardenArm64|TestSelfHostStackCanary' -count=1 -timeout 0
```

## Remaining limits

- The check is on the target, not on the signature. A forged pointer to another address-taken function with a different signature passes the check and misreads its arguments.
- The backward edge is the stack canary and, on arm64, the signed return address. The x86-64 shadow stack is SEC-030.
- The runtime's own indirect branches, the signal trampolines and the C shim, do not check their targets. Their targets are stubs or C code.
- The x86-64 image carries no IBT property note, and Linux enforces no user-space IBT ([SHADOW-STACK.md](SHADOW-STACK.md)).
- A `-shared` object refuses `-harden`, so an export reached through the dynamic loader has no stub.
