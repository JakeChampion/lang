# Compact ELF fixture

`TestSelfHostELF` supplies an eight-byte RELRO region without the virtual gap
that the compact writer requires. The writer rejects that input with status 3.
The old test discards stderr, so its report calls this a failure at check 3.
The isolated original fixture records the actual layout diagnostic.

The corrected fixture supplies constants, the target-alignment gap and mutable
globals. It checks both load segments, their file and virtual offsets, the
constant file size, mapped zero-fill space, the protected range and both payloads.
The virtual gap stays out of the file. The earlier ELF checks remain intact.
The runner now reports stderr and handles a failed process start.

The Linux self-test passes in 1.999 seconds. Go vet and the format gate pass.
The adjacent symbol-table test skips under the ARM host's x86 execution runner.
That skip is not an execution result. Native x86 CI still covers that test.

No compiler or standard-library input changes. Every input matches the Android
gap qualification, whose four stages match `fd5b67c5cab98a6d6ee1f48ba773b2bb9abcb600c3f43eeb5aee163defd434a4`.
The earlier costs and output comparisons retain their original scope.

The binding record identifies the final Go source and the unchanged compiler
qualification. The archives retain the original CI and local failures.
Every current-head non-Netlify check still gates merge.
