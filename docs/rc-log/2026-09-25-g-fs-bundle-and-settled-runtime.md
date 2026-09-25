# The rest of the filesystem bundle, and a runtime that settles its needs

2026-09-25. Self-host typed path. Step 1 of retiring the AST lowering,
continued from `2026-09-25-f-fs-leaves-and-unit-results.md`.

## What changed

The rest of the filesystem bundle is retyped with the same mechanical rules
(`usize` blocks, `i64` syscall operands, `as i32` results and stored bytes,
`as usize` for a byte stored into an array slot): `stat`, `lstat` and
`statfs`, `window_size` / `set_window_size`, `read_file`, `utf8_valid` (its
buffer parameter is a `usize` now), `read_file_bytes`, `create_dir_all`,
`read_link`, `write_file` / `write_file_exec`, `temp_dir`, `read_dir` /
`read_dir_all`, `remove_dir_all`, `open_file` / `open_with`, the writer and
reader operations, the `fd_*` family and the sync family. Helper sources
that check on their own: 90 of 128.

## The runtime settles its needs

A typed body can call a block of the hand-written runtime that the AST body
never reached. `read_dir` appends to a `string[]`, and the typed append
calls `__fern_arr_inc_elems`, which the hand-written runtime emits only on
its need. Needs are marked as each body is emitted, and the block that
defines `arr_inc_elems` is emitted before the filesystem bundle on both
backends, so the need arrived too late and the link failed on
`__fn___fern_arr_inc_elems.r`.

Both backends now emit the entry's runtime into a capture buffer
(`EmitState.capture`, the same mechanism the SSA emitter uses for one op's
arm). When the pass added a need, the captured text is dropped and the
runtime is emitted again from the state before it, with the grown set
closed, until a pass adds nothing. A program whose typed runtime adds no
need, which is every program before this change, takes one pass.
`FERN_SEM_IR_REPORT` prints a helper's `produced` line once per pass, so a
program that needed a second pass reports it twice.

The x86-64 runtime section moved out of `emit_unit_flat_with` into
`emit_entry_runtime` so it can be run again; its lines are unchanged. The
`TestCloseNeedsPrecedesEveryRuntimeGate` source lint now checks the new entry
points too, and each pass closes its needs before it emits.

## Measured

- Row `fs-bundle-reads-and-writes`: creates a directory tree, writes, reads
  as text and bytes, stats, lists, reads a missing file and removes the tree.
  The module and the whole bundle produce; the answer matches the AST leg on
  x86-64 and arm64 and native's; the sanitize leg reclaims everything.
