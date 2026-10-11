# 2026-10-11 — three repeated questions asked once

`ssa.register_form`, `asmcore` struct lookups, `seminline` body sizes. Refs
#8171. No emitted byte changes.

## What changed

- **`register_form` prunes only after a pass that changed something.**
  `prune_dead` is idempotent. The prune after `drop_low_wraps` now runs
  only when that pass replaced a wrap, and the prune after
  `thread_forwarding` only when forwarding moved an edge or dropped a block.
  `thread_forwarding` reports which in a `Threading`.
- **asmcore resolves struct names through the struct table's index.**
  `is_struct_decl_name`, `struct_local_tag`, `field_type_tag_in` and the
  field read in `infer_expr_type` each scanned every declaration by name.
  `irtables.stab_first` returns the lowest index of a name, which is the
  declaration the scan found.
- **`seminline` counts a body when it changes.** The splice rounds asked
  `count_insts` of every body each round, and `splice_calls` asked it of
  the caller and the callee at every call it considered. A size array now
  sits beside the bodies, recounted where a splice rewrote one. The split
  loop asks `drop_unread` whether it dropped anything, rather than counting
  the body before and after.

## Measured

Callgrind, x86-64, each compiler on its own warm certifier memo. The base is
this branch before each change.

| subject | change | before | after |
|---|---|--:|--:|
| `checker.fern` | `register_form` prunes | 15.830 G | 15.805 G (−0.16%) |
| `fern.fern`, the whole compiler | asmcore struct index | 74.352 G | 73.755 G (−0.80%) |
| `fern.fern`, the whole compiler | `seminline` sizes | 73.755 G | 73.464 G (−0.39%) |

`prune_dead` went from 8,426 calls to 5,530 on `checker.fern`.
`struct_local_tag` was 404 M Ir over 42 K calls on the whole compiler, and
`count_insts` went from 385 M to 78 M. The two subjects rank these changes
differently: the struct lookups and the size counts scale with the
program, so `checker.fern` barely shows them.

Each change compiles `checker.fern` to the same bytes for x86-64, arm64 and
wasm, and the changed compiler rebuilds itself byte for byte.
