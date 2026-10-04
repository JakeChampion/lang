# Extended attributes as bytes

Extended attributes can contain arbitrary bytes. Returning their values as
unchecked strings violated the UTF-8 invariant in #5714.

`getxattr` and `lgetxattr` now validate the complete value and return
`Err(InvalidUtf8(path))` for malformed UTF-8. Valid text keeps embedded NULs.
The raw siblings preserve every byte:

| API | Result |
| --- | --- |
| `getxattr_bytes(path, name)` | `Result[u8[], IoError]`, following the final symlink |
| `lgetxattr_bytes(path, name)` | `Result[u8[], IoError]`, inspecting the link itself |
| `setxattr_bytes(path, name, value)` | `Result[void, IoError]`, following the final symlink |
| `lsetxattr_bytes(path, name, value)` | `Result[void, IoError]`, setting the link itself |

Raw getters return owned arrays. Raw setters borrow `u8[]` values; their
public parameter is an array, not a byte view. Existing text setters remain
available. The existing 65,536-byte read buffer is unchanged: a larger value
on Darwin returns the kernel's range error. WASI still lacks extended
attributes, so all eight APIs are rejected with the `xattr` capability's E066
diagnostic. This change does not add raw filesystem path APIs.

The primary compiler appends IR tags 374-377 without renumbering published
operations. Integration preserves main's `fd_drop_cache` tag 366 and
`raw_task_state` tag 367. The unpublished `mismatch_bytes` and
`count_runs_bytes` operations move to 378 and 379. Its native runtimes
support packed byte arrays and the diagnostic
slot representation. Both interpreters implement the raw APIs and strict
text getters. The Go native changes provide the bootstrap seed needed to
compile the primary interpreter's new host calls.

## Validation checkpoint

The shared fixture seeds attributes through host syscalls, then checks Fern
reads and independently reads Fern writes back through the host. It covers
all 256 byte values, thirteen malformed UTF-8 cases, Unicode with embedded
NUL, empty attributes, missing paths and names, retained result arrays,
unchanged setter inputs, and following or preserving a final symlink.

Linux Go and primary x86-64/ARM64 tests pass, including the primary
interpreter, WASI refusal and balanced native allocation censuses. The full
Linux unit suite and all lint gates pass. Darwin Go/interpreter and primary
tests pass, as do the GNU and primary `stat`, SELinux, listing and DD consumers.
Registry, constructor and SSA admission checks cover all 357 operations.
Byte-set scans and scheduler/fetch checks exercise both sides of the tag merge.
The raw DD swab corpus crosses the 128 KiB cache-advice batch boundary.
The consumer run includes the [Darwin metadata corrections](STRING-DARWIN-METADATA-2026-10-04.md).

Fresh bootstraps use the published `stage0-20261004-ef49ae0` pin without a
local seed override. All three stages are identical on both tested hosts:

| Host | Compiler bytes | SHA-256 |
| --- | ---: | --- |
| arm64 Linux | 12,989,056 | `f4c20ee91b3691ef3a30e1588784adb0c48e2d7efa540c0e26ace6c4431d4e92` |
| arm64 Darwin | 13,195,041 | `ab3ed25f461e812455f447565b7eac8c748e52824c377eb21b84fca7a265f8d1` |

The reproduced Darwin compiler also passes the host-seeded fixture through
actual native execution with a balanced census, interpreter execution and
WASI refusal. The integrated source includes main `9c8eb0032`.

The bootstrap refresh passed on x86 Linux, ARM64 Linux and ARM64 Darwin in
[run 37183701037](https://github.com/JakeChampion/lang/actions/runs/37183701037).
The released binaries' hashes were checked against the exact lock installed
here. The ARM64 artifacts also match the independently reproduced local
compilers from the release source `ef49ae0dc`. Default-seed CI across all
three hosts remains a publication gate for this integration.
