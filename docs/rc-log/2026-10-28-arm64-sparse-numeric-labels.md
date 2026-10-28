# Bound numeric-label memory by the input

Current-head CI found an ARM64 assembler memory failure under #13742.
The fuzz worker used more than 2 GiB on an 8,135-byte input.
CI did not publish its minimized seed.
A separate 31-byte seed reproduces the same failure class with numeric label `134217728`.

The assembler used the numeric label value as an index into a dense counter array.
The controlled driver used 2,098,212 KiB and then reached the mandatory allocation guard.
The sparse counter stores only labels that occur in the input.
Decimal names retain their digits, so large values cannot overflow an integer index.
The same driver now uses 932 KiB and resolves the branch successfully.
Both runs use the same producer and the same input.

The permanent regression retains the original 512 MiB memory budget.
Exact branch-offset controls cover repeated definitions, decimal normalization and forward and backward references.
Separate controls require refusal of undefined names and prevent an integer-overflow alias.
The controls pass on x86-64, ARM64 and Wasm.
The unchanged fuzz budget completes 18,250 executions in 30 seconds.
No memory threshold or runtime guard changes.

The [raw evidence](../benchmarks/pair-return-ci-repair-2026-10-28/assembler-memory/) retains both failures and successful checks.
Full current-head CI and issue #4412 remain incomplete.
