# ARM64 large arrays

Refs #13727 and PR #13728. Parent: #13725.

The qualified source is `3ba81dccd2de600e45fa9539247e28352e07118b`.
The clean standard build completed with actual make and version exit zero in 78.737 seconds.
The pinned stage-0 build and its smoke checks passed.
Every selected source hash remained unchanged during qualification.

| Check | Actual result |
| --- | --- |
| New array cases and ARM64 regressions | 30 passing nodes, zero failures, zero skips |
| Direct production ARM64 compiler | Six cases, compile and execution exit zero |
| Direct record allocation counts | 4096, 4101, 8194, each balanced with zero live bytes |
| Direct float allocation counts | One per case, balanced with zero live bytes |
| Native image suite | 326 TAP tests, 85 balanced zero-live censuses |
| Wasm image suite | 326 TAP tests, 61 nonzero balanced zero-live censuses |
| ARM64 image suite | 326 TAP tests, 46 nonzero balanced zero-live censuses |
| Source audit | 219 passing nodes, zero skips |
| Unchanged complexity gate | Three passing nodes, zero skips |
| Runtime checks | Five target sets plus stock feature and fixture checks |
| Format and auxiliary checks | 2084 Fern sources, Go build and vet, workflow lint, CI self-tests, runtime bundle, gofmt |

The new fixtures check every element and constant-index boundary loads for record and float arrays with 4095, 4100, and 8193 entries.
All new fixture runs use the stock strict-IR, sanitizer, and nonzero balanced zero-live assertions.
The direct runs exercise the production in-process assembler.
The stock Go harness exercises GNU assembly and its normal compiler cache.
Its private compiler binary hash was not separately captured.

The initial reduced runtime-record fixture produced five invalid stores at offsets 32768 through 32800.
Constant-only exploratory arrays used static data and did not reproduce that path.
The first offset correction passed record cases but exposed the float literal-pool limit.
All original failures remain separate from final qualification.

The first pool candidate used distinct structured label IDs but repeated textual label names.
Its direct ARM64 cases passed while GNU assembly failed.
That committed candidate does not qualify the final label correction.
Two unfinished image runs stopped with actual outer exit 143 and retain unqualified partial logs.

The final native script exited zero and completed every test.
My collector then rejected the unchanged formatter fixture because that fixture allocated zero blocks.
The original wrapper exit one and result remain intact.
The separate census audit checks the complete original log and all 85 balanced zero-live rows without a test repeat.

The allocation measurement recorded 36900494 balanced events against the unchanged 34951426 baseline.
The 5.58-percent drift exceeds the unchanged one-percent advisory threshold and stays below the unchanged ten-percent failure ceiling.
The first comparison used the wrong baseline filename and exited two before comparison.
The corrected comparison reused the exact report and exited zero without another measurement.

No RSS row was collected.
The counters do not establish CPU cost, resident-memory bounds, or production limits.
The ARM result covers selected tests and 46 standalone image executables, not the whole Go ARM harness.
All actual M5 and epic gates remain open.

`evidence.json` binds every raw artifact to its original bytes.
Gzip files decompress to the exact original source or log.
The manifest identifies preserved local binaries and marks their additional inventory as post-execution.
Existing per-case records retain the actual executed binary hashes where the coordinator captured them.

The seven parent arithmetic files remain byte-identical throughout this prerequisite.
Their decoder and machine integration remain separate work.
All production guards, test sizes, expected values, baselines, and thresholds remain unchanged.
