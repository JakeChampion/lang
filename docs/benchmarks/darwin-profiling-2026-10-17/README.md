# Native Darwin profiles, 2026-10-17

CPU sampling and allocation profiling now build for ARM64 Darwin in folded-stack
and pprof modes. The runtime reads the running Mach-O image, including its ASLR
slide and the __LINKEDIT virtual address beyond zero-initialized data. Symbol
lookup validates mapped bounds and uses the native writer's address-sorted
nlist table. Linux retains its existing ELF reader and file mapping.

The platform linker resolves `__mh_execute_header` only for Mach-O linking;
generic ELF linking continues to reject an undefined reference to it. The
runtime caches that header in its reserved word at offset 56, outside the
zero timer used during shutdown.

The Darwin signal action includes its native trampoline. The kernel passes the
handler, signal style, signal, siginfo, context and sigreturn token; the
trampoline preserves the context/style/token across sampling and passes them to
sigreturn. The signal reader follows Darwin's mcontext pointer and ARM64 saved
register layout. Timer, signal-mask, thread ID, open flags and AT_FDCWD constants
are selected for the platform. ABI facts were checked against Apple's public
XNU headers and syscall definitions; no XNU implementation is included here.

Starting fresh from main `2c23674c2`, source `f5dd3114b` reaches identical
stages 2 and 3 at 12,683,296 bytes, SHA-256
`16024e0c24d017aa16fb3195cb89afd24dfb45feb7858b15006250775b09bc99`.
The preceding source `3f71a3cee` has identical compiler, test and macOS workflow
contents; its targeted suite passes in 49.079 seconds. That suite covers all
existing Linux CPU/allocation profiling, every Darwin mode's Mach-O cross-build,
header relocation, real Mach-O symbol lookup with simulated ASLR and BSS gaps,
import-free module inventory and the unchanged complexity ratchet.

All eleven strict driver-size checks pass with complete coverage and unchanged
baselines/tolerance (125.833 seconds). Generated runtime source for two Linux
targets and all four profiling modes remains byte-identical: 122,208 bytes,
SHA-256 `179ec45884e48fc2f332768b63d05bc94a7dfb622aa57f98c8e989ed6789f8bf`.

This Linux workspace cannot execute Mach-O binaries. The macOS CI selection
explicitly includes TestSelfHostDarwinProfile, which executes all four modes,
checks sampled hot stacks/allocation weights and asks Go pprof to parse the
files. Native Darwin execution must pass there before merging. WASI allocation
profiling remains separate work under #5547.


Starting fresh from main `792940195` after certifier compaction, source
`15a3d7315` reaches identical stages 2 and 3 at 12,687,392 bytes, SHA-256
`121d9c01772bd38ce1f2be69acaf2ef667e83a70d21fc5cccf4ccb5469852e2c`.
Linux profiling execution, all Darwin cross-build modes, real Mach-O symbol
lookup, header relocation, certification, import-free inventory and complexity
checks pass in 46.182 seconds. All eleven strict size checks pass with complete
coverage and unchanged baselines/tolerance (119.844 seconds). Current raw logs
use the `main-7929-` prefix. This integration repeat does not execute Darwin;
that remains an explicit macOS CI gate.

The publication branch starts fresh from main `1d74b81b4`, including the
expanded x86 register policy and carry-flag optimization. Its profiling,
Mach-O, certification, carry, register-constraint, inventory and complexity
suite passes in 90.456 seconds (`main-1d74-tests.log`). The macOS profiling
test fails on a non-ARM64 Darwin runner so a mismatched runner cannot silently
skip native validation. Updated bootstrap and size checks are in progress;
the earlier fixed-point hashes above identify their original source versions.

After integrating main `4fdf2e7ee` (source `2a4d6e40c`), including the
owner's runtime retirement and zero-drop changes, the extended targeted suite
passes in 95.228 seconds. Stages 2 and 3 are identical at 12,680,528 bytes,
SHA-256 `928251f041e38056ad57661b4a6cd47056f968d09da46424b0ad3e185e3dc0ac`.
All eleven strict size checks pass with complete coverage and unchanged
baselines/tolerance (131.390 seconds). Raw evidence uses `main-4fdf-`.
Both the profiling and new native-pollable macOS tests remain selected.

After integrating main `bbadbc3ff` (source `88378906e`), preserving the
owner's element-re-read optimization, the profiling, Mach-O, certification,
element-re-read, inventory and complexity suite passes in 51.680 seconds.
Stages 2 and 3 are identical at 12,701,040 bytes, SHA-256
`9adf34fa31131c3ca8408ec0479d845015eb78c773c2656de777d51f3c958bda`.
All eleven strict size checks pass with complete coverage and unchanged
baselines/tolerance (127.017 seconds). Raw evidence uses `main-bbad-`.
These are Linux integration results. Native Darwin execution remains a
separate required macOS CI gate for all four profiling modes.
