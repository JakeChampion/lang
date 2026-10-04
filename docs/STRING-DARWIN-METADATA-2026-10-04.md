# Darwin metadata parity

Extended-attribute consumer validation exposed existing differences between
Fern and GNU Stat, LS, Dir and Vdir on macOS. Stat now decodes Darwin device
numbers, uses its `cannot stat` diagnostic, and renders filesystem `%l` as
`?`, matching GNU when the platform's statfs record has no name-limit field.
Stat and listing commands follow gnulib's BSD birth-time rule: zero seconds
or a nanosecond value outside the valid range means an unknown birth time.
Linux keeps its existing rules.

Darwin rejects `pathconf(NAME_MAX)` on `/dev/null`, although the containing
filesystem has valid geometry. Stat retries the filesystem query at the
canonical mount point, after verifying its device matches the operand's.
The builtin statfs error contract is unchanged. If the retry fails, Stat
reports the original error.

GNU comparisons pass for Stat, LS, Dir and Vdir, including fractional epoch
birth times and filesystem geometry through a device node and a symlink.
The primary Darwin compiler passes the byte-formatting corpus plus filesystem
success, fallback, missing-file and continuation cases with balanced allocation
censuses. Full Linux units/lint and Linux/Darwin consumer validation pass.
After the size cleanup below, the final Darwin GNU, primary consumer and
allocation-census checks pass again.

The size comparison uses the same reproduced primary compiler for both
sources: SHA256
`2f30e5028ddaf50cf150f01b71ee83fac4e301c5cf6d538f03d41f6826fdb14c`.
The baseline is the integrated attribute draft before these portability fixes.

| Binary | Before | After |
| --- | ---: | ---: |
| Stat, arm64 Darwin | 282,753 bytes | 282,753 bytes |
| Stat text, arm64 Darwin | 209,484 bytes | 210,484 bytes |
| Stat unwind, arm64 Darwin | 23,396 bytes | 23,516 bytes |
| Stat, x86-64 Linux | 223,152 bytes | 223,152 bytes |
| LS, arm64 Darwin | 515,873 bytes | 515,873 bytes |
| LS text, arm64 Darwin | 402,728 bytes | 402,760 bytes |
| LS, x86-64 Linux | 463,200 bytes | 463,200 bytes |

Darwin's linked sizes stay within the existing segment boundaries. Stat's
new fallback and metadata rules add code and unwind entries; data size is
unchanged. Linux Stat and LS remain the same size.

An intermediate version added 248 bytes to Linux Stat. The assembly showed
112 bytes from a Darwin birth-time helper retained behind a combined guard
and 136 bytes from device helpers that no longer inlined after adding platform
branches. Explicit platform guards at the rendering sites remove both costs.
No size baseline has been raised.
