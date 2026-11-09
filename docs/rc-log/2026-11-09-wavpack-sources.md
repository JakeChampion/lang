# Bounded WavPack sources and file seeks

The sequential reader retains one encoded group and reusable caller PCM storage.
It returns the caller array on every progress event.
It validates the complete group checksum before sample output.
The final event validates the declared sample count, physical EOF, and trailing tags.
Float output preserves exact IEEE bits in the integer sample array.

The tag reader discards APE values as short reads arrive.
A 32 KiB artwork value passes through a 256-byte window.
Encoded group bytes, group samples, metadata bytes, and index entries have separate limits.
Source I/O failures retain their error category.

The file index records group offsets, sample positions, byte counts, and frame counts.
Index construction validates every group and the complete trailer.
It releases encoded and decoded storage after completion.
Each logical entry uses 24 bytes, plus capacity and allocator overhead.
Playback uses binary search, reads one indexed group, and validates it before sample output.

All 132 frozen WavPack 5.8.1 references pass sequential source tests with three read patterns.
The patterns include one-byte reads and irregular short reads.
All 132 also pass indexed playback from the start, middle, last sample, and EOF.
Both full corpora pass native sanitizers and WASI execution, plus Android ARM64 shared-library compilation.
Native sequential tests report 1,620,224 allocations and the same number of frees.
Native indexed tests report 217,000 allocations and the same number of frees.

Boundary tests reject every truncated prefix and damaged byte of the short controls.
They also check metadata budgets, exact index ranges, every sample seek, and I/O failures.
Damaged and truncated indexed groups produce errors before PCM.
An EOF seek performs no source operation.
All boundary tests pass native sanitizers and WASI execution, plus Android ARM64 shared-library compilation.
Every native suite reports zero live bytes.

Small source and file controls pass in the self-host interpreter.
The full corpora and mutation campaigns use the compiled test gates.
The complexity ratchet, module sidebar test, and source lint pass without limit increases.
Full CI and matched performance measurements remain required.

The reader exposed valid local shadow bindings that both ownership checkers rejected.
PR #14782 fixes that compiler defect.
The reader retains the valid source names.
The Go loader also rejected qualified builtin enum patterns in the error controls.
PR #14784 fixes that resolution defect.
Both compiler fixes remain prerequisites for the registered source tests.

Issue: #14778. Parent: #14686. Roadmap: #14508.
