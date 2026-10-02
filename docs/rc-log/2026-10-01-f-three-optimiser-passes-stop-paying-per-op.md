# 2026-10-01 — three optimiser passes stop paying per op

`ir.const_i32_readable` / `const_i64_readable`, `ir.fold_const_binaries`,
`ir.hoist_loop_invariants`, `util.is_decimal_i32` / `is_decimal_i64`.
Refs #8171. No emitted byte changes: the `selfhost-emit-hashes` sweep is
1,959 rows with 0 differing against a compiler built from main.

## What the profile named

On the stage-2 compile of `checker.fern` after the kept-field change, with
the callers resolved through the binary's symbol table:

- `fold_const_binaries` 941 M Ir inclusive (2.3%), and under it
  `const_i32_readable` 292 M self. A textual `const_i32` (tag 2, what a
  source literal lowers to) was judged readable by rendering
  `i32_to_string(digits_to_i32(text))` and comparing. Six arms of the
  folder test `const_i32_readable(cur[i])` on the same index before any
  of them folds, so a text constant that folds nothing paid the check
  once per arm, on every pass over the list. The pass itself ran to a
  fixpoint by rebuilding the whole list each round, so the final round,
  which folds nothing, copied every op (with its string's retain) to
  prove it.
- `cp_max_slot` 382 M Ir, 347 M of it from `licm_stored_locals`, which
  scanned the whole op list once per loop for the slot bound: loops ×
  ops per function.

## What changed

`util.is_decimal_i32` / `is_decimal_i64` answer "is this text what the
renderer produces" by scanning it: an optional `-`, digits, no leading
zero, within range by length then digit by digit against the bound. The
readability checks call them. The driver `ir_strength_run` prints twenty
texts (both signs, both zeros, hex, a suffix, the four range edges per
width, an overlong one) with each verdict and an `agree` flag against the
rendering round trip, pinned in the golden.

`fold_const_binaries` writes `out` from its first fold on: the ops before
it are copied then (`fold_prefixed`), and a pass that folds nothing leaves
`cur` as it is. The pattern list is untouched; only the tail of the loop
and the per-pass exit changed.

`hoist_loop_invariants` reads `cp_max_slot` once and follows the local
count as hoists add cache slots; `licm_stored_locals` takes the bound.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source;
"native-built" is the one `bin/fern` builds. Both rows are built from f6e1b49 and this change rebased on it; main had moved under the measurement by the CI-only commits and #10960, #10961, #10967, none of which touch these passes.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 41.52 G | 40.92 G (−1.4%) |
| stage 2, `fold_const_binaries` self Ir | 577.3 M | 520.4 M |
| stage 2, `const_i32_readable` self Ir | 291.8 M | 194.8 M |
| stage 2, `cp_max_slot` self Ir | 377.9 M | 48.6 M |
| native-built, total Ir | 70.17 G | 69.50 G (−1.0%) |

## A trap, recorded

The first A/B built both stage-2 compilers from the changed source tree
with the two native-built compilers, and they came out byte-identical, as
the hash sweep had already said they would: the native-built compilers
emit the same code, so the stage-2 pair has to be built from the two
SOURCE trees. The measurement above is.

## Witnessed

`TestSelfHostIRStrengthPeephole` (the readability rows and the fold
goldens), `TestSelfHostIRLICM`, `TestSelfHostLICMX86_64` / `Arm64` /
`WasmIR`, `TestSelfHostTypedFoldValues` / `Shape` / `TypedLICM`,
`TestSelfHostIRLower*`, `TestSelfHostSemanticSourceRC`,
`TestSelfHostPerModuleEmitAllFixpointX86_64`, the lint ratchet, and the
emit-hash sweep.

## Next

`const_i32_readable` keeps 194.8 M self Ir after the change because the
six arms still ask it once each per op position per round; deciding
readability once per position (a verdict carried on the `Op`, or a
per-round list beside the scan) is the next cut in the same pass.

On the same profile: `__fern_str_concat` from `x86_gas_trim` (216 M) and
`x86_str_split` (79 M), the `slice + ""` copies `x86_gas_prepare` makes
per line, several per line; `util.hash_bucket` through
`NameIndex.chain` (616 M) under `checker.Scope.lookup_struct` and
`semrecords.find_struct`; `ssa_lift.lift_impl` at 2.6% self.
