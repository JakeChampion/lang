Parent: #13687. This scope qualifies the current native performance-report pins after merged safety changes.

Main run 40577171916 at b42 fails both strict native comparison jobs.
The complete reports contain 78 rows per target for the unchanged 39 benchmarks.
The x86-64 report has 36 findings outside the existing tolerances, and ARM64 has 38.
Both artifact ZIP digests and their source/run identities match the official metadata.

Current main 25dc preserves the b42 compiler, standard library, benchmark inputs, and comparator exactly.
All benchmark inputs also match the earlier pin source 45148 exactly.
An unmodified current static measurement reproduces all 78 native static rows from those CI reports exactly.
It also records the full 117-row three-target static report without a compiler rebuild.

Before/current assembly for two small benchmarks identifies the fixed native size increase.
The merged emitter adds mandatory signal-stack result checks, cold failure handlers, and a backtrace-top guard.
These checks remain required and unchanged.
The three dynamic outlier programs also retain identical observed checksums under the old and current compilers on native and ARM64/QEMU10.
These finite observations do not establish universal compatibility, speed, or memory bounds.

Predeclared scope:

- Refresh only the 156 native metric values in `.github/perf-baseline.txt` from the two complete verified official reports.
- Preserve every metric key, benchmark, comparator, workflow, strict flag, default one-percent tolerance, and per-row map tolerance.
- Preserve all production safety checks and compiler behavior.
- Preserve the separate allocation baseline of 34,951,426 and its advisory/failure policy exactly.
- Preserve the separate self-host emitted-size baseline and every other baseline.
- Require exact report completeness and source identities before a pin update.
- Require the unchanged strict comparator to fail the old pins and pass the new complete reports.
- Require a controlled counter increase outside tolerance and a missing-row completeness control to fail for their exact reasons.

Acceptance:

- [ ] Preserve official reports, verified artifact/source identities, complete metric sets, and before/current emitted observations.
- [ ] Refresh all 156 native pins with unchanged keys, per-row tolerances, scripts, inputs, and safety behavior.
- [ ] Pass the unchanged strict comparator for both complete reports and meaningful drift/completeness controls.
- [ ] Pass comparator self-tests, source checks, and format/diff checks appropriate to this data-only change.
- [ ] Merge one READY PR normally at the exact full head with bounded evidence and no broader CI acceptance claim.

The repository instructions require a measured re-bank when code generation moves a pinned baseline.
This scope records the measured cost of the merged safety changes without a tolerance or workload change.
Actual main recovery, M5, and epic acceptance remain open until their own gates pass.
