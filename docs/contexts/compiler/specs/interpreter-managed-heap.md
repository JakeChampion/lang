# Managed interpreter heap

**Status:** Integration validation
**Context(s):** Self-host interpreter, value identity, memory lifetime
**Date:** 2026-10-31

## Problem

Composite cells serialize arrays as values. Each read creates a new array store.
Writes from receive and reactor builtins then disappear through cell and closure aliases.
A store table without reclamation retains memory for each request.

## Solution

Give each interpreter run a managed heap with stable object references.
Retain array identity through user cells and reassigned captures.
Reclaim unreachable objects and reuse their slots.

## User Stories

1. As a Fern developer, I expect every permitted alias to observe builtin buffer writes.
2. As a server developer, I need repeated interpreted requests to reclaim dead buffers.

## Implementation Decisions

- Heap nodes contain pure data and generation-tagged references. E057 remains unchanged.
- External root handles use the Drop finalizer. Aliases share each handle through host reference counts.
- Trace references from live handles through composite-cell payloads. Reclaim dead payloads on allocation pressure.
- Include array view offsets and lengths in cell payloads.
- Share the heap across environments and builtin calls. Preserve scalar-cell fast paths.
- Keep temporary values, escaped closures, and returned session values alive through their root handles.
- Keep external root counts in a separate pure-data Cell. A finalizer can then release a root while the object table is local.
- Avoid a release queue that grows with repeated reads. Root release changes the separate count table directly.

## Testing Strategy

First test root handles through aliases, containers, closures, and returns.
Then test slot reuse, stale references, unreachable cycles, and repeated requests.
Compare receive and reactor results with the Go oracle and compiled programs.
Check strict IR and sanitizer results on native targets and WASI.
Measure retained heap capacity and payload bytes as the request count grows.
Measure interpreter throughput and compiler size before any baseline change.

## Integration Sequence

1. Add a pure heap payload with scalar data and internal references. Keep host root handles outside that payload.
2. Give each module evaluation and Session its own heap. Pass it through Env, function calls, and builtin value constructors.
3. Give VArr a root for its store. Preserve that root across views, with each view's offset and length.
4. Replace composite user-cell text storage and VCellV capture storage with traced heap edges. A read creates an external root for each returned reference.
5. Preserve heap ownership in values returned by eval_module_argv and Session.run. Collection must not invalidate a value after its original environment is gone.
6. Run identity, capture, receive, reactor, and reclamation tests before removal of the serialization path.

The current entry points create environments in new_env and new_module_env.
Array constructors v_arr and v_arr_of currently allocate their own stores.
Composite reads currently decode text in deref_cell and cell_method.
Update these paths together so an implicit constructor cannot create a separate heap inside one run.

## Payload Integration Plan

Keep the current scalar and composite text tags for immutable data.
Encode each array as an edge index plus its element type, offset, view flag, and view length.
The edge selects the original array node. A cell read creates a new external root for that node.

Keep the immutable initial elements in VArr. Store builtin buffer writes in the array node's word payload.
The cell encoder writes the initial elements into its text and adds flat edges for every nested array store.
This preserves ordinary arrays of functions outside cells. The cell encoder still rejects forbidden values.

Pass one heap through Env, direct calls, methods, builtins, and array constructors.
Composite user cells and reassigned captures each own a root for their current payload.
Their updates replace text and edges in that node.
Scalar cells retain their current implementation. The encoder still rejects forbidden cells, handles, and functions.

The decoder must retain child roots before any later managed allocation can collect their nodes.
Array views share the store root and keep their fixed offset and length.
Values that escape eval_module_argv or Session.run retain their heap through those roots.
Keep the old path until identity, capture, receive, reactor, and reclamation tests pass together.

The larger live-graph probe exposed repeated full scans during graph growth.
The candidate now stores a free-slot chain in inactive nodes.
Allocation removes one free slot without a search.
When no free slot remains, collection starts at twice the live count from the last collection.
The threshold has a minimum of one and saturates at the maximum i32 value.
Each collection clears dead payloads and rebuilds the free-slot chain.

Slot metadata retains its high-water capacity. The current module does not shrink that metadata.

