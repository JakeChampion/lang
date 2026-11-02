# Kernel loops and the leaf budget

The new kernel rule also admitted loops as small leaves.
In HTTP parsing, their expansion displaced a larger splice that removes an intermediate record.
The repair keeps a call boundary for kernel loops.
Straight-line kernel comparisons remain eligible.

Native ARM64 measurements use the same benchmark source and Docker image.
Each row has a separate compiler source record.

| Variant | HTTP assembly lines | HTTP instructions | Ordered-map instructions |
|---|---:|---:|---:|
| Original PR | 16465 | 24156656 | 95978671 |
| Leaf extension disabled | 16356 | 23812615 | 98047294 |
| Direct array sites disabled | 16350 | 24212656 | 95978671 |
| Kernel loop budget | 16353 | 23808615 | 95978671 |

The repair passes the preceding ARM HTTP limits of 16242 lines and 23864615 instructions, with their existing tolerance.
It preserves the measured ordered-map improvement.
The original CI logs also measure the lower x86 ordered-map limit.
The repair restores every raised limit from the author update and retains both lower ordered-map limits.

The direct compiler control covers ARM64 and x86 assembly.
The original compiler expands the loop twice.
The repaired compiler retains its calls.
The Go host helper rebuilds the current CLI, so a different bootstrap producer does not select an older source implementation.
Full CI remains required.
