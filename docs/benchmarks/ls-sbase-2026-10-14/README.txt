Idle-host contender follow-up, 2026-10-14 Linux x86-64, AMD EPYC 9V74,
kernel 6.18.44. No heavy test/profile/build workers ran during these timings.
GNU9.12, latest published uutils0.12.0, BusyBox1.38.0, Toybox0.8.15.
Sbase c546c3a5724c81cee9a11d816a38ccdf17472129, make ls with
CFLAGS='-O3 -march=x86-64-v3', dynamic glibc2.41.

Same frozen binaries as ../../coreutils-log/2026-10-13-listing.md, not the later
ownership pilot. SHA256:
Fern base 5b851ea5ea0b7e79f2e21f39da2e592ca510caa12cc0beaf6832a6d2ff486bbf
Fern changed 259536594261842a9dcd7012cc48c5a9f97004e45eb94b6f54592f617cd7dae1
Sbase 827dddc73603074774e92eef0f03ec24693127641c1ff77b5b9fc55dec843f91

Fixtures: scripts/coreutils-bench.d/ls.sh, 4000 flat names,1500 mixed entries,
40-deep recursive tree. LC_ALL=C LANG=C TZ=UTC. Compare stdout,stderr and
exit status with GNU before timing; exclude differing implementations.
Hyperfine2.0.0, no shell (-N),100 samples,5 warmups per command.
The original-order exports cover all eight previously measured modes.
The reversed-order exports cover only names and classify, where sbase matches.
Additional numeric-column reversal controls record -sC and -isC, with sbase
excluded because it refuses those options. The Fern changed/base direction
flips with command order; the original 9.9% result is not claimed as uniform.
JSON retains per-sample times, deviations, memory and CPU data. The summaries
retain exact arguments and exclusions. These are workload-specific results,
not global compatibility or worldwide-fastest evidence.

Recreate the commands using your explicit binary and fixture paths:
  hyperfine -N --runs 100 --warmup 5 --export-json results.json \
    -n fern-base 'FERN_BASE FIXTURE' -n fern-new 'FERN_NEW FIXTURE' \
    -n gnu 'GNU FIXTURE' -n uutils 'UUTILS FIXTURE' \
    -n busybox 'BUSYBOX FIXTURE' -n toybox 'TOYBOX FIXTURE' -n sbase 'SBASE FIXTURE'
For classify add -F to each command; repeat with the command order reversed.
For unsupported modes omit sbase and every other nonmatching implementation.
Interpret the first names sbase/GNU means as a tie, not a sbase win:
the reversed order separates them in GNU's favor. Sbase beats uutils in both
orders for both eligible workloads. Do not benchmark during concurrent tests.