## Out of Scope

New cell types, changes to E057, network API changes, and a separate Go implementation.

## Validation Evidence

The candidate pins finalizer ABIs and prevents field transfers from finalized owners.
Root handles pass aliases, containers, returns, temporaries, and closures on native Darwin and WASI.
Both runs measure 17 allocations, 17 frees, and zero live bytes.

The collector prototype stores pure nodes, traces internal references, reclaims dead cycles, and reuses slots with new generations.
It rejects a stale reference after slot reuse.
External counts use a separate Cell. Private count transactions contain no Root parameters or local Root values.

| Target | Rounds per loop | Allocations | Frees | Live bytes |
| --- | --- | --- | --- | --- |
| Darwin | 8 | 135 | 135 | 0 |
| WASI | 8 | 137 | 137 | 0 |
| Darwin | 4096 | 57003 | 57003 | 0 |
| WASI | 4096 | 57005 | 57005 | 0 |
| Darwin | 100000 | 1399659 | 1399659 | 0 |
| WASI | 100000 | 1399661 | 1399661 | 0 |

Each run checks request allocation, repeated reads, a rooted graph, and an unreachable cycle.
Request and read loops use one slot. The rooted graph and later cycle use three slots.
Every phase ends with zero dead payload bytes. Only the loop count changes between scales.

Strict IR and reference-count certification remain enabled.
The latest compiler includes dependency 1e19c99735 and the two candidate source patches.
It certifies 9076 functions with zero skips. The ignored prototype is build/heap-pilot/collector.fern.
All eight root and collector cases pass again at this revision.

Raw logs use /tmp/fern-heap-main1e19-validation. Earlier logs use /tmp/fern-heap-current-validation.

Release builds measure peak resident sizes of 1146880, 1097728, and 1163264 bytes at 8, 4096, and 100000 rounds.
The sanitized 100000-round build measures 67551232 bytes.
The sanitizer has a 67108864-byte quarantine, as specified by asmcore.quarantine_default.
Without the runtime census, that sanitized binary still measures 67485696 bytes.
With the census, the release binary measures 1130496 bytes.

These controls attribute the larger footprint to sanitizer instrumentation. They do not establish interpreter throughput.

## Open Questions

The separate interpheap module holds pure text, word, and reference payloads.
Its API checks generations, traces cycles, creates child roots, and replaces payloads.
The interpreter now uses this module for array stores, composite user cells, and reassigned composite captures.
Callers must keep references within their original heap. Env integration must enforce this contract.

The module probe also checks a root whose heap escapes its creation scope.
All six native and WASI runs pass at 8, 4096, and 100000 rounds.
Request and read loops retain one slot. Graph and cycle phases retain three slots.
Collection leaves zero text bytes, words, and edges.

Native allocations are 209, 89411, and 2199299. WASI allocations are 211, 89413, and 2199301.
Every count matches its free count, with zero live bytes.
Raw logs use /tmp/fern-heap-module. The result table is /tmp/fern-heap-module-results.json.

The native Go finalizer test now executes all 14 cases on this Mac.
It passes strict compilation, exact output, sanitizer checks, and balanced census assertions in 49.155 seconds.
Its log is /tmp/fern-heap-native-finalizer-regression.log.

The Drop controls use noinline. The inline pass still fails to distinguish borrowed child reads from the later release.
Resolve that proof failure before a general finalizer claim.

The permanent cell-backed-root-lifetime regression row contains the validated pilot source.
Its Go WASI harness requires an x86 compiler driver and skips on this Mac. The direct native and WASI runs above execute without skips.

The integration still needs complete regression, performance, and size validation.
Interpreter throughput and retained capacity still need measurements after integration.

## Live Graph and Allocation Pressure

The growth probe builds one rooted chain, checks every node, and then releases the chain.
The allocation-pressure probe retains that chain during four temporary allocations per live node.
Both probes then collect the heap and require zero live payload bytes.

The first eight-node run passed before each larger run.
Only the node count changes between scales.
The new permanent test runs these probes on the native host and WASI.
It also runs the alias, replacement, stale-reference, cycle, and escaped-root controls.

