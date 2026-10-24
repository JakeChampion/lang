# PR 13070 performance after the main update

[Perf run 40043103715](https://github.com/JakeChampion/lang/actions/runs/40043103715)
measures source commit `0a32a7eb8dfcb36a7c1a348f3a01fe1d09eb1044`.
The branch contains main commit `96b2a7c32d06057299bcb85d1b0f2e438fbf69f5` and the fractal example repair.

[Main run 40037362860](https://github.com/JakeChampion/lang/actions/runs/40037362860)
supplies the reference performance reports for that main commit.
The reports use the same benchmark inputs.
The reference source has the same compiler, library, and benchmark inputs as main.

The branch records 28,667,184 allocations and 28,667,184 frees.
The allocation difference from main is +452,295 events.
The cliff report records 30,829,288 copied bytes and 269,526 crossings.
The differences from main are +175,432 bytes and +52 crossings.

The allocation baseline uses the measured branch counts.
The cliff baseline uses the measured branch counts.
Native instruction gates pass on both architectures.
The emitted-size report retains the full measurement.
