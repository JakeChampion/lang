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
