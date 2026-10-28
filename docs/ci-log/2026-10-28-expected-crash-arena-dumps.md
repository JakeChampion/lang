# Expected crashes of a program with a runtime arena

`TestSelfHostImagePermissions` timed out on the x86-64 self-host shard 5 on
main from `0ad5969c5`. Each of its 14 subtests ran two programs that must
crash, and each subtest took about 110 seconds. On a local host it takes about
one second.

## Cause

The runtime reserves a large arena. A crashed Fern program that uses the
runtime writes a core with an apparent size of 18.2 GB. Most of it is the
untouched arena. The GitHub runner sends a core to systemd-coredump through a
pipe. A pipe cannot skip a hole, so the kernel writes every byte. A piped core
handler also ignores `RLIMIT_CORE`
(see `2026-09-16-expected-crash-dumps.md`).

`TestSelfHostShadowStack` had the same cause. Its hijacked binary links the
full program text, with the runtime, and it took about 240 seconds.

## Change

`e2eharness.WithoutCoreDump` runs a command through `sh`. The shell writes 0
to its own `/proc/self/coredump_filter` and then runs the program with `exec`.
The filter survives `exec`, so the core holds no memory mappings. Locally, the
same crash then writes a core of 45 KB. The two tests use the helper only for
the program that must crash. `TestWithoutCoreDump` checks the filter in the
child, the arguments and the unchanged filter of the parent.
