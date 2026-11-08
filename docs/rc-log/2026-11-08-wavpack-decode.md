# Native lossless WavPack

The decoder implements WavPack version 4 lossless blocks in Fern.
It restores adaptive median codes, every decorrelation term, joint stereo, false stereo, integer extensions, and float extensions.
The integer API returns signed samples at their declared bit depth.
The raw float API preserves every IEEE 754 bit.
The source derives from WavPack 5.8.1 at `4827b9889665b937b6ed71b9c6c0123152cd7a02`, under BSD-3-Clause.

The byte-view index retains bounded metadata and offsets without an encoded input copy.
It validates contiguous multichannel groups and declared sample totals.
The reader retains one packet scratch array and reuses caller output.
Its sample budget applies to one group, so long streams can exceed the whole-file decode budget.
Exact seeks decode and verify the complete target group.
The caller retains the complete unchanged encoded input view.

Block checksums cover the encoded headers and metadata.
PCM checksums cover restored predictor samples.
Extension checksums cover reconstructed integer and float extensions.
The parser accepts bounded trailing APEv1, APEv2, and ID3v1 tags.
Hybrid audio, correction files, and DSD return `Unsupported`.
The module performs no file or device I/O.

The frozen corpus has 127 independently encoded and decoded files.
It covers one through eight channels, non-byte bit depths, all compression modes, short final blocks, float exceptions, and tag boundaries.
WavPack 5.8.1 verifies every PCM sample.
FFmpeg 7.1.5 also agrees on 125 files.
It differs on two files with the newer integer extension optimization.
Fern matches WavPack on those files.

The reference library reads the APEv2 and ID3v1 test titles.
It ignores the APEv1 title, so that case qualifies PCM across the tag.
The generator records every source hash and comparison result.
It can regenerate the corpus or check an independent Fern decoder executable.
The standard tests need no reference library or FFmpeg at runtime.

All three suites pass native sanitizers and WASI execution.
They also compile as Android ARM64 shared libraries.
The full native corpus reports 102231 allocations, 102231 frees, and zero live bytes.
The boundary suite covers all truncated prefixes and every corrupt byte of its small fixtures.
It checks budgets, exact seeks, EOF, preserved output suffixes, and bounded bit fields.
Four small reference files remain in the self-host interpreter gate.

The full interpreter corpus completes four cases in 75 seconds.
That measured run stops at the timeout, and the full corpus remains in native and WASI qualification.
The complexity and module-documentation gates pass without changed limits.
All three registered native suites agree with the Go oracle.
The small corpus and boundary suite also agree in the self-host interpreter.

Full CI and matched CPU, allocation, memory, and latency comparisons remain required.
No performance lead is claimed.

Fern's [float contract](../FLOAT-SEMANTICS.md) permits NaN payload changes in float values.
The `decode_bits` and streaming APIs preserve the exact source bits, including signaling NaNs.
The `decode_float` convenience API follows that language contract.
The tests verify exact raw bits for every float sample and NaN classification for float values.

Issue: #14686. Parent: #14508. Compiler prerequisites: #14773 and #14775.
