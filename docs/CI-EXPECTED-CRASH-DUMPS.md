# Avoid copying memory for expected stack-overflow core dumps

The two native x86 TRMC deep-stack tests deliberately overflow the stack with
TRMC disabled. On the measured GitHub runner, almost all their time was spent
handling the expected crashes. Successful programs took milliseconds; each
expected crash took about 83 seconds.

## Scoped change

The expected-overflow subprocess writes zero to its Linux
`/proc/self/coredump_filter` before executing the program. This omits memory
mapping contents from its core dump. The filter survives `exec`; the parent
test process and the successful comparison subprocess retain their filters.
No system-wide core handler or runner configuration changes.

Setting `ulimit -c 0` alone is insufficient here: the measured runner already
had that soft limit, and its core handler was a pipe to systemd-coredump.
[Linux core(5)](https://man7.org/linux/man-pages/man5/core.5.html) documents that
piped handlers ignore `RLIMIT_CORE`, and describes filter inheritance. A small
dump can still be produced; this does not disable the crash or the handler.

The tests retain the 300,000-element inputs, 16 MiB stack limit and successful
TRMC-enabled comparison. The helper additionally requires an actual SIGSEGV
for the overflow leg, so a filter write failure, shell setup failure or an
ordinary nonzero exit cannot satisfy the assertion. Unexpected crashes in the
successful leg retain memory diagnostics. Memory diagnostics are intentionally
omitted only for the deliberately crashing leg.

## Controlled native measurement

Experiment revision `4e5e91017eb4e227875a395aa5ab77ffcee76d07`:

- [Pilot run](https://github.com/JakeChampion/lang/actions/runs/35038275083):
  a tiny native executable verified the full ABBA pipeline in under a second.
- [Full run](https://github.com/JakeChampion/lang/actions/runs/35038387393):
  the same pipeline selected the two original deep-stack tests.
- Four CPUs, Linux x86-64, one fixed test binary for all four trials. Pilot and
  full binary SHA256 both
  `2b59125b70662b2795a28cba6498920012226781ebb09c2ea3fe9587c019a645`.
- Parent filter `00000033`, expected-crash candidate filter `00000000`.
  Parent filter and system core pattern were verified unchanged.

| Trial | Expected-crash filter | Two-test wall time (seconds) |
| --- | --- | ---: |
| A1 | Inherited | 169.511 |
| B1 | Omit mappings | 0.555 |
| B2 | Omit mappings | 0.644 |
| A2 | Inherited | 167.093 |
| Mean A | Inherited | 168.302 |
| Mean B | Omit mappings | 0.600 |

Each trial ran both tests without skips. Raw events verified successful
TRMC-enabled execution and SIGSEGV for both disabled executions, together with
the actual subprocess filter values. Artifacts are
`native-stack-profile-{pilot,full}-1`. The experimental harness and profiling
environment switch are not part of this production change.

These are isolated test timings, not a claim about full-workflow latency.
Recorded child CPU excludes the external dump handler's CPU. CI worker
balancing should be measured again after these long tests become short.

## Regression coverage

`TestRunWithStackLimit` uses tiny native shell programs to check the effective
stack limit, inherited successful-child filter, cleared expected-crash filter,
actual SIGSEGV and unchanged parent filter. The original deep-stack tests
remain the compiler correctness checks. Full target coverage runs in CI.
