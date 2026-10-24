# PR 13050 performance after the main update

[Perf run 40037769986](https://github.com/JakeChampion/lang/actions/runs/40037769986)
measures source commit `c703ab1451386e6f42bb59525494f11bafd3a1d4`.
The branch contains main commit `8c5e704903500165dbddccf5e092ea5fbda373c8` and the fractal example repair.

[Main run 40021164935](https://github.com/JakeChampion/lang/actions/runs/40021164935)
supplies the reference performance reports for that main commit.
The reports use the same benchmark inputs.

The branch records 28,188,626 allocations and 28,188,626 frees.
The allocation difference from main is +8,192 events.
The cliff report records 30,984,280 copied bytes and 276,181 crossings.
The differences from main are +224 bytes and -12 crossings.

The allocation baseline uses the measured branch counts.
The cliff baseline uses the measured branch counts.
Native instruction gates pass on both architectures.
The emitted-size report retains the full measurement.
