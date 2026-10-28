# Filesystem, temporary files, and descriptors

Status: audit and scoped fixes for [SEC-036](https://github.com/JakeChampion/lang/issues/13538).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
The handle discipline is recorded in [HANDLE-AUDIT.md](HANDLE-AUDIT.md), and the I/O rules in [STARTUP-IO-AUDIT.md](STARTUP-IO-AUDIT.md).

## What exists

| Surface | Native rule |
|---|---|
| `open_reader`, `open_writer`, `open_appender`, `open_exclusive` | One `openat` through `__fern_open_res` with a fixed flag word per builtin. `open_exclusive` carries `O_CREAT` and `O_EXCL` and creates with mode 0600; the others create with mode 0666 under the umask |
| `open_reader_with`, `open_writer_with` | The same open with a flag word the caller composes. Bit 512 is `O_NOFOLLOW`, bit 4 is `O_EXCL`, bit 1 is `O_CREAT`, and bit 16 is `O_DIRECTORY` |
| `open_dir` and the `Dir` methods | A directory descriptor opened with `O_DIRECTORY`, `O_NOFOLLOW` and `O_CLOEXEC`. `access`, `chmod`, `chown`, `lstat`, `open_dir`, `remove_dir`, `remove_file` and `entries` run relative to it, so a path component the caller already resolved cannot be swapped under them |
| `temp_dir(prefix)` | A directory `/tmp/<prefix>-<16 hex digits>` with mode 0700 |
| `io.temp_file(prefix)` | A regular file `/tmp/<prefix>-<16 hex digits>` opened exclusively with mode 0600, returned with its path and its writer |
| `rename_noreplace`, `rename_exchange` | `renameat2` with `RENAME_NOREPLACE` and `RENAME_EXCHANGE`, so a rename cannot overwrite a file that appeared between a check and the move |
| `pipe`, `reactor_new`, `ring_new` | Created with `O_CLOEXEC` |

## Changes in this record

`temp_dir` named its directory after the monotonic clock in nanoseconds.
Another process on the host could predict the next name, and a name that existed made the call fail without a retry.
The name now carries eight bytes from `random_bytes`, and the call retries up to 64 times while the kernel answers `EEXIST`.
The three native runtimes carry the same rule, with their own `mkdirat` numbers.

No regular file open set `O_CLOEXEC`, so every open file reached each child process.
`subprocess`, `proc_exec` and `proc_exec_as` run `execve` with the descriptor table as it is, and a child that reads `/proc/self/fd` saw the parent's files.
Every open through `__fern_open_res` now sets `O_CLOEXEC` on Linux and on Darwin.
A descriptor a program hands to a child on purpose goes through `fd_dup_onto`, and `dup2` clears the flag on the new number.
The Go oracle opened its files with the flag already, so the two engines now agree.

`io.temp_file` is new.
It opens the file exclusively.
A prepared file or symbolic link at a guessed name makes the open fail with `EEXIST`, and the call tries the next name.
The prefix must not contain a separator.

## Verification

`TestSelfHostTempFiles` runs on x86-64-linux and on arm64-linux under QEMU.

- `names`: three `temp_dir` calls and two `io.temp_file` calls must print distinct names that match `/tmp/sec-[0-9a-f]{16}`. Each directory must have mode 0700. Each file must be a regular file with mode 0600 and the six written bytes. The call must refuse a prefix with a separator with errno 22.
- `symlinks`: the directory holds a file, a link to it, and a dangling link. `open_exclusive` must answer `AlreadyExists` for both links and must not create the dangling link's target. `open_reader_with` with the no-follow bit must answer errno 40 for the link and succeed for the file. `open_reader` must follow the link, which is the control.
- `close-on-exec`: a program that holds a file open runs `/bin/sh -c 'ls -l /proc/self/fd'` through `subprocess`, and the child's listing must not name the file. Before the change the listing named it. The case runs natively on x86-64, because a host shell under QEMU user mode lists descriptors QEMU itself holds.

The `std/filesystem_ops` suite gains a `temp_file` round trip and the separator refusal, and passes natively on both targets and under the Go oracle.

```sh
go test ./internal/testing/e2ecompiler -run 'TestSelfHostTempFiles' -count=1 -timeout 0
```

## Remaining limits

- `temp_dir` and `io.temp_file` use `/tmp` and do not read `TMPDIR`. A caller that needs another root creates the entry under a directory it opened itself.
- A `Dir` handle has no relative file open. A program that holds a directory open still opens a file inside it by path. The directory methods cover the metadata and removal operations.
- A plain `open_writer` follows a symbolic link, including a dangling one, and creates its target. A caller that writes into a directory other users can write uses `open_exclusive` or the no-follow bit.
- `subprocess` makes its capture pipes the child's standard streams with `dup2`, as intended. The test does not cover `proc_exec` after `proc_fork`, which runs the same `execve`.
- Ownership of a descriptor after `close` is the gap recorded in [HANDLE-AUDIT.md](HANDLE-AUDIT.md).
