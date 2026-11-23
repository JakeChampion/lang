# pigz

A Fern port of [pigz](https://zlib.net/pigz/) 2.8, which compresses and
decompresses gzip, zlib and zip files. It compresses on all CPUs. For each
option that the port has, the output bytes, the messages and the exit codes
are those of pigz 2.8.

```
$ pigz file              # writes file.gz and removes file
$ pigz -d file.gz        # writes file and removes file.gz
$ pigz -9 -k file        # the smallest output of zlib, and file stays
$ pigz -11 -k file       # the smaller output of zopfli, and much slower
$ pigz -c file > out.gz  # writes to the standard output
$ pigz -t file.gz        # tests file.gz
$ pigz -l file.gz        # lists file.gz
$ pigz < in > in.gz      # with no file, the standard input
```

## Build and run

```
$ fern -O -target x86-64-linux -o pigz examples/pigz/pigz.fern   # or arm64-linux
```

The messages start with the name of the program, as in pigz. A program
named `unpigz` or `gunzip` decompresses, and a name that ends in `cat`
decompresses to the standard output.

## Options

| Option | Long form | Effect |
| --- | --- | --- |
| `-0` to `-9`, `-11` | `--fast`, `--best` | The compression level. The default is 6. Level 11 uses zopfli. |
| `-A xxx` | `--alias xxx` | The name of a zip entry from the standard input. |
| `-b mmm` | `--blocksize mmm` | The block size in KiB, from 32. The default is 128. |
| `-c` | `--stdout`, `--to-stdout` | Write to the standard output and keep the input. |
| `-C ccc` | `--comment ccc` | Put a comment in the gzip or zip header. |
| `-d` | `--decompress`, `--uncompress` | Decompress. |
| `-f` | `--force` | Replace an output that exists, and compress links and `.gz` files. |
| `-h` | `--help` | Show the help and stop. |
| `-H` | `--huffman` | Use only Huffman codes. |
| `-i` | `--independent` | Compress each block with no dictionary. |
| `-k` | `--keep` | Keep the input file. |
| `-K` | `--zip` | Write a zip file with one entry (`.zip`). |
| `-l` | `--list` | List the compressed input. |
| `-L` | `--license` | Show the license and stop. |
| `-m`, `-T` | `--no-time` | Do not store or restore the time. |
| `-M` | `--time` | Store or restore the time. |
| `-n` | `--no-name` | Do not store or restore the name and the time. |
| `-N` | `--name` | Store or restore the name and the time. |
| `-p n` | `--processes n` | Compress with n processes. The default is one for each CPU. |
| `-q` | `--quiet`, `--silent` | Write no messages. |
| `-r` | `--recursive` | Go into each directory. |
| `-R` | `--rsyncable` | End the blocks where a hash of the input says. |
| `-S .sss` | `--suffix .sss` | Use the suffix `.sss` in place of `.gz`. |
| `-t` | `--test` | Test the compressed input and write nothing. |
| `-U` | `--rle` | Find only runs of one byte. |
| `-v` | `--verbose` | Write more messages. |
| `-V` | `--version` | Show the version and stop. |
| `-Y` | `--synchronous` | Write the output file to the disk before the next file. |
| `-z` | `--zlib` | Write zlib (`.zz`). |
| `-I n` | `--iterations n` | The count of optimization passes of level 11. The default is 15. |
| `-J n` | `--maxsplits n` | The maximum count of blocks of level 11 in one part. The default is 15. |
| `-O` | `--oneblock` | Do not split level 11 parts into smaller blocks. |
| `-F` | `--first` | pigz 2.8 reads this option and its zopfli does nothing with it. The port does the same. |

The port reads the options as pigz does. First it reads the options in the
`GZIP` and `PIGZ` environment variables, then each option on the command
line, then the files. `--` ends the options. Before `--`, `-` is the
standard input. Letters can share one dash, for example `-dc`. A parameter
can follow its letter, for example `-p4`, except for `-S` and `-C`.

## Formats

Compression writes gzip, zlib (`-z`) or zip (`-K`). Decompression, `-t` and
`-l` read each of them, and also compress (LZW, `.Z`) data:

- gzip: each member of a file with more than one member. As pigz does, the
  port ignores bytes after the last member, and `-v` tells about them.
- zlib: one stream. Bytes after it give a warning and exit code 1.
- zip: the first entry, with or without a data descriptor, and with Zip64
  lengths. Other entries give a warning, and the zip file stays.
- compress: the data of `compress` with 9 to 16 bits.

A decompression of a name that does not exist tries the name with each
compressed suffix of pigz (`.gz`, `.zz`, `.zip`, `.Z`, `.tgz` and the
others).

Each mode reads its input in parts and writes its output in parts, so the
memory does not grow with the input. The length of the output has no
limit. A test of a gzip member of 4.7 GB used 1.9 MB of memory.

- Decompression, `-t` and `-l` read the input in parts of 256 KiB. The
  deflate stream of `std/deflate` (`inflate_part`) keeps a window of
  32 KiB, and it gives the output in parts of at most 32 KiB.
- `-l` without `-t` reads the input to its end, for its length and its
  last bytes.

Two cases keep more:

- `-R` reads the input in blocks, as pigz does. The port keeps the input
  from the start of the current block, because the block ends at a hit of
  the hash. Level 11 also keeps the 32 KiB before the block.
- With `-lv`, the port keeps all of a zip file in memory, for the comment
  of its central directory.

When data does not decode, the standard output has the output before the
error, as in pigz. For deflate data, that is the output of each symbol
before the error. For compress data, it is each full buffer of 32 KiB. A
file that does not decode is removed.

## The same bytes as pigz

pigz writes what zlib 1.3 writes, in its own block layout. So the port has
two parts:

- `std/deflate/zlib` is a port of zlib 1.3 deflate in the standard
  library. pigz uses raw deflate, a window of 32 KiB, memLevel 8, the
  levels 0 to 9, and two strategies, Huffman-only (`-H`) and RLE (`-U`).
  The module keeps the window, the hash chains, the symbol buffer and the
  tree builder of zlib. It also keeps the room of the output buffer,
  because the stored blocks and the flushes of zlib use it.
  `zlib_compress` of `std/deflate` uses the same module.

- `layout.fern` is the block layout of pigz. One process (`-p 1`) writes one
  deflate stream, as `single_compress` does. More processes write the jobs
  of `parallel_compress`, and each job has the 32 KiB before it as its
  dictionary.

- So `-p 1` and `-p 2` give different bytes, and each gives the bytes of
  pigz. Each block but the last ends on a byte boundary, with empty stored
  or static blocks as pigz writes them.

- `zopfli.fern` is a port of the zopfli of pigz 2.8 (`zopfli/src/zopfli`):
  `ZopfliDeflatePart` with dynamic blocks, which is what level 11 calls. It
  keeps the order of each calculation of zopfli. The costs of the optimal
  parse are `float` in zopfli, so the port rounds each one to `f32`. The
  cost model uses `log`, and `std/f64` gives the bits of the `log` of glibc
  2.39. For level 11, each block has the 32 KiB before it as its window,
  as in pigz. The layout ends each block as pigz does.

- With one process, pigz 2.8 does not count the output of zopfli in the
  compressed length. So the zip entry of `-11 -K -p 1` has a compressed
  length of zero, and the port writes the same bytes.

The output does not depend on the count of processes above 1. As in
pigz, the compression is a queue of jobs. A job is about 1 MiB of whole
blocks, and a `std/worker` worker compresses it. Up to n jobs run at a
time.

The parent reads the input and writes the output of each job in
order. After it writes the output of a job, it starts the next job. So the
other jobs do not wait for the slowest one.

On macOS and on wasm32-wasi, `std/worker` runs the workers inline
(#15584).

## Canterbury and Silesia round trips

On x86-64 Linux, Fern and pigz 2.8 produced these gzip sizes with four
workers. GNU gzip and pigz both decoded Fern's output to the original
SHA-256 digest at every level. Fern also decoded pigz's output at all
three levels and GNU gzip's level-1 output.

| Corpus | Input bytes | Level 1 bytes | Level 6 bytes | Level 9 bytes |
| --- | ---: | ---: | ---: | ---: |
| Canterbury (11 files) | 2,810,784 | 864,968 | 732,519 | 729,274 |
| Silesia (12 files) | 211,938,580 | 77,189,411 | 68,247,770 | 67,666,924 |

To reproduce, concatenate the regular files of each tar archive in
lexicographic member-name order, without the tar headers. Run each
compressor with `-nc -p 4` and the indicated level, reading the joined
input from stdin. The joined Canterbury input has SHA-256
`0737d6c25a3337923a1429471546118448f07133e55c8f43fadde7984384982c`.
The joined Silesia input has SHA-256
`8df8cf2a9456a3765834b7cd8b7c1114df9dca708dd505e4d37bc12e536395b0`.
[Canterbury archive](https://corpus.canterbury.ac.nz/resources/cantrbry.tar.gz) ·
[Silesia archive](https://github.com/DataCompression/corpus-collection/tree/main/Silesia-Corpus)

## Speed

A file of 108 MB: 80 MB of the Markdown, Go and Fern sources of this
repository, then 28 MB of the `fern` binary. x86-64 with 4 CPUs, pigz 2.8
with zlib 1.3, 2026-11-15. The runs of pigz and of the port alternate, and
each row has the fastest of three runs, in seconds. CPU is user and system
time. For each compression row, the port wrote the same bytes as pigz.

Other work kept the load average of the machine at 4 to 6 during the
runs. So neither program got the four CPUs. Compare the CPU times.

| Run | pigz wall | pigz CPU | port wall | port CPU |
| --- | --: | --: | --: | --: |
| `-1 -p 1` | 1.19 | 1.18 | 1.25 | 1.24 |
| `-1 -p 4` | 0.80 | 1.26 | 1.35 | 1.71 |
| `-6 -p 1` | 3.77 | 3.76 | 4.20 | 4.10 |
| `-6 -p 4` | 1.45 | 4.20 | 1.90 | 4.93 |
| `-9 -p 1` | 7.72 | 7.67 | 8.02 | 7.90 |
| `-9 -p 4` | 3.87 | 8.16 | 4.91 | 8.86 |
| `-d -p 1` | 0.47 | 0.45 | 0.37 | 0.37 |
| `-d -p 4` | 0.44 | 0.54 | 0.51 | 0.49 |

The port decompresses in less CPU time than pigz.

The decompression now reads and writes in parts. A later run used a corpus
of 114 MB of the same kind, with `-d -p 1` and a load average near 2.
There, pigz used 0.50 s of CPU time and 2.2 MB of memory. The port used
0.22 s and 1.9 MB. The port that read all of its input used 0.26 s and
150 MB.

In CPU time, the port compresses 1.03 to 1.09 times slower with one
process. With four, it is 1.09 to 1.36 times slower. With four processes,
each worker copies its input and its output, and it starts with an empty
heap. When it compresses with one process, the port uses 1.5 MB of
memory, and pigz uses 2.6 MB.

Level 11 is slower. The input was the first 300,000 bytes of the `fern`
binary, with `-11 -p 1`. pigz used 1.04 s of CPU time, and the port used
1.61 s. Most of
that time is in the search for matches of zopfli.

The hash tables `head` and `prev` are `u16[]`, as in zlib, so they use
128 KB and not 256 KB. In the same runs, the port with `i32[]` tables used
4.70 s of CPU at `-6 -p 1` and 9.45 s at `-9 -p 1`. So the 2-byte tables
take 13% and 16% less CPU time there.

The compiler makes four loops into calls to its kernels (#15974): the
copy of the input into the window, the slide of the window, the clear of
`head`, and the slide of `head` and `prev` (`docs/BYTE-COPY-LOOPS.md`). The
runs of the table are older than the slide kernel.

Most of the time of a compression is in `longest`, the port of
`longest_match`. Its loop waits for the loads of the window and of the hash
chain, and for compares that the processor predicts wrong. So the count of
its instructions has a small effect on the time.

Changes to the assembly by hand show this. With no bounds checks, the loop
took the same time. With all of its values in registers, it took 1.5% less
time. Two-byte compares, as zlib makes them, took 7% less time, and the port
now makes them. A check and a two-byte read in five instructions, as #15976
gives them, took the same time.

This cause is not measured: `longest` takes ten arguments, and some of them
go on the stack. The values of the loop of `deflate_slow` that live across
the call are in stack slots, because x86-64 has five callee-saved registers.

### Silesia and 1 GiB log comparison

On 2026-11-21, the Fern port at `eac0faf9b4` used less wall time than
pigz 2.8 to decompress both inputs at every level. Pigz compressed both
inputs faster at all three levels. The zlib-ng results use its single-threaded
`minigzip` utility. Fern and pigz used four workers.

Each cell gives **wall / CPU seconds** for one timed run on x86-64 Linux
(AMD EPYC 9V74, four logical CPUs available to each process). CPU includes
user and system time across workers. These individual observations include
file I/O and process startup.

| Input | Level | Operation | Fern | pigz 2.8 | zlib-ng 2.3.3 |
| --- | ---: | --- | ---: | ---: | ---: |
| Silesia | 1 | compress | 1.226 / 3.868 | 0.736 / 2.772 | 1.113 / 1.112 |
| Silesia | 1 | decompress | 0.496 / 0.493 | 0.701 / 0.808 | 0.366 / 0.366 |
| Silesia | 6 | compress | 3.322 / 10.34 | 2.262 / 8.83 | 3.47 / 3.438 |
| Silesia | 6 | decompress | 0.419 / 0.419 | 0.777 / 0.905 | 0.422 / 0.419 |
| Silesia | 9 | compress | 6.041 / 20.29 | 5.148 / 20.253 | 8.38 / 8.324 |
| Silesia | 9 | decompress | 0.378 / 0.378 | 0.722 / 0.862 | 0.315 / 0.314 |
| Log | 1 | compress | 2.574 / 7.75 | 1.342 / 5.066 | 1.748 / 1.701 |
| Log | 1 | decompress | 1.071 / 1.069 | 1.989 / 2.408 | 1.027 / 1.015 |
| Log | 6 | compress | 4.35 / 14.593 | 2.918 / 11.249 | 5.904 / 5.887 |
| Log | 6 | decompress | 0.882 / 0.881 | 1.344 / 1.892 | 0.649 / 0.649 |
| Log | 9 | compress | 17.818 / 64.8 | 15.885 / 62.819 | 21.316 / 21.21 |
| Log | 9 | decompress | 0.995 / 0.994 | 2.319 / 2.47 | 0.7 / 0.699 |

**Inputs and commands.** Silesia uses the concatenation and hash above.
The synthetic log repeats the following ASCII records and ends at exactly
1,073,741,824 bytes, including the partial last record. Its SHA-256 is
`6c5ed1232fc88e28a965505c3d9b2c2380d009415a3e5cfe90ac38783cba17d5`.

```python
block = b"".join(
    f"2026-11-21T12:{i // 60 % 60:02}:{i % 60:02}Z GET /items/{i % 997} status=200 bytes={i * 37 % 8192}\n".encode("ascii")
    for i in range(65536)
)
with open("log.bin", "wb") as log:
    copies, tail = divmod(1 << 30, len(block))
    for _ in range(copies):
        log.write(block)
    log.write(block[:tail])
```

Build Fern with `-O`, pigz 2.8 with zlib 1.3 as in the CI oracle, and
zlib-ng 2.3.3 with `./configure --static && make minigzip`. Read stdin
from the input file and write compressed stdout to a regular file.
Pass `-c -LEVEL -p 4 -n` to Fern and pigz, or `-c -LEVEL` to minigzip,
replacing LEVEL with 1, 6 or 9. Rotate the starting compressor at each level.
For decompression, give all three programs the same pigz stream for that
level, pass `-d -c` (plus `-p 4 -n` for Fern and pigz), and direct stdout
to `/dev/null`. Outside timing, GNU gzip decoded every compressor's output
and all three decoders reproduced the input SHA-256 from each shared stream.

## Messages and exit codes

The messages and the exit codes are those of pigz 2.8. Some examples:

- `pigz: abort: ...` and exit 22 for an invalid option, level or parameter.
- `pigz: skipping: ...` and exit 1 for a file that does not exist, a
  directory, a link, an output file that exists, or data that does not
  decode.
- `pigz: abort: read error on f (Permission denied)` and the error number
  as the exit code for an input that does not open.

## What remains

- Speed: compression is not yet as fast as pigz (see the table). The
  causes that "Speed" gives are not fixed. Level 11 is about 1.5 times
  slower than pigz.

- With `-lv`, the port keeps all of a zip file in memory, for the comment
  of its central directory.

- With `-N`, a header name that is not UTF-8 gives another output name.
  pigz uses the bytes of the name. A Fern path is a `string`, which is
  always UTF-8 (`docs/STRINGS-SOTA.md`). So the port puts U+FFFD in place
  of each byte that is not UTF-8. A listing shows the bytes of the name,
  as pigz does.

## Tests

- `gz_test.fern`: the options, the environment variables, the names, the
  headers and trailers, the decoders and their messages, the list lines,
  and the check combination. It also checks the output of
  `std/deflate/zlib`, `zopfli` and the block layouts. The bytes that it
  expects come from zlib 1.3, from the zopfli of pigz 2.8, and from pigz
  2.8.
  `TestSelfHostExamplePigzUnit` runs it on x86-64, arm64 and wasm32-wasi
  with the leak census.

- `TestSelfHostExamplePigzCLI` runs 139 cases of the CLI on x86-64 and
  arm64. Each case runs in a new directory. The test compares what each
  run gives with `testdata/pigz-2.8.json`, which holds what pigz 2.8 gave:
  the exit code, the messages, the standard output and each file.

- `TestSelfHostExamplePigzDamaged` runs `-dc`, `-t`, `-l` and `-ltv` on
  damaged streams on x86-64 and arm64. The streams are gzip with each header field, two
  gzip members, zlib, two zip entries and compress.
  The test cuts each stream at many lengths, and it changes one byte at
  many offsets. Each run must give the exit code, the messages and the
  output of pigz 2.8. `testdata/pigz-2.8-damaged.txt` holds the exit code
  and a digest of the messages and the output for each run.

- `TestSelfHostExamplePigzLarge` decompresses and tests a gzip member of
  4.5 GiB on x86-64. The test makes the member from 72 copies of the
  deflate blocks of 64 MiB, so the member is 14 MB.

- CI builds pigz 2.8 with zlib 1.3 from verified source archives.
  It sets `FERN_PIGZ_ORACLE` for the live comparison.
  Local runs can select that same reference through `FERN_PIGZ_ORACLE`.
  `FERN_PIGZ_UPDATE=1` also regenerates the frozen reference file.
