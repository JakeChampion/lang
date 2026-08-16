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
  different key types precisely so the difference between them is readable.
- **Size it to 50-300M instructions.** Enough that fixed startup cost does not
  dominate, small enough that callgrind (~50x slowdown) finishes in seconds.

Adding one is a baseline change: run `scripts/perf-bench` and paste the new
line into `.github/perf-baseline.txt` in the same PR.
