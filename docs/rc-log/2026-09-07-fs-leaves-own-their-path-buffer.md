# Every fs leaf owns the block it NUL-terminates, on the success path too (#8813)

*2026-09-07* — `examples/self_host/asmcore.fern`, the fs-helper bundle both
register backends emit. The open side of what #8402 fixed for `read_chunk`,
and a whole family of leaves rather than one.

## The measurement

`bin/fern-selfhost -target x86-64-linux`, `FERN_LEAKCHECK=1`, 200 rounds, each
program byte-identical in behaviour before and after. The two path columns are
the same file under an 11-character and a 57-character spelling.

| program | before | after |
| --- | --- | --- |
| `open_reader` + `close`, 11-char path | 600 / 400, **8,000 B** | 600 / **600**, **0** |
| the same, 57-char path | 600 / 400, **17,600 B** | 600 / **600**, **0** |
| `read_file`, 11-char path | 600 / 400, **8,000 B** | 600 / **600**, **0** |
| the same, 57-char path | 600 / 400, **17,600 B** | 600 / **600**, **0** |
| `write_file` | 600 / 200, 17,600 B | 600 / **600**, **0** |
| `read_file_bytes` | 800 / 400, 17,600 B | 800 / **800**, **0** |
| `access` | 400 / 200, 9,600 B | 400 / **400**, **0** |
| `write_file` + `remove_file` | 1000 / 400, 25,600 B | 1000 / **1000**, **0** |
| `create_dir_all` + `remove_dir_all` | 2600 / 800, **52,494,400 B** | 2600 / **2600**, **0** |
| `read_dir` | 1600 / 400, **26,256,000 B** | 1600 / 1200, **17,600 B** |
| `stat` | 600 / 200, 44,800 B | 600 / 400, 35,200 B |
| `read_file` on a DIRECTORY (EISDIR) | 800 / 200, **843,200 B** | 1000 / 600, **17,600 B** |
| `read_file` on invalid UTF-8 | 800 / 200, 25,600 B | 1000 / 600, **17,600 B** |
| all nine leaves in one round, short dir | 7600 / 2800, 78,859,200 B | 7600 / 7000, **52,800 B** |
| the same against a 46-character-longer dir | 7600 / 2800, **78,923,200 B** | 7600 / 7000, **52,800 B** |

The byte count tracking the PATH is the tell the issue was filed on: 8,000
against 17,600 for the identical program. It is flat now, and so is the
combined round, which is what says the fix is the family and not one leaf.

The two remaining non-zero rows are Ok PAYLOADS, not path buffers, and they are
flat in the path: `stat`'s 176-byte `FileStat` struct and `read_dir`'s
`string[]` of names are producers the owned-payload family of #8402 does not
name (it stops at `read_file` / `read_file_bytes` / `read_chunk` / `read_line`
/ `env`). The failing-call rows keep the two blocks #8806 left open, the
`IoError` and the path string inside it.

## What was wrong

Each leaf copies its `path` argument into `__raw_alloc(plen + 1)` so the
syscall gets a C string. On the ERROR path that block became the `IoError`'s
path string (`__raw_string(pathz, plen)`) and so had an owner. On the SUCCESS
path nothing named it and the block was simply lost.

`read_dir` and `remove_dir_all` lost more than the path: a fresh 64 KiB dirent
buffer per `getdents` round and an 8-byte cursor, which is where the 26 MB and
52 MB above come from. `write_file` lost its `contents` copy and
`read_file_bytes` its whole content buffer — both scale with the DATA, not the
path, so a path-length probe alone would have missed them. `read_file` keeps
its content buffer on the success path (it IS the result) and abandoned it on
the two error paths that happen after it is allocated, which on a directory is
the file's own st_size.

## The fix

The shape #8402 established, and it needed no new intrinsic: a fused string
over the block at the block's TRUE size names its owner, and the frame's own
reclaim gives it back.

    var pathz_own: string = __raw_string(pathz, plen + 1);

placed at the first point every path reaches, right after the syscall that
reads the buffer — and for `create_dir_all` after the last one, because it
rewrites the buffer component by component. An error path cannot box the same
block a second time, so it copies instead, through one helper the bundle
already had a home for:

    function __fern_path_copy(pz: i32, n: i32): string

emitted alongside `__fern_io_error` (always in the bundle, since every leaf
calls it). It takes the raw pointer and length rather than the leaf's borrowed
`path` string, so the copy is outside the RC analysis entirely, and `n == 0`
answers the empty literal rather than reaching `__raw_alloc(0)`.

`remove_dir_all`'s child path is the one block that shrank instead: it is
handed to the recursive call as a Fern STRING, which builds its own
NUL-terminated copy, so the byte it carried for a terminator is gone and the
block is sized to the string boxed over it.

## Traps

