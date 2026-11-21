# Skip loop-split analysis for functions that cannot split

Part of #11301 and #8171.

The native emitters ask the SSA splitter to consider each function after register allocation. A split needs a spilled value and five distinct blocks. The preheader precedes the outer header, then come the inner header, inner latch and outer latch. Check those conditions before constructing block positions and finding back edges. Eligible functions follow the existing split planner.

Both self-built compilers compiled the checker at `21280a059e0e` to the same x86-64 executable. Callgrind counted instructions with RC certification enabled and its memo disabled. Frame counts cover moves to and from negative frame-pointer offsets. The allocation run emitted checker assembly with RC certification disabled at the GitHub runner paths. Both runs ended with zero live bytes.

| Checker measurement | Before | After | Change |
| --- | ---: | ---: | ---: |
| Instructions | 48,182,169,469 | 48,179,529,538 | -2,639,931 |
| Frame loads | 2,478,130,719 | 2,477,899,581 | -231,138 |
| Frame stores | 835,550,088 | 835,522,964 | -27,124 |
| Heap allocations (and frees) | 54,242,612 | 54,238,038 | -4,574 |

The two compilers each reproduced their own binary. The spilled-mate, long-value and cold-value fixtures retained their assembly shapes and exit codes. The loop-split fixture retained its x86 frame cell. Five 4 MiB digests (MD5, SHA-1, SHA-256, SHA-512 and SM3) emitted byte-identical binaries before and after on x86-64 and ARM64. All 2,044 conformance cases gave the same result across x86-64, ARM64 and WASI. The compiler complexity ratchet and formatter passed. The existing allocation pin was 54,426,974. The before-and-after pair attributes 4,574 fewer allocations to this change. Both compilers recorded 1,042 unproved cases for fern_main locally, one below the existing CI pin. The four checker census rows matched their pins.
