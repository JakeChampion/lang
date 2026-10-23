# Constant word-index addressing

Source `23bd387f4`, based on main `4fdf2e7ee`, lets native raw word loads
and stores use constant indexes 0–4095 as memory displacements. The selectors
scale the word index by eight; byte indexes keep their byte displacement.
Larger and negative word indexes retain their register-index path. This
removes an index materialization and its register without changing the memory
operation or the shared IR's meaning.

The regression independently writes through byte addresses and reads through
word indexes, then reverses those roles for stores. It checks zero, small,
maximum encoded, out-of-range and negative indexes. Execution, operand shapes,
sanitizer counts and both GNU and in-process linking pass on x86-64 and QEMU
ARM64, alongside WASI dependency verification and the complexity ratchet
(23.639 seconds). QEMU results are correctness evidence, not ARM speed claims.

Stages 2 and 3 match at 12,669,440 bytes, SHA-256
`fa2044688f104f101bf9698758212f50a9576163bf4ccdab02ec782548cbb370`.
All eleven strict size checks pass with complete coverage and unchanged
baselines/tolerance (141.409 seconds).

The included probe makes one million calls with varying base addresses, so
the read cannot be moved out of the loop. Both current-main and candidate
fixed-point compilers produce programs that print `888000000`. Cachegrind
counts 14,001,213 instructions before and 13,001,213 after: exactly one fewer
instruction per lookup, 7.14% fewer for this small workload. This is not an
elapsed-time result or a compiler-wide speed claim.

The compiler/program/source hashes, full Cachegrind outputs and logs are
retained beside the source. Reproduce on Linux x86-64 with Valgrind installed:

```sh
python3 reproduce.py BASE_COMPILER CANDIDATE_COMPILER STDLIB OUTPUT
```

`FERN_BENCH_CPU` selects the pinned CPU (default 0). This addresses native
operand materialization under #12548 and helps compiled runtime helpers
under #2649; it does not complete either issue.

A focused repeat checks the register-entry bodies, excluding stack-entry
argument loads so those cannot satisfy an operand-shape assertion. Both native
targets and both link paths pass again; the repeat log is retained. The
standalone reproduction script also runs successfully after packaging.

Fresh integration on main bbadbc3ff (source 844df69b8) passes raw-word
execution/operand-shape, dependency verification, certification, field
re-read, calling-convention, inventory and complexity checks (41.638s).
Stages 2 and 3 are identical at 12,694,048 bytes, SHA-256
956bfaea3df203a4ef038dd02db90910e5b9937a949cff6ba0ab3236f91f8142.
All eleven strict size checks pass with complete coverage and unchanged
baselines/tolerance (118.495 seconds). Evidence uses the main-bbad- prefix.
Earlier instruction-count measurements retain their original source and
compiler hashes; they have not been relabeled as this main revision.
