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
| `Dir.open_reader`, `Dir.open_writer`, `Dir.open_reader_with`, `Dir.open_writer_with` | One `openat` on the directory descriptor with `O_NOFOLLOW` and `O_CLOEXEC`. The forms with a flag word take the word of `open_reader_with` and `open_writer_with`. `Dir.open_writer` creates and truncates with mode 0666 under the umask. A symbolic link in the entry's place fails with `ELOOP` |
| `temp_dir(prefix)` | A directory `<root>/<prefix>-<16 hex digits>` with mode 0700. The root is `TMPDIR` when that is an absolute path, and `/tmp` otherwise |
| `io.temp_file(prefix)` | A regular file `<root>/<prefix>-<16 hex digits>` opened exclusively with mode 0600, returned with its path and its writer |
| `rename_noreplace`, `rename_exchange` | `renameat2` with `RENAME_NOREPLACE` and `RENAME_EXCHANGE`, so a rename cannot overwrite a file that appeared between a check and the move |
| `pipe`, `reactor_new`, `ring_new` | Created with `O_CLOEXEC` |
| A path, a program name, an argument or an environment entry | Refused with `EINVAL` when it holds a NUL byte, before a system call sees it. A pathname unix socket follows the same rule. A Linux abstract socket name starts with a NUL byte and can hold more |

## Changes in this record

`temp_dir` named its directory after the monotonic clock in nanoseconds.
Another process on the host could predict the next name, and a name that existed made the call fail without a retry.
The name now carries eight bytes from `random_bytes`, and the call retries up to 64 times while the kernel answers `EEXIST`.
The three native runtimes carry the same rule, with their own `mkdirat` numbers.

`temp_dir` and `io.temp_file` used `/tmp` and ignored `TMPDIR`, so a program on a host with a private temporary root wrote into the shared one.
Both now read `TMPDIR`. The value is the root when it is an absolute path with more than the leading separator, with one trailing separator dropped. Anything else leaves the root at `/tmp`.
The Go oracle applies the same rule, so the differential gate sees one root.

No regular file open set `O_CLOEXEC`, so every open file reached each child process.
`subprocess`, `proc_exec` and `proc_exec_as` run `execve` with the descriptor table as it is, and a child that reads `/proc/self/fd` saw the parent's files.
Every open through `__fern_open_res` now sets `O_CLOEXEC` on Linux and on Darwin.
A descriptor a program hands to a child on purpose goes through `fd_dup_onto`, and `dup2` clears the flag on the new number.
The Go oracle opened its files with the flag already, so the two engines now agree.

`io.temp_file` is new.
It opens the file exclusively.
A prepared file or symbolic link at a guessed name makes the open fail with `EEXIST`, and the call tries the next name.
The prefix must not contain a separator.

A `Dir` handle had no file open.
A program that held a directory open still opened a file inside it by path.
A rename of the directory between the two steps sent the open to another tree.
`Dir.open_reader`, `Dir.open_writer`, `Dir.open_reader_with` and `Dir.open_writer_with` open the entry with `openat` on the handle.
The entry resolves under the directory the handle holds open, wherever that directory now is.
The open refuses a symbolic link in the entry's place with `ELOOP`, as `Dir.open_dir` does.

The three native runtimes share one flag table with `open_reader_with` and `open_writer_with`.
The Go oracle opens the entry with `openat` on Linux and Darwin.
Both keep the directory object alive through the syscall and set close-on-exec atomically.
Other hosts retain the path fallback and a separate link check.
The self-hosted interpreter reaches the entry through the descriptor's `/proc` link until the stage0 pin carries the new methods.

## Verification

`TestSelfHostTempFiles` runs on x86-64-linux and on arm64-linux under QEMU.

- `tmpdir`: the same program runs five times. `TMPDIR` is a fresh directory, that directory with a trailing separator, a relative path, `/` and the empty string. The first two runs must place every name under the directory. The other three must place every name under `/tmp`.
- `names`: three `temp_dir` calls and two `io.temp_file` calls must print distinct names that match `/tmp/sec-[0-9a-f]{16}`. Each directory must have mode 0700. Each file must be a regular file with mode 0600 and the six written bytes. The call must refuse a prefix with a separator with errno 22.
- `symlinks`: the directory holds a file, a link to it, and a dangling link. `open_exclusive` must answer `AlreadyExists` for both links and must not create the dangling link's target. `open_reader_with` with the no-follow bit must answer errno 40 for the link and succeed for the file. `open_reader` must follow the link, which is the control.
- `close-on-exec`: a program that holds a file open runs `/bin/sh -c 'ls -l /proc/self/fd'` through `subprocess`, and the child's listing must not name the file. Before the change the listing named it. The case runs natively on x86-64, because a host shell under QEMU user mode lists descriptors QEMU itself holds.
- `close-on-exec-fork`: the same probe through `proc_fork` and `proc_exec`, which `subprocess` does not use. The child's listing must not name the file. It runs natively on x86-64 for the same reason.

The `DirHandle` tests in `internal/testing/e2e` run on both native targets and under the Go oracle.
Their `open_under` function reads a file through the handle and names a missing entry.
It refuses a link to a directory and a link to a file with errno 40.
It writes a file twice and sees the second open truncate it.
It refuses an exclusive create of a present file, and it writes into the file with the flag word 0.

It then renames the open subdirectory away and creates a new subdirectory of the same name with a different file.
The original file is still readable through the open handle.
The hosted interpreter fixture `interp_hosted/native.fern` runs the same opens under both interpreters, and the two must print the same lines.

The `std/filesystem_ops` suite gains a `temp_file` round trip and the separator refusal, and passes natively on both targets and under the Go oracle.

```sh
go test ./internal/testing/e2ecompiler -run 'TestSelfHostTempFiles' -count=1 -timeout 0
```

## NUL bytes

A Fern string can hold a NUL byte. The kernel reads a path, a program name and an argument up to the first NUL.
The native runtimes copied the bytes and added the terminator, so `write_file("created.txt\0.tmp", ...)` created `created.txt`, while `path.extension` answered `tmp` for the same string.
`subprocess("/bin/sh\0/nonexistent", ...)` ran `/bin/sh`. The Go oracle and wasm32-wasi refused these already.
Each native runtime routine that passes a string to the kernel as a C string now refuses a NUL byte with `EINVAL` ([AUD-03](https://github.com/JakeChampion/lang/issues/14970)).
`subprocess` reports the refusal as a failed start: exit code 127 and `Invalid argument` on the standard error of the child.
An extended attribute value goes to the kernel with its length, not as a C string, so it can hold a NUL byte.

## Remaining limits

- A plain `open_writer` follows a symbolic link, including a dangling one, and creates its target. A caller that writes into a directory other users can write uses `open_exclusive` or the no-follow bit.
- The built-in `close` consumes its handle ([HANDLE-AUDIT.md](HANDLE-AUDIT.md)). A handle that a spawned task holds can still be closed by its owner (#15049), and a result that holds a borrowed handle can still pass to an `own` parameter (#13867).
