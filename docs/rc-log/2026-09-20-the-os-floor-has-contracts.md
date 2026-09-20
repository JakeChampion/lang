# 2026-09-20 — the OS floor has contracts

A census of the typed lowering over the whole local corpus — the conformance
cases, the coreutils, `examples/bench`, `examples/cli`, `examples/tests` and
the compiler, 864 programs that compile, compiled with `FERN_SEM_IR_REPORT=1` —
reduced to root refusals (a refusal that names another function's is a
cascade, and the typed path takes a caller's whole call tree with one root):

| root | sites |
|---|---|
| call target has no semantic contract | 773 |
| uninstantiated generic | 117 |
| function value is not a field (verifier) | 113 |
| calls a function value of N arguments, a type the AST lowering builds | 48 |
| map value type | 45 |
| unsupported slice source: u8[] / i32[] | 38 |
| function signature slot (verifier) | 30 |
| unsupported call target: Map[string, JsonValue].iter | 29 |
| view element escapes its source | 21 |
| unsupported void call | 20 |

The largest root was the OS floor: `Writer.flags` 226, `isatty` 69,
`temp_dir` 55, `subprocess` 45, `hostname` 40, `getcwd` 28, `Writer.stat`
26, `Reader.stat` 23, `read_link` 19, and the tail of the directory, link,
permission, process and socket ops — the same family the register path was
missing until this morning's #9829, and for the same reason: the compiler
never calls them, so nothing had measured them.

## What changed

Each is a contract in `semsource.os_contracts` or
`handle_metadata_contracts` and an op in `ssarc.os_query_site`,
`fs_op_site` or `handle_metadata_site`, the same shape as `env` and
`read_file`. Every string and array argument is lent, every scalar a value,
a fresh string or array is the caller's, and a Result or record box owns
every part of itself. The handle methods (`stat`, `flags`, `seek`, the
three syncs, `write_some`, `truncate`) are asked of the bare descriptor like
`close`.

| | before | after |
|---|---|---|
| declarations produced whole | 54,212 of 86,867 (62%) | 69,892 of 86,874 (80%) |
| programs produced whole | 685 of 864 | 756 of 864 |
| no-contract root sites | 773 | 152 |

The 152 left are `tcp_send` 32, `set_file_times` 27, `window_size` 12,
`mknod` 9, `signal_ignore`, `proc_exec_as`, `Writer.isatty`, the two wasm
pollables, `poll`, `getgroups`, `chmod_at` and a tail of ones and twos. No
program's exit status changed between the two censuses.

## Measured

`TestSelfHostSemanticProduction` gains four rows, each compiled through
both lowerings on x86-64, x86-64 under the sanitizer and arm64 (the handle
row on wasm too): the queries, the handle metadata, the directory and
permission ops over a `temp_dir`, and `subprocess`. All agree with the AST
lowering and free no less than it.

Per builtin, three calls in a loop, x86-64 under the sanitizer:

| builtin | AST lowering | typed lowering | left per call |
|---|---|---|---|
| `getcwd` | allocs 6, frees 0, 12,480 B | frees 3, 12,360 B | 4,120 B |
| `hostname`, `uname_field` | frees 0, 1,344 B | frees 3, 1,248 B | 416 B |
| `environ` | frees 24 of 459, 35,088 B | frees 456 of 456, 312 B | 104 B |
| `subprocess` | frees 0, 394,152 B | frees 12, 393,720 B | 131,240 B |
| `read_link` | frees 3 of 9, 12,480 B | frees 6 of 9, 12,096 B | 4,032 B |
| handle `flags` / `stat` / `close` | frees 15 of 18, 528 B | frees 18 of 18, 0 B | 0 |

Two findings there, neither this slice's, both filed as #9832:

1. **The AST lowering never releases the fresh result of these builtins**
   (`frees 0` on `getcwd`, `hostname`, `uname_field`, `subprocess`); the
   typed lowering does, and holds the smaller number on every row.
2. **The runtime helpers leak a raw buffer per call**: `getcwd`'s and
   `read_link`'s 4 KiB path buffer, `subprocess`'s 128 KiB read buffer, the
   `uname`/`hostname` scratch. Those are `__raw_alloc`s the helper source in
   `asmcore` never frees, so they show on both legs alike.

## Trap

A probe has to be typed for the typed path to take it. The first round of
probes wrote `var v = getcwd();` and every one reported `produced 0 of 1`,
so their numbers were the AST lowering's twice over; `var v: string =` is
what the producer types. Read the tally before reading the leak.
