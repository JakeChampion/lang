# PR 13038 performance after the main update

[Perf run 40037362860](https://github.com/JakeChampion/lang/actions/runs/40037362860)
measures source commit `03f99e0f4c8593f5b7c21fe9aa5cd5d813a7f76a`.
The branch contains main commit `8c5e704903500165dbddccf5e092ea5fbda373c8` and the fractal example repair.

[Main run 40021164935](https://github.com/JakeChampion/lang/actions/runs/40021164935)
supplies the reference performance reports for that main commit.
The reports use the same benchmark inputs.

The branch records 28,214,889 allocations and 28,214,889 frees.
The allocation difference from main is +34,455 events.
The cliff report records 30,653,856 copied bytes and 269,474 crossings.
The differences from main are -330,200 bytes and -6,719 crossings.

The allocation baseline uses the measured branch counts.
The cliff baseline uses the measured branch counts.
Native instruction gates pass on both architectures.
The emitted-size report retains the full measurement.