- **The box size must be the block's TRUE size.** `__fern_alloc` gives
  `__raw_alloc(n)` a `3 + ceil(n/8)`-word block and `__fern_str_free`'s fused
  arm computes the class from the string's LENGTH, so a `plen + 1` block boxed
  at `plen` lands one word short whenever `plen % 8 == 0` — recoverable but
  8 bytes lighter every call, and invisible at every other length. That is why
  the `dir_tree` gate pads its tree name until the child path is a multiple of
  8: at an unpadded length the leg passes with the defect in place.
- **A path-length probe does not see a data-length leak.** `write_file`'s
  contents copy and `read_file_bytes`' content buffer are flat in the path and
  were stranded per call; only the allocs-equal-frees half of the gate catches
  them.
- **`read_file`'s error paths need the content buffer named per branch**, not
  once up front: the success return owns that block, so a binding ahead of the
  branches would be a second owner and a double free.
- **Measure the ERROR paths separately.** `read_file` on a directory strands
  4 KiB a call — 33 times the whole success-path leak — and no success probe
  reaches it.
- **`bin/fern-selfhost` is not rebuilt by `go test`.** The suites compile their
  own driver from the sources, so a stale CLI in `bin/` reports the OLD
  numbers from a hand probe while the gates read the new ones. An hour went
  into a "the fix did nothing" reading that was a stale binary.

## What gates it

- `TestSelfHostFsPathBufferReclaimX86_64` — nine leaves, each measured at two
  path lengths that differ by 48 characters (a run of `./` components, so both
  spellings name the same file) and required to cost the same live bytes, then
  at 20 and 200 rounds with every block back. `stat` and `read_dir` assert the
  path independence plus a PINNED count of blocks left per round, so a
  re-abandoned dirent buffer cannot hide behind the payload leak.
- `TestSelfHostFsErrorPathBufferReclaimX86_64` — six failing calls, each
  allowed exactly the `IoError` and its path string, which is what catches the
  content buffer on the EISDIR and invalid-UTF-8 branches.
- `TestSelfHostIoResultBoxReclaimX86_64/open_close` and `/open_close_bound`
  moved from a slope of one block a round to the ordinary
  allocs-equal-frees assertion, as #8806 said they should when this closed.
- Unmoved: `TestSelfHostOptBoxReclaimX86_64`, the leak / alloc-count /
  construction-retain / container-sink matrices, `TestSelfHostFeatureCensus`,
  the whole 986-subtest `TestFernFixturesSelfHost` corpus on x86-64 and arm64.

Each piece was reverted in isolation and each has a leg that goes red alone:

| reverted | what fails |
| --- | --- |
| `open_res`'s path binding | `Fs/open_close`, all three `IoResultBox` legs |
| `read_file`'s path binding | `Fs/read_file` |
| `read_file`'s per-branch content binding | `FsError/read_file_eisdir`, `/read_file_bad_utf8` |
| `read_file_bytes`' path binding | `Fs/read_file_bytes` |
| `read_file_bytes`' content binding | `Fs/read_file_bytes` |
| `write_file`'s path binding | `Fs/write_file`, `Fs/write_remove` |
| `write_file`'s contents-copy binding | `Fs/write_file`, `Fs/write_remove` |
| `remove_file`'s path binding | `Fs/write_remove` |
| `create_dir_all`'s path binding | `Fs/dir_tree` |
| `remove_dir_all`'s path binding | `Fs/dir_tree` |
| `remove_dir_all`'s dirent + cursor bindings | `Fs/dir_tree` |
| `read_dir`'s path binding | `Fs/read_dir` |
| `read_dir`'s dirent + cursor bindings | `Fs/read_dir` |
| `stat`'s path binding | `Fs/stat` |
| `access`'s path binding | `Fs/access` |
| the child path's exact sizing | `Fs/dir_tree` |

## Still open

- **`stat`'s `FileStat` and `read_dir`'s `string[]` of names.** One block a
  round and two a round respectively, flat in the path. They want the
  owned-payload admission of #8402 extended past the five producers it names,
  which is irlower work rather than a runtime one.
- **The same `+ 1` sizing survives at two sites**, `read_dir`'s per-name copy
  and `temp_dir`'s rejected-prefix copy. Both blocks are leaked outright today
  by the two entries above and by the `IoError` residue, so the class match is
  unobservable and nothing can gate it; it belongs with whichever change makes
  those blocks come back. `temp_dir`'s RESULT block is a third: it is returned,
  so it has an owner, and its `total + 1` allocation against a `total`-length
  box loses a word at one length in eight.
- **`__fern_proc_exec` and `__fern_subprocess` strand their argv array and one
  NUL-terminated block per argument** when the exec fails. Deliberately not
  folded in: a pointer array is not a string, so the fused handback does not
  name it, and the shape wants its own change. Filed as **#8837**.
- The **wasm** emitter builds its paths in hand-written WAT against WASI
  `path_open` and shares none of this; it was not measured (no wasmtime here).
