# pigz

A Fern port of [pigz](https://zlib.net/pigz/) 2.8, which compresses and
decompresses gzip, zlib and zip files. It compresses on all CPUs. For each
option that the port has, the output bytes, the messages and the exit codes
are those of pigz 2.8.

```
$ pigz file              # writes file.gz and removes file
$ pigz -d file.gz        # writes file and removes file.gz
$ pigz -9 -k file        # the smallest output, and file stays
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
| `-0` to `-9` | `--fast`, `--best` | The compression level. The default is 6. |
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
| `-I n`, `-J n`, `-O`, `-F` | `--iterations`, `--maxsplits`, `--oneblock`, `--first` | The settings of level 11. The port reads them and does nothing with them. |

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

## The same bytes as pigz

pigz writes what zlib 1.3 writes, in its own block layout. So the port has
two parts:

- `zdeflate.fern` is a port of the parts of zlib 1.3 deflate that pigz
  uses: raw deflate, a window of 32 KiB, memLevel 8, the levels 0 to 9,
  and two strategies, Huffman-only (`-H`) and RLE (`-U`). It keeps the
  window, the hash chains, the symbol buffer and the tree builder of zlib.
  It also keeps the room of the output buffer, because the stored blocks
  and the flushes of zlib use it.

- `layout.fern` is the block layout of pigz. One process (`-p 1`) writes one
  deflate stream, as `single_compress` does. More processes write the jobs
  of `parallel_compress`, and each job has the 32 KiB before it as its
  dictionary.

- So `-p 1` and `-p 2` give different bytes, and each gives the bytes of
  pigz. Each block but the last ends on a byte boundary, with empty stored
  or static blocks as pigz writes them.

The output does not depend on the count of processes above 1. As in
pigz, the compression is a queue of jobs. A job is about 1 MiB of whole
blocks, and a `std/worker` worker compresses it. Up to n jobs run at a
time.

The parent reads the input and writes the output of each job in
order. After it writes the output of a job, it starts the next job. So the
other jobs do not wait for the slowest one.

`-R` reads all of its input
first, because its blocks end where the hash of the input says. On macOS
and on wasm32-wasi, `std/worker` runs the workers inline (#15584).

## Speed

A file of 108 MB: 80 MB of the Markdown, Go and Fern sources of this
repository, then 28 MB of the `fern` binary. x86-64 with 4 CPUs, pigz 2.8
with zlib 1.3, 2026-11-15. The runs of pigz and of the port alternate, and
each row has the fastest of three runs, in seconds. CPU is user and system
time. For each compression row, the port wrote the same bytes as pigz.

Other work kept the load average of the machine at 11 to 16 during the
runs. So neither program got the four CPUs. Compare the CPU times.

| Run | pigz wall | pigz CPU | port wall | port CPU |
| --- | --: | --: | --: | --: |
| `-1 -p 1` | 1.42 | 1.17 | 1.72 | 1.34 |
| `-1 -p 4` | 1.39 | 1.24 | 2.98 | 1.90 |
| `-6 -p 1` | 4.10 | 3.75 | 5.07 | 4.05 |
| `-6 -p 4` | 2.56 | 3.78 | 4.83 | 4.76 |
| `-9 -p 1` | 7.61 | 7.19 | 8.75 | 7.48 |
| `-9 -p 4` | 3.61 | 7.27 | 8.38 | 8.18 |
| `-d -p 1` | 0.39 | 0.38 | 0.31 | 0.30 |
| `-d -p 4` | 0.44 | 0.42 | 0.44 | 0.33 |

The port decompresses faster than pigz. In CPU time, it compresses 1.04
to 1.15 times slower with one process, and 1.13 to 1.53 times slower with
four. With four processes, each worker copies its input and its output,
and it starts with an empty heap. With one process, the port uses 1.6 MB
of memory, and pigz uses 2.5 MB.

The hash tables `head` and `prev` are `u16[]`, as in zlib, so they use
128 KB and not 256 KB. In the same runs, the port with `i32[]` tables used
4.70 s of CPU at `-6 -p 1` and 8.97 s at `-9 -p 1`. So the 2-byte tables
take 14% and 17% less CPU time there.

The compiler makes two kinds of loop into calls to its kernels (#15974):
the copy of the input into the window, and the slide of the window. The
kernels have no 2-byte form, so the clear of `head` stays a loop. The slide
of `head` and `prev` runs a copy of its loop with no checks (#15975). The
runs of the table are older than these two changes.

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
  causes that "Speed" gives are not fixed.

- Level 11 (zopfli). `-11` aborts with exit code 22 and the message
  `level 11 (zopfli) is not available in this port`.

  The cost model of zopfli uses `log`, so the bytes of pigz need the
  results of the `log` of glibc. We compared the two on the integers from
  1 to 2^20. The `log` of `std/f64` and the `log` of glibc 2.39 give 40
  different results there, each one unit in the last place apart. Neither
  one is correctly rounded at all 40. glibc itself gives a different result
  at one of them with FMA and without FMA. So a port gives the bytes of
  pigz only with the `log` of glibc, as the machine selects it.

- The port reads all of the input before it decompresses, tests or lists,
  and before a compression with `-R`. pigz reads these in parts.

- When data does not decode, pigz writes the output that it decoded before
  the error to the standard output. The port writes the members that it
  decoded before the error.

- Decompression of more than 2 GiB of output, which the arrays of Fern
  cannot hold.

## Tests

- `gz_test.fern`: the options, the environment variables, the names, the
  headers and trailers, the decoders and their messages, the list lines,
  and the check combination. It also checks the deflate of `zdeflate` and
  the block layouts against bytes from zlib 1.3 and pigz 2.8.
  `TestSelfHostExamplePigzUnit` runs it on x86-64, arm64 and wasm32-wasi
  with the leak census.

- `TestSelfHostExamplePigzCLI` runs 125 cases of the CLI on x86-64 and
  arm64. Each case runs in a new directory. The test compares what each
  run gives with `testdata/pigz-2.8.json`, which holds what pigz 2.8 gave:
  the exit code, the messages, the standard output and each file.

- CI builds pigz 2.8 with zlib 1.3 from verified source archives.
  It sets `FERN_PIGZ_ORACLE` for the live comparison.
  Local runs can select that same reference through `FERN_PIGZ_ORACLE`.
  `FERN_PIGZ_UPDATE=1` also regenerates the frozen reference file.
