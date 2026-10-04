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
operations. Integration preserves main's `fd_drop_cache` tag 366 and moves
the unpublished `mismatch_bytes` operation to 378. Its native runtimes support packed byte arrays and the diagnostic
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
Registry, constructor and SSA admission checks cover all 356 operations.
The raw DD swab corpus crosses the 128 KiB cache-advice batch boundary.
The consumer run includes the [Darwin metadata corrections](STRING-DARWIN-METADATA-2026-10-04.md).

Fresh Go seeds produce primary compilers with identical bootstrap stages
two and three on both hosts:

| Host | Compiler bytes | SHA-256 |
| --- | ---: | --- |
| arm64 Linux | 12,849,248 | `ce3fa6bd91675a314e5839dcce5fdcbb94282ed47bfc8b753a73d66d42b82ad1` |
| arm64 Darwin | 13,079,201 | `ac7c961ac42ba8ca8c8f29d191cb27fa2ea983ed24e830914e9efe62f938d3ec` |

The reproduced Darwin compiler also passes the host-seeded fixture through
actual native execution with a balanced census, interpreter execution and
WASI refusal. The integrated source includes main `0c445a1ac`.

The bootstrap pin refresh is still required. The old pin cannot compile the
newly used host calls. Fresh-seed validation does not replace that refresh
or subsequent default-seed CI checks.
