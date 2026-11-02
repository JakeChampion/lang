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

The final repair also shares the wrapper for fixed-count mismatch calls.
Their generic kernel expansion added size without a useful count specialization.
Dynamic-count array comparisons still take the packed kernel directly.
The final ARM HTTP result is 16239 assembly lines and 23864615 instructions.
The ordered-map count remains 95978671.

All 39 benchmark outputs match the qualified parent.
The final 78 ARM metrics and 39 x86 static metrics pass the strict limits.
The 117 self-host size metrics pass the existing advisory gate.
All 144 static profile metrics pass the unchanged strict limits.

The profile harness uses CI's source paths because diagnostic images contain those paths.
Longer local paths caused the two initial startup-size findings.
Both canonical startup sizes exactly match their pins.

Compiler stages 2, 3 and 4 match at `092162cd5831a577138025c12e38eeb44a3617cf532639b9e464d199eb6f7da4`.
Stage 1 differs.
The final qualification preserves author head `2a95a53e23` and the merged record-token dependency.
The records under `final/` retain their source, producer and measurement scopes.
