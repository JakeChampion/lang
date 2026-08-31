# `examples/bench` — the performance corpus

Small, self-contained, deterministic Fern programs, each isolating one cost
the language pays. They exist to be *measured*, not to demonstrate anything:
`scripts/perf-bench` compiles each one and records its retired-instruction
count, and `scripts/ci-check-perf` compares that against the checked-in
baseline in `.github/perf-baseline.txt`.

Instruction counts, not wall time. A count is identical to the digit across
runs and across hosts for a given commit, so a 1% regression is visible; wall
time on a shared CI runner is not.

Rules for a benchmark in here:

- **No I/O, no clock, no randomness.** The instruction count must be a
  function of the commit alone.
- **Return a checksum**, so a miscompile that skips the work cannot look like
  a win. Exit codes must stay under 126 (WASI refuses anything above).
- **Isolate one cost.** `map_string` and `map_int` are the same shape with
  different key types precisely so the difference between them is readable, and
  `string_find_byte` / `string_rfind_byte` are the same for the forward and
  backward search kernels.
- **Make the checksum depend on the work.** Returning 0 is not a checksum: a
  loop optimised away sums to the same 0 as a loop that ran. `string_rfind_byte`
  puts its needle at index 3 rather than 0 for exactly this reason.
- **Check the count scales with the round count.** Halving the rounds must halve
  the retired count. If it does not, something is hoisting the call out of the
  measured loop and the benchmark is measuring nothing while looking healthy.
- **Size it to 50-300M instructions.** Enough that fixed startup cost does not
  dominate, small enough that callgrind (~50x slowdown) finishes in seconds.

Adding one is a baseline change in **two** files, because two lanes share this
corpus:

- `scripts/perf-bench` measures the NATIVE compiler's output, both counts, for
  the host arch → `.github/perf-baseline.txt`. The other arch's `.text` half is
  a static count over cross-emitted assembly, so it can be produced from either
  host; only its `.ir` half needs that arch's runner.
- `scripts/perf-bench-selfhost` measures what the SELF-HOSTED compiler emits,
  `.emit` only, for all three targets from one build →
  `.github/perf-baseline-selfhost.txt`.

Paste both sets in the same PR. Naming only the first is how
`utf8_ingest_unchecked` and `utf8_ingest_validated` came to sit in this corpus
with six self-host entries and no native ones — measured by a lane that
compared them against nothing, which reports green.
