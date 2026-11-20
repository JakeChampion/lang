# The Perceus comparison on native arm64

On 2026-11-20, a native arm64 Linux runner measured ten workloads at full size.
The runner used Fern at `5b850f03`, Koka 3.2.9, Lean 4.34.1 and Roc at `ea210f5`.
Each workload ran once per language before measurement.
The harness then ran each language four times in a balanced order with an unlimited stack.

All 160 timed runs gave the expected output.
In a separate run for each workload, Fern freed every allocation.
The [workflow run](https://github.com/JakeChampion/lang/actions/runs/44800155367) contains the job summary and the raw results.

The x86-64 measurements from November 15 cover the
[first five workloads](2026-11-15-d-the-run-before-a-run-decides-its-system-time.md),
[binary trees](2026-11-15-e-a-tree-walk-is-unrolled-once.md) and
[the four list and map workloads](2026-11-15-f-four-more-workloads.md).

## Processor time

Each cell gives the median CPU time in milliseconds. Parentheses give the fastest run.

| workload | size | fern | koka | lean | roc |
|---|---|---|---|---|---|
| rbtree | 4200000 | 503 (502) | 480 (480) | 2112 (2106) | 2357 (2349) |
| rbtree-ck | 4200000 | 816 (813) | 1038 (1031) | 2575 (2567) | 4536 (4535) |
| deriv | 10 | 495 (494) | 628 (625) | 797 (795) | 2764 (2728) |
| nqueens | 13 | 502 (500) | 560 (557) | 977 (973) | 3293 (3290) |
| cfold | 20 | 69 (68) | 120 (119) | 149 (146) | 232 (229) |
| binarytrees | 21 | 8844 (8774) | 10252 (10211) | 18773 (18687) | 29706 (29676) |
| msort | 1000000 | 406 (363) | 462 (436) | 1247 (1245) | 722 (681) |
| qsort | 1000000 | 579 (571) | 582 (571) | 1282 (1254) | 955 (907) |
| treemap | 20 | 231 (228) | 212 (201) | 359 (353) | 425 (411) |
| pmap | 1000000 | 2432 (2086) | 3065 (2784) | 5909 (5654) | 3548 (3338) |

## Peak resident memory

Each cell gives the largest peak in MiB across the four timed runs.

| workload | size | fern | koka | lean | roc |
|---|---|---|---|---|---|
| rbtree | 4200000 | 130 | 132 | 200 | 194 |
| rbtree-ck | 4200000 | 1150 | 1151 | 1382 | 1374 |
| deriv | 10 | 420 | 459 | 552 | 974 |
| nqueens | 13 | 94 | 98 | 132 | 126 |
| cfold | 20 | 136 | 156 | 166 | 146 |
| binarytrees | 21 | 258 | 262 | 264 | 386 |
| msort | 1000000 | 32 | 32 | 130 | 33 |
| qsort | 1000000 | 32 | 32 | 84 | 33 |
| treemap | 20 | 66 | 68 | 72 | 98 |
| pmap | 1000000 | 70 | 72 | 84 | 63 |
