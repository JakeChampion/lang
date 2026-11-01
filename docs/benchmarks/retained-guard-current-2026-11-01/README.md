# Current retained-guard qualification

This record tracks the remaining acceptance for issue #13717 on main commit
`2fa9f40ab202cb7fea3f071652a732e9402038e1`.
It adds evidence for existing proof repairs and lets the complexity gates
use the existing x86 runner. It changes no model, emitter, runtime
instruction, assertion, domain, baseline, or tolerance.

## Earlier failures and repairs

The [bounded allocation-guard record](../bounded-allocation-guards-2026-10-27/README.md)
preserves the original fresh-main failure, emitted constructor diagnostic,
failed candidates, and compiler identities. Its base is
`231becae0394b07f7bdbc5cf24922b6e9db67aed`; the correction source is
`3a70b650d36331d2febe0f88d207bf0702e09ef4`.
PR #13720 merged the correction normally. This record does not recast its
then-pending CI as a pass.

The array-box proof keeps capacities two and three. The first-mapping proof
keeps requests of 40 and 48 bytes. Only the selected parser recognizes the
exact external `__fern_alloc_size_abort.c` target. The proof accepts it only
when known symbolic flags establish that the transfer is not taken. Taken,
unknown, unconditional, and wrong-symbol transfers remain rejected. Pointer
arithmetic uses the explicit valid-region and nonwrapping premises, rather
than an unconditional carry value.

Subsequent prerequisites qualify the wide arithmetic, decoder, helper,
caller classes, and retained-helper controls in PRs #13729, #13743, #13747,
#13752, and #13754. Their records retain separate source identities and
limitations. They did not by themselves complete the retained-machine group.

PR #13795 then integrated the wide large-push proof into the large-array,
fused-string, and separate-string machines. Its exact head is
`9cbd7832c269961032844ced2f24d1c069e3914c`; its merge is
`7408ace01b83e8921d839ce72565c52cf7a3e0b7`.
The integration includes commit `ca30a8f2ba6cd5254adbc7eeeb23a33bbb550d32`.
The wider guard uses a register comparison against its existing 16 GiB
limit. Its proof retains 61 capacity classes and 77 normal partitions,
plus the oversized unsigned domain. This qualification preserves that
integration and the existing mutation expectations.

The current retained run executes the wide large-push controls in place of
the retired small-domain driver. The original wrong-register, memory, frame,
freelist, call, return, branch, and namespace mutations remain represented.
Three former small-domain width equivalences now require rejection in the
wider domain: the 32-bit bit scan, right shift, and capacity comparison.
The off-by-one guard mutations use the current loaded limit. This record
does not restore obsolete code or change those expectations.

## Current validation

The unchanged stock ARM64 retained-machine pilot passes with strict IR,
sanitizer, and leak checking enabled. The package takes 157.793 seconds;
the selected target takes 130.37 seconds. There are no skips.

The current main fixed-point compiler, SHA256
`32ccbf71e6d0b4495f9ebffba8784ad224e7409f37a2d63dbc0611dd4ffb14b2`,
also emits that driver's ARM64 assembly for an independent native run.
The run reports 49,915,964 allocations and frees, zero live bytes, and
`retained native text machines passed`. This census describes the executed
proof driver. It is not a general compiler heap bound.

The full unchanged stock matrix passes in 1,385.449 seconds. It covers
`ssaretained_run`, `ssaretainedmachines_run`, `ssaallocationguards_run`,
and `ssaretainedwidecontrols_run` on x86-64 Linux, ARM64 Linux, and Wasm.
The stock harness requires nonzero allocations, equal allocation/free
counts, and zero live bytes. Strict IR and sanitizer checks remain enabled.
All twelve selected target cases pass with zero skips. Both stock roots
pass, including all original and current guard/mutation controls they call.

The current native complexity driver reports compiler worst 232 and excess
18,185, and standard-library worst 68 and excess 820. These pass the existing
ceilings 221/17,447 and 68/803 with their unchanged five-percent tolerance.
The native source audit passes 218 nodes. Its apt-only test skips on macOS
and passes separately in the Linux container; the original skip remains in
the record.

A separate attempt to run the stock complexity tests in an emulated amd64
container fails while its x86 compiler executes, with an illegal-instruction
signal. The tolerance unit controls pass in that attempt; the two compiler
dependent tests do not. Their failed log remains separate from the native
driver measurement.

The complexity tests previously skipped every nonempty runner and executed
their driver directly. They now use the existing `runX86_64Bin` helper for
both checkout and literate-fixture inputs. QEMU user execution reads the same
Linux paths, so this preserves each original assertion while removing the
unnecessary skip. With required x86 tooling enabled, all three stock roots
pass in 9.019 seconds with zero skips, including the regression-rejection and
literate-source controls. Native execution uses the same helper's direct path.
The original native execution path also passes in CI run `41317969464`:
the complexity, tolerance, and literate-source roots pass in jobs
`124883618452`, `124883618453`, and `124883618462`. Those observations precede
the runner change and remain separate from its ARM Linux qualification.

[qualification.json](qualification.json) binds the source, compiler, binary,
logs, and target results. It retains failed transport attempts separately.

The current source/runtime gate passes all five runtime target sets, fixture
and feature checks, the compiler, standard library, and 85 standalone drivers.
`make lint-fast` passes. Final-head CI and the normal exact-head merge remain
pending. Issue #13717 remains open.
Parent #13687 is already closed by its separate main CI verdict gate.
This record does not establish additional M5 or epic acceptance.

The stock target command, on a Linux host with the pinned Go, Wasmtime,
and native/cross toolchains, is:

```sh
go test ./internal/testing/e2ecompiler \
  -run '^(TestSelfHostRetainedNativeBodies|TestSelfHostRetainedWideHelperControls)$' \
  -count=1 -v -timeout 60m
```
