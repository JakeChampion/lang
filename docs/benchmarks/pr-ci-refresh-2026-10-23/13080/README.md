# TLS performance after the main update

[Perf run 40037771835](https://github.com/JakeChampion/lang/actions/runs/40037771835)
measures source commit `ce205d8481ad0613d1c05ce7e2875cd757ae4f02`.
The branch contains main commit `8c5e704903500165dbddccf5e092ea5fbda373c8` and the fractal example repair.

[Main run 40021164935](https://github.com/JakeChampion/lang/actions/runs/40021164935)
supplies the reference performance reports for that main commit.
The reports use the same benchmark inputs.

The WASI HTTP benchmark emits 19,599 bytes on this branch and 19,554 bytes on main.
The difference is 45 bytes, or 0.23 percent.
This is the only emitted-size row that differs from main.
The baseline records the measured branch value.

Native instruction gates pass on both architectures.
Allocation and cliff reports match main.
The files retain the full measurements.
