# PR 13078 performance after the main update

[Perf run 40043105567](https://github.com/JakeChampion/lang/actions/runs/40043105567)
measures source commit `324903f037316a26376c00262a2cfb0b7b5bb74d`.
The branch contains main commit `96b2a7c32d06057299bcb85d1b0f2e438fbf69f5` and the fractal example repair.

[Main run 40037362860](https://github.com/JakeChampion/lang/actions/runs/40037362860)
supplies the reference performance reports for that main commit.
The reports use the same benchmark inputs.
The reference source has the same compiler, library, and benchmark inputs as main.

The branch records 28,223,726 allocations and 28,223,726 frees.
The allocation difference from main is +8,837 events.
The cliff report records 30,653,856 copied bytes and 269,474 crossings.
The differences from main are +0 bytes and +0 crossings.

The allocation baseline uses the measured branch counts.
Native instruction gates pass on both architectures.
The emitted-size report retains the full measurement.
