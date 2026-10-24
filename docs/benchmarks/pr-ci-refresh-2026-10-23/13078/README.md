# PR 13078 performance after the main update

[Perf run 40036962452](https://github.com/JakeChampion/lang/actions/runs/40036962452)
measures source commit `f3c0453ce4e6892cd13219c3805b9ccf2718e61f`.
The branch contains main commit `8c5e704903500165dbddccf5e092ea5fbda373c8` and the fractal example repair.

[Main run 40021164935](https://github.com/JakeChampion/lang/actions/runs/40021164935)
supplies the reference performance reports for that main commit.
The reports use the same benchmark inputs.

The branch records 28,189,245 allocations and 28,189,245 frees.
The allocation difference from main is +8,811 events.
The cliff report records 30,984,056 copied bytes and 276,193 crossings.
The differences from main are +0 bytes and +0 crossings.

The allocation baseline uses the measured branch counts.
Native instruction gates pass on both architectures.
The emitted-size report retains the full measurement.
