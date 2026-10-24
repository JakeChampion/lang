# PR 13050 performance after the main update

[Perf run 40042966170](https://github.com/JakeChampion/lang/actions/runs/40042966170)
measures source commit `11e11650054067e1aefc1723f6eae27a7af8af19`.
The branch contains main commit `96b2a7c32d06057299bcb85d1b0f2e438fbf69f5` and the fractal example repair.

[Main run 40037362860](https://github.com/JakeChampion/lang/actions/runs/40037362860)
supplies the reference performance reports for that main commit.
The reports use the same benchmark inputs.
The reference source has the same compiler, library, and benchmark inputs as main.

The branch records 28,223,118 allocations and 28,223,118 frees.
The allocation difference from main is +8,229 events.
The cliff report records 30,654,976 copied bytes and 269,474 crossings.
The differences from main are +1,120 bytes and +0 crossings.

The allocation baseline uses the measured branch counts.
The cliff baseline uses the measured branch counts.
Native instruction gates pass on both architectures.
The emitted-size report retains the full measurement.
