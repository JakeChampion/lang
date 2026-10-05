# The compiler frees its string builders

2026-10-05: `asm_ir.ssa_flat_op`, `asm_ir.emit_ssa_function_x86`,
`asm_ir.emit_entry_runtime_settled`, their arm64 twins, `fern.words_of`,
`coreutils/lib/filetype.fern`, `asmcore.rt_src_read_dir_like`.

## What leaked

A `buf_new` builder is a handle with no drop: `buf_take` copies its text out
and leaves the builder alive, so a builder nothing passes to `buf_free` leaks.
The emitters capture one stack-op arm, one leaf body and one runtime round each
into a fresh builder and take its text, and none of them freed it. A binary
compile also kept `word_buf`'s 1 MiB instruction buffer. In coreutils,
`type_letter` and `mode_string` leaked one builder per mode column printed.
The typed lowering was not involved in any of these: each is a missing call
in the source.

The runtime's `read_dir` and `read_dir_all` allocated each name a byte past
its length and wrapped it as a string of its length. A string is freed at the
size class of its length, so a name whose length is a multiple of 8 went back
to the class 8 bytes smaller. Allocations and frees still balanced, and 8 bytes
were lost per such name per listing, in every program that lists a directory.

## Measured

`FERN_LEAKCHECK` build of the compiler, live bytes at exit:

| Compile | Before | After |
| --- | ---: | ---: |
| A ten-line program, x86-64 binary | 1,123,008 | 0 |
| The same, x86-64 `-emit asm` | 74,400 | 0 |
| The same, arm64 binary | 1,116,288 | 0 |
| The same, arm64-darwin | 67,680 | 0 |
| The same, wasm core module | 0 | 0 |
| `checker.fern`, x86-64 `-emit asm` | 822,400 | 0 |
| `fern.fern`, x86-64 binary (126 M allocations) | not measured | 0 |

`ls -l` over 30 files leaked 1,488 bytes and `stat` over three paths 144; both
are 0 now. `ls -l` over names of every length from 1 to 33 lost 32 bytes to
`read_dir`, one 8-byte loss for each of 8, 16, 24 and 32; it is 0 now.

## Witnessed

`TestSelfHostCompilerFreesWhatItAllocatesX86_64` builds the compiler with
`FERN_LEAKCHECK` and requires a balanced census for each target and output
form and for `checker.fern`; it fails on every native row before this change.
`TestUtilitiesFreeWhatTheyAllocate` does the same for `ls -l`, `ls -la` and
`stat` over a directory with a name of every length from 1 to 33, and fails
on either cause alone.

A survey of all 106 coreutils under `FERN_LEAKCHECK` (about 390 invocations,
inputs up to 4.9 MB) found nothing else in what they do on success. The
remaining non-zero censuses are an `exit()` called below `main` while callers
hold values: `csplit`'s `{*}` ends its run that way, `timeout`'s timer child
does, and so do the error paths. Nothing is released before the process ends,
so the census counts what the callers held; it is constant per process.

## Next

A sweep of `internal/e2eselfhost` with `FERN_LEAKCHECK` forced on every x86-64
program it builds (1,382 tests, 13,891 programs) found no leak in what the
typed lowering emits. Every test comment that said a shape "still leaks"
measured 0, so the 25 files that carried one now require a balanced census on
every case. The next leak lead has to come from a new shape, not from them.
