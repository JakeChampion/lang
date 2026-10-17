# Combined compiler optimizations, 17 October 2026

These measurements cover publication source `fbf56292040558357b4e092ed58a91622e387877`, including main `91d0ba627b34a889f75f2030d15dc7d96d437fb9`. Its bootstrap compiler is 12,584,832 bytes, SHA-256 `e65e665567d2a91fa82fc6587808736cea2cb94bc3246a1dab8104c43e7461c4`. Later main integration and shipping checks are reported separately below; these timings are not relabeled as measurements of a different compiler.

The combined branch includes typed bulk byte copies, local certifier fact projection, thirteen ARM caller registers and native inverted-AND selection. Component histories under the neighboring directories retain their own source/compiler hashes.

| Check | Result |
| --- | --- |
| Cross-feature interaction suite | Pass, 545.124 seconds |
| Latest-main X25519 and complexity repeat | Pass, 27.378 seconds |
| All eleven complete strict driver-size budgets | Pass, 147.584 seconds, unchanged 5% limits |
| Twelve ordinary/sanitized byte-copy executions, three targets | Pass, exact allocation/free counts; ten outputs match the earlier component candidate |
| Inverted-AND leaf, including argument loads and return | x86 5 to 4 instructions; ARM 6 to 4 instructions |
| Standard performance workflow 39038463974 | Pass on both architectures and self-host lane |
| Seven alternating x86 whole-compiler elapsed pairs | Median candidate/baseline 0.993596; concurrent local builds, no elapsed improvement claimed |
| Three cold certification enabled/disabled pairs | Median 1.455592; still above the 15% overhead target |
| Native macOS ARM workflow 39039319474, fifteen pairs | X25519 ratio 1.039977; compiler ratio 1.022351; all outputs agree |

The macOS comparison uses current four-limb crypto input from main 91d0, unlike the older register-pool measurements. Its raw paired ratios span substantial shared-runner noise. The first run shows no native timing improvement; a repeat is in progress to check the apparent regression. The x86 development runs also overlapped other builds and cannot establish a timing improvement. No performance tolerance was widened.

The cold-certification comparison compiles the whole compiler with memoization disabled. Enabled and disabled outputs match. The local projection change does not satisfy #12181's cold-cost target; its measured component instruction-count improvement and original use-after-free rejection remain documented in the certifier history.

## Integration with the byte-range intrinsic

Main `e3f2b854e3bc63349ad90b7457e2361c6290ce3a` adds `__bytes_into`. The merge retains its runtime roots and exports alongside both private loop-copy kernels. The refreshed byte-copy/intrinsic, certifier, ANDNOT, ARM-register, complexity and inventory checks pass (115.479 seconds). Bootstrap stages 2 and 3 match: 12,591,744 bytes, SHA-256 `da2f0975d9a31954d5a210d11ee58422142734cef0f5d6f946e8d8dc139ef0ed`. The earlier e65 timing rows above remain scoped to their original compiler.
