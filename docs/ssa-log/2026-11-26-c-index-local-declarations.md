# Index local declarations in ownership checks

Three ownership queries used to scan the same list. One counts declarations of a name, one reads its sole initializer, and one tests every initializer for freshness. On `9a8ed7050e3f`, a symbolized stage-2 check of `compiler/checker.fern` spent 33,287,076 inclusive instructions in `lu_decl_count` (72,448 calls), 3,037,837 in `lu_init_of` (5,944 calls), and 123,182,508 in `lu_fresh_init_in` (12,802 calls). These costs can overlap.

The checker builds one `util.NameIndex` from the ordered declaration names when it builds the per-function last-use facts. Each query checks the full name in its hash bucket and reads the original declaration by index. The bucket chain visits declarations in source order.

Under Callgrind on x86-64 Linux, both release stage-2 compilers compiled the same checker source from `9a8ed7050e3f`, with IR verification and RC certification enabled and certifier memoization disabled. The instruction count for the full compile fell from 57,918,494,101 to 57,887,454,482 (31,039,619 fewer, 0.0536%) and emitted identical bytes. Checking alone fell from 14,997,845,060 to 14,966,802,065 (31,042,995 fewer, 0.2070%). The leak-checking builds made 73,067,233 and 73,071,956 allocations, an increase of 4,723. Both freed every allocation.

In a smaller program each fresh local holds an array and passes to an owned parameter. Both compilers emitted identical x86-64 binaries at each size, and every program returned the expected value.

| Fresh locals | Base instructions | Indexed instructions | Change |
| ---: | ---: | ---: | ---: |
| 0 | 17,877,113 | 17,877,113 | 0 |
| 1 | 36,358,399 | 36,359,306 | +907 |
| 32 | 54,469,021 | 54,400,047 | -68,974 |
| 128 | 121,274,582 | 120,031,782 | -1,242,800 |

Both compilers reproduced themselves. All 6,258 conformance compile results matched, with 4,458 identical binaries and 1,800 matching refusals. All 526 JSON error diagnostics matched, as did six fixed checker binaries, the HTTP component and both freestanding assembly outputs. The 39 focused ownership checks passed. Two distinct resolved names with the same hash bucket kept their fresh and borrowed values separate under shadowing on x86-64 Linux, ARM64 Linux and WASI. Consuming the borrow before its last use gave the same E051 diagnostic.

All 2,360 per-function mutant reports agreed between the builds.

On their own checker sources the two builds measured 58,530,296 and 58,532,792 shared-array bytes across 1,019,935 and 1,019,921 copies. Each compiler reproduced the other count when given the other source. All five certifier counts matched between builds, including 5,350 refuted, 226 weakened and 227 missed on x86-64.
