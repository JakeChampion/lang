# Main merge and record guard compatibility

The branch merges main at `eb0e5fdc0b2e5c07cdb0f9c6fa570adc2ec09908` after the measured shape change `a0b1aec34f0fd7541ac9bceeb68408462765ac84`.
Main changes 217 files relative to the prior base `369e2f6f31d7c7b8a859bb61a7ee34bfd2ef98d6`.
The merge has no textual conflicts.
The preservation record verifies exact main bytes for 213 files.
Four files retain the branch changes described below.

| File | Branch change above main |
|---|---|
| `compiler/asm_ir.fern` | Lazy shape bitmap and empty-plan return |
| `compiler/semlower.fern` | Scalar-return eligibility guard for physical contracts |
| `compiler/ssaunits.fern` | Distinct successor counts without temporary arrays |
| `self_host_sem_production_test.go` | Existing allocation expectation of 12 for the element beside its array |

The merge retains the owner changes to the checker, interpreter, block scopes, RC holds, constant parameters and standard library.
It also retains the owner ports of earlier main repairs from this branch.
The merge does not establish a new performance result or bootstrap fixpoint.
Earlier measurements retain their exact source snapshots and subjects.

The first selected matrix records 1,617 passes, 19 skips and exit one.
Only the three record-guard target cases fail, because main requires explicit unsafe scopes for raw counter accesses.
The fixture now scopes those reads and writes with `unsafe`.
The compiler requirement remains intact.
The fixture retains its allocation, finalizer, alias and underflow assertions.

The focused pair-return rerun passes all 37 nodes in 30.829 seconds, with zero failures and zero skips.
It runs on x86-64, ARM64 and Wasm with strict IR and leak checks.
The 19 skips in the first matrix cover native OS-floor cases on Wasm.
All other selected cases pass: array truncation, block scopes, semantic production, semantic allocations and SSA unit replay.

Source checks pass.
Feature and unchanged complexity checks pass four nodes in 9.169 seconds.
All 2,110 formatter inputs pass in 3.137 seconds.
The Go formatter reports no differences in the modified guard test.

The archives retain both the initial failures and the successful focused rerun.
The manifest records raw byte counts and hashes, and every compressed file passes a decompression identity check.
Applicable current-head CI, retained-machine integration and issue #4412 remain incomplete.
