# Every op the stack machine can emit, the register path can emit

The compiler compiling itself had been whole on the register path since
2026-09-17, and nothing had measured the rest of the tree. A sweep over the
corpus — the 594 conformance cases, the coreutils, `examples/bench`,
`examples/cli`, `examples/tests` and `fern.fern` itself, 934 programs of
which 863 compile (the other 71 are refused before emit, the same 71 both
ways), 95,072 functions, compiled through `bin/fern-selfhost` with
`FERN_SSA_REPORT=1` for each native ISA — found the gap and its shape:

| | before | after |
|---|---|---|
| programs with a declined function | 115 | 1 |
| functions declined | 381 | 1 |
| distinct ops declining | 58 | 1 |

The same numbers on x86-64 and arm64, since the lift is shared. Every one of
the 58 was an OS-floor op the compiler never calls — `open_file` 78,
`fd_flags` 40, `fd_stat` 24, `isatty` 15, `random_bytes` 14, `getcwd` 14,
`reader_seek` 12, `read_link` 10, then the long tail of the signal, socket,
process and permission ops — and 92 of the 115 programs were coreutils.

## What changed

Each stack machine's per-op table left its function driver: the chain of
guards inside `emit_function_via_ir_named` is `emit_stack_op(o, i, lp, r, s)`
on both ISAs, the driver is the prologue, the loop and the epilogue around it.
The register path's `ssa_flat_op` runs that function for the op it names,
where it had hand-dispatched five families (the byte kernels, the map ops, the
byte-buffer builder, `heap_bump_bytes`, the string ops) to the same arms. The
lift admits any op with a known stack effect this way — `ir.op_pops` models
it and `ir.op_pushes` says one — behind its own arms, and the hand-kept lists
of which ops go through the flat arm (`flat_op_argc`, `flat_str_kind`) are
gone with the special case for `heap_bump_bytes`.

Six ops had no pop count in `ir.op_pops` at all — `heap_mark`,
`heap_release_to`, `chroot`, `setuid`, `setgid`, `setgroups` — so
`irverifystack` bailed on every function using them as "unmodelled" and the
lift could not bridge them. They have one now.

`dyn_dispatch` is the op this cannot reach: its arm reads the call's arguments
from the frame slots the lowering spilled them to, and the register path keeps
no such slots. `ssa_lift_admits_run.fern` walks every registered kind and
prints the declined ones; the Go gate pins that list at `dyn_dispatch` plus
the three kinds (`load`, `store`, `call_closure_direct`) nothing produces.

## Purity

The refactor moves no byte where nothing was bridged: the compiler before
and the compiler after emit identical `-emit asm` text for every program of
`examples/bench`, `examples/tests`, `examples/cli` and the coreutils that had
no declined function, on both ISAs: 231 of the 339 identical on each, the
one refusal the same both ways. The programs whose text
moved are exactly the ones that had one — their bridged functions are now
register-allocated around the flat arm — and each runs to the same stdout
and exit status as before. The compiler compiling itself is the same check
at scale: the `-emit asm` of `fern.fern` through the register path is
byte-identical, old compiler against new, on arm64 (3,516,957 lines).

## What it does not claim

A bridged op is emitted as the stack machine emits it, with every value live
across it spilled, so this is coverage, not speed: none of the 58 ops is on a
hot path in the corpus. And `ssa_lift_admits_run` says what the lift admits,
not that each bridged arm is right under the register path; the SSA backend
gate's `os_floor` program covers the process and host queries, `umask` and
the handle ops both ways on both ISAs, and the corpus differential is the
measurement for the rest.
