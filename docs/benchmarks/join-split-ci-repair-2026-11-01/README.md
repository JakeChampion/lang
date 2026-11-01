# Join-split CI repair

The initial performance report exceeds the original ARM64 tree-instruction
limit and both native HTTP size limits. The follow-up restores every raised
limit across the three baseline files. All tolerances remain unchanged.
Measured improvements retain their lower pins.

The source correction leaves boolean-only joins to the existing later SSA
pass. The direct and ranged buffer paths share one copy body. Both native
emitters recognize a common fallthrough destination through empty blocks.
The existing phi-move checks still control which blocks qualify as empty.

With the corrected compiler, ARM64 Linux measures 58,725,558 instructions for
the tree and 16,096 emitted lines for HTTP. Both meet the original limits.
The strict ARM64 report passes after the ASCII scan improvement lowers its
size pin. The 39 benchmark programs match the previous compiler's outputs.
These runs measure instruction counts, not native latency.

`emitted-size.txt` contains all 117 cross-target rows. `profiles.txt` contains
144 size rows across four profiles and two native targets. The Wasm size
warnings also occur with the previous compiler, whose affected programs are
larger. Three small diagnostic byte counts match the previous compiler on
the same local paths. Their original limits remain unchanged.

The focused Go selection contains 19 roots: 18 execute successfully, and the
TCO wrapper skips under QEMU because it needs host filesystem paths.
`runtime.json` records direct execution of its unchanged fixture on four
targets. That pure TCO program needs no heap allocations. The unchanged
join and buffer fixtures have nonzero balanced censuses on all four targets.

Native runs use the sanitizer. Wasm supplies a leak census only.

Own compiler stages two, three and four match byte for byte.
`qualification.json` binds their hashes, compiler inputs, reports and test roots.
Full current-head CI remains the merge gate, including native x86 dynamic
measurements. The self-host size and profile comparators retain their existing
advisory policy. The native benchmark comparator retains its strict policy.

`merge-chain.json` records a later test-scan correction.
Both compilers emit 492 moves in the unchanged function.
The corrected compiler removes one unconditional jump, from 63 to 62.
The numeric SSA merge chain retains 21 labels and six moves.
The scanner excludes the adjacent kernel tail and retains the eight-move limit.
No compiler source or benchmark limit changes accompany this correction.

The `combined` directory records the integration with main after PR 14008.
The compiler inputs and Go test sources match the earlier private proof.
All 1,245 compiler and standard library inputs retain their recorded hashes.
The owner also supplies the separately validated semantic snapshot correction.
The original proof retains its original parents and scope.
`combined/integration.json` binds that proof to the actual main commit.

The combined selection has 27 passing roots and one documented TCO wrapper skip.
The unchanged TCO fixture also executes directly on all four targets.
All 39 ARM64 benchmark observations match the previous compiler.
The strict ARM64 report and the original performance limits pass.
The size reports retain the same documented advisories and comparison policies.

`combined/snapshot.diff` contains the complete semantic graph snapshot correction.
The map fixture removes one empty forwarding block.
The tuple match sends each constant-payload predecessor directly to its selected arm.
The fixture and print driver remain unchanged.
The independent print driver produces the exact new snapshot.
Fresh full current-head CI remains mandatory before merge.
