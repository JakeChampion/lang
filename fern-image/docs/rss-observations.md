# Native Linux RSS observations

This qualifier records the Fern process's own Linux RSS fields after exec.
It does not use the Python collector's child rusage as isolated worker RSS.
The raw record contains 54 fresh-process workload observations and 12 parent-memory controls.
Compiler `69f65e675aba-dirty` includes main `602c87e2e` and the merged Wasm CRC cause fix.
The record includes compiler, binary, collector, workload, library, and workspace stdlib hashes.
The collector verifies those hashes before and after collection.

## Workloads and observation points

Update modes use a 512x512 RGBA image with 1,048,576 payload bytes.
Unique updates transfer the image through the update function.
Shared updates keep the original image. Retained updates keep every output and verify earlier versions after the observation.
The eager modes repeat a nearest resize from 512x512 to 256x256 and either discard or retain its outputs.

Row modes use a virtual 1024x10000 solid source and a 512x20000 bilinear node.
They request consecutive rows with a 12,288-byte payload cache.
Tile modes request 64x16 rectangles from that graph through the cached tile interface.
The retain modes keep their outputs through the live observation and verify them afterward.
Transparent eager inputs and solid generated inputs do not qualify representative color throughput.

The worker reads `/proc/self/status` before the workload, while selected outputs remain live, and after workload cleanup.
It records VmRSS, VmHWM, and VmSize in bytes with checked KiB conversion.
The allocator counters include the live status observation and its temporary text allocations.
The final status read and report construction occur after those allocator counters.
The last RSS observation precedes report construction and process exit.

The measurement binary enables `FERN_LEAKCHECK=1` without the sanitizer quarantine.
Each of the nine modes runs eight and 32 operations in three fresh processes.
Every execution verifies its output and reports zero final live bytes.

## Observed RSS at 32 operations

Values below are ranges across three processes, in KiB.
The VmHWM column reports the kernel field at the observation after workload cleanup.
It does not describe an exact transient peak or a memory upper bound.

| Mode | Live VmRSS | VmHWM after cleanup |
| --- | ---: | ---: |
| Unique updates | 1,212–1,216 | 1,216–1,220 |
| Shared updates | 2,236–2,240 | 2,244–2,248 |
| Retained updates | 33,076 | 33,088 |
| Discarded eager outputs | 1,476 | 1,480 |
| Retained eager outputs | 9,540 | 9,548 |
| Discarded rows | 204–208 | 204–208 |
| Retained rows | 276–280 | 280–284 |
| Discarded tiles | 208 | 208 |
| Retained tiles | 364–368 | 368–372 |

The process reserves about 17 GiB of virtual address space in these runs.
Virtual reservations do not equal resident storage.
The allocator keeps reusable pages after object release, so cleanup does not necessarily reduce RSS.
Zero final live bytes do not imply zero resident bytes.

## Parent-memory isolation control

The collector forces fork before exec for every observation.
The control adds 96 MiB of touched parent memory before it launches the worker.
Its parent VmRSS ranges from 110,052 to 110,072 KiB.
The worker's observed VmHWM ranges from 1,216 to 33,092 KiB across the unique and retained modes.
Those worker observations differ from the unpadded workload ranges by at most one 4-KiB page.

The collector's cumulative child `ru_maxrss` reaches 106,224 to 106,248 KiB during the controls.
That field includes the inherited parent footprint and does not represent isolated Fern RSS.
The raw record labels it separately.
These controls support the distinction between self observations after exec and inherited collector measurements.

## Limits and reproduction

Linux updates RSS accounting in batches and reports page-sized units.
VmHWM does not establish the exact highest transient resident set.
These observations do not isolate allocator overhead, decoder aggregate costs, cgroup memory charges, or concurrent workers.
They do not qualify memory pressure, recoverable OOM, CPU deadlines, or production safety.
RSS and observer allocation values can vary between otherwise identical runs.
The tests do not impose an RSS threshold.

This host does not delegate a writable memory cgroup to the agent.
Cgroup peak charge qualification remains separate work and must distinguish total charged memory from RSS.
The earlier [allocator observations](memory-measurements.md) and [row cache observations](row-cache.md) retain their own measurement contracts.

```sh
FERN_LEAKCHECK=1 bin/fern -target x86-64-linux \
  -o /tmp/image-rss fern-image/benchmarks/rss_probe.fern
python3 fern-image/benchmarks/measure_rss.py \
  /tmp/image-rss "$PWD/bin/fern" /tmp/rss.json
```

The [raw record](../benchmarks/results/rss-m4.json) includes every observation and control.
Three portable parser cases cover checked conversion, malformed status fields, and arithmetic overflow.
Native CI also executes all nine workload modes with the sanitizer at eight operations.
The RSS workload requires native Linux procfs. ARM64/QEMU procfs describes the emulator process and needs separate interpretation.
