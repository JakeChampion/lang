# The raw floor is typed

2026-09-24. Self-host typed path. Step 1 of retiring the AST lowering
(`SELFHOST-SEMANTIC-SOURCE.md`, "Retiring the AST lowering").

## What changed

The intrinsics the Fern-source runtime helpers are written on had no checker
row, no `semsource` contract and no `ssarc` arm, so any checked module that
named one fell back to the AST lowering. Seventeen of them now have all three,
from two tables:

- `checker.raw_floor_sigs` is the signature. `intrinsic_result` and
  `intrinsic_params` read it, and `semsource.raw_floor_contracts` builds its
  contracts from it, so the checker and the boundary cannot disagree.
- `ssarc.raw_floor_ops` is the op `irlower` emits for each name.

An address is a `usize`, matching `__alloc` and the `__load_*` / `__store_*`
family already there. A syscall's operands and result are `i64` words, so a
negative errno compares as one. `__raw_string` and `__raw_array` hand a block to
a fresh `string` or `i32[]` the caller owns. `__raw_data` is lent its string.
`__raw_array` answers `i32[]`, which is every use and what asmcore's own type
table says.

The two rc test hooks got contracts in the same change, `__rc_dec` and
`__rc_inc`, each lent its array. The count a hook moves is one the plan does
not see, which is what a hook for the underflow detector is for.
`TestSelfHostOverReleaseReportArm64`'s program produces whole now, the first of
the two refusals `2026-09-24-h-…` named.

## The link bug under it

`__raw_scratch`, `__raw_environ` and `__raw_splice_pipe` lower to the address
of a static word (`__fern_scratch`, `__fern_envp`, `__fern_splice_pipe`). Each
word was defined only under a runtime helper's need, so a program that took the
address itself failed to link on both lowerings, on x86-64 and arm64:
`unresolved symbol __fern_scratch`. `ssa_sym_addr` now marks the need whose gate
defines the symbol. The splice word moved out of the fs bundle, which runs only
when the heap does, to sit beside the scratch buffer. On arm64 the envp word
and its `_start` save both live under the heap gate, so there the address marks
the heap.

## Measured

- `TestSelfHostSemanticProduction` gains two rows on x86-64, x86-64 under the
  sanitizer, and arm64. Wasm has no raw floor.
  - `raw-floor-intrinsics`: every intrinsic in one program. It answers as the
    AST lowering does, and it reclaims whole (4 allocs, 4 frees). The AST
    lowering leaks 152 bytes in 3 blocks on it. It fails on main, where the
    typed path refuses the module.
  - `raw-floor-symbols-link-without-a-helper`: the three static words with no
    helper to mark them. It fails to link on main, on both lowerings.
- `TestSelfHostRawFloorIsTypedWhole` reads the names `irlower` lowers and fails
  when either table lacks one. Deleting one row from either table fails it.

## Next lead

The helper sources themselves. They were never checked, so every address in
them is an `i32` and every syscall operand an `i32`. Rewriting them against
these types is the next change. After it, `emit_ir_runtime_fern_fn` can ask the
substitution for their bundle. Two names need more than a retype:

- `__fern_str_eq` takes a string or a raw pointer today.
  `TestSelfHostStrEqSymbolTypeChecks` is refused on it.
- `__fern_map_find` calls through a bare code address.
