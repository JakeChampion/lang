# Filesystem type names

**Status:** Validated locally; awaiting predecessor merge and review
**Contexts:** Process runtime, filesystem metadata and coreutils
**Date:** 2026-10-06

## Problem

Darwin's filesystem type number is a kernel index rather than a stable magic
number. FsStat exposes only that number, so stat cannot display the filesystem
name and refuses its default filesystem report on Darwin.

## Solution

Expose fs_type_name as a string on FsStat. Darwin returns the filesystem name
reported by its kernel. Linux returns an empty string, keeping numeric type
identification separate from GNU's human-readable naming table. The stat
utility uses this field for Darwin filesystem names and default reports.

## Implementation decisions

Extend the existing filesystem metadata record and generated runtime. The name
is an owned immutable value, independent of scratch storage and later system
calls. Bound reads by the kernel field's length and stop at its first NUL.
Keep existing numeric fields, failed-call diagnostics and path limits intact.
Mirror the record in the Go oracle and preserve pinned compiler bootstrap.

## Testing strategy

Compare public metadata with host system-call results on Darwin and Linux.
Check name lifetime across repeated calls and copies with allocation census
and sanitizers. Exercise missing paths and Linux's empty name. Compare the
stat filesystem-name directive and default report with GNU on Darwin, retaining
Linux's existing naming tests. Run native bootstrap and full lint.

## Out of scope

Inventing filesystem names from Darwin type indices, changing Linux's naming
table, or changing filesystem count and path-limit semantics.

## Open questions

No product decisions remain. Kernel layout was measured against the host SDK.
Native Darwin and Linux runtime checks, GNU stat parity, allocation checks and
full lint pass. Native bootstrap stages 2 and 3 are byte-identical. Publication
still requires integration with the merged predecessor and the CI/review gates.