| Probe | Nodes | Collections | Sum of slot counts at collection | Retained slots |
| --- | --- | --- | --- | --- |
| Growth | 8 | 5 | 23 | 8 |
| Growth | 16384 | 16 | 49151 | 16384 |
| Allocation pressure | 8 | 9 | 95 | 16 |
| Allocation pressure | 16384 | 20 | 196607 | 32768 |

Native Darwin and WASI have identical collection counters and slot counts.
The probes enforce linear bounds on the sum of slot counts at collection.
The allocation-pressure probe also enforces a slot bound of twice the persistent live graph size.
The final collection leaves zero text bytes, words, edges, and live nodes.
All fourteen direct runs have equal allocation and free counts, with zero live bytes.
Strict IR and reference-count certification have zero skips.

The updated small-graph probe retains one slot for requests and reads.
Its graph and cycle phases now retain four slots instead of three.
This increase follows from the new growth threshold.
At 100000 rounds, native allocation and free counts are both 2099297.
WASI allocation and free counts are both 2099299.

Raw logs use /tmp/fern-heap-growth-validation.
The result table is /tmp/fern-heap-growth-validation-results.json.
The persistent table is docs/benchmarks/interpreter-managed-heap-2026-10-31/growth.json.
The earlier full-scan control uses /tmp/fern-heap-growth-before-*.json.
Exploratory process times include startup and sanitizer costs. They do not establish interpreter throughput.

The permanent TestSelfHostInterpreterHeap passes all fourteen cases in 27.496 seconds on this Mac.
It uses the current compiler sources, strict checks, exact output, and balanced census assertions.
Its log is /tmp/fern-heap-growth-permanent-test.log.

## Interpreter Integration Evidence

Each module evaluation and Session creates one heap. Environments and call paths share that heap.
Array copies and views keep their store root. Composite cell payloads trace the original array stores.
A cell read creates child roots before it decodes the immutable value data.

Scalar cells retain their original path. E057 and E082 remain unchanged.

The count transaction now accepts only its counter Cell, rather than the whole Heap.
This keeps the Root finalizer's borrowed ABI stable in the pinned bootstrap compiler.
The pinned compiler now builds the integrated compiler and interpreter driver with strict checks.
The general finalizer ABI pin and field-transfer guards remain in the candidate.

The permanent combined regression passes in 43.784 seconds on this Mac.
Its fourteen heap cases, six length-identity modes, and four TCP/UDP modes all pass.
The Linux reactor test legitimately skips on this host.

Direct Linux ARM64 container runs verify reactor identity in both interpreters and a compiled program.
The compiled run balances six allocations. The interpreted run balances 6403 allocations.
Every sanitized run leaves zero live bytes.

The native and WASI interpreter self-tests balance 75963 and 83282 allocations respectively.
Both have zero live bytes and zero strict-verifier skips.
Raw logs use /tmp/fern-managed-interp-combined-regression and /tmp/fern-managed-interp-selftest.

An integrated request probe replaces captured buffers and user-cell contents at 8, 4096, and 100000 rounds.
All three runs retain six slots and one returned array after the final collection.
Native allocation and free counts are 3384, 620672, and 15102176. Every run leaves zero live bytes.
Raw tables use /tmp/fern-managed-request-small.json and /tmp/fern-managed-request-large.json.

The large-array test fails its unchanged duration limit on both the candidate and the unmodified PR branch.

| Source | 20000 elements, seconds | 80000 elements, seconds |
| --- | --- | --- |
| Control | 1.45118425 | 23.06311125 |
| Candidate | 1.472664292 | 23.322957583 |

These results show the same quadratic growth pattern. No performance gate or baseline changed.
The source experiments with a separate heap argument and a scalar store-query helper did not improve that pattern.
Those experiments are removed. The control log is /tmp/fern-pr13959-array-inplace-control.log.
The ownership correction below resolves this failure.

The first identity fixtures also exposed compiled behavior after unsafe writes to shared literal arrays.
The reactor control keeps that evidence in /tmp/fern-managed-reactor-literal-control.fern.
The valid reactor fixture now allocates its buffers explicitly, as the receive-buffer programs do.
View cells and view captures remain invalid. The final length fixture uses an ordinary view created before array-length changes.

