# PR 13070 performance after the main update

[Perf run 40037360608](https://github.com/JakeChampion/lang/actions/runs/40037360608)
measures source commit `57e4c269d74669208ffbda488a5c502b05d0892f`.
The branch contains main commit `8c5e704903500165dbddccf5e092ea5fbda373c8` and the fractal example repair.

[Main run 40021164935](https://github.com/JakeChampion/lang/actions/runs/40021164935)
supplies the reference performance reports for that main commit.
The reports use the same benchmark inputs.

The branch records 28,632,630 allocations and 28,632,630 frees.
The allocation difference from main is +452,196 events.
The cliff report records 31,159,424 copied bytes and 276,239 crossings.
The differences from main are +175,368 bytes and +46 crossings.

The allocation baseline uses the measured branch counts.
The cliff baseline uses the measured branch counts.
Native instruction gates pass on both architectures.
The emitted-size report retains the full measurement.
