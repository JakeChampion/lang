# Safety contract and unsafe boundary proposal

Status: [research] proposal for [SEC-003](https://github.com/JakeChampion/lang/issues/13505).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
This document separates current behavior from proposed requirements.
It changes no compiler behavior.

## Current behavior

Fern uses typed values, reference counts, copy-on-write collections, and ownership modes.
The checker rejects specific ownership and view escapes.
It does not implement the Rust borrow model.
The normative examples live in `conformance/cases/`.

| Rule | Normative example | Current result |
|---|---|---|
| Use after consumption of an owned parameter | `diag_e050` | The checker rejects the use |
| Borrowed argument at an owned parameter | `diag_e051` | The checker rejects the call |
| Local array view returned to a caller | `diag_e063` | The checker rejects the escape |
| Local string view returned to a caller | `diag_e065` | The checker rejects the escape |
| Closure capture of a borrowed view | `diag_e082` | The checker rejects the capture |
| Array index outside its length | `oob_index_read`, `oob_index_negative`, `oob_index_write` | The runtime aborts |
| Invalid array slice construction | `oob_slice_range` | The runtime aborts |
| Empty slice at the array length | `slice_at_length` | The operation succeeds |
| Ordinary integer overflow | `int_wrap` | Arithmetic wraps at the operand width |
| Checked integer arithmetic | `int_checked_ops` | Arithmetic returns `Some` or `None` |

See [the semantic index](../spec/semantics.md) for further rules and examples.
See [integer semantics](INTEGER-SEMANTICS.md) for division, shifts, and casts.
This proposal preserves those integer rules.
Memory safety does not require every arithmetic overflow to trap.
Allocation and address calculations require separate range checks.

Fern uses optional values instead of a general null value.
That type distinction does not prove definite initialization on every control-flow path.
SEC-006 must audit those paths and raw-memory initialization separately.

Raw memory and syscall intrinsics form a trusted implementation floor.
They can forge addresses, corrupt array lengths, bypass bounds checks, or change memory permissions.
FFI code can violate the same invariants.
The current language lacks the explicit unsafe boundary that this program requests.
Therefore, the existence of bounds and ownership checks does not establish complete safe-code memory safety today.

The current runtime assumes non-atomic reference counts.
Async I/O does not imply shared-heap thread safety.
An interrupt, foreign thread, or raw syscall can violate that assumption.
SEC-009 and SEC-020 require a separate concurrency design.

## Proposed safe-code requirements

These requirements define the target contract, not a current certification.
Safe code must not forge references or expose unchecked address arithmetic.
It must not observe expired references, double releases, or uninitialized values.
It must not access memory outside a valid array or slice.
The compiler must preserve these properties after optimization.

A safe wrapper around unsafe code must establish its full contract for every permitted input.
The wrapper must validate sizes, offsets, alignment, lifetime, ownership, and error results before access.
Its returned values must satisfy ordinary language invariants.
Raw handles require explicit ownership and transfer rules.
Optional hardware defenses cannot supply a missing semantic check.

The same semantic contract must apply to minimal, safe, hardened, and diagnostic profiles.
Unsupported hardware must not disable a semantic check.
A required optional mitigation must fail explicitly when the platform cannot supply it.

## Proposed unsafe surface

The proposed syntax uses an `unsafe` function modifier and an `unsafe` block.
The parser and checker do not accept this proposal yet.
An unsafe function declares caller obligations.
An unsafe block permits the raw operations that its local proof justifies.
An unsafe function body must still use explicit blocks around raw operations.

The initial protected operations are:

- Integer-to-pointer conversion and pointer provenance changes.
- Raw loads, stores, allocation, release, and reference-count intrinsics.
- Unchecked pointer arithmetic and foreign memory views.
- FFI calls without a validated safe wrapper.
- Arbitrary syscalls, MMIO, and page-permission changes.
- Runtime hooks that bypass handle, lifetime, or thread ownership rules.

Ordinary checked array access remains safe.
A byte-oriented operation is not unsafe solely because it ignores Unicode boundaries.
An operation requires an unsafe boundary when its caller must uphold memory or runtime invariants.
For example, `slice_unchecked` still checks its byte range.
Its name alone does not define the classification.
The inventory must use actual implementation obligations.

The unsafe marker must not suppress type checks, ordinary bounds checks, or ownership checks.
It must not imply correct behavior for invalid pointers.
Documentation must state the obligations for each unsafe operation.
A safe caller must not acquire raw access through a public re-export or an indirect call.

## Migration proposal

The recommended migration uses one coordinated incompatible compiler change.
The change must annotate the compiler, runtime helpers, and standard library before it enables refusal.
The Go oracle must apply the same classification for differential tests.
The bootstrap pin must accept the required syntax before compiler sources use it.
The implementation must preserve the no-libc native contract.

A compatibility mode that treats unmarked raw code as safe weakens the contract.
This proposal does not include such a mode.
Old source with raw operations must receive a diagnostic that names the operation and its required boundary.
Current ordinary arithmetic remains unchanged.

This migration affects public raw-memory users and bootstrap order.
The user must approve its compatibility policy before implementation.
Alternative: reserve this syntax now, and defer enforced raw-operation refusal to a later language version.
That alternative must report the absent safe-code guarantee explicitly.

## Required conformance additions

The implementation must include these negative cases:

- A safe function calls each protected intrinsic without an unsafe block.
- A safe function calls an unsafe function through an alias or function value.
- An import or public re-export conceals an unsafe operation.
- A safe wrapper returns a view whose foreign owner no longer exists.
- A size calculation wraps before a raw allocation or copy.
- A resource handle escapes after close or ownership transfer.
- A thread shares non-atomic reference-counted state without a permitted transfer.

Positive controls must include valid annotated runtime helpers and safe wrappers.
The conformance matrix must cover both native architectures and the Go oracle.
Current bounds, ownership, arithmetic, and nullable-value behavior must remain unchanged.
Every rejected source must produce a stable diagnostic before executable output.

## Panic and abort policy

Current bounds failures print their defined cause and terminate with exit status 134 on native Linux.
Current checked arithmetic returns a value instead of an exception.
This proposal preserves both behaviors.
It introduces no unwinder and no implicit recovery from corrupted runtime state.
Signal handlers and crash reports require allocation-free failure paths under SEC-021.

## Evidence and release limits

Normative examples prove specific observations, not whole-language soundness.
Compile-fail tests cannot prove a complete alias or lifetime model.
Self-host fixpoints cannot exclude a stable miscompile.
Sanitizers cannot certify the absence of all memory errors.
Physical hardware tests cannot certify semantic safety.

SEC-003 remains open until review resolves the unsafe migration policy and the implementation plan.
The later language issues supply enforcement and adversarial evidence.
No release must describe this proposal as an implemented safe-code guarantee.