## Explicit Environment Ownership

The allocation profile identifies array copies inside write_array_in_place on the unmodified source.
The statement and loop evaluators now consume Env through scope, update, capture-cell, and trim operations.
Shallow Env records convert borrowed projections at explicit transfer boundaries.
Read-only expression evaluators keep their borrowed contract.
While errors return the current Env without retention of the initial Env.

The combined heap, array-performance, and identity regression passes in 113.433 seconds on this Mac.
All eight array semantic cases and the unchanged duration bound pass.
The fourteen heap cases, six length-identity modes, and four socket modes also pass.
The Linux reactor test legitimately skips on this Mac.
A separate Linux ARM64 container run passes the updated interpreter's reactor identity case with 6401 balanced allocations and zero live bytes.

The frame and session contracts pass on WASI in 18.820 seconds.
Both Linux targets compile, but execution legitimately skips without QEMU on this Mac.
The test hook now creates one heap and retains shared Env values at owned transfer boundaries.
Its alias, scope-pop allocation, capture, and error-state assertions remain unchanged.

The updated request probe retains six slots and one returned array at 8, 4096, and 100000 rounds.
Allocation and free counts match at 3363, 612475, and 14902171. Every run leaves zero live bytes.
integrated-requests.json records these updated counts.
The earlier counts above describe the source before explicit Env ownership.

The standalone ownership candidate passes lint-fast and the complete unit-rest package group.
Its interpreter self-tests pass on native Darwin and WASI with 75303 and 82622 balanced allocations.
Its WASI driver adds 6572 bytes, and its x86 compiler image adds 12288 bytes.
The native driver size stays unchanged.
These size measurements isolate the ownership change. They do not include the managed heap.

No baseline or test limit changed.

Raw logs use /tmp/fern-managed-heap-owned-*.

The integrated heap passes lint-fast and the complete unit-rest package group.
Its native and WASI interpreter self-tests balance 75735 and 83054 allocations, with zero live bytes and zero verifier skips.
The native driver adds 33200 bytes. The WASI driver adds 20663 bytes.
The x86 compiler image grows from 15705536 to 15743088 bytes, below its unchanged 15881456-byte baseline.
Strict checks verify all 8418 image functions with zero skips.

integrated-sizes.json records the source identities, compiler hash, sizes, and census results.

All 117 static size rows pass the unchanged strict comparator across x86, ARM64, and WASI.
selfhost-perf.txt records every measured row.
Current-main integration and full PR CI remain necessary before publication and merge.

## Integration With Current Contracts

The renewed candidate uses PR #13959 head 09c9786fcc as its base.
This base includes the parser, stdlib, and Env ownership repairs.
It is a dependency candidate. It is not a merged main revision.
The original prepared branch remains intact.

The candidate passes lint-fast and the complete unit-rest package group.
The heap, identity, socket, and unchanged array-performance tests pass in 55.123 seconds.
Native finalizers and the WASI frame and session controls pass in 109.656 seconds.
All fourteen WASI finalizer cases also pass through the Linux driver in 70.900 seconds.
Linux frame binaries compile, but their local execution skips without QEMU.
The separate Linux ARM64 reactor regression passes all four modes in 37.519 seconds.

The native and WASI interpreter self-tests balance 75735 and 83054 allocations.
Both leave zero live bytes and have zero verifier skips.
All 117 static metrics pass the unchanged strict comparator.
Their report is identical to selfhost-perf.txt.

The paired size control restores all changed compiler files from 09c9786fcc.
Both builds use the same compiler and stdlib.
The heap adds 33200 bytes to the native interpreter and 14091 bytes to its WASI build.
Its graph storage, root counts, and bounded collector account for this added functionality.
The x86 compiler image grows from 15713728 to 15743088 bytes.
It remains below the unchanged 15881456-byte baseline.

Strict checks model all 8419 image functions with zero skips.
current-contracts.json records these source identities and measurements.

Raw logs use /tmp/fern-managed-current-*.
Full PR CI remains required before merge.
