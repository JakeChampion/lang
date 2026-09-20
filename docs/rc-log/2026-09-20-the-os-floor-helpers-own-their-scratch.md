# 2026-09-20 — the OS-floor helpers own their scratch

The second half of #9832: the runtime helper sources in `asmcore.rt_src_*`
that back the OS floor kept the `__raw_alloc` block each call filled, so a
program calling `getcwd` or `subprocess` in a loop grew without bound on both
lowerings, and nothing could pin the typed path's OS-floor rows at zero.
This entry closes that half; the first half (the AST lowering never releases
a fresh builtin result) stays open on the issue.

## What was wrong

Two shapes, the same one `docs/rc-log/2026-09-07-fs-leaves-own-their-path-buffer.md`
closed for the nine fs leaves it measured:

- **A scratch buffer the result is copied out of.** `getcwd` and `read_link`
  (the 4 KiB path buffer), `hostname` and `uname_field` (the 390-byte utsname
  block), `subprocess` (the two 64 KiB drain buffers, the fd block, the
  command and argv copies), `environ` (its entries were boxed at the raw
  environ length rather than copied), `random_bytes`, `tcp_recv`, `poll`,
  `termios_get` / `termios_set`, `setgroups`, `cpu_count`, `getgroups`.
- **A NUL-terminated path copy boxed only on the error path.** Fourteen
  outcome leaves — `chdir`, `chroot`, `create_dir`, `remove_dir`,
  `create_link`, `create_symlink`, `rename`, `chmod`, `chmod_at`, `truncate`,
  `mknod`, `chown_at`, `set_file_times` — handed `pathz` to the `IoError` on
  failure and lost it on success; the three two-path leaves lost the second
  buffer on both paths. `create_symlink` alone was 88 bytes per successful
  call, which is what the new production row first reported as 152 bytes
  held after three calls.

`strerror_unknown_src` boxed its `Unknown error N` string one byte short of
the block it allocated, the size trap the 09-07 entry documents; the block
is now allocated at the string's length.

## The fix

The idiom that entry established, and no new intrinsic: a dead
`var x_own: string = __raw_string(block, TRUE_SIZE)` right after the syscall
that reads the block names its owner, the frame's reclaim returns it, and an
error path copies the bytes through `__fern_path_copy` rather than boxing
the block twice. `poll` had returns inside its scan loop, so it now records
the hit and returns once; `setgroups` validated inside its fill loop, so it
validates first and allocates after. `random_bytes(0)` and `tcp_recv(fd, 0)`
answer the empty array before reaching `__raw_alloc(0)`.

The typed path was refusing `truncate`, `termios_get`, `termios_set` and
`set_window_size` as the free builtins with `call target has no semantic
contract`, which made a module calling them fall back whole to the AST
lowering and its unreleased results. Each is now a row of
`semsource.fs_op_contracts` / `os_floor_contracts` and an arm of
`ssarc.fs_op_site` / `os_floor_site`, emitting the op the AST lowering
emits.

## Measured

x86-64, `FERN_SANITIZE=1`, `FERN_LEAKCHECK=1`, three calls of each in a loop
on the typed lowering (`FERN_SEM_IR=1`), bytes still held at exit:

| builtin | before | after |
|---|---|---|
| `getcwd` | 12,360 | 0 |
| `hostname` + `uname_field` | 2,496 | 0 |
| `environ` | 312 | 0 |
| `read_link` | 12,096 | 0 |
| `subprocess("echo", ["hi", "there"], "")` | 393,600 | 0 |
| `create_symlink`, Ok path | 88 per call | 0 |
| `create_symlink`, Err path | 32 per call | 0 |
| `create_link`, `rename`, `create_dir`, `remove_dir`, `chmod`, `chmod_at`, `chown_at`, `set_file_times`, `chdir`, Ok and Err paths | one path block per call | 0 |
| `truncate` | module refused whole | 0 |
| `random_bytes`, `cpu_count`, `getgroups`, `termios_get` / `termios_set` on a non-tty | one scratch block per call | 0 |

On the AST lowering the same probes hold only the fresh result per call
(`getcwd` 312, `hostname` 192, `read_link` 384, `subprocess` 408), which is
the first half of #9832 and unchanged here.

## What gates it

`TestSelfHostSemanticProduction/os-floor-fresh-results-are-freed`: `getcwd`,
`hostname`, `uname_field`, `environ`, `create_symlink` read back through
`read_link` and on a missing directory, `truncate` of a written file,
`termios_get` and `termios_set` on descriptor 0, and `subprocess` with
arguments, three times inside a `temp_dir`. It is the first OS-floor row
with `noLeak`: the sanitize leg pins the produced bodies at zero bytes held,
where the earlier rows could only pin the relative figure.
