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

The primary compiler appends IR tags 374-377 without renumbering existing
operations. Its native runtimes support packed byte arrays and the diagnostic
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
tests pass, as do the GNU and primary `stat`, SELinux and listing consumers.
The consumer run includes the [Darwin metadata corrections](STRING-DARWIN-METADATA-2026-10-04.md).

Fresh Go seeds produce primary compilers with identical bootstrap stages
two and three on both hosts:

| Host | Compiler bytes | SHA-256 |
| --- | ---: | --- |
| arm64 Linux | 12,847,456 | `77f8eb03d820e5e10f9a89f49f9e4637982e6a38611cf6cddc8a36860c267c57` |
| arm64 Darwin | 13,079,089 | `2f30e5028ddaf50cf150f01b71ee83fac4e301c5cf6d538f03d41f6826fdb14c` |

The reproduced Darwin compiler also passes the host-seeded fixture through
actual native execution with a balanced census, interpreter execution and
WASI refusal. The integrated source includes main `f4de66fd5`.

The bootstrap pin refresh is still required. The old pin cannot compile the
newly used host calls. Fresh-seed validation does not replace that refresh
or subsequent default-seed CI checks.
