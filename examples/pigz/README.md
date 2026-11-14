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

The output does not depend on the count of processes above 1. Each round of
the compression reads about 4 MiB for each process. Process k of n
compresses the blocks k, k + n, k + 2n and so on of the round. The
processes are `std/worker` workers and the parent. Then the parent writes
the output of the round in order.

`-R` reads all of its input
first, because its blocks end where the hash of the input says. On macOS
and on wasm32-wasi, `std/worker` runs the workers inline (#15584).

## Speed

A file of 108 MB: 80 MB of the Markdown, Go and Fern sources of this
repository, then 32 MB of the `fern` binary. x86-64 with 4 CPUs, pigz 2.8
with zlib 1.3, 2026-11-14. The runs of pigz and of the port alternate, and
each row has the fastest of three runs, in seconds. Other work used about
one CPU of the machine during the runs. For each compression row, the port
wrote the same bytes as pigz.

| Run | pigz wall | pigz CPU | port wall | port CPU |
| --- | --: | --: | --: | --: |
| `-1 -p 1` | 1.21 | 1.20 | 1.41 | 1.39 |
| `-1 -p 4` | 0.52 | 1.30 | 0.84 | 1.75 |
| `-6 -p 1` | 3.80 | 3.75 | 4.94 | 4.85 |
| `-6 -p 4` | 1.50 | 3.89 | 2.24 | 5.22 |
| `-9 -p 1` | 7.64 | 7.58 | 10.82 | 10.66 |
| `-9 -p 4` | 2.90 | 7.77 | 4.45 | 10.83 |
| `-d -p 1` | 0.43 | 0.43 | 0.34 | 0.34 |
| `-d -p 4` | 0.52 | 0.53 | 0.45 | 0.44 |

The port decompresses faster than pigz. It compresses 1.2 to 1.4 times
slower with one process, and 1.5 to 1.6 times slower with four. With one
process, the port uses 1.5 MB of memory, and pigz uses 2.6 MB.

Most of the time of a compression is in `longest`, the port of
`longest_match`. The code that Fern makes for its candidate loop has a
bounds check at each read of the window and of the hash chain. It also
keeps some loop values on the stack. In the loop of `deflate_slow`, the
write to `prev` also tests the count of the array at each position: the
array goes to `longest` on each step, and after that call the compiler
does not know that the loop holds the only reference.

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
  cause is the code that Fern makes for the loops of zlib, so the next work
  is in the compiler.

- Level 11 (zopfli). `-11` aborts with exit code 22 and the message
  `level 11 (zopfli) is not available in this port`.

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
