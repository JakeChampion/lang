# String-array release through the ordinary runtime source pipeline

Native argument replacement and diagnostic exits still release string-array
buffers through `__fern_str_arr_free`. The typed ownership pipeline handles
ordinary user arrays separately.

The two native assembly routines are replaced by one Fern source body in
`asmcore.rt_src_str_arr_free`. It walks string elements only when the outer
buffer is uniquely owned. Each element uses the existing string-release
floor; the final outer release uses the array-release floor. Shared and
immortal arrays therefore keep their elements, and the floor reports invalid
counts and quarantined blocks. The same outer release handles both small
and large allocation classes. No allocator or diagnostic primitive is added.

The focused test emits all three native targets. Native execution checks
shared outer buffers, shared and duplicate elements, immortal buffers, null
entries, empty and large buffers, and allocation churn. Sanitized execution
requires a balanced census and checks the aborts for zero counts and a
second release of a quarantined buffer. The macOS workflow selects this test
explicitly so those behavior checks also run on Darwin.

The focused runtime, argument, need-aggregation and complexity checks pass
on x86-64 and WASI; the new test also emits ARM64 Linux and Darwin. Native
ARM64 execution remains a CI gate. Source lint, actionlint and test-name
checks pass. The previous compiler fails the source-body assertion on all
three native targets.

The migration also passes the focused runtime and argument checks on the
combined release-cleanup branch. Bootstrap stage 2 equals stage 3 there;
the compiler is 5,424 bytes smaller than that branch before this migration.

Thirty-two paired benchmark emissions change only this helper on native
targets and leave WASI byte-identical. Excluding unwind and literal-pool
directives, its x86-64 instruction count drops from 51 to 49 and ARM64 from
56 to 53. The longer assembly text includes the ordinary function's unwind
metadata. A pinned-CPU x86-64 probe performs 30 million allocation/release
iterations over ten interleaved pairs: median elapsed time is 0.493 seconds
before and 0.479 seconds after. This checks that the migration does not
introduce the allocation-reuse prototype's slowdown; it is not a general
application speed claim.
