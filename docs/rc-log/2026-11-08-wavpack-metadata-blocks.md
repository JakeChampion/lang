# WavPack metadata-only blocks

WavPack 5.8.1 can write a metadata-only block after audio through `WavpackStoreMD5Sum` and `WavpackFlushSamples`.
That block has zero samples and neutral coding flags.
The original reader compared those flags with the audio profile and rejected valid integer and float files.
The original `eb1cea55ca03ffbb2c8e38114b8f01d38696d73a` tree reproduces the failure.
The reference library decodes the same input with zero errors.

The reader now finds its profile in the first audio block.
It applies PCM compatibility and sample-total checks to audio blocks.
Metadata-only blocks still pass encoded checksum and mandatory metadata checks.
They can precede the first audio block or follow the final audio block.
The source profile and group sequence retain their previous checks.

Five new frozen files cover integer and float metadata before and after audio.
One file gives the metadata-only block a zero sample total and omits its optional encoded checksum.
WavPack verifies its PCM and stored MD5 metadata independently.
The complete corpus now contains 132 files, with 130 exact FFmpeg comparisons.
The two previously recorded FFmpeg integer-extension differences remain.

The full corpus, boundary suite, and seven small controls pass native sanitizers and WASI.
All three compile as Android ARM64 shared libraries.
The full native run reports 103800 allocations, 103800 frees, and zero live bytes.
All three registered native suites agree with the Go oracle.
The small corpus and boundary suite agree in the self-host interpreter.
The complexity gate passes without changed limits.

The seek boundary control checks every position through EOF in its 35-frame source.
This includes the reference encoder's shorter final groups.
The Python reference tools use standard formatting, and deterministic regeneration passes.

Issue: #14780. Parent: #14686. Implementation: #14777.
