# 2026-09-20 — the OS floor is whole

The tail the first OS-floor slice left (`2026-09-20-the-os-floor-has-contracts.md`,
152 no-contract sites; 72 after the two slices between) is closed. Every
builtin the census found without a contract has one now, and the two the
corpus never calls that sit beside them (`signal_default`, `setuid`) came
along so the family is complete:

- the file times and nodes: `set_file_times`, `mknod`, `chmod_at`, `chdir`,
  `chroot`;
- the process and its groups: `set_process_group`, `setgroups`,
  `getgroups`, `setuid`, `setgid`, `priority`, `set_priority`,
  `proc_exec`, `proc_exec_as`;
- the signals: `signal_ignore`, `signal_default`, `signal_disposition`,
  `signal_mask`;
- readiness and sockets: `poll`, `timer_fd`, `tcp_send`, `tcp_recv`,
  `tcp_close`, the two wasm pollables;
- the terminal: `window_size`, and on a handle `isatty`, `dup_onto`,
  `Reader.window_size`;
- `sync`, the one void builtin in the set, which stands as a statement.

Each is a row of `semsource.os_floor_contracts` (or
`handle_metadata_contracts` for the handle methods) and an arm of
`ssarc.os_floor_site` / `os_floor_process_site` / `handle_metadata_site`
emitting the op the AST lowering emits. Paths and arrays are lent, scalars
are values, a fresh array (`getgroups`, `tcp_recv`) is the caller's, and a
Result box owns its parts — the shape the first slice set.

## Measured

Corpus census, x86-64, `FERN_SEM_IR_REPORT=1`, 864 programs:

| | before | after |
|---|---|---|
| declarations produced whole | 71,254 of 86,879 (82%) | 78,746 of 86,882 (90%) |
| programs produced whole | 765 of 864 | 792 of 864 |
| no-contract root sites | 72 | 8 |

No program's compile status changed, and the compiler emits itself
byte-identically on x86-64 and arm64 with the change. The eight sites left
are not the OS floor: `Empty.to_json` and `string.tail` are methods the
producer cannot key, four are hoisted bodies named through a box, and two
are `Reader.termios_get` / `Reader.set_window_size`, whose `Termios` record
this boundary has no schema for yet.

The jump is larger than the 72 sites: the variant-field slice before this
one had admitted `Future[T].Pending` and moved nothing, because the async
and sim tests were held again by `poll` and the pollables. With those
contracts the whole `std/async` and `std/sim` trees produce, and with them
the coreutils that reach `signal_mask`, `set_file_times`, `mknod` and
`chroot`.

`TestSelfHostSemanticProduction` gains `os-floor-signals-and-process` (the
signal, priority, group and process-group builtins, `chroot`, `poll` over a
`timer_fd`) and `os-floor-times-nodes-terminal` (`chmod_at`,
`set_file_times` read back through `stat`, a FIFO from `mknod`, `chdir`,
`window_size` of a descriptor and of a handle, `isatty` and `dup_onto` on
a handle). Both are native-only rows: the wasi profile grants none of
these. Each arm counts whichever way the host answers — `chroot` and
`setgroups` succeed as root and fail unprivileged — so the count is the
same in a container and on a developer machine, and the pin is agreement
between the lowerings and no less freed. The typed bodies free more here
too (30 of 36 boxes against the AST lowering's 24 of 36 on the probe),
which is #9832's shape again.
