# Empty array results in value blocks

Issue #13675 records the compiler prerequisite for native ICC model data selection in #13674.
The application files remain unchanged during this fix.
The Go checker and interpreter accept the reduced program.
The previous self-host checker reports an internal error when the inferred array participates in a comparison.
The target compiler regression instead records typed lowering refusal at the value-block arm.

## Cause and change

The value-block join descends into both array element types.
An empty literal has no element type, so this descent loses its concrete sibling type.
The resulting partial array type cannot support later checked uses or typed arm lowering.
An explicit annotation and a branch with an early return avoid that join and already passed.
Those cases remain controls rather than application workarounds.

The new helper examines actual empty-array trees against the sibling array structure.
It adopts the complete sibling store only when those literal structures fit.
It preserves array depth, ownership/view distinctions, nominal elements, and integer widths.
Material values and unrelated unknown expressions retain their existing paths and diagnostics.
The Go checker, application syntax, baselines, and thresholds remain unchanged.

## Development evidence

The original selected before run failed in 33.383 seconds without skips.
Six valid cases refused typed lowering on all three targets.
The log also contains passing rejection, early-return, and explicit-annotation controls.
The subsequent suite adds nested empty trees, an if-expression, an imported generic payload, and a depth rejection.
Its 66 nodes pass without failures or skips in 33.732 seconds.

A later review added two controls that actually select the empty branch and then retain or update its array.
Only those new cases execute in the follow-up, which passes nine nodes without skips on all three targets.

The positive cases check retained arrays and modified COW results on all three targets.
They require a nonzero balanced heap census and zero live bytes.
The negative cases preserve exact Go diagnostic sequences for types, depth, unknown calls, and owned/view conflicts.
The reduced source, actual before logs, and the expanded committed tests retain their distinct phases.

The clean compiler source commit is `35b5bd31bb01a6d373aaad83beb6883537c328a3`.
Its standard two-stage build, stage1 smoke, and version command pass in 92.413 seconds.
The shipped compiler SHA256 is `fd43d45fdd54cd7883425e6259dc838997d3eb413fb78792dc65656b9d185c46`.
The raw compiler SHA256 is `79710ddc04e621b8f74503a5fd84b37fa709a918caaabf2108cc2195cf732a64`.
A read-only source check of the frozen ICC application succeeds with that compiler.
That check is not application runtime or corpus qualification.

## Completed qualification

Current-source direct diagnostics pass 4,756 nodes without failures or skips.
The broader focused suite passes 388 nodes without failures or skips, including both unchanged complexity gates.
The focused suite takes 340.507 seconds, with 8.97 seconds for the compiler and stdlib ratchets.

The unchanged allocation gate records 35,061,110 balanced allocations/frees against baseline 34,951,426, a difference of approximately 0.31 percent.
It passes the unchanged one-percent advisory tolerance and ten-percent ceiling.
The report contains no RSS row and supports no physical memory claim.
The full Go checker records 2,000 passing nodes without skips.

The native, Wasm, and standalone ARM scripts each pass all 268 TAP cases.
They report 77 native, 53 Wasm, and 38 ARM balanced zero-live heap censuses.
The 38 standalone ARM executables do not qualify the whole Go ARM harness.
All selected source and compiler hashes remain unchanged through these scripts.
The native and ARM sanitizer executions and Wasm leak-census checks retain their existing scope.

All 2,057 selected Fern files pass the format check.
Go build/vet, workflow lint, test names, CI selftests, runtime bundle checks, Go formatting, and diff checks pass.
The published records retain actual subcheck exit codes and separate the development phases.
No application source, Go checker implementation, baseline, tolerance, or exemption changes accompany the fix.
Queued, pending, cancelled, skipped, and failed substantive CI states are not passes.

The final coverage addition is commit `c0c826ed8d893365038ff9df78201a9934713929`.
It adds two test rows and changes no compiler, stdlib, or previously tested case.
The follow-up record keeps its own source commit and actual elapsed time.
The earlier broad results retain their original source identity.
