# Calling convention and frame audit

Status: [reference] audit record for [SEC-013](https://github.com/JakeChampion/lang/issues/13515) and [SEC-014](https://github.com/JakeChampion/lang/issues/13516).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).

The record reads both native backends as they stand.
It names the convention Fern code uses among itself, the boundary where the platform convention applies, the tests that pin each rule, and the gaps.
It changes one thing, which the last section records.

## Two conventions

Fern code does not call Fern code on the platform convention.
`docs/SELFHOST-SSA-BACKEND.md` defines the internal convention, and this table restates the parts that matter here.

| Rule | x86-64 | arm64 |
|---|---|---|
| Frame record | `pushq %rbp; movq %rsp, %rbp` only in a function that needs a frame pointer; the rest keep `%rsp` fixed and save registers with `pushq` | `stp x29, x30, [sp, #-16]!; mov x29, sp` |
| Stack-entry parameters | `16 + 8*i(%rbp)` | `[x29, #16 + 16*i]`, 16 bytes per slot |
| Register-entry pool | `%rax, %rsi, %rdi, %r8, %r9, %r10` | `x0, x9` to `x15` |
| Callee-saved pool | `%rbx, %r12` to `%r15`, saved per function when used | `x19` to `x28`, saved per function when used |
| Scratch the emitter owns | `%r11`, `%rcx`, `%rdx` | `x4`, `x16`, `x17` |
| Reserved and untouched | none | `x18` |
| Float values | the 8-byte pattern in an integer register | the same; lane values in `v16` to `v31`, never `v8` to `v15` |
| Stack pointer across a call | fixed; frame words hold cold saves | fixed; frame words hold cold saves |
| Unwind data | `.cfi_` directives and `.eh_frame` | `.cfi_` directives and `.eh_frame` |
| Thread-local storage | none; no `%fs` access | none; no `tpidr_el0` access |

The internal pool is a subset of each platform's caller-saved set, and the callee-saved pool is a subset of each platform's callee-saved set.
A Fern function called from C through `-shared` preserves everything in its own callee-saved pool.
It uses no other callee-saved register, so it preserves what the platform requires.
Floats never occupy `d8` to `d15` or a callee-saved vector register.

The platform convention applies at two boundaries.
A raw syscall (`__syscall3` to `__syscall6`) is the kernel's convention, and `TestSelfHostProfileSyscallABI` pins the numbers per target.
A foreign call (`__c_call0` to `__c_call4`) goes through one shim per arity (`emit_ccall_shim1` in `compiler/asm_ir.fern`, `emit_ccall_shim1_arm64` in `compiler/asm_arm64_ir.fern`).

The shim builds a frame record and loads the callee from the stack entry.
It loads up to four integer arguments into the platform argument registers, and calls.
On x86-64 it aligns the stack pointer to 16 bytes before the call.
It also zeroes `%al`, which System V reads as the vector register count for a variadic callee.
On arm64 the stack pointer stays 16-byte aligned because every slot is 16 bytes, and the hardware faults on a misaligned stack-pointer access.

A result returns in `%rax` or `x0`.
An `f32` or `f64` result moves from `%xmm0` or `d0` into that register after a widening of `f32`.

## What the tests pin

| Rule | Test |
|---|---|
| The shim's register loads, alignment, and call on x86-64 | `TestSelfHostCCallIRX86_64` |
| The shim's register loads and call on arm64 | `TestSelfHostCCallIRArm64` |
| A C callee entered through the shim sees a 16-byte aligned stack, and a callee that clobbers every caller-saved integer and vector register leaves the Fern caller's live values intact | `TestSharedLibX86CCallFFI`, `TestArm64SharedLibCCallFFI` |
| A float result returns in the result register | `TestSelfHostCCallFPShimsReturnInTheResultRegister` |
| Exported Fern functions called from C with integer, float, and ten-argument signatures | `TestSharedLibX86ExportDlopen`, `TestSharedLibX86ExportArgsJNI` |
| A leaf builds no frame and a frame that saves a register records the CFA offset | `TestSelfHostLeafFrame` |
| A large frame on arm64 | `TestArm64LargeFrame` |
| Spills, spill-slot mates, eviction, and phi cycles across spills | `TestSelfHostSSASpillCost`, `TestSelfHostSpillSlotMates`, `TestSelfHostSSAABIEviction`, `TestSelfHostSSAPhiCyclesAcrossSpills`, `TestSelfHostSSAEvictionSpillsEveryLiveSharer` |
| Every emitted instruction form encodes as GNU as does | `TestSelfHost{X86,Arm64}*FormsMatchGas`, `TestSelfHost{X86,Arm64}*TableRowsMatchGas` |
| Unwind data on both Linux targets and on Darwin | `TestSelfHostUnwindData`, `TestArm64DarwinEhFrame`, `TestSelfHostMachOPlacesEhFrame` |
| A backtrace from an abort walks the frames on both targets | `TestX86_64AbortBacktrace`, `TestArm64AbortBacktrace`, `TestSelfHostAbortHelperFrameAgrees` |
| Mixed calls under random pass configurations agree with the oracle | `TestDifferential_SelfHostX86_64` with `selfHostPassSwitches` |

## Gaps

| Gap | Consequence |
|---|---|
| A foreign call takes at most four integer arguments and no float argument | A C function with a float parameter or a fifth parameter has no safe call; the checker refuses nothing, because the shim count is the whole surface |
| A variadic C callee on arm64-darwin | Apple's convention passes variadic arguments on the stack; the shim passes them in registers, so such a call is wrong on that target |
| Tail calls | A call in tail position to a register entry is a jump (`jmp __fn_f.r`, `b __fn_f.r`), so the caller's frame is gone and a backtrace omits it; `TestSelfHostAbortBacktraceCorruptFrame` keeps work after its call for that reason |
| The register entry `.r` is not exported | A C caller reaches only the stack entry, which is the exported symbol, so this is a limit on size and not on safety |
| A signal handler's frame | The alternate signal stack is 64 KiB (`docs/security/STACK-GUARD.md`); a handler that allocates is outside this record |

## Changes in this record

The x86-64 shim did not set `%al` before the call.
System V reads `%al` as an upper bound on the vector registers a variadic callee must save.
A variadic callee therefore read whatever the register held.
The shim now zeroes it, which is correct for every callee because the shim passes no vector argument.
`TestSelfHostCCallIRX86_64` asserts the instruction, and `TestSharedLibX86CCallFFI` gains the alignment callee and the clobbering callee.

The arm64 shim had no run-time test. `TestArm64SharedLibCCallFFI` now loads an arm64 shared object from a C program under QEMU and calls back into C through every shim arity.
Its callees check the AAPCS64 stack alignment and clobber every caller-saved integer and vector register.

## Commands

```
go test ./internal/testing/e2ecompiler -run 'TestSelfHostCCallIR|TestSelfHostLeafFrame|TestSelfHostUnwindData|TestSelfHostSSAABIEviction'
go test ./internal/testing/e2e -run 'TestSharedLibX86CCallFFI|TestArm64SharedLibCCallFFI|TestSharedLibX86ExportDlopen|TestSharedLibX86ExportArgsJNI|TestX86_64AbortBacktrace|TestArm64AbortBacktrace'
```

Host: Linux 6.18.44-fc-v165, Intel(R) Xeon(R) Processor @ 2.80GHz, Go go1.27.3, QEMU 8.2.2, wasmtime 46.0.1.
